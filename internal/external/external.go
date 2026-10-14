// Package external isolates every read of the third-party MSSQL database
// behind one interface, so the rest of the application never speaks SQL to it
// and can run entirely against the in-memory mock.
package external

import (
	"context"
	"errors"
	"time"
)

// Saler is a salesperson as the external directory knows them.
type Saler struct {
	Login string
	// Store is the salesperson's MANDANT_NAME.
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
	// MandantID is the MANDANT_ID of the client's store, which is what stores
	// are matched and searched by.
	MandantID string
}

// Customer is one row of the customer information view
// (V_EXT_CUSTOMERS_INFO), for the "Информация клиенти" page. Any field other
// than Code may be empty.
type Customer struct {
	Code              string
	Name              string
	Mandant           string // the serving store
	Address           string
	Phone             string
	AccountablePerson string
}

// Profile is one of a customer's own accounts in the catalog
// (V_CATALOG_USERS, IS_WHO_SALER = 0).
type Profile struct {
	Login    string
	Password string
	Mandant  string
	// LastLogin is the zero time when the profile has never logged in.
	LastLogin time.Time
	Active    bool
}

// FirstPerCode keeps the first row seen for each customer code, in order. The
// view returns several rows per customer carrying the same information, so
// the first one is used.
func FirstPerCode(rows []Customer) []Customer {
	seen := make(map[string]bool, len(rows))
	out := make([]Customer, 0, len(rows))
	for _, c := range rows {
		if seen[c.Code] {
			continue
		}
		seen[c.Code] = true
		out = append(out, c)
	}
	return out
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
	// MANDANT_IDs.
	ListClientsByStores(ctx context.Context, mandantIDs []string) ([]Client, error)

	// ListCustomers returns every customer (one entry per customer code), for
	// the customer information list.
	ListCustomers(ctx context.Context) ([]Customer, error)

	// GetCustomer returns the information about one customer by code.
	GetCustomer(ctx context.Context, code string) (*Customer, error)

	// LoginExists reports whether any catalog account (customer or
	// salesperson) already uses the login.
	LoginExists(ctx context.Context, login string) (bool, error)

	// ListTransports returns the transport options of a serving store, by its
	// mandant id, sorted.
	ListTransports(ctx context.Context, mandantID string) ([]string, error)

	// ListProfiles returns the customer's own catalog profiles, with their
	// passwords, for the given code. An empty result means no profile exists.
	ListProfiles(ctx context.Context, code string) ([]Profile, error)

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
