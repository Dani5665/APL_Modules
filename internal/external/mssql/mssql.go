// Package mssql is the real, read-only implementation of
// external.Directory backed by the third-party SQL Server database.
package mssql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/microsoft/go-mssqldb"

	"haynesproform/internal/external"
)

// queryTimeout caps every individual statement. The external database is not
// ours, so a slow answer must never hold a request open.
const queryTimeout = 5 * time.Second

// Directory reads the external database. All access is read-only.
type Directory struct {
	db *sql.DB
}

var _ external.Directory = (*Directory)(nil)

// Open connects to the external database. The pool is deliberately small:
// this application issues only a handful of short lookups per request.
func Open(ctx context.Context, dsn string) (*Directory, error) {
	db, err := sql.Open("sqlserver", dsn)
	if err != nil {
		return nil, fmt.Errorf("%w: open: %v", external.ErrUnavailable, err)
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, fmt.Errorf("%w: connect: %v", external.ErrUnavailable, err)
	}
	return &Directory{db: db}, nil
}

// Ping implements external.Directory.
func (d *Directory) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	if err := d.db.PingContext(ctx); err != nil {
		return fmt.Errorf("%w: %v", external.ErrUnavailable, err)
	}
	return nil
}

// Close implements external.Directory.
func (d *Directory) Close() error { return d.db.Close() }

// GetSaler implements external.Directory.
func (d *Directory) GetSaler(ctx context.Context, login string) (*external.Saler, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	login = strings.TrimSpace(login)

	var s external.Saler
	err := d.db.QueryRowContext(ctx, qSaler, login).Scan(&s.Login, &s.Store)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, external.ErrNotFound
	case err != nil:
		return nil, wrapQueryErr("GetSaler", err)
	}
	s.Login = strings.TrimSpace(s.Login)
	s.Store = strings.TrimSpace(s.Store)
	return &s, nil
}

// ListClientLogins implements external.Directory.
func (d *Directory) ListClientLogins(ctx context.Context, clientCode string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	rows, err := d.db.QueryContext(ctx, qClientLogins, strings.TrimSpace(clientCode))
	if err != nil {
		return nil, wrapQueryErr("ListClientLogins", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var login string
		if err := rows.Scan(&login); err != nil {
			return nil, wrapQueryErr("ListClientLogins scan", err)
		}
		if login = strings.TrimSpace(login); login != "" {
			out = append(out, login)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, wrapQueryErr("ListClientLogins", err)
	}
	return out, nil
}

// GetClientByCode implements external.Directory.
//
// The view carries one column for what this application shows as both the
// client's store and their "Обект на клиента" - MANDANT_NAME - confirmed
// against production. Object is set equal to Store rather than left empty;
// see the field comment on external.Client.
func (d *Directory) GetClientByCode(ctx context.Context, code string) (*external.Client, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	var c external.Client
	err := d.db.QueryRowContext(ctx, qClientByCode, strings.TrimSpace(code)).
		Scan(&c.Code, &c.Name, &c.Store)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, external.ErrNotFound
	case err != nil:
		return nil, wrapQueryErr("GetClientByCode", err)
	}
	c.Object = c.Store
	trimClient(&c)
	return &c, nil
}

// ListClientsByStores implements external.Directory.
func (d *Directory) ListClientsByStores(ctx context.Context, storeValues []string) ([]external.Client, error) {
	if len(storeValues) == 0 {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	args := make([]any, len(storeValues))
	for i, v := range storeValues {
		args[i] = strings.TrimSpace(v)
	}
	query := strings.ReplaceAll(qClientsByStoresTemplate, inParamsMarker, buildInClause(len(args)))

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrapQueryErr("ListClientsByStores", err)
	}
	defer rows.Close()

	var out []external.Client
	for rows.Next() {
		var c external.Client
		if err := rows.Scan(&c.Code, &c.Name, &c.Store); err != nil {
			return nil, wrapQueryErr("ListClientsByStores scan", err)
		}
		c.Object = c.Store
		trimClient(&c)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapQueryErr("ListClientsByStores", err)
	}
	return out, nil
}

// buildInClause returns "@p1, @p2, ..., @pN" for a parameterised IN list.
func buildInClause(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = fmt.Sprintf("@p%d", i+1)
	}
	return strings.Join(parts, ", ")
}

func trimClient(c *external.Client) {
	c.Code = strings.TrimSpace(c.Code)
	c.Name = strings.TrimSpace(c.Name)
	c.Object = strings.TrimSpace(c.Object)
	c.Store = strings.TrimSpace(c.Store)
}

// wrapQueryErr marks any transport or query failure as unavailable, which the
// HTTP layer renders as the Bulgarian "database unavailable" page.
func wrapQueryErr(op string, err error) error {
	return fmt.Errorf("%w: %s: %v", external.ErrUnavailable, op, err)
}
