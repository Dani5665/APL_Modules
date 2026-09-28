package http

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"haynesproform/internal/auth"
	"haynesproform/internal/dates"
	"haynesproform/internal/external/mock"
	"haynesproform/internal/modules"
	"haynesproform/internal/store"
)

// adminSetup wires a store, a salesperson, an admin and one pending request,
// and returns the admin session and the request id.
func adminSetup(t *testing.T, h *harness) (*session, int64) {
	t.Helper()

	email, code := sofiaSetup(t, h)
	user := h.loginUser(email)
	user.followEntryLink(code, "ivan.petrov")

	client := h.firstMockClient(mock.StoreSofia)
	assertStatus(t, user.post("/request", submitForm(h, client.Code)), http.StatusOK)

	reqs, _, err := h.db.ListRequests(context.Background(), store.RequestFilter{Limit: 1})
	if err != nil || len(reqs) == 0 {
		t.Fatalf("no request was stored: %v", err)
	}

	admin := h.seedAdmin("admin@example.com")
	return h.loginAdmin(admin), reqs[0].ID
}

func TestAdminPagesRequireLogin(t *testing.T) {
	h := newHarness(t)
	s := h.newSession()

	paths := []string{
		"/admin/requests", "/admin/activations", "/admin/users", "/admin/admins",
		"/admin/stores", "/admin/settings/smtp", "/admin/settings/templates",
		"/admin/settings/schedule", "/admin/outbox", "/admin/audit",
	}
	for _, p := range paths {
		rec := s.get(p)
		if rec.Code != http.StatusSeeOther {
			t.Errorf("GET %s without a session: status %d, want 303", p, rec.Code)
			continue
		}
		if loc := rec.Header().Get("Location"); loc != "/admin/login" {
			t.Errorf("GET %s redirected to %q, want /admin/login", p, loc)
		}
	}
}

// TestUserSessionCannotReachAdmin proves the two account types are separate:
// a salesperson's cookie grants nothing in the admin part.
func TestUserSessionCannotReachAdmin(t *testing.T) {
	h := newHarness(t)
	email, _ := sofiaSetup(t, h)
	s := h.loginUser(email)

	rec := s.get("/admin/requests")
	assertStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/admin/login" {
		t.Errorf("redirected to %q, want /admin/login", loc)
	}
}

func TestAdminLoginRequiresTheSecondFactor(t *testing.T) {
	h := newHarness(t)
	admin := h.seedAdmin("admin@example.com")
	s := h.newSession()

	rec := s.post("/admin/login", url.Values{"email": {admin.Email}, "password": {testPassword}})
	assertStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/admin/login/2fa" {
		t.Fatalf("after the password step redirected to %q, want /admin/login/2fa", loc)
	}

	// The password alone must not open the admin part.
	rec = s.get("/admin/requests")
	assertStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/admin/login/2fa" {
		t.Errorf("a half-authenticated session reached %q, want the 2FA page", loc)
	}
}

// TestAdminMustChangePasswordBlocksEveryPageUntilChanged is the gate in
// requireAdmin: a forced password change (bootstrap admin, or a reset by
// another admin) must hold even once the admin is fully logged in and even
// if they never follow the "Смяна на парола" link - a bookmark or the
// recovery-codes page's own link to /admin must not be able to skip it.
func TestAdminMustChangePasswordBlocksEveryPageUntilChanged(t *testing.T) {
	h := newHarness(t)
	admin := h.seedAdminMustChangePassword("admin@example.com")
	s := h.loginAdmin(admin)

	rec := s.get("/admin/requests")
	assertStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/admin/password" {
		t.Fatalf("a session owing a password change reached %q, want /admin/password", loc)
	}

	// The change form itself, and logging out, must still work.
	assertStatus(t, s.get("/admin/password"), http.StatusOK)

	const newPassword = "brand-new-password-123"
	rec = s.post("/admin/password", url.Values{
		"current_password": {testPassword},
		"new_password":     {newPassword},
		"confirm_password": {newPassword},
	})
	assertStatus(t, rec, http.StatusSeeOther)

	// Once changed, the same session reaches the rest of the admin part.
	assertStatus(t, s.get("/admin/requests"), http.StatusOK)
}

func TestAdminTOTPRejectsAWrongCode(t *testing.T) {
	h := newHarness(t)
	admin := h.seedAdmin("admin@example.com")
	s := h.newSession()

	assertStatus(t, s.post("/admin/login",
		url.Values{"email": {admin.Email}, "password": {testPassword}}), http.StatusSeeOther)
	s.refreshCSRF(auth.AdminAudience)

	rec := s.post("/admin/login/2fa", url.Values{"code": {"000000"}})
	assertStatus(t, rec, http.StatusUnauthorized)
	assertContains(t, rec, "Невалиден код.")
}

