package serve

import (
	"context"
	cryptorand "crypto/rand"
	"fmt"
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
	driver := envOr("WOLF_DB_DRIVER", "sqlite")
	switch strings.ToLower(driver) {
	case "sqlite":
		dsn := os.Getenv("WOLF_DB_DSN")
		if dsn == "" {
			home, _ := os.UserHomeDir()
			_ = os.MkdirAll(home+"/.wolf", 0o750)
			dsn = home + "/.wolf/wolf.db"
		}
		return db.NewSQLite(dsn)
	case "postgres":
		dsn := os.Getenv("WOLF_DB_DSN")
		if dsn == "" {
			return nil, fmt.Errorf("WOLF_DB_DSN required for postgres driver")
		}
		return db.NewPostgres(dsn)
	default:
		return nil, fmt.Errorf("unknown db driver %q", driver)
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
	if !opt.SkipScanInit {
		if _, err := scanners.LoadAndInstall(ctx); err != nil {
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

	secret, err := resolveJWTSecret()
	if err != nil {
		return err
	}
	auth.SetJWTSecret(secret)
	if err := secrets.LoadMasterKey(); err != nil {
		wolflog.L().Warn().Err(err).Msg("secrets master key unavailable; the secrets store will reject writes")
	}
	if err := bootstrapAdmin(ctx, store); err != nil {
		wolflog.L().Warn().Err(err).Msg("admin bootstrap skipped")
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
	home, herr := os.UserHomeDir()
	if herr != nil || home == "" {
		secret = make([]byte, 32)
		if _, err := cryptorand.Read(secret); err != nil {
			return nil, fmt.Errorf("read CSPRNG for JWT secret: %w", err)
		}
		wolflog.L().Warn().Msg("WOLF_MASTER_KEY unset and no home dir; tokens won't survive restart")
		return secret, nil
	}
	path := filepath.Join(home, ".wolf", "jwt-secret")
	if data, rerr := os.ReadFile(path); rerr == nil && len(data) >= 32 {
		return data, nil
	}
	secret = make([]byte, 32)
	if _, err := cryptorand.Read(secret); err != nil {
		return nil, fmt.Errorf("read CSPRNG for JWT secret: %w", err)
	}
	if mkErr := os.MkdirAll(filepath.Dir(path), 0o700); mkErr == nil {
		if wErr := os.WriteFile(path, secret, 0o600); wErr == nil {
			wolflog.L().Info().Str("path", path).Msg("JWT secret persisted; tokens will survive restart")
		} else {
			wolflog.L().Warn().Err(wErr).Msg("WOLF_MASTER_KEY unset and ~/.wolf/jwt-secret not writable; tokens won't survive restart")
		}
	}
	return secret, nil
}

func bootstrapAdmin(ctx context.Context, store db.Store) error {
	email := strings.TrimSpace(strings.ToLower(os.Getenv("WOLF_ADMIN_EMAIL")))
	password := os.Getenv("WOLF_ADMIN_PASSWORD")
	if email == "" || password == "" {
		return nil
	}
	if len(password) < 12 {
		return fmt.Errorf("WOLF_ADMIN_PASSWORD must be at least 12 characters")
	}
	if existing, _ := store.GetUserByEmail(ctx, email); existing != nil {
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
