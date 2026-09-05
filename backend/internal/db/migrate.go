package db

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver for golang-migrate

	"tms/backend/migrations"
)

func newMigrator(dsn string) (*migrate.Migrate, func(), error) {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return nil, nil, fmt.Errorf("migrate: source: %w", err)
	}

	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("migrate: open: %w", err)
	}

	driver, err := migratepgx.WithInstance(sqlDB, &migratepgx.Config{})
	if err != nil {
		_ = sqlDB.Close()
		return nil, nil, fmt.Errorf("migrate: driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "pgx5", driver)
	if err != nil {
		_ = sqlDB.Close()
		return nil, nil, fmt.Errorf("migrate: instance: %w", err)
	}
	return m, func() { _ = sqlDB.Close() }, nil
}

// MigrateUp applies all pending migrations.
func MigrateUp(dsn string, logger *slog.Logger) error {
	m, closeDB, err := newMigrator(dsn)
	if err != nil {
		return err
	}
	defer closeDB()

	if err := m.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			logger.Info("migrations already up to date")
			return nil
		}
		return fmt.Errorf("migrate up: %w", err)
	}
	v, dirty, _ := m.Version()
	logger.Info("migrations applied", "version", v, "dirty", dirty)
	return nil
}

// MigrateDown rolls back exactly one migration step.
func MigrateDown(dsn string, logger *slog.Logger) error {
	m, closeDB, err := newMigrator(dsn)
	if err != nil {
		return err
	}
	defer closeDB()

	if err := m.Steps(-1); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			logger.Info("nothing to roll back")
			return nil
		}
		return fmt.Errorf("migrate down: %w", err)
	}
	v, dirty, _ := m.Version()
	logger.Info("migration rolled back", "version", v, "dirty", dirty)
	return nil
}
