package http

import (
	"haynesproform/internal/external"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"haynesproform/internal/external/mock"
)

func TestInfoPageExplainsItselfAndLoadsTheList(t *testing.T) {
	h := newHarness(t)
	email, _ := sofiaSetup(t, h)
	s := h.loginUser(email)

	rec := s.get("/info")
	assertStatus(t, rec, http.StatusOK)
	assertContains(t, rec,
		"Тук можете да намерите информация за всички клиенти и да пуснете заявка за създаване на профил в NEXT",
		`hx-get="/info/search"`,
	)
}

// The information list is not limited to the user's stores.
func TestInfoSearchListsAllCustomers(t *testing.T) {
	h := newHarness(t)
	email, _ := sofiaSetup(t, h)
	s := h.loginUser(email)

	sofia := h.firstMockClient(mock.StoreSofia)
	varna := h.firstMockClient(mock.StoreVarna)

	rec := s.get("/info/search")
	assertStatus(t, rec, http.StatusOK)
	assertContains(t, rec, sofia.Name, varna.Name, "/info/"+sofia.Code)

	rec = s.get("/info/search?q=" + url.QueryEscape(strings.ToUpper(varna.Name)))
	assertStatus(t, rec, http.StatusOK)
	assertContains(t, rec, varna.Name)
	assertNotContains(t, rec, sofia.Name)

	rec = s.get("/info/search?q=" + url.QueryEscape("няма-такъв-клиент"))
	assertContains(t, rec, "Няма намерени клиенти.")
}

func TestInfoDetailShowsTheCustomerAndItsProfiles(t *testing.T) {
	h := newHarness(t)
	email, code := sofiaSetup(t, h)
	s := h.loginUser(email)

	rec := s.get("/info/" + code)
	assertStatus(t, rec, http.StatusOK)

	client := h.firstMockClient(mock.StoreSofia)
	assertContains(t, rec, client.Code, client.Name, "Обслужващ обект", "Адрес", "Телефон",
		"Отговорно лице", "Последен вход", "Статус")
	for _, u := range h.clientLogins(client.Code) {
		assertContains(t, rec, u)
	}
	assertNotContains(t, rec, "Клиентът няма създаден профил в NEXT")
}

func TestInfoDetailShowsDashesAndTheNoProfileMessage(t *testing.T) {
	h := newHarness(t)
	email, _ := sofiaSetup(t, h)
	s := h.loginUser(email)

	// The seeded customer without a profile also has no address or phone.
	rec := s.get("/info/100000099")
	assertStatus(t, rec, http.StatusOK)
	assertContains(t, rec, "Клиент без профил ЕООД", "Клиентът няма създаден профил в NEXT",
		`<span>-</span>`)
}

func TestInfoDetailForAnUnknownCustomerIs404(t *testing.T) {
	h := newHarness(t)
	email, _ := sofiaSetup(t, h)
	s := h.loginUser(email)

	assertStatus(t, s.get("/info/999999998"), http.StatusNotFound)
}

func TestMenuHasTheNewGroupsAndNames(t *testing.T) {
	h := newHarness(t)
	email, _ := sofiaSetup(t, h)
	s := h.loginUser(email)

	rec := s.get("/")
	assertStatus(t, rec, http.StatusOK)
	assertContains(t, rec, "NEXT - КАТАЛОГ", "Информация клиенти", "NEXT - МОДУЛИ",
		"Активация - модули", "Списък - активни", "Потребители - NEXT")
}

func TestInfoListScrollsInBatchesOf25(t *testing.T) {
	h := newHarness(t)
	email, _ := sofiaSetup(t, h)
	s := h.loginUser(email)

	// 20 seeded clients plus the one without a profile: 21 in all, so one
	// batch holds everything and nothing asks for more.
	rec := s.get("/info/search")
	assertStatus(t, rec, http.StatusOK)
	if n := strings.Count(rec.Body.String(), `class="client-card"`); n != 21 {
		t.Errorf("first batch has %d customers, want 21", n)
	}
	assertNotContains(t, rec, `hx-trigger="revealed"`, "Показани")

	// The batch size itself.
	if infoPageSize != 25 {
		t.Errorf("infoPageSize = %d, want 25", infoPageSize)
	}
}

func TestInfoListLoadsTheNextBatchOnScroll(t *testing.T) {
	all := make([]external.Customer, 60)
	for i := range all {
		all[i] = external.Customer{Code: strconv.Itoa(100000000 + i), Name: "Клиент " + strconv.Itoa(i)}
	}
	v := infoView{Customers: all[:infoPageSize], Page: pagination{Page: 1, PageSize: infoPageSize, Total: 60,
		Query: map[string]string{"q": "клиент"}}}

	r, err := newRenderer()
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	if err := r.renderPartial(rec, http.StatusOK, "customer/info", "info_results", v); err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	if n := strings.Count(body, `class="client-card"`); n != 25 {
		t.Errorf("first batch shows %d customers, want 25", n)
	}
	for _, want := range []string{`hx-trigger="revealed"`, `/info/search?page=2&amp;q=`, "Общо клиенти: 60"} {
		if !strings.Contains(body, want) {
			t.Errorf("first batch lacks %q:\n%s", want, body)
		}
	}

	// The last batch ends the chain.
	v.Page.Page = 3
	v.Customers = all[50:]
	rec = httptest.NewRecorder()
	if err := r.renderPartial(rec, http.StatusOK, "customer/info", "info_more", v); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.Body.String(), "revealed") {
		t.Error("the last batch still asks for more")
	}
}
