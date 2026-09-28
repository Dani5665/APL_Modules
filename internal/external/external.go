// Package external isolates every read of the third-party MSSQL database
// behind one interface, so the rest of the application never speaks SQL to it
// and can run entirely against the in-memory mock.
package external

import (
	"context"
	"errors"
)

// Saler is a salesperson as the external directory knows them.
type Saler struct {
	Login string
	// Store is the external store value, compared against Store.ExternalValue
	// in the local database.
	Store string
}

// Client is one customer. A client is identified by a 9-digit code
// (CUSTOMER_NUMBER in the external directory).
type Client struct {
	Code string
	Name string
	// Object ("Обект на клиента") and Store both come from the same
	// MANDANT_NAME column in the external directory's V_CATALOG_USERS view -
	// confirmed against production - so they always carry the same value for
	// a client read from there. See DECISIONS.md.
	Object string
	Store  string
}

// Directory is the read-only view of the external database.
type Directory interface {
	// GetSaler returns the salesperson with the given LOGIN (IS_WHO_SALER = 1,
	// ACTIVE = 1), including their store.
	GetSaler(ctx context.Context, login string) (*Saler, error)

	// GetClientByCode returns the client with the given 9-digit code
	// (CUSTOMER_NUMBER).
	GetClientByCode(ctx context.Context, code string) (*Client, error)

	// ListClientLogins returns the client's own usernames (IS_WHO_SALER = 0,
	// ACTIVE = 1), sorted, for the given 9-digit client code
	// (CUSTOMER_NUMBER).
	ListClientLogins(ctx context.Context, clientCode string) ([]string, error)

	// ListClientsByStores returns every client tied to any of the given
	// external store values.
	ListClientsByStores(ctx context.Context, storeValues []string) ([]Client, error)

	// Ping reports whether the directory is reachable.
	Ping(ctx context.Context) error

	// Close releases any resources held by the implementation.
	Close() error
}

// ErrNotFound is returned when a lookup finds no matching row. Callers must
// treat it as "no access" rather than surfacing it as a system failure.
var ErrNotFound = errors.New("external: not found")

// ErrNotConfigured marks a query whose real SQL has not been supplied yet
// (see the PLACEHOLDER entries in README.md).
var ErrNotConfigured = errors.New("external: query not configured")

// ErrUnavailable wraps any transport or query failure against the external
// database. It is what the HTTP layer turns into the Bulgarian
// "database unavailable" page.
var ErrUnavailable = errors.New("external: database unavailable")
