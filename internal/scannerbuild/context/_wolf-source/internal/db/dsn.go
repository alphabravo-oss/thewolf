package db

import (
	"bufio"
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

// DatabaseConfig is how Wolf opens the store. Env wins over database.env.
type DatabaseConfig struct {
	Driver     string
	DSN        string
	EnvManaged bool
	FileSet    bool
}

func databaseOverridePath() string {
	if p := strings.TrimSpace(os.Getenv("WOLF_DB_FILE")); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".wolf", "database.env")
}

// ResolveDatabase reads WOLF_DB_* then ~/.wolf/database.env (or WOLF_DB_FILE).
func ResolveDatabase() DatabaseConfig {
	cfg := DatabaseConfig{
		Driver:     strings.TrimSpace(os.Getenv("WOLF_DB_DRIVER")),
		DSN:        strings.TrimSpace(os.Getenv("WOLF_DB_DSN")),
		EnvManaged: strings.TrimSpace(os.Getenv("WOLF_DB_DSN")) != "" || strings.TrimSpace(os.Getenv("WOLF_DB_DRIVER")) != "",
	}
	fd, fdsn, ok := loadOverride()
	cfg.FileSet = ok
	if cfg.Driver == "" {
		cfg.Driver = fd
	}
	if cfg.Driver == "" {
		cfg.Driver = "sqlite"
	}
	if cfg.DSN == "" {
		cfg.DSN = fdsn
	}
	return cfg
}

func loadOverride() (driver, dsn string, ok bool) {
	path := databaseOverridePath()
	if path == "" {
		return "", "", false
	}
	f, err := os.Open(path)
	if err != nil {
		return "", "", false
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch k {
		case "WOLF_DB_DRIVER":
			driver = v
		case "WOLF_DB_DSN":
			dsn = v
		}
	}
	return driver, dsn, driver != "" || dsn != ""
}

// SavePostgresOverride writes a postgres DSN for the next process start.
func SavePostgresOverride(dsn string) error {
	dsn = strings.TrimSpace(dsn)
	if err := validatePostgresDSN(dsn); err != nil {
		return err
	}
	path := databaseOverridePath()
	if path == "" {
		return fmt.Errorf("no home directory for database.env")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	body := "# Applied from Settings. Env WOLF_DB_DSN / WOLF_DB_DRIVER wins.\n" +
		"WOLF_DB_DRIVER=postgres\n" +
		"WOLF_DB_DSN=" + dsn + "\n"
	return os.WriteFile(path, []byte(body), 0o600)
}

func validatePostgresDSN(dsn string) error {
	if dsn == "" {
		return fmt.Errorf("dsn required")
	}
	if strings.ContainsAny(dsn, "\n\r") {
		return fmt.Errorf("dsn must be a single line")
	}
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
		return fmt.Errorf("dsn must be a postgres:// URL")
	}
	return nil
}

// PingPostgres opens a throwaway connection. It does not migrate.
func PingPostgres(ctx context.Context, dsn string) error {
	if err := validatePostgresDSN(dsn); err != nil {
		return err
	}
	db, err := sqlx.Open("postgres", dsn)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.PingContext(ctx)
}

// DatabaseView is a password-free snapshot for the admin UI.
type DatabaseView struct {
	Driver     string `json:"driver"`
	User       string `json:"user,omitempty"`
	Host       string `json:"host,omitempty"`
	Database   string `json:"database,omitempty"`
	SSLMode    string `json:"sslmode,omitempty"`
	EnvManaged bool   `json:"env_managed"`
	FileSet    bool   `json:"file_set"`
}

func (c DatabaseConfig) View() DatabaseView {
	v := DatabaseView{Driver: c.Driver, EnvManaged: c.EnvManaged, FileSet: c.FileSet}
	if strings.EqualFold(c.Driver, "postgres") {
		u, err := url.Parse(c.DSN)
		if err == nil && u.Host != "" {
			v.Host = u.Host
			v.Database = strings.TrimPrefix(u.Path, "/")
			v.SSLMode = u.Query().Get("sslmode")
			if u.User != nil {
				v.User = u.User.Username()
			}
		}
		return v
	}
	v.Database = filepath.Base(c.DSN)
	return v
}