// TestAdminTOTPRejectsAReplayedCode covers the replay guard: the same code
// must not work twice inside its window.
func TestAdminTOTPRejectsAReplayedCode(t *testing.T) {
	h := newHarness(t)
	admin := h.seedAdmin("admin@example.com")

	fresh, err := h.db.AdminByID(context.Background(), admin.ID)
	if err != nil {
		t.Fatalf("reload admin: %v", err)
	}
	secret, err := h.app.Enc.Decrypt(fresh.TOTPSecretEnc)
	if err != nil {
		t.Fatalf("decrypt secret: %v", err)
	}
	code := totpCode(t, secret, time.Now())

	// First use succeeds.
	first := h.newSession()
	assertStatus(t, first.post("/admin/login",
		url.Values{"email": {admin.Email}, "password": {testPassword}}), http.StatusSeeOther)
	first.refreshCSRF(auth.AdminAudience)
	assertStatus(t, first.post("/admin/login/2fa", url.Values{"code": {code}}), http.StatusSeeOther)

	// The same code, in the same window, is refused for a second login.
	second := h.newSession()
	assertStatus(t, second.post("/admin/login",
		url.Values{"email": {admin.Email}, "password": {testPassword}}), http.StatusSeeOther)
	second.refreshCSRF(auth.AdminAudience)

	rec := second.post("/admin/login/2fa", url.Values{"code": {code}})
	assertStatus(t, rec, http.StatusUnauthorized)
	assertContains(t, rec, "Невалиден код.")
}

func TestAdminEnrollmentOnFirstLogin(t *testing.T) {
	h := newHarness(t)

	hash, err := auth.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := h.db.CreateAdmin(context.Background(), "new@example.com", hash, false); err != nil {
		t.Fatalf("create admin: %v", err)
	}

	s := h.newSession()
	rec := s.post("/admin/login", url.Values{"email": {"new@example.com"}, "password": {testPassword}})
	assertStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/admin/login/enroll" {
		t.Fatalf("redirected to %q, want /admin/login/enroll", loc)
	}
	s.refreshCSRF(auth.AdminAudience)

	rec = s.get("/admin/login/enroll")
	assertStatus(t, rec, http.StatusOK)
	assertContains(t, rec, "Настройка на двуфакторна автентикация", "data:image/png;base64,")

	// Pull the candidate secret out of the rendered hidden field.
	secret := extractHiddenValue(t, rec.Body.String(), "secret")

	// A wrong code keeps the same secret on the page.
	bad := s.post("/admin/login/enroll", url.Values{"secret": {secret}, "code": {"000000"}})
	assertStatus(t, bad, http.StatusUnprocessableEntity)
	assertContains(t, bad, "Невалиден код")
	if again := extractHiddenValue(t, bad.Body.String(), "secret"); again != secret {
		t.Error("a failed confirmation produced a different secret; the admin would have to re-scan")
	}

	// The right code completes enrollment and shows the recovery codes once.
	good := s.post("/admin/login/enroll",
		url.Values{"secret": {secret}, "code": {totpCode(t, secret, time.Now())}})
	assertStatus(t, good, http.StatusOK)
	assertContains(t, good, "Кодове за възстановяване", "Запишете тези кодове сега.")

	n, err := h.db.CountUnusedRecoveryCodes(context.Background(), 1)
	if err != nil {
		t.Fatalf("count recovery codes: %v", err)
	}
	if n != recoveryCodeCount {
		t.Errorf("stored %d recovery codes, want %d", n, recoveryCodeCount)
	}
}

// extractHiddenValue pulls the value of a hidden input out of rendered HTML.
func extractHiddenValue(t *testing.T, body, name string) string {
	t.Helper()
	marker := `name="` + name + `" value="`
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("no hidden field %q in the response", name)
	}
	rest := body[i+len(marker):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		t.Fatalf("malformed hidden field %q", name)
	}
	return rest[:j]
}

// TestAdmin2FADisabledSkipsTheSecondFactor exercises the dev-only bypass:
// with ADMIN_2FA_ENABLED=false (only ever valid alongside EXTERNAL_DB_MODE
// =mock; see config.Load and DECISIONS.md), the password alone completes
// the login, whether or not the admin has TOTP enrolled.
func TestAdmin2FADisabledSkipsTheSecondFactor(t *testing.T) {
	h := newHarnessNo2FA(t)
	admin := h.seedAdmin("admin@example.com") // pre-enrolled; must be ignored

	s := h.newSession()
	rec := s.post("/admin/login", url.Values{"email": {admin.Email}, "password": {testPassword}})
	assertStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/admin" {
		t.Fatalf("after login redirected to %q, want /admin", loc)
	}
	s.refreshCSRF(auth.AdminAudience)

	// The session is immediately usable; no /admin/login/2fa detour.
	rec = s.get("/admin/requests")
	assertStatus(t, rec, http.StatusOK)
}

