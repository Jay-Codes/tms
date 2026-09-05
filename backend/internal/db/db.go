// Package db owns the Postgres connection pool and hosts the sqlc-generated
// query code (see sqlc.yaml, queries/ and the generated sqlc/ package).
//
// Per SPEC §2.1 every org-scoped query must take org_id as a mandatory
// parameter; the repository layer built on top of this pool enforces that.
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	connectTimeout    = 5 * time.Second
	defaultMaxIdleDur = 5 * time.Minute
)

// DefaultMaxConns is the pool size used when the caller passes nothing usable
// (config.DefaultDBMaxConns is the DB_MAX_CONNS default that normally feeds it).
// Size it against the deployment's Postgres max_connections.
const DefaultMaxConns = 20

// Pool wraps a pgx connection pool.
type Pool struct {
	*pgxpool.Pool
}

// Open parses the DSN and creates a pool of at most maxConns connections
// (DefaultMaxConns when maxConns is not positive). It does not require the
// database to be reachable — callers can start serving in a degraded state and
// let /healthz report the outage.
func Open(ctx context.Context, dsn string, maxConns int) (*Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("db: parse dsn: %w", err)
	}
	if maxConns <= 0 {
		maxConns = DefaultMaxConns
	}
	cfg.MaxConns = int32(maxConns)
	cfg.MaxConnIdleTime = defaultMaxIdleDur
	cfg.ConnConfig.ConnectTimeout = connectTimeout

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: new pool: %w", err)
	}
	return &Pool{Pool: pool}, nil
}

// Ping checks database reachability (implements httpserver.Pinger).
func (p *Pool) Ping(ctx context.Context) error {
	if p == nil || p.Pool == nil {
		return fmt.Errorf("db: no pool")
	}
	return p.Pool.Ping(ctx)
}

// Close releases all pooled connections.
func (p *Pool) Close() {
	if p != nil && p.Pool != nil {
		p.Pool.Close()
	}
}
