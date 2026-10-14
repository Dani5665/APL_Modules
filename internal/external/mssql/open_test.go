package mssql

import (
	"context"
	"errors"
	"testing"

	"haynesproform/internal/external"
)

// An unreachable database must not stop the application from starting: Open
// returns a usable Directory with the error, and every query reports the
// database as unavailable instead of crashing.
func TestOpenWithAnUnreachableDatabaseStillReturnsADirectory(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, "sqlserver://user:pass@127.0.0.1:1?database=x&connection+timeout=1")
	if d == nil {
		t.Fatalf("Open returned no directory: %v", err)
	}
	defer d.Close()
	if !errors.Is(err, external.ErrUnavailable) {
		t.Errorf("Open error = %v, want ErrUnavailable", err)
	}

	if _, err := d.ListClientsByStores(ctx, []string{"1"}); !errors.Is(err, external.ErrUnavailable) {
		t.Errorf("ListClientsByStores error = %v, want ErrUnavailable", err)
	}
	if err := d.Ping(ctx); !errors.Is(err, external.ErrUnavailable) {
		t.Errorf("Ping error = %v, want ErrUnavailable", err)
	}
}
