package http

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"haynesproform/internal/external/mock"
)

func TestAdminStoresNeedAMandantIDAndAreUniqueByIt(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	s := h.loginAdmin(h.seedAdmin("admin@example.com"))

	// MANDANT_NAME alone is not enough.
	rec := s.post("/admin/stores", url.Values{"name": {"Магазин А"}})
	assertStatus(t, rec, http.StatusUnprocessableEntity)
	assertContains(t, rec, "Въведете MANDANT_ID")

	rec = s.post("/admin/stores", url.Values{"mandant_id": {"7"}})
	assertStatus(t, rec, http.StatusUnprocessableEntity)
	assertContains(t, rec, "Въведете MANDANT_NAME")

	assertStatus(t, s.post("/admin/stores", url.Values{"name": {"Магазин А"}, "mandant_id": {" 7 "}}), http.StatusSeeOther)
	stores, err := h.db.ListStores(ctx, false)
	if err != nil || len(stores) != 1 || stores[0].Name != "Магазин А" || stores[0].MandantID != "7" {
		t.Fatalf("stores = %+v, %v; want one with the trimmed MANDANT_ID 7", stores, err)
	}
	assertContains(t, s.get("/admin/stores"), "MANDANT_ID", "MANDANT_NAME", `value="7"`)

	// The same MANDANT_ID, or the same name, cannot be used twice.
	rec = s.post("/admin/stores", url.Values{"name": {"Друг"}, "mandant_id": {"7"}})
	assertStatus(t, rec, http.StatusUnprocessableEntity)
	assertContains(t, rec, "Вече съществува магазин")
	rec = s.post("/admin/stores", url.Values{"name": {"Магазин А"}, "mandant_id": {"8"}})
	assertStatus(t, rec, http.StatusUnprocessableEntity)

	// Editing keeps the rule.
	id := strconv.FormatInt(stores[0].ID, 10)
	assertStatus(t, s.post("/admin/stores/"+id, url.Values{"name": {"Магазин А"}, "mandant_id": {"9"}, "active": {"1"}}), http.StatusSeeOther)
	if got, _ := h.db.StoreByID(ctx, stores[0].ID); got.MandantID != "9" {
		t.Errorf("MANDANT_ID after edit = %q, want 9", got.MandantID)
	}
	assertStatus(t, s.post("/admin/stores/"+id, url.Values{"name": {"Магазин А"}, "mandant_id": {""}}), http.StatusUnprocessableEntity)
}

// Access follows MANDANT_ID, not the store's name: a store that merely has the
// customer's store name, but another id, gives no access.
func TestClientAccessFollowsTheMandantIDNotTheName(t *testing.T) {
	h := newHarness(t)
	// Named like the Sofia store, but carrying Varna's MANDANT_ID.
	wrong := h.seedStore(mock.StoreSofia, mock.MandantVarna)
	const email = "prodavach@example.com"
	h.seedUser(email, wrong)
	s := h.loginUser(email)

	sofia := h.firstMockClient(mock.StoreSofia)
	varna := h.firstMockClient(mock.StoreVarna)

	assertStatus(t, s.get("/clients/"+sofia.Code), http.StatusForbidden)
	assertStatus(t, s.get("/clients/"+varna.Code), http.StatusOK)

	// And the searched list holds the clients of that MANDANT_ID.
	rec := s.get("/clients/search")
	assertContains(t, rec, varna.Name)
	assertNotContains(t, rec, sofia.Name)
}

// The transport list is looked up by the MANDANT_ID of the stored store that
// carries the customer's serving store name.
func TestTransportsComeFromTheStoresMandantID(t *testing.T) {
	h := newHarness(t)
	const email = "prodavach@example.com"
	sofiaName := h.seedStore(mock.StoreSofia, mock.MandantVarna) // Varna's id on purpose
	h.seedUser(email, sofiaName)
	s := h.loginUser(email)

	rec := s.get("/info/" + h.firstMockClient(mock.StoreSofia).Code)
	assertStatus(t, rec, http.StatusOK)
	assertContains(t, rec, "Куриер Еконт")
	assertNotContains(t, rec, "Куриер Спиди")
}

func TestNoTransportsWhenTheCustomersStoreIsNotKept(t *testing.T) {
	h := newHarness(t)
	const email = "prodavach@example.com"
	other := h.seedStore(mock.StoreVarna, mock.MandantVarna)
	h.seedUser(email, other)
	s := h.loginUser(email)

	rec := s.get("/info/" + h.firstMockClient(mock.StoreSofia).Code)
	assertStatus(t, rec, http.StatusOK)
	assertContains(t, rec, "Няма налични опции")
	assertNotContains(t, rec, "Куриер")
}

// Local activations are listed by the store's name (their MANDANT_NAME
// snapshot).
func TestModulesListUsesTheStoresName(t *testing.T) {
	h := newHarness(t)
	sofia := h.seedStore(mock.StoreSofia, mock.MandantSofia)
	const email = "prodavach@example.com"
	h.seedUser(email, sofia)
	s := h.loginUser(email)

	client := h.firstMockClient(mock.StoreSofia)
	h.seedActivation(client, h.clientLogins(client.Code)[0])

	rec := s.get("/modules")
	assertStatus(t, rec, http.StatusOK)
	assertContains(t, rec, client.Name)
}
