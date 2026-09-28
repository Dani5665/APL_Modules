package store

import (
	"context"
	"path/filepath"
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
