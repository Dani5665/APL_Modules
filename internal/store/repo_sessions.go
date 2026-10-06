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

// PurgeExpired removes sessions that are past their expiry, together with
// idle sessions. It is called periodically.
func (db *DB) PurgeExpired(ctx context.Context, idleCutoff time.Time) error {
	_, err := db.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at <= ? OR last_seen_at <= ?`,
		dates.NowUTC(), dates.FormatUTC(idleCutoff))
	return classify(err)
}
