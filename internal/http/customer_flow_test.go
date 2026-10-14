package http

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"haynesproform/internal/dates"
	"haynesproform/internal/external/mock"
	"haynesproform/internal/store"
)

func TestClientSearchListsTheStoresClients(t *testing.T) {
	h := newHarness(t)
	email, _ := sofiaSetup(t, h)
	s := h.loginUser(email)

	sofia := h.firstMockClient(mock.StoreSofia)
	varna := h.firstMockClient(mock.StoreVarna)

	// The page itself loads the results afterwards.
	rec := s.get("/")
	assertStatus(t, rec, http.StatusOK)
	assertContains(t, rec, `hx-get="/clients/search"`)
	assertNotContains(t, rec, sofia.Name)

	rec = s.get("/clients/search")
	assertStatus(t, rec, http.StatusOK)
	assertContains(t, rec, sofia.Name, "/clients/"+sofia.Code)
	assertNotContains(t, rec, varna.Name)

	rec = s.get("/clients/search?q=" + url.QueryEscape(strings.ToUpper(sofia.Name)))
	assertStatus(t, rec, http.StatusOK)
	assertContains(t, rec, sofia.Name)

	rec = s.get("/clients/search?q=" + url.QueryEscape("няма-такъв-клиент"))
	assertStatus(t, rec, http.StatusOK)
	assertContains(t, rec, "Няма намерени клиенти по това търсене.")
}

func TestHomeKeepsTheSearchAndPageInTheLoadURL(t *testing.T) {
	h := newHarness(t)
	email, _ := sofiaSetup(t, h)
	s := h.loginUser(email)

	rec := s.get("/?q=" + url.QueryEscape("балкан") + "&page=2")
	assertStatus(t, rec, http.StatusOK)
	assertContains(t, rec, `value="балкан"`, `hx-get="/clients/search?page=2&amp;q=`)
}

func TestClientPageShowsTheClientAndTheRequestForm(t *testing.T) {
	h := newHarness(t)
	email, code := sofiaSetup(t, h)
	s := h.loginUser(email)

	rec := s.get("/clients/" + code)
	assertStatus(t, rec, http.StatusOK)

	client := h.firstMockClient(mock.StoreSofia)
	assertContains(t, rec,
		client.Code,
		client.Name,
		client.Object,
		"Активни модули по потребители",
		"Чакащи запитвания за този клиент",
		"Тест период",
		"Fast Calculator",
		"HaynesPro",
		"Изпрати запитване",
		`hx-post="/clients/`+code+`/request"`,
	)
	for _, u := range h.clientLogins(client.Code) {
		assertContains(t, rec, u)
	}
}

func TestClientPageDeniesAClientOfAnotherStore(t *testing.T) {
	h := newHarness(t)
	email, _ := sofiaSetup(t, h)
	s := h.loginUser(email)

	varna := h.firstMockClient(mock.StoreVarna)

	rec := s.get("/clients/" + varna.Code)
	assertStatus(t, rec, http.StatusForbidden)
	assertContains(t, rec, "Нямате достъп до този клиент.")
	// No client data may leak on refusal.
	assertNotContains(t, rec, varna.Name, varna.Object)

	// Submitting for it is refused too, and stores nothing.
	rec = s.post("/clients/"+varna.Code+"/request", submitForm(h, varna.Code))
	assertStatus(t, rec, http.StatusForbidden)
	_, total, err := h.db.ListRequests(context.Background(), store.RequestFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list requests: %v", err)
	}
	if total != 0 {
		t.Errorf("a request for another store's client was stored")
	}
}

func TestClientPageForAnUnknownClientIsDenied(t *testing.T) {
	h := newHarness(t)
	email, _ := sofiaSetup(t, h)
	s := h.loginUser(email)

	rec := s.get("/clients/999999999")
	assertStatus(t, rec, http.StatusForbidden)
	assertContains(t, rec, "Нямате достъп до този клиент.")
}

func TestClientPageShowsPendingRequests(t *testing.T) {
	h := newHarness(t)
	email, code := sofiaSetup(t, h)
	s := h.loginUser(email)

	rec := s.get("/clients/" + code)
	assertContains(t, rec, "Няма чакащи запитвания за този клиент.")

	client := h.firstMockClient(mock.StoreSofia)
	assertStatus(t, s.post("/clients/"+code+"/request", submitForm(h, client.Code)), http.StatusOK)

	rec = s.get("/clients/" + code)
	assertStatus(t, rec, http.StatusOK)
	assertNotContains(t, rec, "Няма чакащи запитвания за този клиент.")
}

