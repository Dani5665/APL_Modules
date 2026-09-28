package store

import (
	"context"
	"strings"

	"haynesproform/internal/dates"
)

const activationColumns = `id, client_code, client_name, client_object, client_store, username,
	module, tier, start_date, end_date, revoked_at, source_request_id, created_by_admin_id,
	created_at, updated_at`

func scanActivation(s interface{ Scan(...any) error }) (*Activation, error) {
	var a Activation
	err := s.Scan(&a.ID, &a.ClientCode, &a.ClientName, &a.ClientObject, &a.ClientStore, &a.Username,
		&a.Module, &a.Tier, &a.StartDate, &a.EndDate, &a.RevokedAt, &a.SourceRequestID,
		&a.CreatedByAdminID, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, classify(err)
	}
	return &a, nil
}

// InsertActivation stores one granted (username x module) row. It accepts a
// Querier so an approval can create every row in a single transaction.
func (db *DB) InsertActivation(ctx context.Context, q Querier, a Activation) (int64, error) {
	now := dates.NowUTC()
	res, err := q.ExecContext(ctx,
		`INSERT INTO activations
			(client_code, client_name, client_object, client_store, username, module, tier,
			 start_date, end_date, source_request_id, created_by_admin_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ClientCode, a.ClientName, a.ClientObject, a.ClientStore, a.Username, a.Module, a.Tier,
		a.StartDate, a.EndDate, a.SourceRequestID, a.CreatedByAdminID, now, now)
	if err != nil {
		return 0, classify(err)
	}
	return res.LastInsertId()
}

// ActivationByID loads one activation.
func (db *DB) ActivationByID(ctx context.Context, id int64) (*Activation, error) {
	return scanActivation(db.QueryRowContext(ctx,
		`SELECT `+activationColumns+` FROM activations WHERE id = ?`, id))
}

// UpdateActivation replaces the editable fields of an activation, including
// the snapshotted client data (PLACEHOLDER-D, resolved; see DECISIONS.md).
func (db *DB) UpdateActivation(ctx context.Context, a Activation) error {
	_, err := db.ExecContext(ctx,
		`UPDATE activations SET
			client_code = ?, client_name = ?, client_object = ?, client_store = ?,
			username = ?, module = ?, tier = ?, start_date = ?, end_date = ?, updated_at = ?
		 WHERE id = ?`,
		a.ClientCode, a.ClientName, a.ClientObject, a.ClientStore, a.Username, a.Module, a.Tier,
		a.StartDate, a.EndDate, dates.NowUTC(), a.ID)
	return classify(err)
}

// RevokeActivation ends an activation immediately.
func (db *DB) RevokeActivation(ctx context.Context, id int64) error {
	now := dates.NowUTC()
	_, err := db.ExecContext(ctx,
		`UPDATE activations SET revoked_at = ?, updated_at = ? WHERE id = ? AND revoked_at IS NULL`,
		now, now, id)
	return classify(err)
}

// RestoreActivation clears a revocation.
func (db *DB) RestoreActivation(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx,
		`UPDATE activations SET revoked_at = NULL, updated_at = ? WHERE id = ?`, dates.NowUTC(), id)
	return classify(err)
}

// DeleteActivation removes an activation row permanently.
func (db *DB) DeleteActivation(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM activations WHERE id = ?`, id)
	return classify(err)
}

// ActivationFilter narrows the activation listing. Status is one of
// "active", "expired", "revoked", "pending" or "" for all.
type ActivationFilter struct {
	Status     string
	Search     string
	Store      string
	Module     string
	ClientCode string
	Username   string
	Limit      int
	Offset     int
}