// TestAdmin2FADisabledWorksWithoutEnrollment covers the common dev case: a
// brand-new admin with no TOTP secret at all still logs straight in.
func TestAdmin2FADisabledWorksWithoutEnrollment(t *testing.T) {
	h := newHarnessNo2FA(t)

	hash, err := auth.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := h.db.CreateAdmin(context.Background(), "new@example.com", hash, false); err != nil {
		t.Fatalf("create admin: %v", err)
	}

	s := h.newSession()
	rec := s.post("/admin/login", url.Values{"email": {"new@example.com"}, "password": {testPassword}})
	assertStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/admin" {
		t.Fatalf("after login redirected to %q, want /admin (no enrollment detour)", loc)
	}
}

func TestAdminPagesRender(t *testing.T) {
	h := newHarness(t)
	s, requestID := adminSetup(t, h)

	pages := []struct {
		path string
		want string
	}{
		{"/admin/requests", "Запитвания"},
		{"/admin/requests/" + strconv.FormatInt(requestID, 10), "Данни от подаването"},
		{"/admin/activations", "Клиенти и активации"},
		{"/admin/activations/new", "Нова активация"},
		{"/admin/users", "Потребители (търговци)"},
		{"/admin/users/new", "Нов потребител"},
		{"/admin/admins", "Администратори"},
		{"/admin/stores", "Магазини"},
		{"/admin/settings/smtp", "SMTP настройки"},
		{"/admin/settings/templates", "Имейл шаблони"},
		{"/admin/settings/templates/request_created", "Налични променливи"},
		{"/admin/settings/schedule", "График на месечната справка"},
		{"/admin/outbox", "Изходящи имейли"},
		{"/admin/audit", "Одит"},
		{"/admin/password", "Смяна на парола"},
	}

	for _, p := range pages {
		t.Run(p.path, func(t *testing.T) {
			rec := s.get(p.path)
			assertStatus(t, rec, http.StatusOK)
			assertContains(t, rec, p.want)
		})
	}
}

func TestApproveCreatesOneActivationPerUsernameAndModule(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	email, code := sofiaSetup(t, h)

	client := h.firstMockClient(mock.StoreSofia)
	logins := h.clientLogins(client.Code)
	if len(logins) < 2 {
		t.Fatalf("the seeded client has %d usernames, want at least 2", len(logins))
	}

	user := h.loginUser(email)
	user.followEntryLink(code, "ivan.petrov")
	assertStatus(t, user.post("/request", url.Values{
		"usernames":              {logins[0], logins[1]},
		"module_fast_calculator": {"1"},
		"module_haynespro":       {"1"},
		"haynespro_tier":         {"PRO"},
		"start_date":             {dates.Today()},
		"months":                 {"3"},
	}), http.StatusOK)

	reqs, _, err := h.db.ListRequests(ctx, store.RequestFilter{Limit: 1})
	if err != nil || len(reqs) == 0 {
		t.Fatalf("no request stored: %v", err)
	}
	id := reqs[0].ID

	admin := h.seedAdmin("admin@example.com")
	s := h.loginAdmin(admin)

	rec := s.post("/admin/requests/"+strconv.FormatInt(id, 10)+"/approve",
		url.Values{"admin_comment": {"Одобрено по договор."}})
	assertStatus(t, rec, http.StatusSeeOther)

	// Two usernames times two modules.
	acts, err := h.db.ListActivationsForClient(ctx, client.Code)
	if err != nil {
		t.Fatalf("list activations: %v", err)
	}
	if len(acts) != 4 {
		t.Fatalf("created %d activations, want 4", len(acts))
	}

	wantEnd, err := dates.EndDate(dates.Today(), 3)
	if err != nil {
		t.Fatalf("end date: %v", err)
	}
	for _, a := range acts {
		if a.EndDate != wantEnd {
			t.Errorf("activation %d ends %q, want %q", a.ID, a.EndDate, wantEnd)
		}
		if !a.SourceRequestID.Valid || a.SourceRequestID.Int64 != id {
			t.Errorf("activation %d is not linked to request %d", a.ID, id)
		}
		if a.Module == modules.HaynesPro && a.Tier != modules.TierPro {
			t.Errorf("HaynesPro activation %d has tier %q, want PRO", a.ID, a.Tier)
		}
		if !a.IsActive() {
			t.Errorf("activation %d is not active today", a.ID)
		}
	}

	updated, err := h.db.RequestByID(ctx, id)
	if err != nil {
		t.Fatalf("reload request: %v", err)
	}
	if updated.Status != store.StatusApproved {
		t.Errorf("status = %q, want approved", updated.Status)
	}
	if updated.AdminComment != "Одобрено по договор." {
		t.Errorf("comment = %q", updated.AdminComment)
	}
	if !updated.DecidedByAdminID.Valid || updated.DecidedByAdminID.Int64 != admin.ID {
		t.Error("the deciding admin was not recorded")
	}
}

