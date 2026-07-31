// Package database owns the single PostgreSQL connection pool used by every
// repository. It deliberately does NOT run GORM AutoMigrate — schema
// ownership belongs to the versioned SQL migrations (see /database/schema.sql
// and the migrate target in the Makefile), not to whatever struct tags
// happen to be in the Go code that day. Letting an ORM own production schema
// is how you end up with drift nobody can explain.
package database

import (
	"fmt"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/replypilot/backend/internal/config"
)

func New(cfg config.DBConfig, gormLogger gormlogger.Interface) (*gorm.DB, error) {
	dsn := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Name, cfg.SSLMode,
	)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: gormLogger,
		NowFunc: func() time.Time {
			return time.Now().UTC()
		},
	})
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get underlying sql.DB: %w", err)
	}

	// Pool sizing matters more than it looks: with N service replicas each
	// holding MaxOpenConns connections, total connections against Postgres
	// is N * MaxOpenConns. Size this per-replica limit with the fleet size
	// in mind, and put PgBouncer in front at any real scale — see
	// docs/ARCHITECTURE.md §10.
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(time.Duration(cfg.ConnMaxLifetimeMinutes) * time.Minute)

	return db, nil
}