// ListActivations returns activations sorted by client, then username, then
// module, together with the total number of matches.
func (db *DB) ListActivations(ctx context.Context, f ActivationFilter) ([]Activation, int, error) {
	where, args := activationWhere(f)

	var total int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM activations`+where, args...).Scan(&total); err != nil {
		return nil, 0, classify(err)
	}

	query := `SELECT ` + activationColumns + ` FROM activations` + where +
		` ORDER BY client_name, client_code, username, module`
	pageArgs := args
	if f.Limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		pageArgs = append(append([]any{}, args...), f.Limit, max(f.Offset, 0))
	}

	rows, err := db.QueryContext(ctx, query, pageArgs...)
	if err != nil {
		return nil, 0, classify(err)
	}
	defer rows.Close()

	var out []Activation
	for rows.Next() {
		a, err := scanActivation(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *a)
	}
	return out, total, classify(rows.Err())
}

func activationWhere(f ActivationFilter) (string, []any) {
	var conds []string
	var args []any
	today := dates.Today()

	switch f.Status {
	case "active":
		conds = append(conds, `revoked_at IS NULL AND start_date <= ? AND end_date >= ?`)
		args = append(args, today, today)
	case "pending":
		conds = append(conds, `revoked_at IS NULL AND start_date > ?`)
		args = append(args, today)
	case "expired":
		conds = append(conds, `revoked_at IS NULL AND end_date < ?`)
		args = append(args, today)
	case "revoked":
		conds = append(conds, `revoked_at IS NOT NULL`)
	}

	if s := strings.TrimSpace(f.Search); s != "" {
		conds = append(conds, `(client_code LIKE ? ESCAPE '\' OR client_name LIKE ? ESCAPE '\'
			OR client_object LIKE ? ESCAPE '\' OR username LIKE ? ESCAPE '\')`)
		p := "%" + escapeLike(s) + "%"
		args = append(args, p, p, p, p)
	}
	if f.Store != "" {
		conds = append(conds, `client_store = ?`)
		args = append(args, f.Store)
	}
	if f.Module != "" {
		conds = append(conds, `module = ?`)
		args = append(args, f.Module)
	}
	if f.ClientCode != "" {
		conds = append(conds, `client_code = ?`)
		args = append(args, f.ClientCode)
	}
	if f.Username != "" {
		conds = append(conds, `username = ?`)
		args = append(args, f.Username)
	}
	if len(conds) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// ListActiveActivations returns every activation in force today, sorted for
// the Excel export: by client name, client code, username, then module.
func (db *DB) ListActiveActivations(ctx context.Context) ([]Activation, error) {
	today := dates.Today()
	rows, err := db.QueryContext(ctx,
		`SELECT `+activationColumns+` FROM activations
		 WHERE revoked_at IS NULL AND start_date <= ? AND end_date >= ?
		 ORDER BY client_name, client_code, username, module`, today, today)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()

	var out []Activation
	for rows.Next() {
		a, err := scanActivation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, classify(rows.Err())
}

// ListActivationsForClient returns every activation of one client.
func (db *DB) ListActivationsForClient(ctx context.Context, clientCode string) ([]Activation, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+activationColumns+` FROM activations
		 WHERE client_code = ? ORDER BY username, module`, clientCode)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()

	var out []Activation
	for rows.Next() {
		a, err := scanActivation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, classify(rows.Err())
}

// Overlap describes an existing activation that covers part of a proposed
// period for the same username and module.
type Overlap struct {
	Username  string
	Module    string
	Tier      string
	StartDate string
	EndDate   string
}

// FindOverlaps returns existing, non-revoked activations for the given
// usernames and modules whose period intersects [start, end]. The admin is
// warned about these before approving, but may proceed.
func (db *DB) FindOverlaps(ctx context.Context, q Querier, clientCode string, usernames []string, moduleKeys []string, start, end string) ([]Overlap, error) {
	if len(usernames) == 0 || len(moduleKeys) == 0 {
		return nil, nil
	}
	args := []any{clientCode}
	for _, u := range usernames {
		args = append(args, u)
	}
	for _, m := range moduleKeys {
		args = append(args, m)
	}
	args = append(args, end, start)

	rows, err := q.QueryContext(ctx,
		`SELECT username, module, tier, start_date, end_date FROM activations
		 WHERE client_code = ?
		   AND username IN (`+placeholders(len(usernames))+`)
		   AND module   IN (`+placeholders(len(moduleKeys))+`)
		   AND revoked_at IS NULL
		   AND start_date <= ? AND end_date >= ?
		 ORDER BY username, module`, args...)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()

	var out []Overlap
	for rows.Next() {
		var o Overlap
		if err := rows.Scan(&o.Username, &o.Module, &o.Tier, &o.StartDate, &o.EndDate); err != nil {
			return nil, classify(err)
		}
		out = append(out, o)
	}
	return out, classify(rows.Err())
}

// DistinctActivationStores lists the store values present on activations, for
// the admin filter dropdown.
func (db *DB) DistinctActivationStores(ctx context.Context) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT DISTINCT client_store FROM activations WHERE client_store <> '' ORDER BY client_store`)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, classify(err)
		}
		out = append(out, s)
	}
	return out, classify(rows.Err())
}

// ClientSummary is one client as shown in the salesperson's "Clients of my
// stores" list. Its fields match external.Client so the same template
// partial renders either shape.
type ClientSummary struct {
	Code   string
	Name   string
	Object string
	Store  string
	// Activations are the client's currently active activations, ordered by
	// username and module. The list shows them under the client's heading
	// row, the same way the grouped Excel export does.
	Activations []Activation
}

// ListActiveClientsByStores returns the distinct clients that currently have
// at least one active activation (not revoked, within its date range) at any
// of the given store values, optionally filtered by a search term over the
// code, name and object. It is the local counterpart of
// external.Directory.ListClientsByStores: the "Clients of my stores" list is
// sourced from what this application has actually activated, not from every
// client the external directory knows about, so a client with nothing
// activated does not appear.
//
// The store filter runs in SQL; the search and the paging run in Go. SQLite's
// LIKE only case-folds ASCII, so a SQL search would make Cyrillic names
// case-sensitive - matching this application's Bulgarian data needs Go's
// Unicode-aware strings.ToLower instead. The full result set for the
// account's stores is at most a few hundred clients even at the application's
// stated scale, so filtering it in Go costs nothing that matters.
func (db *DB) ListActiveClientsByStores(ctx context.Context, storeValues []string, search string, limit, offset int) ([]ClientSummary, int, error) {
	if len(storeValues) == 0 {
		return nil, 0, nil
	}

	today := dates.Today()
	args := make([]any, 0, len(storeValues)+2)
	for _, s := range storeValues {
		args = append(args, s)
	}
	args = append(args, today, today)

	query := `SELECT ` + activationColumns + `
		FROM activations
		WHERE client_store IN (` + placeholders(len(storeValues)) + `)
		  AND revoked_at IS NULL AND start_date <= ? AND end_date >= ?
		ORDER BY client_name, client_code, username, module`

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, classify(err)
	}
	defer rows.Close()

	// Group by client code, keeping the order in which clients first appear.
	// The client's details come from its first activation; each activation
	// carries a snapshot of them, so older rows may differ slightly.
	var all []ClientSummary
	index := map[string]int{}
	for rows.Next() {
		a, err := scanActivation(rows)
		if err != nil {
			return nil, 0, err
		}
		i, ok := index[a.ClientCode]
		if !ok {
			i = len(all)
			index[a.ClientCode] = i
			all = append(all, ClientSummary{
				Code: a.ClientCode, Name: a.ClientName,
				Object: a.ClientObject, Store: a.ClientStore,
			})
		}
		all[i].Activations = append(all[i].Activations, *a)
	}
	if err := classify(rows.Err()); err != nil {
		return nil, 0, err
	}

	// A search matches the client's code, name or object, or any of its
	// usernames. A match keeps the whole client with all its activations.
	if s := strings.TrimSpace(search); s != "" {
		needle := strings.ToLower(s)
		filtered := all[:0]
		for _, c := range all {
			if clientMatches(c, needle) {
				filtered = append(filtered, c)
			}
		}
		all = filtered
	}

	total := len(all)
	if limit <= 0 {
		return all, total, nil
	}
	if offset < 0 {
		offset = 0
	}
	from := min(offset, len(all))
	to := min(from+limit, len(all))
	return all[from:to], total, nil
}

func clientMatches(c ClientSummary, needle string) bool {
	if strings.Contains(strings.ToLower(c.Code), needle) ||
		strings.Contains(strings.ToLower(c.Name), needle) ||
		strings.Contains(strings.ToLower(c.Object), needle) {
		return true
	}
	for _, a := range c.Activations {
		if strings.Contains(strings.ToLower(a.Username), needle) {
			return true
		}
	}
	return false
}