func TestApproveQueuesTheEmailToTheSubmitterOnly(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if err := h.db.SetSetting(ctx, store.KeyNotifyRecipients, "admin-notify@example.com"); err != nil {
		t.Fatalf("set recipients: %v", err)
	}

	s, id := adminSetup(t, h)
	assertStatus(t, s.post("/admin/requests/"+strconv.FormatInt(id, 10)+"/approve",
		url.Values{}), http.StatusSeeOther)

	msgs, _, err := h.db.ListOutbox(ctx, "", 10, 0)
	if err != nil {
		t.Fatalf("list outbox: %v", err)
	}

	var approved *store.OutboxMessage
	for i := range msgs {
		if msgs[i].TemplateKey == "request_approved" {
			approved = &msgs[i]
		}
	}
	if approved == nil {
		t.Fatal("no approval email was queued")
	}

	got := approved.RecipientList()
	if len(got) != 1 || got[0] != "prodavach@example.com" {
		t.Errorf("approval email recipients = %v, want only the submitter", got)
	}
}

func TestDenyDoesNotCreateActivations(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	s, id := adminSetup(t, h)

	rec := s.post("/admin/requests/"+strconv.FormatInt(id, 10)+"/deny",
		url.Values{"admin_comment": {"Няма договор."}})
	assertStatus(t, rec, http.StatusSeeOther)

	client := h.firstMockClient(mock.StoreSofia)
	acts, err := h.db.ListActivationsForClient(ctx, client.Code)
	if err != nil {
		t.Fatalf("list activations: %v", err)
	}
	if len(acts) != 0 {
		t.Errorf("a denial created %d activations, want 0", len(acts))
	}

	r, err := h.db.RequestByID(ctx, id)
	if err != nil {
		t.Fatalf("reload request: %v", err)
	}
	if r.Status != store.StatusDenied {
		t.Errorf("status = %q, want denied", r.Status)
	}
}

// TestDeniedTestPeriodIsNotConsumed covers the rule that only pending or
// approved test periods count against the client's single one.
func TestDeniedTestPeriodIsNotConsumed(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	email, code := sofiaSetup(t, h)
	client := h.firstMockClient(mock.StoreSofia)
	logins := h.clientLogins(client.Code)

	user := h.loginUser(email)
	user.followEntryLink(code, "ivan.petrov")
	assertStatus(t, user.post("/request", url.Values{
		"usernames":   {logins[0]},
		"test_period": {"1"},
		"start_date":  {dates.Today()},
	}), http.StatusOK)

	reqs, _, _ := h.db.ListRequests(ctx, store.RequestFilter{Limit: 1})
	id := reqs[0].ID

	admin := h.seedAdmin("admin@example.com")
	s := h.loginAdmin(admin)
	assertStatus(t, s.post("/admin/requests/"+strconv.FormatInt(id, 10)+"/deny",
		url.Values{}), http.StatusSeeOther)

	used, err := h.db.TestPeriodUsed(ctx, client.Code, 0)
	if err != nil {
		t.Fatalf("test period check: %v", err)
	}
	if used {
		t.Error("a denied test period was counted as used")
	}

	// And a fresh test-period request is accepted again.
	user2 := h.loginUser(email)
	user2.followEntryLink(code, "ivan.petrov")
	rec := user2.post("/request", url.Values{
		"usernames":   {logins[0]},
		"test_period": {"1"},
		"start_date":  {dates.Today()},
	})
	assertStatus(t, rec, http.StatusOK)
	assertContains(t, rec, "Запитването е изпратено.")
}

func TestDecidingATwiceDecidedRequestIsRefused(t *testing.T) {
	h := newHarness(t)
	s, id := adminSetup(t, h)
	path := "/admin/requests/" + strconv.FormatInt(id, 10)

	assertStatus(t, s.post(path+"/approve", url.Values{}), http.StatusSeeOther)

	// A second decision must neither change the request nor add activations.
	rec := s.post(path+"/deny", url.Values{})
	assertStatus(t, rec, http.StatusConflict)

	r, err := h.db.RequestByID(context.Background(), id)
	if err != nil {
		t.Fatalf("reload request: %v", err)
	}
	if r.Status != store.StatusApproved {
		t.Errorf("status = %q, want it to stay approved", r.Status)
	}

	client := h.firstMockClient(mock.StoreSofia)
	acts, _ := h.db.ListActivationsForClient(context.Background(), client.Code)
	if len(acts) != 1 {
		t.Errorf("after a repeated decision there are %d activations, want 1", len(acts))
	}
}

func TestAdminEditRequestBeforeDeciding(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	s, id := adminSetup(t, h)

	client := h.firstMockClient(mock.StoreSofia)
	logins := h.clientLogins(client.Code)

	rec := s.post("/admin/requests/"+strconv.FormatInt(id, 10)+"/edit", url.Values{
		"usernames":        {logins[0], logins[1]},
		"module_haynespro": {"1"},
		"haynespro_tier":   {"ULTRA"},
		"start_date":       {dates.Today()},
		"months":           {"6"},
	})
	assertStatus(t, rec, http.StatusSeeOther)

	r, err := h.db.RequestByID(ctx, id)
	if err != nil {
		t.Fatalf("reload request: %v", err)
	}
	if len(r.Usernames) != 2 {
		t.Errorf("usernames = %v, want two", r.Usernames)
	}
	if len(r.Modules) != 1 || r.Modules[0].Module != modules.HaynesPro || r.Modules[0].Tier != modules.TierUltra {
		t.Errorf("modules = %+v, want only HaynesPro Ultra", r.Modules)
	}
	if r.Months != 6 {
		t.Errorf("months = %d, want 6", r.Months)
	}
}

