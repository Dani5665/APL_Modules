package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Setting keys. SMTP credentials and notification recipients live in the
// settings table so they can be changed without a redeploy.
const (
	KeySMTPHost         = "smtp.host"
	KeySMTPPort         = "smtp.port"
	KeySMTPSecurity     = "smtp.security"
	KeySMTPUsername     = "smtp.username"
	KeySMTPPasswordEnc  = "smtp.password_enc"
	KeySMTPFromAddress  = "smtp.from_address"
	KeySMTPFromName     = "smtp.from_name"
	KeyNotifyRecipients = "notify.recipients"
)

// Setting reads one setting, returning def when it is absent.
func (db *DB) Setting(ctx context.Context, key, def string) (string, error) {
	var v string
	err := db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return def, nil
	case err != nil:
		return "", classify(err)
	}
	return v, nil
}

// Settings reads every setting into a map.
func (db *DB) Settings(ctx context.Context) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, classify(err)
		}
		out[k] = v
	}
	return out, classify(rows.Err())
}

// SetSetting writes one setting.
func (db *DB) SetSetting(ctx context.Context, key, value string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return classify(err)
}

// SetSettings writes several settings in one transaction.
func (db *DB) SetSettings(ctx context.Context, values map[string]string) error {
	return db.InTx(ctx, func(tx *sql.Tx) error {
		for k, v := range values {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO settings (key, value) VALUES (?, ?)
				 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v); err != nil {
				return classify(err)
			}
		}
		return nil
	})
}

// EmailTemplate is an editable subject and HTML body.
type EmailTemplate struct {
	Key       string
	Subject   string
	BodyHTML  string
	UpdatedAt string
}

// EmailTemplateByKey loads one template.
func (db *DB) EmailTemplateByKey(ctx context.Context, key string) (*EmailTemplate, error) {
	var t EmailTemplate
	err := db.QueryRowContext(ctx,
		`SELECT key, subject, body_html, updated_at FROM email_templates WHERE key = ?`, key).
		Scan(&t.Key, &t.Subject, &t.BodyHTML, &t.UpdatedAt)
	if err != nil {
		return nil, classify(err)
	}
	return &t, nil
}

// ListEmailTemplates returns every template.
func (db *DB) ListEmailTemplates(ctx context.Context) ([]EmailTemplate, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT key, subject, body_html, updated_at FROM email_templates ORDER BY key`)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()

	var out []EmailTemplate
	for rows.Next() {
		var t EmailTemplate
		if err := rows.Scan(&t.Key, &t.Subject, &t.BodyHTML, &t.UpdatedAt); err != nil {
			return nil, classify(err)
		}
		out = append(out, t)
	}
	return out, classify(rows.Err())
}

// SaveEmailTemplate inserts or replaces a template.
func (db *DB) SaveEmailTemplate(ctx context.Context, key, subject, bodyHTML, now string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO email_templates (key, subject, body_html, updated_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET
		     subject = excluded.subject,
		     body_html = excluded.body_html,
		     updated_at = excluded.updated_at`,
		key, subject, bodyHTML, now)
	return classify(err)
}

// EnsureEmailTemplate seeds a template only if it does not exist yet, so an
// admin's edits are never overwritten at startup.
func (db *DB) EnsureEmailTemplate(ctx context.Context, key, subject, bodyHTML, now string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO email_templates (key, subject, body_html, updated_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(key) DO NOTHING`, key, subject, bodyHTML, now)
	return classify(err)
}

// ExportSchedule is the single monthly export schedule row.
type ExportSchedule struct {
	Enabled    bool
	DayOfMonth int
	TimeHHMM   string
	Recipients string
	// GroupByClient remembers the admin's last choice of the "Групирай по
	// обекти" checkbox, so it carries over to the next scheduled and manual
	// send from this page.
	GroupByClient bool
	LastRunAt     sql.NullString
	LastResult    string
	NextRunAt     sql.NullString
}

// RecipientList splits the stored recipients into addresses.
func (s ExportSchedule) RecipientList() []string { return SplitEmails(s.Recipients) }

// SplitEmails parses a stored comma- or newline-separated address list.
func SplitEmails(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// ExportSchedule reads the schedule row.
func (db *DB) ExportSchedule(ctx context.Context) (*ExportSchedule, error) {
	var s ExportSchedule
	err := db.QueryRowContext(ctx,
		`SELECT enabled, day_of_month, time_hhmm, recipients, group_by_client,
		        last_run_at, last_result, next_run_at
		 FROM export_schedule WHERE id = 1`).
		Scan(&s.Enabled, &s.DayOfMonth, &s.TimeHHMM, &s.Recipients, &s.GroupByClient,
			&s.LastRunAt, &s.LastResult, &s.NextRunAt)
	if err != nil {
		return nil, classify(err)
	}
	return &s, nil
}

// SaveExportSchedule stores the admin-editable part of the schedule and the
// recomputed next run time.
func (db *DB) SaveExportSchedule(ctx context.Context, enabled bool, day int, hhmm, recipients string, groupByClient bool, nextRunAt sql.NullString) error {
	_, err := db.ExecContext(ctx,
		`UPDATE export_schedule
		 SET enabled = ?, day_of_month = ?, time_hhmm = ?, recipients = ?, group_by_client = ?, next_run_at = ?
		 WHERE id = 1`,
		enabled, day, hhmm, recipients, groupByClient, nextRunAt)
	return classify(err)
}

// ClaimExportRun atomically moves next_run_at forward, and reports whether
// this caller won the claim. Advancing the marker in the same statement that
// reads it is what prevents a double send.
func (db *DB) ClaimExportRun(ctx context.Context, expectedNextRun, newNextRun string) (bool, error) {
	res, err := db.ExecContext(ctx,
		`UPDATE export_schedule SET next_run_at = ? WHERE id = 1 AND next_run_at = ?`,
		newNextRun, expectedNextRun)
	if err != nil {
		return false, classify(err)
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// RecordExportRun stores the outcome of a run.
func (db *DB) RecordExportRun(ctx context.Context, at, result string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE export_schedule SET last_run_at = ?, last_result = ? WHERE id = 1`, at, result)
	return classify(err)
}