// TestModulesPageListsOnlyClientsWithActiveModules: the modules list is
// sourced from local activations, not from every client the external
// directory knows about.
func TestModulesPageListsOnlyClientsWithActiveModules(t *testing.T) {
	h := newHarness(t)
	email, _ := sofiaSetup(t, h)
	s := h.loginUser(email)

	rec := s.get("/modules")
	assertStatus(t, rec, http.StatusOK)
	assertContains(t, rec, "Няма клиенти с активни модули за вашите магазини.")
	assertNotContains(t, rec, h.firstMockClient(mock.StoreSofia).Name)

	sofiaClient := h.firstMockClient(mock.StoreSofia)
	varnaClient := h.firstMockClient(mock.StoreVarna)
	h.seedActivation(sofiaClient, h.clientLogins(sofiaClient.Code)[0])
	// Activated, but at a store this account does not cover.
	h.seedActivation(varnaClient, h.clientLogins(varnaClient.Code)[0])

	rec = s.get("/modules")
	assertStatus(t, rec, http.StatusOK)
	assertContains(t, rec, sofiaClient.Name)
	assertNotContains(t, rec, varnaClient.Name)
}

func TestModulesSearchFiltersTheList(t *testing.T) {
	h := newHarness(t)
	email, _ := sofiaSetup(t, h)
	s := h.loginUser(email)

	client := h.firstMockClient(mock.StoreSofia)
	login := h.clientLogins(client.Code)[0]
	h.seedActivation(client, login)

	rec := s.get("/modules/list?q=" + url.QueryEscape(client.Code))
	assertStatus(t, rec, http.StatusOK)
	assertContains(t, rec, `class="group-head"`, client.Code, client.Name, login)

	// A username also finds its client.
	rec = s.get("/modules/list?q=" + url.QueryEscape(login))
	assertStatus(t, rec, http.StatusOK)
	assertContains(t, rec, client.Name)

	rec = s.get("/modules/list?q=" + url.QueryEscape("няма-такъв-клиент"))
	assertStatus(t, rec, http.StatusOK)
	assertContains(t, rec, "Няма намерени клиенти с активни модули по това търсене.")
}

// TestUsersCannotChangeTheirPassword: passwords are set by administrators
// only, so no user-facing route exists.
func TestUsersCannotChangeTheirPassword(t *testing.T) {
	h := newHarness(t)
	email, _ := sofiaSetup(t, h)
	s := h.loginUser(email)

	assertStatus(t, s.get("/password"), http.StatusNotFound)
	rec := s.post("/password", url.Values{
		"current_password": {testPassword},
		"new_password":     {"nova-parola-1234"},
		"confirm_password": {"nova-parola-1234"},
	})
	if rec.Code == http.StatusSeeOther || rec.Code == http.StatusOK {
		t.Errorf("POST /password status = %d, want a refusal", rec.Code)
	}
	assertStatus(t, h.newSession().post("/login",
		url.Values{"email": {email}, "password": {testPassword}}), http.StatusSeeOther)
}

// TestLoginIgnoresURLParameters: the login page takes nothing from the URL.
func TestLoginIgnoresURLParameters(t *testing.T) {
	h := newHarness(t)
	email, _ := sofiaSetup(t, h)

	s := h.newSession()
	rec := s.get("/login?client=100000001&login=ivan.petrov&next=/modules")
	assertStatus(t, rec, http.StatusOK)
	assertNotContains(t, rec, "100000001", "ivan.petrov")

	rec = s.post("/login?next=/modules", url.Values{"email": {email}, "password": {testPassword}})
	assertStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Errorf("login redirected to %q, want /", loc)
	}
}

func TestEntryLinkRouteIsGone(t *testing.T) {
	h := newHarness(t)
	assertStatus(t, h.newSession().get("/r/100000001/ivan.petrov"), http.StatusNotFound)
}

func TestLoginPreloadsTheClientList(t *testing.T) {
	h := newHarness(t)
	email, _ := sofiaSetup(t, h)

	// The preload runs in the background; the page that follows must work
	// whether or not it has finished.
	s := h.loginUser(email)
	assertStatus(t, s.get("/clients/search"), http.StatusOK)
}

// TestRequestWithoutAnEndDate: choosing "Без крайна дата" stores an unlimited
// request, and approving it creates activations that never expire.
func TestRequestWithoutAnEndDate(t *testing.T) {
	h := newHarness(t)
	email, code := sofiaSetup(t, h)
	s := h.loginUser(email)

	rec := s.get("/clients/" + code)
	assertContains(t, rec, `<option value="0">Без крайна дата</option>`)

	client := h.firstMockClient(mock.StoreSofia)
	form := submitForm(h, client.Code)
	form.Set("months", "0")
	assertStatus(t, s.post("/clients/"+code+"/request", form), http.StatusOK)

	ctx := context.Background()
	reqs, _, err := h.db.ListRequests(ctx, store.RequestFilter{Limit: 10})
	if err != nil || len(reqs) != 1 {
		t.Fatalf("requests = %d, err %v; want 1", len(reqs), err)
	}
	r, err := h.db.RequestByID(ctx, reqs[0].ID)
	if err != nil {
		t.Fatalf("RequestByID: %v", err)
	}
	if r.Months != 0 || r.EndDate() != dates.NoEndDate || r.DurationLabel() != "Без крайна дата" {
		t.Fatalf("request = months %d, end %q, label %q", r.Months, r.EndDate(), r.DurationLabel())
	}

	admin := h.seedAdmin("admin@example.com")
	if _, err := h.app.Requests.Approve(ctx, r.ID, admin.ID, ""); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	acts, err := h.db.ListActivationsForClient(ctx, client.Code)
	if err != nil || len(acts) == 0 {
		t.Fatalf("activations = %d, err %v", len(acts), err)
	}
	for _, a := range acts {
		if a.EndDate != dates.NoEndDate || a.StatusOn("2099-01-01") != store.ActivationActive {
			t.Errorf("activation end %q status %v, want an unlimited active one", a.EndDate, a.StatusOn("2099-01-01"))
		}
	}

	rec = s.get("/clients/" + code)
	assertContains(t, rec, "Без крайна дата")
}

