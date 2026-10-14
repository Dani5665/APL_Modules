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

// listTimeout is for the whole-customer list, which is a much larger result.
const listTimeout = 20 * time.Second

// Directory reads the external database. All access is read-only.
type Directory struct {
	db *sql.DB
}

var _ external.Directory = (*Directory)(nil)

// Open prepares the external database. The pool is deliberately small: this
// application issues only a handful of short lookups per request.
//
// When the database cannot be reached right now, Open still returns a usable
// Directory together with the error: the pool connects again on every query,
// so the application can start and recover once the database is back. Only a
// nil Directory means Open failed outright.
func Open(ctx context.Context, dsn string) (*Directory, error) {
	db, err := sql.Open("sqlserver", dsn)
	if err != nil {
		return nil, fmt.Errorf("%w: open: %v", external.ErrUnavailable, err)
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)

	d := &Directory{db: db}
	pingCtx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return d, fmt.Errorf("%w: connect: %v", external.ErrUnavailable, err)
	}
	return d, nil
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
	var mandantID sql.NullString
	err := d.db.QueryRowContext(ctx, qClientByCode, strings.TrimSpace(code)).
		Scan(&c.Code, &c.Name, &c.Store, &mandantID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, external.ErrNotFound
	case err != nil:
		return nil, wrapQueryErr("GetClientByCode", err)
	}
	c.Object = c.Store
	c.MandantID = mandantID.String
	trimClient(&c)
	return &c, nil
}

// ListClientsByStores implements external.Directory.
func (d *Directory) ListClientsByStores(ctx context.Context, mandantIDs []string) ([]external.Client, error) {
	if len(mandantIDs) == 0 {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	args := make([]any, len(mandantIDs))
	for i, v := range mandantIDs {
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
		var mandantID sql.NullString
		if err := rows.Scan(&c.Code, &c.Name, &c.Store, &mandantID); err != nil {
			return nil, wrapQueryErr("ListClientsByStores scan", err)
		}
		c.Object = c.Store
		c.MandantID = mandantID.String
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
	c.MandantID = strings.TrimSpace(c.MandantID)
}

// wrapQueryErr marks any transport or query failure as unavailable, which the
// HTTP layer renders as the Bulgarian "database unavailable" page.
func wrapQueryErr(op string, err error) error {
	return fmt.Errorf("%w: %s: %v", external.ErrUnavailable, op, err)
}

// scanCustomer reads one customer row; NULLs become empty strings.
func scanCustomer(s interface{ Scan(...any) error }) (external.Customer, error) {
	var code, name, mandant, addr, phone, person sql.NullString
	if err := s.Scan(&code, &name, &mandant, &addr, &phone, &person); err != nil {
		return external.Customer{}, err
	}
	trim := func(n sql.NullString) string { return strings.TrimSpace(n.String) }
	return external.Customer{
		Code: trim(code), Name: trim(name), Mandant: trim(mandant),
		Address: trim(addr), Phone: trim(phone), AccountablePerson: trim(person),
	}, nil
}

// ListCustomers implements external.Directory.
func (d *Directory) ListCustomers(ctx context.Context) ([]external.Customer, error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()

	rows, err := d.db.QueryContext(ctx, qCustomers)
	if err != nil {
		return nil, wrapQueryErr("ListCustomers", err)
	}
	defer rows.Close()

	var all []external.Customer
	for rows.Next() {
		c, err := scanCustomer(rows)
		if err != nil {
			return nil, wrapQueryErr("ListCustomers scan", err)
		}
		if c.Code != "" {
			all = append(all, c)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, wrapQueryErr("ListCustomers", err)
	}
	return external.FirstPerCode(all), nil
}

// GetCustomer implements external.Directory.
func (d *Directory) GetCustomer(ctx context.Context, code string) (*external.Customer, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	c, err := scanCustomer(d.db.QueryRowContext(ctx, qCustomerByCode, strings.TrimSpace(code)))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, external.ErrNotFound
	case err != nil:
		return nil, wrapQueryErr("GetCustomer", err)
	}
	return &c, nil
}

// ListProfiles implements external.Directory.
func (d *Directory) ListProfiles(ctx context.Context, code string) ([]external.Profile, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	rows, err := d.db.QueryContext(ctx, qProfiles, strings.TrimSpace(code))
	if err != nil {
		return nil, wrapQueryErr("ListProfiles", err)
	}
	defer rows.Close()

	var out []external.Profile
	for rows.Next() {
		var login, password, mandant sql.NullString
		var last sql.NullTime
		var active sql.NullBool
		if err := rows.Scan(&login, &password, &mandant, &last, &active); err != nil {
			return nil, wrapQueryErr("ListProfiles scan", err)
		}
		p := external.Profile{
			Login:    strings.TrimSpace(login.String),
			Password: password.String,
			Mandant:  strings.TrimSpace(mandant.String),
			Active:   active.Bool,
		}
		if last.Valid {
			p.LastLogin = last.Time
		}
		if p.Login != "" {
			out = append(out, p)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, wrapQueryErr("ListProfiles", err)
	}
	return out, nil
}

// LoginExists implements external.Directory.
func (d *Directory) LoginExists(ctx context.Context, login string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	var one int
	err := d.db.QueryRowContext(ctx, qLoginExists, strings.TrimSpace(login)).Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, wrapQueryErr("LoginExists", err)
	}
	return true, nil
}

// ListTransports implements external.Directory.
func (d *Directory) ListTransports(ctx context.Context, mandantID string) ([]string, error) {
	mandantID = strings.TrimSpace(mandantID)
	if mandantID == "" {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	rows, err := d.db.QueryContext(ctx, qTransports, mandantID)
	if err != nil {
		return nil, wrapQueryErr("ListTransports", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var s sql.NullString
		if err := rows.Scan(&s); err != nil {
			return nil, wrapQueryErr("ListTransports scan", err)
		}
		if v := strings.TrimSpace(s.String); v != "" {
			out = append(out, v)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, wrapQueryErr("ListTransports", err)
	}
	return out, nil
}
