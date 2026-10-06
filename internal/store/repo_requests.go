package store

import (
	"context"
	"database/sql"
	"strings"

	"haynesproform/internal/dates"
	"haynesproform/internal/modules"
)

const requestColumns = `r.id, r.client_code, r.client_name, r.client_object, r.client_store,
	r.submitter_user_id, r.submitter_email, r.saler_login, r.test_period, r.start_date, r.months, r.unlimited,
	r.status, r.admin_comment, r.decided_by_admin_id, r.decided_at, r.created_at, r.updated_at,
	COALESCE(a.email, '')`

const requestFrom = ` FROM requests r LEFT JOIN admins a ON a.id = r.decided_by_admin_id`

func scanRequest(s interface{ Scan(...any) error }) (*Request, error) {
	var r Request
	var unlimited bool
	err := s.Scan(&r.ID, &r.ClientCode, &r.ClientName, &r.ClientObject, &r.ClientStore,
		&r.SubmitterUserID, &r.SubmitterEmail, &r.SalerLogin, &r.TestPeriod, &r.StartDate, &r.Months, &unlimited,
		&r.Status, &r.AdminComment, &r.DecidedByAdminID, &r.DecidedAt, &r.CreatedAt, &r.UpdatedAt,
		&r.DecidedByEmail)
	if err != nil {
		return nil, classify(err)
	}
	if unlimited {
		r.Months = 0
	}
	return &r, nil
}

// NewRequest carries everything needed to record a submission.
type NewRequest struct {
	ClientCode      string
	ClientName      string
	ClientObject    string
	ClientStore     string
	SubmitterUserID int64
	SubmitterEmail  string
	SalerLogin      string
	TestPeriod      bool
	StartDate       string
	Months          int
	Usernames       []string
	Modules         []RequestModule
}

// CreateRequest stores a pending request with its usernames and modules.
func (db *DB) CreateRequest(ctx context.Context, n NewRequest) (int64, error) {
	var id int64
	err := db.InTx(ctx, func(tx *sql.Tx) error {
		now := dates.NowUTC()
		res, err := tx.ExecContext(ctx,
			`INSERT INTO requests
				(client_code, client_name, client_object, client_store, submitter_user_id,
				 submitter_email, saler_login, test_period, start_date, months, unlimited, status,
				 created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			n.ClientCode, n.ClientName, n.ClientObject, n.ClientStore, nullInt64(n.SubmitterUserID),
			n.SubmitterEmail, n.SalerLogin, n.TestPeriod, n.StartDate, max(n.Months, 1), n.Months == 0, StatusPending,
			now, now)
		if err != nil {
			return classify(err)
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		return db.replaceRequestDetails(ctx, tx, id, n.Usernames, n.Modules)
	})
	return id, err
}

// UpdateRequestDetails lets an admin revise a pending request before deciding.
func (db *DB) UpdateRequestDetails(ctx context.Context, id int64, testPeriod bool, startDate string, months int, usernames []string, mods []RequestModule) error {
	return db.InTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE requests SET test_period = ?, start_date = ?, months = ?, unlimited = ?, updated_at = ?
			 WHERE id = ? AND status = ?`,
			testPeriod, startDate, max(months, 1), months == 0, dates.NowUTC(), id, StatusPending); err != nil {
			return classify(err)
		}
		return db.replaceRequestDetails(ctx, tx, id, usernames, mods)
	})
}

