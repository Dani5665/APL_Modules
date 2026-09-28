// Package store owns the local SQLite database: connection setup, schema
// migrations and the repositories used by the rest of the application.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// DB wraps the SQLite handle used throughout the application.
type DB struct {
	*sql.DB
	path string
}

// Path returns the database file path.
func (db *DB) Path() string { return db.path }

// Open prepares the database file and returns a ready connection pool with
// the schema migrated to the latest version.
//
// SQLite is a single-writer store and this application is a single instance,
// so the pool is deliberately capped at one connection: concurrent writers
// would only trade "busy" errors for queueing that the pool does anyway.
func Open(ctx context.Context, path string) (*DB, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("create data directory %s: %w", dir, err)
		}
	}

	dsn := "file:" + url.PathEscape(path) + "?" + url.Values{
		"_pragma": {
			"journal_mode(WAL)",
			"busy_timeout(5000)",
			"foreign_keys(ON)",
			"synchronous(NORMAL)",
		},
		"_time_format": {"sqlite"},
	}.Encode()

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(0)

	if err := sqlDB.PingContext(ctx); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("connect to sqlite %s: %w", path, err)
	}

	db := &DB{DB: sqlDB, path: path}
	if err := db.Migrate(ctx); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return db, nil
}

// InTx runs fn inside a transaction, committing on success and rolling back
// on error or panic.
func (db *DB) InTx(ctx context.Context, fn func(*sql.Tx) error) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// Backup writes a consistent copy of the database to dest using VACUUM INTO,
// which works while the application keeps serving requests.
func (db *DB) Backup(ctx context.Context, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	// VACUUM INTO refuses to overwrite, so clear a leftover from a crashed run.
	if err := os.Remove(dest); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale backup %s: %w", dest, err)
	}
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, dest); err != nil {
		return fmt.Errorf("vacuum into %s: %w", dest, err)
	}
	return nil
}

// Healthy reports whether the database answers a trivial query.
func (db *DB) Healthy(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var one int
	if err := db.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil {
		return fmt.Errorf("sqlite health check: %w", err)
	}
	return nil
}
