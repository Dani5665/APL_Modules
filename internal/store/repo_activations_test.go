package store

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"haynesproform/internal/dates"
	"haynesproform/internal/modules"
)

func newTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func insertTestActivation(t *testing.T, db *DB, a Activation) int64 {
	t.Helper()
	id, err := db.InsertActivation(context.Background(), db.DB, a)
	if err != nil {
		t.Fatalf("InsertActivation: %v", err)
	}
	return id
}

func TestListActiveClientsByStoresOnlyReturnsActivatedClients(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	today := dates.Today()
	future, err := dates.EndDate(today, 3)
	if err != nil {
		t.Fatalf("EndDate: %v", err)
	}

	// A client with an active activation at the store in question.
	insertTestActivation(t, db, Activation{
		ClientCode: "100000001", ClientName: "Активен Клиент ЕООД", ClientObject: "Обект 1",
		ClientStore: "Магазин Резбарска", Username: "user1", Module: modules.FastCalculator,
		StartDate: today, EndDate: future,
	})

	clients, total, err := db.ListActiveClientsByStores(ctx, []string{"Магазин Резбарска"}, "", 50, 0)
	if err != nil {
		t.Fatalf("ListActiveClientsByStores: %v", err)
	}
	if total != 1 || len(clients) != 1 {
		t.Fatalf("got %d clients (total=%d), want 1", len(clients), total)
	}
	if clients[0].Code != "100000001" || clients[0].Name != "Активен Клиент ЕООД" {
		t.Errorf("client = %+v, want the activated client", clients[0])
	}
}

// TestListActiveClientsByStoresHidesClientsWithNoActivation is the case the
// feature exists for: a store with local stores/users configured but no
// activations yet shows no clients at all, regardless of what the external
// directory knows about.
func TestListActiveClientsByStoresHidesClientsWithNoActivation(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	clients, total, err := db.ListActiveClientsByStores(ctx, []string{"Магазин Резбарска"}, "", 50, 0)
	if err != nil {
		t.Fatalf("ListActiveClientsByStores: %v", err)
	}
	if total != 0 || len(clients) != 0 {
		t.Fatalf("got %d clients (total=%d), want 0 (no activations exist)", len(clients), total)
	}
}

func TestListActiveClientsByStoresExcludesOtherStatuses(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	today := dates.Today()
	future, _ := dates.EndDate(today, 3)

	// Expired: end date in the past.
	insertTestActivation(t, db, Activation{
		ClientCode: "100000002", ClientName: "Изтекъл Клиент", ClientObject: "O",
		ClientStore: "Магазин Резбарска", Username: "u2", Module: modules.FastCalculator,
		StartDate: "2020-01-01", EndDate: "2020-01-31",
	})

	// Pending: start date in the future.
	insertTestActivation(t, db, Activation{
		ClientCode: "100000003", ClientName: "Предстоящ Клиент", ClientObject: "O",
		ClientStore: "Магазин Резбарска", Username: "u3", Module: modules.FastCalculator,
		StartDate: future, EndDate: future,
	})

	// Revoked: currently within its window but revoked.
	revokedID := insertTestActivation(t, db, Activation{
		ClientCode: "100000004", ClientName: "Прекратен Клиент", ClientObject: "O",
		ClientStore: "Магазин Резбарска", Username: "u4", Module: modules.FastCalculator,
		StartDate: today, EndDate: future,
	})
	if err := db.RevokeActivation(ctx, revokedID); err != nil {
		t.Fatalf("RevokeActivation: %v", err)
	}

	clients, total, err := db.ListActiveClientsByStores(ctx, []string{"Магазин Резбарска"}, "", 50, 0)
	if err != nil {
		t.Fatalf("ListActiveClientsByStores: %v", err)
	}
	if total != 0 || len(clients) != 0 {
		t.Fatalf("got %d clients (total=%d), want 0 (expired, pending and revoked must not count)",
			len(clients), total)
	}
}

func TestListActiveClientsByStoresFiltersByStore(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	today := dates.Today()
	future, _ := dates.EndDate(today, 3)

	insertTestActivation(t, db, Activation{
		ClientCode: "100000005", ClientName: "Клиент А", ClientObject: "O",
		ClientStore: "Магазин Резбарска", Username: "u5", Module: modules.FastCalculator,
		StartDate: today, EndDate: future,
	})
	insertTestActivation(t, db, Activation{
		ClientCode: "100000006", ClientName: "Клиент Б", ClientObject: "O",
		ClientStore: "SOFIA", Username: "u6", Module: modules.FastCalculator,
		StartDate: today, EndDate: future,
	})

	clients, total, err := db.ListActiveClientsByStores(ctx, []string{"SOFIA"}, "", 50, 0)
	if err != nil {
		t.Fatalf("ListActiveClientsByStores: %v", err)
	}
	if total != 1 || len(clients) != 1 || clients[0].Code != "100000006" {
		t.Fatalf("got %+v (total=%d), want only the SOFIA client", clients, total)
	}
}

