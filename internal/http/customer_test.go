package http

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"haynesproform/internal/dates"
	"haynesproform/internal/external/mock"
	"haynesproform/internal/modules"
	"haynesproform/internal/store"
)

// totpCode generates a valid code for a secret, for the admin login helper.
func totpCode(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, at)
	if err != nil {
		t.Fatalf("generate TOTP code: %v", err)
	}
	return code
}

// sofiaSetup wires one Sofia store with one salesperson, and returns the
// salesperson's email and a Sofia client.
func sofiaSetup(t *testing.T, h *harness) (string, string) {
	t.Helper()
	sofia := h.seedStore("Магазин София", mock.StoreSofia)
	const email = "prodavach@example.com"
	h.seedUser(email, sofia)
	return email, h.firstMockClient(mock.StoreSofia).Code
}

func TestLoginRequiredForCustomerPages(t *testing.T) {
	h := newHarness(t)
	s := h.newSession()

	for _, path := range []string{"/", "/clients/search", "/clients/100000001", "/modules", "/modules/list"} {
		rec := s.get(path)
		if rec.Code != http.StatusSeeOther {
			t.Errorf("GET %s without a session: status %d, want 303", path, rec.Code)
			continue
		}
		if loc := rec.Header().Get("Location"); loc != "/login" {
			t.Errorf("GET %s redirected to %q, want /login", path, loc)
		}
	}
}

func TestUserLoginRejectsBadCredentials(t *testing.T) {
	h := newHarness(t)
	email, _ := sofiaSetup(t, h)
	s := h.newSession()

	rec := s.post("/login", url.Values{"email": {email}, "password": {"wrong-password"}})
	assertStatus(t, rec, http.StatusUnauthorized)
	assertContains(t, rec, "Грешен имейл или парола.")

	// An unknown account produces exactly the same message.
	rec = s.post("/login", url.Values{"email": {"nobody@example.com"}, "password": {testPassword}})
	assertStatus(t, rec, http.StatusUnauthorized)
	assertContains(t, rec, "Грешен имейл или парола.")
}

func TestUserLoginLocksOutAfterRepeatedFailures(t *testing.T) {
	h := newHarness(t)
	email, _ := sofiaSetup(t, h)
	s := h.newSession()

	for i := 0; i < 5; i++ {
		s.post("/login", url.Values{"email": {email}, "password": {"wrong"}})
	}

	// The correct password no longer helps while the lockout stands.
	rec := s.post("/login", url.Values{"email": {email}, "password": {testPassword}})
	assertStatus(t, rec, http.StatusTooManyRequests)
	assertContains(t, rec, "Твърде много неуспешни опити")
}

func TestDeactivatedUserCannotLogIn(t *testing.T) {
	h := newHarness(t)
	email, _ := sofiaSetup(t, h)

	u, err := h.db.UserByEmail(context.Background(), email)
	if err != nil {
		t.Fatalf("read user: %v", err)
	}
	if err := h.db.SetUserActive(context.Background(), u.ID, false); err != nil {
		t.Fatalf("deactivate user: %v", err)
	}

	rec := h.newSession().post("/login", url.Values{"email": {email}, "password": {testPassword}})
	assertStatus(t, rec, http.StatusUnauthorized)
}

// submitForm builds a valid submission for the given client.
func submitForm(h *harness, clientCode string) url.Values {
	logins := h.clientLogins(clientCode)
	return url.Values{
		"usernames":              {logins[0]},
		"module_fast_calculator": {"1"},
		"start_date":             {dates.Today()},
		"months":                 {"3"},
	}
}

func TestSubmitStoresAPendingRequest(t *testing.T) {
	h := newHarness(t)
	email, code := sofiaSetup(t, h)
	s := h.loginUser(email)

	client := h.firstMockClient(mock.StoreSofia)
	rec := s.post("/clients/"+code+"/request", submitForm(h, client.Code))
	assertStatus(t, rec, http.StatusOK)
	assertContains(t, rec, "Запитването е изпратено.")

	reqs, total, err := h.db.ListRequests(context.Background(), store.RequestFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list requests: %v", err)
	}
	if total != 1 {
		t.Fatalf("stored %d requests, want 1", total)
	}

	r := reqs[0]
	if r.Status != store.StatusPending {
		t.Errorf("status = %q, want pending", r.Status)
	}
	if r.ClientCode != client.Code || r.ClientName != client.Name || r.ClientObject != client.Object {
		t.Errorf("client snapshot = %q/%q/%q, want %q/%q/%q",
			r.ClientCode, r.ClientName, r.ClientObject, client.Code, client.Name, client.Object)
	}
	if r.SubmitterEmail != email {
		t.Errorf("submitter = %q, want %q", r.SubmitterEmail, email)
	}
	if len(r.Modules) != 1 || r.Modules[0].Module != modules.FastCalculator {
		t.Errorf("modules = %+v, want only Fast Calculator", r.Modules)
	}
	if r.Months != 3 {
		t.Errorf("months = %d, want 3", r.Months)
	}
}

