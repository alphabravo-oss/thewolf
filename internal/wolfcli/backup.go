package wolfcli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/alphabravocompany/thewolf/internal/db"
)

func newDBCmd() *cobra.Command {
	var sqlitePath, postgresDSN string
	migrate := &cobra.Command{
		Use:   "migrate-from-sqlite",
		Short: "Copy a stopped SQLite deployment into an empty PostgreSQL database",
		Long: "Runs the PostgreSQL migrations, then copies every table in one transaction.\n" +
			"The SQLite file is opened read-only. Counts are verified; any mismatch rolls back.\n" +
			"--postgres defaults to WOLF_DB_DSN when WOLF_DB_DRIVER=postgres (keeps the password out of ps).",
		RunE: func(cmd *cobra.Command, args []string) error {
			if postgresDSN == "" && strings.EqualFold(os.Getenv("WOLF_DB_DRIVER"), "postgres") {
				postgresDSN = os.Getenv("WOLF_DB_DSN")
			}
			if sqlitePath == "" || postgresDSN == "" {
				return fmt.Errorf("--sqlite and --postgres are required")
			}
			started := time.Now()
			if err := db.MigrateSQLiteToPostgres(cmd.Context(), sqlitePath, postgresDSN, cmd.OutOrStdout()); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "migration committed in %s\n", time.Since(started).Round(time.Millisecond))
			return nil
		},
	}
	migrate.Flags().StringVar(&sqlitePath, "sqlite", "", "source wolf.db path (opened read-only)")
	migrate.Flags().StringVar(&postgresDSN, "postgres", "", "target PostgreSQL DSN")
	cmd := &cobra.Command{Use: "db", Short: "Database maintenance"}
	cmd.AddCommand(migrate)
	return cmd
}

// errPostgresBackup is returned by the retired SQLite tar commands.
var errPostgresBackup = errors.New("wolf backup/restore only handled SQLite, which is no longer supported; " +
	"back up PostgreSQL with pg_dump/pg_restore or CNPG Barman (docs/deployment.md)")

func newBackupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "backup",
		Short: "Removed: use pg_dump or CNPG Barman",
		RunE:  func(*cobra.Command, []string) error { return errPostgresBackup },
	}
}

func newRestoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restore",
		Short: "Removed: use pg_restore or CNPG Barman",
		RunE:  func(*cobra.Command, []string) error { return errPostgresBackup },
	}
}
