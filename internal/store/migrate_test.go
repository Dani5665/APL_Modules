package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	// Open already migrated; running again must be a no-op, not an error.
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	var tables int
	err = db.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'activations'`).Scan(&tables)
	if err != nil || tables != 1 {
		t.Fatalf("activations table missing: count=%d err=%v", tables, err)
	}

	var scheduleRows int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM export_schedule`).Scan(&scheduleRows); err != nil {
		t.Fatalf("read export_schedule: %v", err)
	}
	if scheduleRows != 1 {
		t.Fatalf("export_schedule rows = %d, want 1", scheduleRows)
	}

	var fk int
	if err := db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatalf("read foreign_keys pragma: %v", err)
	}
	if fk != 1 {
		t.Fatal("foreign keys are not enabled")
	}
}

// Migration 0007 moves the notification addresses and the export schedule's
// addresses onto their email templates. The schema change itself already ran
// when the database was opened, so its data statements are replayed against
// the legacy values here.
func TestTemplateRecipientsMigrationCarriesTheOldListsOver(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	now := "2026-01-01T00:00:00Z"
	for _, k := range []string{"request_created", "scheduled_export"} {
		if err := db.EnsureEmailTemplate(ctx, k, "s", "b", "", now); err != nil {
			t.Fatalf("seed %s: %v", k, err)
		}
	}
	if err := db.SetSetting(ctx, KeyNotifyRecipients, "old-notify@example.com"); err != nil {
		t.Fatalf("set legacy setting: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE export_schedule SET recipients = 'old-export@example.com' WHERE id = 1`); err != nil {
		t.Fatalf("set legacy schedule: %v", err)
	}

	migs, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	var sql string
	for _, m := range migs {
		if m.name == "template_recipients" {
			sql = m.sql
		}
	}
	if sql == "" {
		t.Fatal("migration 0007 not found")
	}
	// Everything after the ALTER (already applied) is the data carry-over.
	_, data, _ := strings.Cut(sql, "ALTER TABLE email_templates ADD COLUMN recipients TEXT NOT NULL DEFAULT '';")
	if _, err := db.ExecContext(ctx, data); err != nil {
		t.Fatalf("carry-over: %v", err)
	}

	for key, want := range map[string]string{
		"request_created":  "old-notify@example.com",
		"scheduled_export": "old-export@example.com",
	} {
		got, err := db.TemplateRecipients(ctx, key)
		if err != nil || len(got) != 1 || got[0] != want {
			t.Errorf("%s recipients = %v, %v; want %s", key, got, err, want)
		}
	}
}