func TestAdminEditRejectsAnInvalidChange(t *testing.T) {
	h := newHarness(t)
	s, id := adminSetup(t, h)

	rec := s.post("/admin/requests/"+strconv.FormatInt(id, 10)+"/edit", url.Values{
		"usernames":  {},
		"start_date": {dates.Today()},
		"months":     {"3"},
	})
	assertStatus(t, rec, http.StatusUnprocessableEntity)
	assertContains(t, rec, "Изберете поне един потребител.")
}

func TestAdminExportDownloadsAWorkbook(t *testing.T) {
	h := newHarness(t)
	s, id := adminSetup(t, h)
	assertStatus(t, s.post("/admin/requests/"+strconv.FormatInt(id, 10)+"/approve",
		url.Values{}), http.StatusSeeOther)

	rec := s.get("/admin/activations/export.xlsx")
	assertStatus(t, rec, http.StatusOK)

	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "spreadsheetml") {
		t.Errorf("Content-Type = %q, want a spreadsheet type", ct)
	}
	disp := rec.Header().Get("Content-Disposition")
	if !strings.Contains(disp, "aktivni_moduli_") || !strings.HasSuffix(disp, `.xlsx"`) {
		t.Errorf("Content-Disposition = %q", disp)
	}
	// A valid xlsx is a zip, so it starts with the local file header magic.
	if body := rec.Body.Bytes(); len(body) < 4 || string(body[:2]) != "PK" {
		t.Error("the downloaded file is not a zip container")
	}
}

func TestAdminExportGroupedByClient(t *testing.T) {
	h := newHarness(t)
	s, id := adminSetup(t, h)
	assertStatus(t, s.post("/admin/requests/"+strconv.FormatInt(id, 10)+"/approve",
		url.Values{}), http.StatusSeeOther)

	rec := s.get("/admin/activations/export.xlsx?group=1")
	assertStatus(t, rec, http.StatusOK)
	if disp := rec.Header().Get("Content-Disposition"); !strings.Contains(disp, "aktivni_moduli_po_klienti_") {
		t.Errorf("Content-Disposition = %q, want the grouped file name", disp)
	}
}

func TestAdminManualActivationLifecycle(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	s, _ := adminSetup(t, h)

	create := url.Values{
		"client_code":   {"100000099"},
		"client_name":   {"Ръчен Клиент ООД"},
		"client_object": {"Централен обект"},
		"client_store":  {"Магазин София"},
		"username":      {"office_100000099"},
		"module":        {"HAYNESPRO"},
		"tier":          {"ULTRA"},
		"start_date":    {dates.Today()},
		"months":        {"6"},
	}
	assertStatus(t, s.post("/admin/activations", create), http.StatusSeeOther)

	acts, err := h.db.ListActivationsForClient(ctx, "100000099")
	if err != nil || len(acts) != 1 {
		t.Fatalf("created %d activations, want 1 (err %v)", len(acts), err)
	}
	a := acts[0]

	wantEnd, _ := dates.EndDate(dates.Today(), 6)
	if a.EndDate != wantEnd {
		t.Errorf("end date = %q, want %q (derived from the month count)", a.EndDate, wantEnd)
	}

	// Revoke, then restore, then delete.
	idStr := strconv.FormatInt(a.ID, 10)
	assertStatus(t, s.post("/admin/activations/"+idStr+"/revoke", url.Values{}), http.StatusSeeOther)
	if got, _ := h.db.ActivationByID(ctx, a.ID); got.Status() != store.ActivationRevoked {
		t.Errorf("status after revoke = %q, want revoked", got.Status())
	}

	assertStatus(t, s.post("/admin/activations/"+idStr+"/restore", url.Values{}), http.StatusSeeOther)
	if got, _ := h.db.ActivationByID(ctx, a.ID); got.Status() != store.ActivationActive {
		t.Errorf("status after restore = %q, want active", got.Status())
	}

	assertStatus(t, s.post("/admin/activations/"+idStr+"/delete", url.Values{}), http.StatusSeeOther)
	if acts, _ := h.db.ListActivationsForClient(ctx, "100000099"); len(acts) != 0 {
		t.Errorf("after delete there are %d activations, want 0", len(acts))
	}
}