func (db *DB) replaceRequestDetails(ctx context.Context, q Querier, id int64, usernames []string, mods []RequestModule) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM request_usernames WHERE request_id = ?`, id); err != nil {
		return classify(err)
	}
	seen := map[string]bool{}
	for _, u := range usernames {
		u = strings.TrimSpace(u)
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		if _, err := q.ExecContext(ctx,
			`INSERT INTO request_usernames (request_id, username) VALUES (?, ?)`, id, u); err != nil {
			return classify(err)
		}
	}

	if _, err := q.ExecContext(ctx, `DELETE FROM request_modules WHERE request_id = ?`, id); err != nil {
		return classify(err)
	}
	seenMod := map[modules.Key]bool{}
	for _, m := range mods {
		if seenMod[m.Module] {
			continue
		}
		seenMod[m.Module] = true
		if _, err := q.ExecContext(ctx,
			`INSERT INTO request_modules (request_id, module, tier) VALUES (?, ?, ?)`,
			id, m.Module, m.Tier); err != nil {
			return classify(err)
		}
	}
	return nil
}

// RequestByID loads a request with its usernames and modules.
func (db *DB) RequestByID(ctx context.Context, id int64) (*Request, error) {
	r, err := scanRequest(db.QueryRowContext(ctx,
		`SELECT `+requestColumns+requestFrom+` WHERE r.id = ?`, id))
	if err != nil {
		return nil, err
	}
	if err := db.loadRequestDetails(ctx, r); err != nil {
		return nil, err
	}
	return r, nil
}

func (db *DB) loadRequestDetails(ctx context.Context, r *Request) error {
	rows, err := db.QueryContext(ctx,
		`SELECT username FROM request_usernames WHERE request_id = ? ORDER BY username`, r.ID)
	if err != nil {
		return classify(err)
	}
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			rows.Close()
			return classify(err)
		}
		r.Usernames = append(r.Usernames, u)
	}
	rows.Close()
	if err := classify(rows.Err()); err != nil {
		return err
	}

	mrows, err := db.QueryContext(ctx,
		`SELECT module, tier FROM request_modules WHERE request_id = ? ORDER BY module`, r.ID)
	if err != nil {
		return classify(err)
	}
	defer mrows.Close()
	for mrows.Next() {
		var m RequestModule
		if err := mrows.Scan(&m.Module, &m.Tier); err != nil {
			return classify(err)
		}
		r.Modules = append(r.Modules, m)
	}
	return classify(mrows.Err())
}

// RequestFilter narrows the admin request listing.
type RequestFilter struct {
	Status   RequestStatus
	Search   string
	FromDate string
	ToDate   string
	Limit    int
	Offset   int
}

// ListRequests returns requests newest first, with their usernames and
// modules loaded, plus the total number of matches for pagination.
func (db *DB) ListRequests(ctx context.Context, f RequestFilter) ([]Request, int, error) {
	where, args := requestWhere(f)

	var total int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*)`+requestFrom+where, args...).Scan(&total); err != nil {
		return nil, 0, classify(err)
	}

	query := `SELECT ` + requestColumns + requestFrom + where + ` ORDER BY r.created_at DESC, r.id DESC`
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

	var out []Request
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *r)
	}
	if err := classify(rows.Err()); err != nil {
		return nil, 0, err
	}
	for i := range out {
		if err := db.loadRequestDetails(ctx, &out[i]); err != nil {
			return nil, 0, err
		}
	}
	return out, total, nil
}

func requestWhere(f RequestFilter) (string, []any) {
	var conds []string
	var args []any

	if f.Status != "" {
		conds = append(conds, `r.status = ?`)
		args = append(args, f.Status)
	}
	if s := strings.TrimSpace(f.Search); s != "" {
		conds = append(conds, `(r.client_code LIKE ? ESCAPE '\' OR r.client_name LIKE ? ESCAPE '\' OR r.submitter_email LIKE ? ESCAPE '\')`)
		p := "%" + escapeLike(s) + "%"
		args = append(args, p, p, p)
	}
	if f.FromDate != "" {
		// created_at is a UTC timestamp; comparing against the start of the
		// day keeps the filter inclusive.
		conds = append(conds, `r.created_at >= ?`)
		args = append(args, f.FromDate+"T00:00:00Z")
	}
	if f.ToDate != "" {
		conds = append(conds, `r.created_at <= ?`)
		args = append(args, f.ToDate+"T23:59:59Z")
	}
	if len(conds) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// CountRequestsByStatus returns how many requests are in the given status.
func (db *DB) CountRequestsByStatus(ctx context.Context, status RequestStatus) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM requests WHERE status = ?`, status).Scan(&n)
	return n, classify(err)
}

// ListRequestsForClient returns a client's requests in the given statuses,
// newest first.
func (db *DB) ListRequestsForClient(ctx context.Context, clientCode string, statuses ...RequestStatus) ([]Request, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	args := []any{clientCode}
	for _, s := range statuses {
		args = append(args, s)
	}
	rows, err := db.QueryContext(ctx,
		`SELECT `+requestColumns+requestFrom+
			` WHERE r.client_code = ? AND r.status IN (`+placeholders(len(statuses))+`)
			 ORDER BY r.created_at DESC, r.id DESC`, args...)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()

	var out []Request
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	if err := classify(rows.Err()); err != nil {
		return nil, err
	}
	for i := range out {
		if err := db.loadRequestDetails(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// TestPeriodUsed reports whether the client has already consumed its single
// test period. A pending or approved test-period request consumes it; a
// denied one does not.
func (db *DB) TestPeriodUsed(ctx context.Context, clientCode string, excludeRequestID int64) (bool, error) {
	return db.testPeriodUsed(ctx, db.DB, clientCode, excludeRequestID)
}

func (db *DB) testPeriodUsed(ctx context.Context, q Querier, clientCode string, excludeRequestID int64) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx,
		`SELECT count(*) FROM requests
		 WHERE client_code = ? AND test_period = 1 AND status IN (?, ?) AND id <> ?`,
		clientCode, StatusPending, StatusApproved, excludeRequestID).Scan(&n)
	return n > 0, classify(err)
}

// DecideRequest records an approve or deny decision on a pending request. It
// returns ErrNotFound if the request was already decided, which is what makes
// a double submission harmless.
func (db *DB) DecideRequest(ctx context.Context, tx Querier, id int64, status RequestStatus, adminID int64, comment string) error {
	res, err := tx.ExecContext(ctx,
		`UPDATE requests
		 SET status = ?, admin_comment = ?, decided_by_admin_id = ?, decided_at = ?, updated_at = ?
		 WHERE id = ? AND status = ?`,
		status, comment, nullInt64(adminID), dates.NowUTC(), dates.NowUTC(), id, StatusPending)
	if err != nil {
		return classify(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
