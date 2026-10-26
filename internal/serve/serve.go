package serve

import (
	"context"
	cryptorand "crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api"
	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/controlbind"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/secrets"
	"github.com/alphabravocompany/thewolf/internal/setup/scanners"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
	_ "github.com/alphabravocompany/thewolf/plugins"
)

type Options struct {
	Addr           string
	SkipScanInit   bool
	APIOnly        bool
	Version        string
	Commit         string
	BuildDate      string
	CoreCommit     string
	OverlayVersion string
	OverlayCommit  string
}

func OpenStore() (db.Store, error) {
	cfg := db.ResolveDatabase()
	switch strings.ToLower(cfg.Driver) {
	case "sqlite":
		dsn := cfg.DSN
		if dsn == "" {
			home, _ := os.UserHomeDir()
			_ = os.MkdirAll(home+"/.wolf", 0o750)
			dsn = home + "/.wolf/wolf.db"
		}
		return db.NewSQLite(dsn)
	case "postgres":
		if cfg.DSN == "" {
			return nil, fmt.Errorf("WOLF_DB_DSN required for postgres driver")
		}
		return db.NewPostgres(cfg.DSN)
	default:
		return nil, fmt.Errorf("unknown db driver %q", cfg.Driver)
	}
}

func Run(ctx context.Context, opt Options) error {
	if opt.Addr == "" {
		opt.Addr = ":8778"
	}
	if opt.APIOnly {
		if err := os.Setenv("WOLF_API_ONLY", "true"); err != nil {
			return fmt.Errorf("enable API-only mode: %w", err)
		}
	}
	secret, err := resolveJWTSecret()
	if err != nil {
		return err
	}
	if err := secrets.LoadMasterKey(); err != nil {
		return fmt.Errorf("load secrets master key: %w", err)
	}
	auth.SetJWTSecret(secret)
	if !opt.SkipScanInit {
		if _, err := scanners.LoadAndInstall(ctx, scanners.RuntimeFromEnv()); err != nil {
			wolflog.L().Warn().Err(err).Msg("scanner backend not ready at startup; UI will surface diagnostics")
		}
	}
	store, err := OpenStore()
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = store.Close() }()
	controlbind.Bind(store)

	if opt.Version != "" && opt.Version != "dev" {
		routes.AppVersion = opt.Version
	}
	if opt.Commit != "" {
		routes.BuildCommit = opt.Commit
	}
	if opt.BuildDate != "" {
		routes.BuildDate = opt.BuildDate
	}
	if opt.CoreCommit != "" {
		routes.CoreCommit = opt.CoreCommit
	}
	if opt.OverlayVersion != "" {
		routes.OverlayVersion = opt.OverlayVersion
	}
	if opt.OverlayCommit != "" {
		routes.OverlayCommit = opt.OverlayCommit
	}

	if err := bootstrapAdmin(ctx, store); err != nil {
		return fmt.Errorf("admin bootstrap: %w", err)
	}
	wolflog.L().Info().
		Str("tag", scanners.ResolvedTag()).
		Str("default_image", scanners.ActiveImageRefs()["default"]).
		Msg("resolved scanner image tag (override with WOLF_SCANNERS_TAG)")

	srv := api.NewServer(store, opt.Addr)
	wolflog.L().Info().Str("addr", opt.Addr).Msg("wolf serve")
	go routes.StartScheduleLoop(ctx, routes.DefaultHandler)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start() }()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, sc := context.WithTimeout(context.Background(), 15*time.Second)
		defer sc()
		return srv.Shutdown(shutdownCtx)
	}
}

func resolveJWTSecret() ([]byte, error) {
	secret := []byte(envOr("WOLF_MASTER_KEY", ""))
	if len(secret) > 0 {
		return secret, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home directory for JWT secret: %w", err)
	}
	secret, err = secrets.LoadOrCreateKeyFile(filepath.Join(home, ".wolf", "jwt-secret"), func() ([]byte, error) {
		key := make([]byte, 32)
		_, err := cryptorand.Read(key)
		return key, err
	})
	if err != nil {
		return nil, fmt.Errorf("load JWT secret: %w", err)
	}
	if len(secret) < 32 {
		return nil, fmt.Errorf("jwt-secret must be at least 32 bytes; existing file was preserved")
	}
	return secret, nil
}

func bootstrapAdmin(ctx context.Context, store db.Store) error {
	email := strings.TrimSpace(strings.ToLower(os.Getenv("WOLF_ADMIN_EMAIL")))
	password := os.Getenv("WOLF_ADMIN_PASSWORD")
	if email == "" && password == "" {
		return nil
	}
	if email == "" || password == "" {
		return fmt.Errorf("WOLF_ADMIN_EMAIL and WOLF_ADMIN_PASSWORD must both be configured")
	}
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || address.Name != "" {
		return fmt.Errorf("WOLF_ADMIN_EMAIL must be a valid email address")
	}
	if message, ok := auth.ValidatePassword(password); !ok {
		return fmt.Errorf("WOLF_ADMIN_PASSWORD: %s", message)
	}
	existing, err := store.GetUserByEmail(ctx, email)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("look up bootstrap account: %w", err)
	}
	if err == nil && existing != nil {
		return nil
	}
	if err := routes.CheckCommunityLimit(ctx, store, "users"); err != nil {
		return err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return fmt.Errorf("hash admin password: %w", err)
	}
	now := time.Now().UTC()
	return store.CreateUser(ctx, &models.User{
		ID: uuid.New().String(), Email: email, PasswordHash: hash,
		Role: models.RoleAdmin, CreatedAt: now, UpdatedAt: now,
	})
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
