package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type migration struct {
	version int
	name    string
	sql     string
}

// Migrate applies every embedded migration that has not been recorded yet,
// each in its own transaction, in ascending version order.
func (db *DB) Migrate(ctx context.Context) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			name       TEXT NOT NULL,
			applied_at TEXT NOT NULL
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied, err := db.appliedVersions(ctx)
	if err != nil {
		return err
	}

	all, err := loadMigrations()
	if err != nil {
		return err
	}

	for _, m := range all {
		if applied[m.version] {
			continue
		}
		if err := db.InTx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, m.sql); err != nil {
				return fmt.Errorf("apply migration %04d_%s: %w", m.version, m.name, err)
			}
			_, err := tx.ExecContext(ctx,
				`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
				m.version, m.name, time.Now().UTC().Format(time.RFC3339))
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) appliedVersions(ctx context.Context) (map[int]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

// loadMigrations reads the embedded files, whose names must be
// NNNN_description.sql.
func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}

	var out []migration
	seen := map[int]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".sql")
		prefix, name, ok := strings.Cut(base, "_")
		if !ok {
			return nil, fmt.Errorf("migration %s: expected NNNN_description.sql", e.Name())
		}
		version, err := strconv.Atoi(prefix)
		if err != nil {
			return nil, fmt.Errorf("migration %s: %q is not a version number", e.Name(), prefix)
		}
		if other, dup := seen[version]; dup {
			return nil, fmt.Errorf("migrations %s and %s share version %d", other, e.Name(), version)
		}
		seen[version] = e.Name()

		body, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: version, name: name, sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}
