package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Querier is satisfied by both *sql.DB and *sql.Tx, so repository helpers can
// run inside or outside a transaction.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ErrNotFound is returned when a lookup matches no row.
var ErrNotFound = errors.New("store: not found")

// ErrConflict is returned when a write violates a uniqueness constraint.
var ErrConflict = errors.New("store: conflicting value")

// ErrInUse is returned when a row cannot be deleted because it is referenced.
var ErrInUse = errors.New("store: row is still referenced")

// classify maps driver errors onto the package's sentinel errors so callers
// can react without matching on driver-specific text everywhere.
func classify(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "UNIQUE constraint failed"),
		strings.Contains(msg, "constraint failed: UNIQUE"):
		return ErrConflict
	case strings.Contains(msg, "FOREIGN KEY constraint failed"):
		return ErrInUse
	}
	return err
}

// placeholders returns "?, ?, ..." with n entries, for an IN clause whose
// values are always bound as parameters.
func placeholders(n int) string {
	if n <= 0 {
		return "NULL"
	}
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

// nullString converts an empty string to a SQL NULL.
func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// nullInt64 converts a zero id to a SQL NULL.
func nullInt64(v int64) sql.NullInt64 {
	if v == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: v, Valid: true}
}