func TestListActiveClientsByStoresSearchesCodeNameAndObject(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	today := dates.Today()
	future, _ := dates.EndDate(today, 3)

	insertTestActivation(t, db, Activation{
		ClientCode: "100000007", ClientName: "Автосервиз Балкан", ClientObject: "Сервиз Люлин",
		ClientStore: "Магазин Резбарска", Username: "u7", Module: modules.FastCalculator,
		StartDate: today, EndDate: future,
	})

	for _, q := range []string{"100000007", "Балкан", "Люлин", "балкан", "U7"} {
		clients, total, err := db.ListActiveClientsByStores(ctx, []string{"Магазин Резбарска"}, q, 50, 0)
		if err != nil {
			t.Fatalf("ListActiveClientsByStores(%q): %v", q, err)
		}
		if total != 1 || len(clients) != 1 {
			t.Errorf("search %q: got %d clients, want 1", q, len(clients))
		}
	}

	clients, total, err := db.ListActiveClientsByStores(ctx, []string{"Магазин Резбарска"}, "не съществува", 50, 0)
	if err != nil {
		t.Fatalf("ListActiveClientsByStores: %v", err)
	}
	if total != 0 || len(clients) != 0 {
		t.Errorf("search for a non-matching term returned %d clients, want 0", len(clients))
	}
}

// TestListActiveClientsByStoresGroupsActivations checks that each client
// comes back once, carrying its active activations ordered by username and
// module, and that revoked activations are left out of the group.
func TestListActiveClientsByStoresGroupsActivations(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	today := dates.Today()
	future, _ := dates.EndDate(today, 3)

	base := Activation{
		ClientCode: "000050431", ClientName: "Абаут Ю Сервиз ЕООД", ClientObject: "Магазин Резбарска",
		ClientStore: "Магазин Резбарска", StartDate: today, EndDate: future,
	}
	add := func(username string, m modules.Key, tier modules.Tier) int64 {
		a := base
		a.Username, a.Module, a.Tier = username, m, tier
		return insertTestActivation(t, db, a)
	}
	add("user2", modules.HaynesPro, modules.TierBusiness)
	add("user1", modules.HaynesPro, modules.TierBusiness)
	add("user1", modules.FastCalculator, "")
	revoked := add("user3", modules.FastCalculator, "")
	if err := db.RevokeActivation(ctx, revoked); err != nil {
		t.Fatalf("RevokeActivation: %v", err)
	}

	clients, total, err := db.ListActiveClientsByStores(ctx, []string{"Магазин Резбарска"}, "", 50, 0)
	if err != nil {
		t.Fatalf("ListActiveClientsByStores: %v", err)
	}
	if total != 1 || len(clients) != 1 {
		t.Fatalf("got %d clients (total=%d), want the one client once", len(clients), total)
	}

	var got []string
	for _, a := range clients[0].Activations {
		got = append(got, a.Username+"/"+string(a.Module))
	}
	want := []string{"user1/FAST_CALCULATOR", "user1/HAYNESPRO", "user2/HAYNESPRO"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("activations = %v, want %v", got, want)
	}
}

func TestListActiveClientsByStoresPaginates(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	today := dates.Today()
	future, _ := dates.EndDate(today, 3)

	for i := 0; i < 5; i++ {
		code := strconv.Itoa(100000010 + i)
		insertTestActivation(t, db, Activation{
			ClientCode: code, ClientName: code, ClientObject: "O", ClientStore: "Магазин Резбарска",
			Username: "u", Module: modules.FastCalculator, StartDate: today, EndDate: future,
		})
	}

	page1, total, err := db.ListActiveClientsByStores(ctx, []string{"Магазин Резбарска"}, "", 2, 0)
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if total != 5 || len(page1) != 2 {
		t.Fatalf("page 1: got %d clients (total=%d), want 2 (total 5)", len(page1), total)
	}

	page2, _, err := db.ListActiveClientsByStores(ctx, []string{"Магазин Резбарска"}, "", 2, 2)
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("page 2: got %d clients, want 2", len(page2))
	}
	if page1[0].Code == page2[0].Code {
		t.Error("page 1 and page 2 returned the same first client")
	}
}

// TestListActiveClientsByStoresNoStores covers the account-with-no-stores
// edge case: no store values means no query is even worth running.
func TestListActiveClientsByStoresNoStores(t *testing.T) {
	db := newTestDB(t)
	clients, total, err := db.ListActiveClientsByStores(context.Background(), nil, "", 50, 0)
	if err != nil {
		t.Fatalf("ListActiveClientsByStores: %v", err)
	}
	if total != 0 || clients != nil {
		t.Errorf("got %v (total=%d), want nil/0", clients, total)
	}
}
