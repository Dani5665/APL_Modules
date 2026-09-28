package store

import (
	"context"
	"database/sql"
	"strings"

	"haynesproform/internal/dates"
)

// AuditEntry is one recorded action.
type AuditEntry struct {
	ID          int64
	ActorType   string
	ActorID     sql.NullInt64
	ActorLabel  string
	Action      string
	Target      string
	IP          string
	DetailsJSON string
	CreatedAt   string
}

// InsertAudit appends an audit entry. It accepts a Querier so an action and
// its audit record can share one transaction.
func (db *DB) InsertAudit(ctx context.Context, q Querier, e AuditEntry) error {
	if q == nil {
		q = db.DB
	}
	if e.DetailsJSON == "" {
		e.DetailsJSON = "{}"
	}
	if e.CreatedAt == "" {
		e.CreatedAt = dates.NowUTC()
	}
	_, err := q.ExecContext(ctx,
		`INSERT INTO audit_log (actor_type, actor_id, actor_label, action, target, ip, details_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ActorType, e.ActorID, e.ActorLabel, e.Action, e.Target, e.IP, e.DetailsJSON, e.CreatedAt)
	return classify(err)
}

// AuditFilter narrows the audit listing.
type AuditFilter struct {
	Action    string
	ActorType string
	Search    string
	FromDate  string
	ToDate    string
	Limit     int
	Offset    int
}

// ListAudit returns audit entries newest first, with the total match count.
func (db *DB) ListAudit(ctx context.Context, f AuditFilter) ([]AuditEntry, int, error) {
	var conds []string
	var args []any

	if f.Action != "" {
		conds = append(conds, `action = ?`)
		args = append(args, f.Action)
	}
	if f.ActorType != "" {
		conds = append(conds, `actor_type = ?`)
		args = append(args, f.ActorType)
	}
	if s := strings.TrimSpace(f.Search); s != "" {
		conds = append(conds, `(actor_label LIKE ? ESCAPE '\' OR target LIKE ? ESCAPE '\' OR details_json LIKE ? ESCAPE '\')`)
		p := "%" + escapeLike(s) + "%"
		args = append(args, p, p, p)
	}
	if f.FromDate != "" {
		conds = append(conds, `created_at >= ?`)
		args = append(args, f.FromDate+"T00:00:00Z")
	}
	if f.ToDate != "" {
		conds = append(conds, `created_at <= ?`)
		args = append(args, f.ToDate+"T23:59:59Z")
	}

	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	var total int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM audit_log`+where, args...).Scan(&total); err != nil {
		return nil, 0, classify(err)
	}

	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	args = append(args, limit, max(f.Offset, 0))

	rows, err := db.QueryContext(ctx,
		`SELECT id, actor_type, actor_id, actor_label, action, target, ip, details_json, created_at
		 FROM audit_log`+where+` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, classify(err)
	}
	defer rows.Close()

	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.ActorType, &e.ActorID, &e.ActorLabel, &e.Action,
			&e.Target, &e.IP, &e.DetailsJSON, &e.CreatedAt); err != nil {
			return nil, 0, classify(err)
		}
		out = append(out, e)
	}
	return out, total, classify(rows.Err())
}

// DistinctAuditActions lists the actions present in the log, for the filter
// dropdown.
func (db *DB) DistinctAuditActions(ctx context.Context) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT action FROM audit_log ORDER BY action`)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, classify(err)
		}
		out = append(out, a)
	}
	return out, classify(rows.Err())
}
