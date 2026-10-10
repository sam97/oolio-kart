// Package postgres opens the Postgres pool, applies the schema migrations,
// and takes advisory locks.
package postgres

import (
	"context"
	"embed"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Open returns a pool for url. It connects lazily, so a database that is down
// fails queries rather than Open.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	return pgxpool.NewWithConfig(ctx, config)
}

// Migrate applies every migration not yet applied. A session lock lets
// several processes call it at once.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	files, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return err
	}
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, files, goose.WithSessionLocker(locker))
	if err != nil {
		return err
	}
	_, err = provider.Up(ctx)
	return err
}

// Lock is a Postgres session-level advisory lock. The lock lives on one
// connection, so a crashed holder releases it when its connection drops.
type Lock struct {
	pool *pgxpool.Pool
	key  int64
}

func NewLock(pool *pgxpool.Pool, key int64) *Lock {
	return &Lock{pool: pool, key: key}
}

// TryLock takes the lock if it is free. ok is false if another session holds
// it. unlock releases it and must be called once ok is true.
func (l *Lock) TryLock(ctx context.Context) (unlock func(), ok bool, err error) {
	conn, err := l.pool.Acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", l.key).Scan(&ok); err != nil || !ok {
		conn.Release()
		return nil, false, err
	}
	unlock = func() {
		// Unlock even if the caller's context is done. If it fails, the
		// connection is closed instead, which also releases the lock.
		if _, err := conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", l.key); err != nil {
			conn.Conn().Close(context.Background())
		}
		conn.Release()
	}
	return unlock, true, nil
}