func TestSubmitForcesTestPeriodRulesServerSide(t *testing.T) {
	h := newHarness(t)
	email, code := sofiaSetup(t, h)
	s := h.loginUser(email)

	client := h.firstMockClient(mock.StoreSofia)
	logins := h.clientLogins(client.Code)

	// A tampered post: test period on, no modules, twelve months, Ultra tier.
	// The browser would not send the disabled fields at all, and the server
	// must impose the rule whatever arrives.
	rec := s.post("/clients/"+code+"/request", url.Values{
		"usernames":      {logins[0]},
		"test_period":    {"1"},
		"haynespro_tier": {"ULTRA"},
		"months":         {"12"},
		"start_date":     {dates.Today()},
	})
	assertStatus(t, rec, http.StatusOK)

	reqs, _, err := h.db.ListRequests(context.Background(), store.RequestFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list requests: %v", err)
	}
	if len(reqs) != 1 {
		t.Fatalf("stored %d requests, want 1", len(reqs))
	}

	r := reqs[0]
	if !r.TestPeriod {
		t.Error("test period was not recorded")
	}
	if r.Months != 1 {
		t.Errorf("months = %d, want 1", r.Months)
	}
	if len(r.Modules) != 2 {
		t.Fatalf("modules = %+v, want Fast Calculator and HaynesPro", r.Modules)
	}

	byKey := map[modules.Key]modules.Tier{}
	for _, m := range r.Modules {
		byKey[m.Module] = m.Tier
	}
	if _, ok := byKey[modules.FastCalculator]; !ok {
		t.Error("Fast Calculator was not forced on")
	}
	if tier := byKey[modules.HaynesPro]; tier != modules.TierBusiness {
		t.Errorf("HaynesPro tier = %q, want BUSINESS", tier)
	}
}

func TestSubmitRejectsATestPeriodAlreadyUsed(t *testing.T) {
	h := newHarness(t)
	email, code := sofiaSetup(t, h)
	client := h.firstMockClient(mock.StoreSofia)
	logins := h.clientLogins(client.Code)

	s := h.loginUser(email)
	first := s.post("/clients/"+code+"/request", url.Values{
		"usernames":   {logins[0]},
		"test_period": {"1"},
		"start_date":  {dates.Today()},
	})
	assertStatus(t, first, http.StatusOK)

	// A second test period for the same client is refused.
	second := s.post("/clients/"+code+"/request", url.Values{
		"usernames":   {logins[0]},
		"test_period": {"1"},
		"start_date":  {dates.Today()},
	})
	assertStatus(t, second, http.StatusUnprocessableEntity)
	assertContains(t, second, "Тестовият период вече е използван за този клиент.")
}

func TestSubmitValidationErrors(t *testing.T) {
	h := newHarness(t)
	email, code := sofiaSetup(t, h)
	client := h.firstMockClient(mock.StoreSofia)
	logins := h.clientLogins(client.Code)

	tests := []struct {
		name string
		form url.Values
		want string
	}{
		{
			name: "no modules",
			form: url.Values{"usernames": {logins[0]}, "start_date": {dates.Today()}, "months": {"3"}},
			want: "Изберете поне един модул.",
		},
		{
			name: "no usernames",
			form: url.Values{"module_fast_calculator": {"1"}, "start_date": {dates.Today()}, "months": {"3"}},
			want: "Изберете поне един потребител.",
		},
		{
			name: "a username of another client",
			form: url.Values{
				"usernames": {"office_999999999"}, "module_fast_calculator": {"1"},
				"start_date": {dates.Today()}, "months": {"3"},
			},
			want: "не принадлежат на този клиент",
		},
		{
			name: "past start date",
			form: url.Values{
				"usernames": {logins[0]}, "module_fast_calculator": {"1"},
				"start_date": {dates.Now().AddDate(0, 0, -1).Format(dates.ISO)}, "months": {"3"},
			},
			want: "Датата на активация не може да е в миналото.",
		},
		{
			name: "HaynesPro without a tier",
			form: url.Values{
				"usernames": {logins[0]}, "module_haynespro": {"1"},
				"start_date": {dates.Today()}, "months": {"3"},
			},
			want: "Изберете ниво за HaynesPro.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := h.loginUser(email)

			rec := s.post("/clients/"+code+"/request", tt.form)
			assertStatus(t, rec, http.StatusUnprocessableEntity)
			assertContains(t, rec, tt.want)

			_, total, err := h.db.ListRequests(context.Background(), store.RequestFilter{Limit: 10})
			if err != nil {
				t.Fatalf("list requests: %v", err)
			}
			if total != 0 {
				t.Errorf("a rejected submission stored %d requests, want 0", total)
			}
		})
	}
}

