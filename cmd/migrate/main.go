package main

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"time"

	"backend-go/internal/config"
	"backend-go/internal/pkg/database"
	"backend-go/internal/pkg/database/migration"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	cfg := config.LoadConfig()
	if cfg.DBPrimaryDSN == "" {
		slog.Error("DB_PRIMARY_DSN is required to run migrations")
		os.Exit(1)
	}

	db, err := sql.Open("pgx", cfg.DBPrimaryDSN)
	if err != nil {
		slog.Error("Failed to open PostgreSQL primary", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	database.ConfigurePool(db, database.PoolConfig{
		MaxOpenConns:    1,
		MaxIdleConns:    1,
		ConnMaxLifetime: cfg.DBConnMaxLifetime,
		ConnMaxIdleTime: cfg.DBConnMaxIdleTime,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := migration.NewEngine().Run(ctx, db); err != nil {
		slog.Error("Database migrations failed", "error", err)
		os.Exit(1)
	}
	slog.Info("Database migrations completed")
}
