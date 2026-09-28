package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"haynesproform/internal/dates"
)

// OutboxMessage is a queued email. Queueing rather than sending inline keeps
// a failing SMTP server from failing a user's request.
type OutboxMessage struct {
	ID             int64
	TemplateKey    string
	Recipients     string
	Subject        string
	BodyHTML       string
	AttachmentName sql.NullString
	AttachmentBlob []byte
	Status         string
	Attempts       int
	LastError      string
	NextAttemptAt  string
	SentAt         sql.NullString
	CreatedAt      string
}

// RecipientList splits the stored recipients into addresses.
func (m OutboxMessage) RecipientList() []string { return SplitEmails(m.Recipients) }

const outboxColumns = `id, template_key, recipients, subject, body_html, attachment_name,
	attachment_blob, status, attempts, last_error, next_attempt_at, sent_at, created_at`

func scanOutbox(s interface{ Scan(...any) error }) (*OutboxMessage, error) {
	var m OutboxMessage
	err := s.Scan(&m.ID, &m.TemplateKey, &m.Recipients, &m.Subject, &m.BodyHTML,
		&m.AttachmentName, &m.AttachmentBlob, &m.Status, &m.Attempts, &m.LastError,
		&m.NextAttemptAt, &m.SentAt, &m.CreatedAt)
	if err != nil {
		return nil, classify(err)
	}
	return &m, nil
}

// QueueEmail appends a message to the outbox for the worker to deliver.
func (db *DB) QueueEmail(ctx context.Context, q Querier, m OutboxMessage) (int64, error) {
	if q == nil {
		q = db.DB
	}
	now := dates.NowUTC()
	if m.NextAttemptAt == "" {
		m.NextAttemptAt = now
	}
	res, err := q.ExecContext(ctx,
		`INSERT INTO email_outbox
			(template_key, recipients, subject, body_html, attachment_name, attachment_blob,
			 status, attempts, next_attempt_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, 'pending', 0, ?, ?)`,
		m.TemplateKey, strings.Join(SplitEmails(m.Recipients), ", "), m.Subject, m.BodyHTML,
		m.AttachmentName, m.AttachmentBlob, m.NextAttemptAt, now)
	if err != nil {
		return 0, classify(err)
	}
	return res.LastInsertId()
}

// ClaimDueEmails returns pending messages whose next attempt time has passed.
func (db *DB) ClaimDueEmails(ctx context.Context, limit int) ([]OutboxMessage, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+outboxColumns+` FROM email_outbox
		 WHERE status = 'pending' AND next_attempt_at <= ?
		 ORDER BY next_attempt_at, id LIMIT ?`, dates.NowUTC(), limit)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()

	var out []OutboxMessage
	for rows.Next() {
		m, err := scanOutbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, classify(rows.Err())
}

// MarkEmailSent records a successful delivery and drops the attachment blob,
// which is regenerated on demand and would otherwise grow the database.
func (db *DB) MarkEmailSent(ctx context.Context, id int64) error {
	now := dates.NowUTC()
	_, err := db.ExecContext(ctx,
		`UPDATE email_outbox
		 SET status = 'sent', sent_at = ?, last_error = '', attachment_blob = NULL
		 WHERE id = ?`, now, id)
	return classify(err)
}

// MarkEmailFailed records a failed attempt. When attempts reach maxAttempts
// the message is parked as failed and shown to admins.
func (db *DB) MarkEmailFailed(ctx context.Context, id int64, attempts, maxAttempts int, errText string, nextAttempt time.Time) error {
	status := "pending"
	if attempts >= maxAttempts {
		status = "failed"
	}
	_, err := db.ExecContext(ctx,
		`UPDATE email_outbox SET status = ?, attempts = ?, last_error = ?, next_attempt_at = ?
		 WHERE id = ?`,
		status, attempts, truncate(errText, 2000), dates.FormatUTC(nextAttempt), id)
	return classify(err)
}

// RetryEmail puts a failed message back in the queue immediately.
func (db *DB) RetryEmail(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx,
		`UPDATE email_outbox SET status = 'pending', attempts = 0, next_attempt_at = ?
		 WHERE id = ? AND status = 'failed'`, dates.NowUTC(), id)
	return classify(err)
}

// DeleteOutboxMessage removes a message from the outbox.
func (db *DB) DeleteOutboxMessage(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM email_outbox WHERE id = ?`, id)
	return classify(err)
}

// ListOutbox returns outbox messages newest first, optionally filtered by
// status, with the total number of matches.
func (db *DB) ListOutbox(ctx context.Context, status string, limit, offset int) ([]OutboxMessage, int, error) {
	where := ""
	var args []any
	if status != "" {
		where = ` WHERE status = ?`
		args = append(args, status)
	}

	var total int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM email_outbox`+where, args...).Scan(&total); err != nil {
		return nil, 0, classify(err)
	}

	// The body and attachment are large and never shown in the listing.
	query := `SELECT id, template_key, recipients, subject, '', attachment_name, NULL,
		status, attempts, last_error, next_attempt_at, sent_at, created_at
		FROM email_outbox` + where + ` ORDER BY id DESC LIMIT ? OFFSET ?`
	args = append(args, limit, max(offset, 0))

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, classify(err)
	}
	defer rows.Close()

	var out []OutboxMessage
	for rows.Next() {
		m, err := scanOutbox(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *m)
	}
	return out, total, classify(rows.Err())
}

// CountFailedEmails reports how many messages are parked as failed.
func (db *DB) CountFailedEmails(ctx context.Context) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM email_outbox WHERE status = 'failed'`).Scan(&n)
	return n, classify(err)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