func TestSubmitRequiresTheCSRFToken(t *testing.T) {
	h := newHarness(t)
	email, code := sofiaSetup(t, h)
	s := h.loginUser(email)

	client := h.firstMockClient(mock.StoreSofia)
	form := submitForm(h, client.Code)
	form.Set("csrf_token", "not-the-right-token")

	rec := s.post("/clients/"+code+"/request", form)
	assertStatus(t, rec, http.StatusForbidden)

	_, total, err := h.db.ListRequests(context.Background(), store.RequestFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list requests: %v", err)
	}
	if total != 0 {
		t.Errorf("a request without a valid CSRF token was stored")
	}
}

func TestSubmitQueuesTheNotificationEmail(t *testing.T) {
	h := newHarness(t)
	email, code := sofiaSetup(t, h)

	if err := h.db.SetSetting(context.Background(), store.KeyNotifyRecipients,
		"admin@example.com, vtori@example.com"); err != nil {
		t.Fatalf("set recipients: %v", err)
	}

	s := h.loginUser(email)
	client := h.firstMockClient(mock.StoreSofia)
	assertStatus(t, s.post("/clients/"+code+"/request", submitForm(h, client.Code)), http.StatusOK)

	msgs, total, err := h.db.ListOutbox(context.Background(), "", 10, 0)
	if err != nil {
		t.Fatalf("list outbox: %v", err)
	}
	if total != 1 {
		t.Fatalf("queued %d emails, want 1", total)
	}
	if msgs[0].TemplateKey != "request_created" {
		t.Errorf("template = %q, want request_created", msgs[0].TemplateKey)
	}
	if got := msgs[0].RecipientList(); len(got) != 2 {
		t.Errorf("recipients = %v, want two addresses", got)
	}
	if !contains(msgs[0].Subject, client.Name) {
		t.Errorf("subject %q does not name the client", msgs[0].Subject)
	}
}

// TestSubmitSucceedsWithoutSMTP proves a request is never lost because email
// is unconfigured: the message is queued and the submission still succeeds.
func TestSubmitSucceedsWithoutSMTP(t *testing.T) {
	h := newHarness(t)
	email, code := sofiaSetup(t, h)

	s := h.loginUser(email)
	client := h.firstMockClient(mock.StoreSofia)

	rec := s.post("/clients/"+code+"/request", submitForm(h, client.Code))
	assertStatus(t, rec, http.StatusOK)
	assertContains(t, rec, "Запитването е изпратено.")

	_, total, err := h.db.ListRequests(context.Background(), store.RequestFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list requests: %v", err)
	}
	if total != 1 {
		t.Errorf("stored %d requests, want 1", total)
	}
}

func TestLogoutEndsTheSession(t *testing.T) {
	h := newHarness(t)
	email, _ := sofiaSetup(t, h)
	s := h.loginUser(email)

	assertStatus(t, s.post("/logout", url.Values{}), http.StatusSeeOther)

	rec := s.get("/")
	assertStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Errorf("after logout redirected to %q, want /login", loc)
	}
}

func TestSecurityHeadersArePresent(t *testing.T) {
	h := newHarness(t)
	rec := h.newSession().get("/login")

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "same-origin",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}

	csp := rec.Header().Get("Content-Security-Policy")
	for _, fragment := range []string{"default-src 'self'", "script-src 'self'", "frame-ancestors 'none'"} {
		if !contains(csp, fragment) {
			t.Errorf("CSP %q is missing %q", csp, fragment)
		}
	}
	// The Alpine CSP build means no unsafe-eval is needed.
	if contains(csp, "unsafe-eval") {
		t.Errorf("CSP allows unsafe-eval: %q", csp)
	}
}

func TestHealthzReportsBothDatabases(t *testing.T) {
	h := newHarness(t)
	rec := h.newSession().get("/healthz")
	assertStatus(t, rec, http.StatusOK)
	assertContains(t, rec, `"status":"ok"`, `"database":"ok"`, `"external":"ok"`)
}

func TestUnknownPathRendersTheBulgarian404(t *testing.T) {
	h := newHarness(t)
	rec := h.newSession().get("/does-not-exist")
	assertStatus(t, rec, http.StatusNotFound)
	assertContains(t, rec, "Страницата не е намерена")
}

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }
