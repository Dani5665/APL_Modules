package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"haynesproform/internal/dates"
)

func TestPurgeAuditBeforeDeletesOnlyOldEntries(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	cutoff := dates.AddMonths(now, -AuditRetentionMonths)
	if got := cutoff.Format("2006-01-02"); got != "2026-07-14" {
		t.Fatalf("cutoff = %s, want 2026-07-14", got)
	}

	add := func(action string, at time.Time) {
		t.Helper()
		if err := db.InsertAudit(ctx, nil, AuditEntry{
			ActorType: "system", Action: action, CreatedAt: dates.FormatUTC(at),
		}); err != nil {
			t.Fatalf("InsertAudit: %v", err)
		}
	}
	add("old.year", now.AddDate(-1, 0, 0))
	add("old.just", cutoff.Add(-time.Second))
	add("keep.edge", cutoff)
	add("keep.recent", now.AddDate(0, -1, 0))
	add("keep.new", now)

	n, err := db.PurgeAuditBefore(ctx, cutoff)
	if err != nil || n != 2 {
		t.Fatalf("PurgeAuditBefore = %d, %v; want 2 deleted", n, err)
	}

	entries, total, err := db.ListAudit(ctx, AuditFilter{Limit: 50})
	if err != nil || total != 3 || len(entries) != 3 {
		t.Fatalf("after purge: %d entries (total %d), %v; want 3", len(entries), total, err)
	}
	for _, e := range entries {
		if e.Action == "old.year" || e.Action == "old.just" {
			t.Errorf("%s survived the purge", e.Action)
		}
	}

	// Nothing left to delete.
	if n, _ := db.PurgeAuditBefore(ctx, cutoff); n != 0 {
		t.Errorf("a second purge deleted %d entries", n)
	}
}

func TestPurgeOutboxBeforeDeletesOnlyOldMessages(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	cutoff := dates.AddMonths(now, -OutboxRetentionMonths)

	queue := func(status string, at time.Time) {
		t.Helper()
		id, err := db.QueueEmail(ctx, nil, OutboxMessage{
			TemplateKey: "request_created", Recipients: "a@example.com", Subject: "s", BodyHTML: "b",
		})
		if err != nil {
			t.Fatalf("QueueEmail: %v", err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE email_outbox SET status = ?, created_at = ? WHERE id = ?`,
			status, dates.FormatUTC(at), id); err != nil {
			t.Fatalf("set status/date: %v", err)
		}
	}
	queue("sent", now.AddDate(-1, 0, 0))
	queue("failed", cutoff.Add(-time.Minute))
	queue("pending", cutoff.Add(-time.Hour))
	queue("sent", cutoff)
	queue("failed", now.AddDate(0, -1, 0))
	queue("pending", now)

	n, err := db.PurgeOutboxBefore(ctx, cutoff)
	if err != nil || n != 3 {
		t.Fatalf("PurgeOutboxBefore = %d, %v; want 3 deleted (any status)", n, err)
	}
	if _, total, err := db.ListOutbox(ctx, "", 50, 0); err != nil || total != 3 {
		t.Fatalf("after purge: total %d, %v; want 3", total, err)
	}
}
