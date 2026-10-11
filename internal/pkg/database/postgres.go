package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// OpenPostgresGroup opens and verifies a primary PostgreSQL connection and zero
// or more read replicas. The caller must close the returned group.
func OpenPostgresGroup(ctx context.Context, primaryDSN string, replicaDSNs []string, cfg PoolConfig) (*DBGroup, error) {
	if primaryDSN == "" {
		return nil, fmt.Errorf("PostgreSQL primary DSN is required")
	}

	opened := make([]*sql.DB, 0, len(replicaDSNs)+1)
	open := func(role, dsn string) (*sql.DB, error) {
		if dsn == "" {
			return nil, fmt.Errorf("PostgreSQL %s DSN is empty", role)
		}
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			return nil, fmt.Errorf("open PostgreSQL %s: %w", role, err)
		}
		ConfigurePool(db, cfg)
		if err := db.PingContext(ctx); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("connect to PostgreSQL %s: %w", role, err)
		}
		opened = append(opened, db)
		return db, nil
	}

	primary, err := open("primary", primaryDSN)
	if err != nil {
		return nil, err
	}
	replicas := make([]*sql.DB, 0, len(replicaDSNs))
	for i, dsn := range replicaDSNs {
		replica, err := open(fmt.Sprintf("replica %d", i), dsn)
		if err != nil {
			for _, db := range opened {
				_ = db.Close()
			}
			return nil, err
		}
		replicas = append(replicas, replica)
	}
	return NewDBGroup(primary, replicas...), nil
}

// Close closes the primary and all configured replica pools.
func (g *DBGroup) Close() error {
	if g == nil {
		return nil
	}
	var closeErr error
	if g.Primary != nil {
		closeErr = errors.Join(closeErr, g.Primary.Close())
	}
	for _, replica := range g.Replicas {
		if replica == nil {
			continue
		}
		if err := replica.Close(); err != nil {
			closeErr = errors.Join(closeErr, err)
		}
	}
	return closeErr
}