func TestAdminManualActivationValidation(t *testing.T) {
	h := newHarness(t)
	s, _ := adminSetup(t, h)

	rec := s.post("/admin/activations", url.Values{
		"client_code": {""},
		"client_name": {""},
		"username":    {""},
		"module":      {"HAYNESPRO"},
		"start_date":  {"not-a-date"},
	})
	assertStatus(t, rec, http.StatusUnprocessableEntity)
	assertContains(t, rec,
		"Въведете клиентски номер.",
		"Въведете име на клиент.",
		"Въведете потребител.",
		"Невалидна начална дата.",
	)
}

func TestAdminUserManagement(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	s, _ := adminSetup(t, h)

	stores, err := h.db.ListStores(ctx, true)
	if err != nil || len(stores) == 0 {
		t.Fatalf("no stores: %v", err)
	}

	rec := s.post("/admin/users", url.Values{
		"email":    {"nov@example.com"},
		"password": {"nova-parola-123"},
		"stores":   {strconv.FormatInt(stores[0].ID, 10)},
	})
	assertStatus(t, rec, http.StatusSeeOther)

	u, err := h.db.UserByEmail(ctx, "nov@example.com")
	if err != nil {
		t.Fatalf("the new user was not created: %v", err)
	}
	if len(u.Stores) != 1 {
		t.Errorf("stores = %v, want one", u.Stores)
	}

	// A duplicate email is refused with a Bulgarian message.
	rec = s.post("/admin/users", url.Values{
		"email":    {"nov@example.com"},
		"password": {"nova-parola-123"},
		"stores":   {strconv.FormatInt(stores[0].ID, 10)},
	})
	assertStatus(t, rec, http.StatusUnprocessableEntity)
	assertContains(t, rec, "Вече съществува потребител с този имейл.")

	// So is a username that is not an email address.
	rec = s.post("/admin/users", url.Values{
		"email":    {"not-an-email"},
		"password": {"nova-parola-123"},
		"stores":   {strconv.FormatInt(stores[0].ID, 10)},
	})
	assertStatus(t, rec, http.StatusUnprocessableEntity)
	assertContains(t, rec, "валиден имейл адрес")

	// And a password below the minimum length.
	rec = s.post("/admin/users", url.Values{
		"email":    {"drug@example.com"},
		"password": {"kratka"},
		"stores":   {strconv.FormatInt(stores[0].ID, 10)},
	})
	assertStatus(t, rec, http.StatusUnprocessableEntity)
	assertContains(t, rec, "поне 10 знака")
}

func TestAdminCannotDeleteThemselvesOrTheLastAdmin(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	s, _ := adminSetup(t, h)

	admins, err := h.db.ListAdmins(ctx)
	if err != nil || len(admins) != 1 {
		t.Fatalf("expected exactly one admin, got %d (err %v)", len(admins), err)
	}
	self := admins[0]

	rec := s.post("/admin/admins/"+strconv.FormatInt(self.ID, 10)+"/delete", url.Values{})
	assertStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "err=self_delete") {
		t.Errorf("self-deletion redirected to %q, want the self_delete error", loc)
	}

	if got, _ := h.db.ListAdmins(ctx); len(got) != 1 {
		t.Error("the admin deleted themselves")
	}
}

func TestAdminStoreCannotBeDeletedWhileAssigned(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	s, _ := adminSetup(t, h)

	stores, err := h.db.ListStores(ctx, false)
	if err != nil || len(stores) == 0 {
		t.Fatalf("no stores: %v", err)
	}
	id := strconv.FormatInt(stores[0].ID, 10)

	rec := s.post("/admin/stores/"+id+"/delete", url.Values{})
	assertStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "err=store_in_use") {
		t.Errorf("redirected to %q, want the store_in_use error", loc)
	}

	if got, _ := h.db.ListStores(ctx, false); len(got) != len(stores) {
		t.Error("a store in use was deleted")
	}
}

func TestAdminSMTPSettingsRoundTrip(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	s, _ := adminSetup(t, h)

	rec := s.post("/admin/settings/smtp", url.Values{
		"host":              {"smtp.example.com"},
		"port":              {"587"},
		"security":          {"starttls"},
		"username":          {"mailer"},
		"password":          {"tajna-parola"},
		"from_address":      {"no-reply@example.com"},
		"from_name":         {"Модули"},
		"notify_recipients": {"admin@example.com\nvtori@example.com"},
	})
	assertStatus(t, rec, http.StatusSeeOther)

	// The password is stored encrypted, never in the clear.
	sealed, err := h.db.Setting(ctx, store.KeySMTPPasswordEnc, "")
	if err != nil {
		t.Fatalf("read setting: %v", err)
	}
	if sealed == "" || strings.Contains(sealed, "tajna-parola") {
		t.Error("the SMTP password was not stored encrypted")
	}
	plain, err := h.app.Enc.Decrypt([]byte(sealed))
	if err != nil || plain != "tajna-parola" {
		t.Errorf("decrypted password = %q, %v", plain, err)
	}

	// The form never renders the password back.
	page := s.get("/admin/settings/smtp")
	assertStatus(t, page, http.StatusOK)
	assertNotContains(t, page, "tajna-parola")
	assertContains(t, page, "smtp.example.com", "admin@example.com")

	// Saving with an empty password keeps the stored one.
	assertStatus(t, s.post("/admin/settings/smtp", url.Values{
		"host": {"smtp.example.com"}, "port": {"587"}, "security": {"starttls"},
		"username": {"mailer"}, "password": {""},
		"from_address": {"no-reply@example.com"},
	}), http.StatusSeeOther)

	again, _ := h.db.Setting(ctx, store.KeySMTPPasswordEnc, "")
	if again != sealed {
		t.Error("an empty password field overwrote the stored password")
	}
}

