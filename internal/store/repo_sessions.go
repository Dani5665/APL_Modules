package store

import (
	"context"
	"time"

	"haynesproform/internal/dates"
)

// CreateSession stores a new session row.
func (db *DB) CreateSession(ctx context.Context, s Session) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO sessions
			(token_hash, subject_type, subject_id, csrf_token, stage, created_at, last_seen_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		s.TokenHash, s.SubjectType, s.SubjectID, s.CSRFToken, s.Stage,
		s.CreatedAt, s.LastSeenAt, s.ExpiresAt)
	return classify(err)
}

// SessionByHash loads a session that has not passed its absolute expiry.
func (db *DB) SessionByHash(ctx context.Context, tokenHash string) (*Session, error) {
	var s Session
	err := db.QueryRowContext(ctx,
		`SELECT token_hash, subject_type, subject_id, csrf_token, stage,
		        created_at, last_seen_at, expires_at
		 FROM sessions WHERE token_hash = ? AND expires_at > ?`,
		tokenHash, dates.NowUTC()).
		Scan(&s.TokenHash, &s.SubjectType, &s.SubjectID, &s.CSRFToken, &s.Stage,
			&s.CreatedAt, &s.LastSeenAt, &s.ExpiresAt)
	if err != nil {
		return nil, classify(err)
	}
	return &s, nil
}

// TouchSession records activity on a session, extending its idle window.
func (db *DB) TouchSession(ctx context.Context, tokenHash string, at time.Time) error {
	_, err := db.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ? WHERE token_hash = ?`,
		dates.FormatUTC(at), tokenHash)
	return classify(err)
}

// PromoteSession moves a session to a new stage - used when an admin clears
// the TOTP step - and rotates nothing else.
func (db *DB) PromoteSession(ctx context.Context, tokenHash, stage string) error {
	_, err := db.ExecContext(ctx, `UPDATE sessions SET stage = ? WHERE token_hash = ?`, stage, tokenHash)
	return classify(err)
}

// DeleteSession removes one session.
func (db *DB) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return classify(err)
}

// DeleteSessionsFor removes every session of one account.
func (db *DB) DeleteSessionsFor(ctx context.Context, t SubjectType, id int64) error {
	_, err := db.ExecContext(ctx,
		`DELETE FROM sessions WHERE subject_type = ? AND subject_id = ?`, t, id)
	return classify(err)
}

// PurgeExpired removes sessions and parked entry links that are past their
// expiry, together with idle sessions. It is called periodically.
func (db *DB) PurgeExpired(ctx context.Context, idleCutoff time.Time) error {
	now := dates.NowUTC()
	if _, err := db.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at <= ? OR last_seen_at <= ?`,
		now, dates.FormatUTC(idleCutoff)); err != nil {
		return classify(err)
	}
	_, err := db.ExecContext(ctx, `DELETE FROM pending_entry_links WHERE expires_at <= ?`, now)
	return classify(err)
}

// PendingEntryLink is an entry link parked until the salesperson logs in.
type PendingEntryLink struct {
	ClientCode string
	SalerLogin string
}

// CreatePendingEntryLink parks an entry link against a pre-session token.
func (db *DB) CreatePendingEntryLink(ctx context.Context, tokenHash, clientCode, salerLogin string, expiresAt time.Time) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO pending_entry_links (token_hash, client_code, saler_login, created_at, expires_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(token_hash) DO UPDATE SET
		     client_code = excluded.client_code,
		     saler_login = excluded.saler_login,
		     expires_at  = excluded.expires_at`,
		tokenHash, clientCode, salerLogin, dates.NowUTC(), dates.FormatUTC(expiresAt))
	return classify(err)
}

// TakePendingEntryLink returns a parked entry link and removes it, so an
// entry link is consumed exactly once.
func (db *DB) TakePendingEntryLink(ctx context.Context, tokenHash string) (*PendingEntryLink, error) {
	var l PendingEntryLink
	err := db.QueryRowContext(ctx,
		`DELETE FROM pending_entry_links
		 WHERE token_hash = ? AND expires_at > ?
		 RETURNING client_code, saler_login`,
		tokenHash, dates.NowUTC()).Scan(&l.ClientCode, &l.SalerLogin)
	if err != nil {
		return nil, classify(err)
	}
	return &l, nil
}

// PeekPendingEntryLink returns a parked entry link without consuming it.
func (db *DB) PeekPendingEntryLink(ctx context.Context, tokenHash string) (*PendingEntryLink, error) {
	var l PendingEntryLink
	err := db.QueryRowContext(ctx,
		`SELECT client_code, saler_login FROM pending_entry_links
		 WHERE token_hash = ? AND expires_at > ?`,
		tokenHash, dates.NowUTC()).Scan(&l.ClientCode, &l.SalerLogin)
	if err != nil {
		return nil, classify(err)
	}
	return &l, nil
}
