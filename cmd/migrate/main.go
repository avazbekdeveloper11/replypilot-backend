// Command migrate applies or rolls back database migrations using
// golang-migrate against the embedded SQL files in internal/migrations.
//
// It deliberately reuses internal/config so the database connection details
// come from exactly the same place as the API service — one source of truth
// for how to reach Postgres, no separate DATABASE_URL to drift out of sync.
//
// Usage:
//
//	migrate up            apply all pending migrations
//	migrate down          roll back the most recent migration (one step)
//	migrate down-all      roll back every migration (destructive; dev only)
//	migrate version       print current version and dirty state
//	migrate force <V>     mark version V as current without running it
//	                      (recovery only — for a migration left "dirty" by a
//	                       crash mid-apply; verify the DB by hand first)
//
// Migrations are intentionally NOT run automatically on API startup. Coupling
// schema changes to app boot means every rolling-deploy replica races to
// migrate, and a bad migration takes down the whole service instead of one
// controlled job. Run this as a discrete step in CI/CD (or the docker-compose
// `migrate` service) before the API rolls out — see docs/ARCHITECTURE.md §9.
package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"

	"github.com/golang-migrate/migrate/v4"
	// Blank import: registers the "postgres" database driver with migrate via
	// its init(), so migrate.NewWithSourceInstance can resolve a postgres://
	// URL. It's never referenced directly — the registration is the point.
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/replypilot/backend/internal/config"
	"github.com/replypilot/backend/internal/migrations"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return errors.New("usage: migrate <up|down|down-all|version|force <V>>")
	}
	command := os.Args[1]

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	m, err := newMigrator(cfg.DB)
	if err != nil {
		return err
	}
	// Both the source (embedded FS) and database handles must be closed;
	// Close returns both errors.
	defer func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil {
			fmt.Fprintln(os.Stderr, "migrate: close source:", srcErr)
		}
		if dbErr != nil {
			fmt.Fprintln(os.Stderr, "migrate: close database:", dbErr)
		}
	}()

	switch command {
	case "up":
		return runNoChangeOK(m.Up(), "no pending migrations")
	case "down":
		return runNoChangeOK(m.Steps(-1), "nothing to roll back")
	case "down-all":
		return runNoChangeOK(m.Down(), "nothing to roll back")
	case "version":
		return printVersion(m)
	case "force":
		return force(m)
	default:
		return fmt.Errorf("unknown command %q (want up|down|down-all|version|force)", command)
	}
}

// newMigrator wires the embedded SQL files (iofs source) to a Postgres
// database driver built from the app config.
func newMigrator(db config.DBConfig) (*migrate.Migrate, error) {
	source, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("open embedded migrations: %w", err)
	}

	dsn := postgresDSN(db)

	// migrate.NewWithSourceInstance opens its own *sql.DB from the URL, so
	// there is no shared pool with the API — a migration run is fully
	// isolated from the running service.
	m, err := migrate.NewWithSourceInstance("iofs", source, dsn)
	if err != nil {
		return nil, fmt.Errorf("init migrator: %w", err)
	}
	return m, nil
}

// postgresDSN builds a URL-form DSN and URL-encodes the password so special
// characters (@, :, /, spaces) in a real production password don't corrupt
// the connection string — a subtle but real production failure otherwise.
func postgresDSN(db config.DBConfig) string {
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(db.User, db.Password),
		Host:   fmt.Sprintf("%s:%d", db.Host, db.Port),
		Path:   db.Name,
	}
	q := u.Query()
	q.Set("sslmode", db.SSLMode)
	u.RawQuery = q.Encode()
	return u.String()
}

// runNoChangeOK treats migrate.ErrNoChange as success — running `up` when
// already up to date is not an error condition for a deploy pipeline.
func runNoChangeOK(err error, noChangeMsg string) error {
	if errors.Is(err, migrate.ErrNoChange) {
		fmt.Println("migrate:", noChangeMsg)
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Println("migrate: ok")
	return nil
}

func printVersion(m *migrate.Migrate) error {
	v, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		fmt.Println("migrate: no migrations applied yet")
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Printf("migrate: version=%d dirty=%t\n", v, dirty)
	return nil
}

func force(m *migrate.Migrate) error {
	if len(os.Args) < 3 {
		return errors.New("force requires a version: migrate force <V>")
	}
	v, err := strconv.Atoi(os.Args[2])
	if err != nil {
		return fmt.Errorf("force version must be an integer: %w", err)
	}
	if err := m.Force(v); err != nil {
		return err
	}
	fmt.Printf("migrate: forced version to %d\n", v)
	return nil
}