func TestAdminSMTPRejectsInvalidRecipients(t *testing.T) {
	h := newHarness(t)
	s, _ := adminSetup(t, h)

	rec := s.post("/admin/settings/smtp", url.Values{
		"host": {"smtp.example.com"}, "port": {"587"}, "security": {"none"},
		"from_address":      {"no-reply@example.com"},
		"notify_recipients": {"valid@example.com, broken@, also-broken"},
	})
	assertStatus(t, rec, http.StatusUnprocessableEntity)
	assertContains(t, rec, "Невалидни адреси за известия")
}

func TestAdminTemplatePreviewEscapesSampleValues(t *testing.T) {
	h := newHarness(t)
	s, _ := adminSetup(t, h)

	rec := s.post("/admin/settings/templates/request_created/preview", url.Values{
		"subject":   {"Запитване за {{client_name}}"},
		"body_html": {"<p>{{client_name}} — {{client_code}}</p>"},
	})
	assertStatus(t, rec, http.StatusOK)
	assertContains(t, rec, "Преглед с примерни данни", "Автосервиз Балкан ЕООД", "100000001")
}

func TestAdminTemplateSaveAndRestore(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	s, _ := adminSetup(t, h)

	assertStatus(t, s.post("/admin/settings/templates/request_denied", url.Values{
		"subject":   {"Променена тема"},
		"body_html": {"<p>Променено съдържание.</p>"},
	}), http.StatusSeeOther)

	tpl, err := h.db.EmailTemplateByKey(ctx, "request_denied")
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	if tpl.Subject != "Променена тема" {
		t.Errorf("subject = %q", tpl.Subject)
	}

	assertStatus(t, s.post("/admin/settings/templates/request_denied/restore",
		url.Values{}), http.StatusSeeOther)

	restored, err := h.db.EmailTemplateByKey(ctx, "request_denied")
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	if restored.Subject == "Променена тема" {
		t.Error("restore did not bring back the default subject")
	}
}

// TestAdminTemplateSaveWarnsAboutUnknownPlaceholders proves a typo is
// reported rather than silently swallowed.
func TestAdminTemplateSaveWarnsAboutUnknownPlaceholders(t *testing.T) {
	h := newHarness(t)
	s, _ := adminSetup(t, h)

	rec := s.post("/admin/settings/templates/request_approved", url.Values{
		"subject":   {"Одобрено {{request_id}}"},
		"body_html": {"<p>{{client_name}} {{gresna_promenliva}}</p>"},
	})
	assertStatus(t, rec, http.StatusOK)
	assertContains(t, rec, "Непознати променливи", "gresna_promenliva")
}

func TestAdminScheduleSave(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	s, _ := adminSetup(t, h)

	assertStatus(t, s.post("/admin/settings/schedule", url.Values{
		"enabled":         {"1"},
		"day_of_month":    {"31"},
		"time_hhmm":       {"08:30"},
		"recipients":      {"upravitel@example.com"},
		"group_by_client": {"1"},
	}), http.StatusSeeOther)

	sch, err := h.db.ExportSchedule(ctx)
	if err != nil {
		t.Fatalf("read schedule: %v", err)
	}
	if !sch.Enabled || sch.DayOfMonth != 31 || sch.TimeHHMM != "08:30" {
		t.Errorf("schedule = %+v", sch)
	}
	if !sch.GroupByClient {
		t.Error("group_by_client was not saved")
	}
	if !sch.NextRunAt.Valid {
		t.Fatal("no next run was computed")
	}
	next, err := dates.ParseUTC(sch.NextRunAt.String)
	if err != nil {
		t.Fatalf("parse next run: %v", err)
	}
	if !next.After(time.Now()) {
		t.Errorf("next run %s is not in the future", next)
	}

	// The checkbox state is remembered: re-rendering the page without
	// resubmitting it still shows it checked.
	page := s.get("/admin/settings/schedule")
	body := page.Body.String()
	if i := strings.Index(body, `id="group_by_client"`); i < 0 || !strings.Contains(body[i:i+120], "checked") {
		t.Error("the group_by_client checkbox is not rendered as checked")
	}

	// Leaving the box unchecked on a later save turns it back off.
	assertStatus(t, s.post("/admin/settings/schedule", url.Values{
		"enabled":      {"1"},
		"day_of_month": {"31"},
		"time_hhmm":    {"08:30"},
		"recipients":   {"upravitel@example.com"},
	}), http.StatusSeeOther)
	sch, err = h.db.ExportSchedule(ctx)
	if err != nil {
		t.Fatalf("read schedule: %v", err)
	}
	if sch.GroupByClient {
		t.Error("group_by_client should be off after saving without the checkbox")
	}
}