func TestActivationPageWarnsAboutOtherStores(t *testing.T) {
	h := newHarness(t)
	email, _ := sofiaSetup(t, h)
	s := h.loginUser(email)

	rec := s.get("/")
	assertStatus(t, rec, http.StatusOK)
	assertContains(t, rec, "Виждате клиентите на следните обекти:",
		"Ако не намирате клиента, който търсите може да е, защото има различен обслужващ обект от тези които вие виждате")
}

func TestSidebarShowsTheUsersNameAboveTheEmail(t *testing.T) {
	h := newHarness(t)
	email, _ := sofiaSetup(t, h)
	s := h.loginUser(email)

	rec := s.get("/")
	assertStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	n, e := strings.Index(body, "Тестов Потребител"), strings.Index(body, email)
	if n < 0 || e < 0 || n > e {
		t.Errorf("name at %d, email at %d; want the name shown, above the email", n, e)
	}
}

func TestAdminRequestStatusIsCalledProcessed(t *testing.T) {
	h := newHarness(t)
	s, id := adminSetup(t, h)

	assertStatus(t, s.post("/admin/requests/"+strconv.FormatInt(id, 10)+"/approve", url.Values{}), http.StatusSeeOther)

	rec := s.get("/admin/requests")
	assertStatus(t, rec, http.StatusOK)
	// The list's badge and the status filter both say "Обработено".
	assertContains(t, rec, "Обработено")
	assertNotContains(t, rec, "Одобрено")
}

func TestAuditPageStatesTheRetention(t *testing.T) {
	h := newHarness(t)
	s, _ := adminSetup(t, h)
	assertContains(t, s.get("/admin/audit"), "Записите се пазят 3 месеца")
}

func TestOutboxPageStatesTheRetention(t *testing.T) {
	h := newHarness(t)
	s, _ := adminSetup(t, h)
	assertContains(t, s.get("/admin/outbox"), "Имейлите се пазят 3 месеца")
}

// The schedule time is picked with 24-hour selects, not a browser time field.
func TestScheduleTimeUsesTwentyFourHourSelects(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	s, _ := adminSetup(t, h)
	h.setTemplateRecipients("scheduled_export", "upravitel@example.com")

	rec := s.get("/admin/settings/schedule")
	assertStatus(t, rec, http.StatusOK)
	assertContains(t, rec, `<select id="hour" name="hour"`, `<select id="minute" name="minute"`,
		`<option value="23"`, `<option value="00"`)
	assertNotContains(t, rec, `type="time"`)

	assertStatus(t, s.post("/admin/settings/schedule", url.Values{
		"enabled": {"1"}, "day_of_month": {"5"}, "hour": {"20"}, "minute": {"45"},
	}), http.StatusSeeOther)
	sch, err := h.db.ExportSchedule(ctx)
	if err != nil || sch.TimeHHMM != "20:45" {
		t.Fatalf("saved time = %q, %v; want 20:45", sch.TimeHHMM, err)
	}

	// The saved time comes back selected.
	rec = s.get("/admin/settings/schedule")
	assertContains(t, rec, `<option value="20" selected>`, `<option value="45" selected>`)

	// Early morning keeps its leading zero.
	assertStatus(t, s.post("/admin/settings/schedule", url.Values{
		"enabled": {"1"}, "day_of_month": {"5"}, "hour": {"07"}, "minute": {"05"},
	}), http.StatusSeeOther)
	if sch, _ = h.db.ExportSchedule(ctx); sch.TimeHHMM != "07:05" {
		t.Errorf("saved time = %q, want 07:05", sch.TimeHHMM)
	}

	// An impossible time is refused.
	rec = s.post("/admin/settings/schedule", url.Values{
		"enabled": {"1"}, "day_of_month": {"5"}, "hour": {"24"}, "minute": {"00"},
	})
	assertStatus(t, rec, http.StatusUnprocessableEntity)
	assertContains(t, rec, "ЧЧ:ММ")
}