func TestAdminScheduleRejectsEnablingWithoutRecipients(t *testing.T) {
	h := newHarness(t)
	s, _ := adminSetup(t, h)

	rec := s.post("/admin/settings/schedule", url.Values{
		"enabled":      {"1"},
		"day_of_month": {"1"},
		"time_hhmm":    {"08:00"},
		"recipients":   {""},
	})
	assertStatus(t, rec, http.StatusUnprocessableEntity)
	assertContains(t, rec, "Добавете поне един получател")
}

func TestAdminSendNowQueuesTheExport(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	s, _ := adminSetup(t, h)

	assertStatus(t, s.post("/admin/settings/schedule", url.Values{
		"day_of_month": {"1"}, "time_hhmm": {"08:00"},
		"recipients": {"upravitel@example.com"},
	}), http.StatusSeeOther)

	assertStatus(t, s.post("/admin/settings/schedule/send-now", url.Values{}), http.StatusSeeOther)

	msgs, _, err := h.db.ListOutbox(ctx, "", 20, 0)
	if err != nil {
		t.Fatalf("list outbox: %v", err)
	}
	var found bool
	for _, m := range msgs {
		if m.TemplateKey == "scheduled_export" {
			found = true
			if !m.AttachmentName.Valid || !strings.HasSuffix(m.AttachmentName.String, ".xlsx") {
				t.Errorf("attachment = %v, want an .xlsx file", m.AttachmentName)
			}
		}
	}
	if !found {
		t.Error("the export email was not queued")
	}
}

func TestAdminSendNowUsesTheSavedGroupByClientChoice(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	s, _ := adminSetup(t, h)

	assertStatus(t, s.post("/admin/settings/schedule", url.Values{
		"day_of_month":    {"1"},
		"time_hhmm":       {"08:00"},
		"recipients":      {"upravitel@example.com"},
		"group_by_client": {"1"},
	}), http.StatusSeeOther)

	assertStatus(t, s.post("/admin/settings/schedule/send-now", url.Values{}), http.StatusSeeOther)

	msgs, _, err := h.db.ListOutbox(ctx, "", 20, 0)
	if err != nil {
		t.Fatalf("list outbox: %v", err)
	}
	var found bool
	for _, m := range msgs {
		if m.TemplateKey == "scheduled_export" {
			found = true
			if !m.AttachmentName.Valid || !strings.Contains(m.AttachmentName.String, "po_klienti") {
				t.Errorf("attachment = %v, want the grouped file name", m.AttachmentName)
			}
		}
	}
	if !found {
		t.Error("the export email was not queued")
	}
}

func TestAuditLogRecordsTheImportantActions(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	s, id := adminSetup(t, h)

	assertStatus(t, s.post("/admin/requests/"+strconv.FormatInt(id, 10)+"/approve",
		url.Values{}), http.StatusSeeOther)

	entries, _, err := h.db.ListAudit(ctx, store.AuditFilter{Limit: 100})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}

	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.Action] = true
	}
	for _, want := range []string{"login.success", "totp.success", "request.submitted", "request.approved"} {
		if !seen[want] {
			t.Errorf("the audit log has no %q entry", want)
		}
	}

	// Nothing secret may reach the log.
	for _, e := range entries {
		for _, forbidden := range []string{testPassword, "password_hash", "totp_secret"} {
			if strings.Contains(e.DetailsJSON, forbidden) {
				t.Errorf("audit entry %d leaks %q: %s", e.ID, forbidden, e.DetailsJSON)
			}
		}
	}
}

func TestAdminLogoutEndsTheSession(t *testing.T) {
	h := newHarness(t)
	s, _ := adminSetup(t, h)

	assertStatus(t, s.post("/admin/logout", url.Values{}), http.StatusSeeOther)

	rec := s.get("/admin/requests")
	assertStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/admin/login" {
		t.Errorf("after logout redirected to %q, want /admin/login", loc)
	}
}

func TestAdminActionsRequireCSRF(t *testing.T) {
	h := newHarness(t)
	s, id := adminSetup(t, h)

	form := url.Values{"csrf_token": {"wrong-token"}}
	rec := s.post("/admin/requests/"+strconv.FormatInt(id, 10)+"/approve", form)
	assertStatus(t, rec, http.StatusForbidden)

	r, err := h.db.RequestByID(context.Background(), id)
	if err != nil {
		t.Fatalf("reload request: %v", err)
	}
	if r.Status != store.StatusPending {
		t.Errorf("a request was decided without a CSRF token: status %q", r.Status)
	}
}
