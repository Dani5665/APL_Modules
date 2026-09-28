package http

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"haynesproform/internal/audit"
	"haynesproform/internal/auth"
	"haynesproform/internal/config"
	"haynesproform/internal/dates"
	"haynesproform/internal/email"
	"haynesproform/internal/entrylink"
	"haynesproform/internal/export"
	"haynesproform/internal/external"
	"haynesproform/internal/external/mock"
	"haynesproform/internal/modules"
	"haynesproform/internal/requests"
	"haynesproform/internal/scheduler"
	"haynesproform/internal/store"
)

// harness is a fully wired application backed by a temporary database and the
// mock external directory.
type harness struct {
	t       *testing.T
	app     *App
	handler http.Handler
	db      *store.DB
	dir     *mock.Directory
}

const testPassword = "test-parola-123"

// newHarness builds an application for tests: real database, real handlers,
// mock external directory, no SMTP. Admin logins require 2FA, matching the
// production default.
func newHarness(t *testing.T) *harness {
	return newHarnessWithConfig(t, nil)
}

// newHarnessNo2FA builds a harness with admin 2FA disabled, exercising the
// dev-only bypass (see DECISIONS.md). Only valid with the mock directory,
// which every test harness already uses.
func newHarnessNo2FA(t *testing.T) *harness {
	return newHarnessWithConfig(t, func(cfg *config.Config) { cfg.Admin2FAEnabled = false })
}

// newHarnessWithConfig builds an application for tests, letting the caller
// adjust the configuration before the handler is wired up.
func newHarnessWithConfig(t *testing.T, mutate func(*config.Config)) *harness {
	t.Helper()
	ctx := context.Background()

	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := email.SeedTemplates(ctx, db); err != nil {
		t.Fatalf("seed templates: %v", err)
	}

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	enc, err := auth.NewEncrypter(key)
	if err != nil {
		t.Fatalf("new encrypter: %v", err)
	}

	cfg := config.Config{
		Addr:                   ":0",
		BaseURL:                "http://test.local",
		DataDir:                t.TempDir(),
		ExternalMode:           config.ExternalMock,
		EncryptionKey:          key,
		SessionIdleTimeout:     time.Hour,
		SessionAbsoluteTimeout: 8 * time.Hour,
		CookieSecure:           false,
		ClientListCacheTTL:     time.Minute,
		Admin2FAEnabled:        true,
	}
	if mutate != nil {
		mutate(&cfg)
	}

	// Discard log output; a failing test reports through t, not the log.
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	dir := mock.New()
	auditLog := audit.New(db, log)
	composer := email.NewComposer(db, cfg.BaseURL)
	outbox := email.NewWorker(db, enc, log)
	generator := export.NewGenerator(db)

	app := &App{
		Cfg:       cfg,
		DB:        db,
		Directory: dir,
		Sessions:  auth.NewManager(db, cfg.SessionIdleTimeout, cfg.SessionAbsoluteTimeout, false),
		Throttle:  auth.NewThrottle(db),
		Enc:       enc,
		Audit:     auditLog,
		Requests:  requests.NewService(db),
		Composer:  composer,
		Outbox:    outbox,
		Export:    generator,
		Scheduler: scheduler.New(db, generator, composer, auditLog, log, outbox.Notify),
		EntryLink: entrylink.PathParser{},
		Log:       log,
	}

	handler, err := NewHandler(app)
	if err != nil {
		t.Fatalf("build handler: %v", err)
	}

	return &harness{t: t, app: app, handler: handler, db: db, dir: dir}
}

// seedStore creates a store and returns its id.
func (h *harness) seedStore(name, external string) int64 {
	h.t.Helper()
	id, err := h.db.CreateStore(context.Background(), name, external)
	if err != nil {
		h.t.Fatalf("create store %s: %v", name, err)
	}
	return id
}

// seedUser creates an active salesperson assigned to the given stores.
func (h *harness) seedUser(email string, storeIDs ...int64) *store.User {
	h.t.Helper()
	return h.seedUserWith(email, false, storeIDs...)
}

// seedUserMustChangePassword is seedUser but for a user whose password was
// just reset by an admin, and so must be changed before anything else works.
func (h *harness) seedUserMustChangePassword(email string, storeIDs ...int64) *store.User {
	h.t.Helper()
	return h.seedUserWith(email, true, storeIDs...)
}

func (h *harness) seedUserWith(email string, mustChangePassword bool, storeIDs ...int64) *store.User {
	h.t.Helper()
	hash, err := auth.HashPassword(testPassword)
	if err != nil {
		h.t.Fatalf("hash password: %v", err)
	}
	if _, err := h.db.CreateUser(context.Background(), email, hash, storeIDs, mustChangePassword); err != nil {
		h.t.Fatalf("create user %s: %v", email, err)
	}
	u, err := h.db.UserByEmail(context.Background(), email)
	if err != nil {
		h.t.Fatalf("read back user %s: %v", email, err)
	}
	return u
}

// seedAdmin creates an admin with 2FA already enrolled.
func (h *harness) seedAdmin(email string) *store.Admin {
	h.t.Helper()
	return h.seedAdminWith(email, false)
}

// seedAdminMustChangePassword is seedAdmin but for an admin whose password
// was set by someone else (a bootstrap or an admin-to-admin reset) and so
// must be changed before anything else works.
func (h *harness) seedAdminMustChangePassword(email string) *store.Admin {
	h.t.Helper()
	return h.seedAdminWith(email, true)
}

func (h *harness) seedAdminWith(email string, mustChangePassword bool) *store.Admin {
	h.t.Helper()
	ctx := context.Background()

	hash, err := auth.HashPassword(testPassword)
	if err != nil {
		h.t.Fatalf("hash password: %v", err)
	}
	id, err := h.db.CreateAdmin(ctx, email, hash, mustChangePassword)
	if err != nil {
		h.t.Fatalf("create admin %s: %v", email, err)
	}

	enrollment, err := auth.NewEnrollment(email)
	if err != nil {
		h.t.Fatalf("new enrollment: %v", err)
	}
	sealed, err := h.app.Enc.Encrypt(enrollment.Secret)
	if err != nil {
		h.t.Fatalf("encrypt secret: %v", err)
	}
	if err := h.db.EnrollAdminTOTP(ctx, id, sealed, 0); err != nil {
		h.t.Fatalf("enroll TOTP: %v", err)
	}

	a, err := h.db.AdminByID(ctx, id)
	if err != nil {
		h.t.Fatalf("read back admin: %v", err)
	}
	return a
}

// session is a logged-in browser: it holds the cookies and the CSRF token.
type session struct {
	h       *harness
	cookies []*http.Cookie
	csrf    string
}

// newSession starts an empty browser session.
func (h *harness) newSession() *session { return &session{h: h} }

// do performs a request, carrying the session's cookies and storing any that
// come back.
func (s *session) do(method, target string, form url.Values) *httptest.ResponseRecorder {
	s.h.t.Helper()

	var req *http.Request
	if form == nil {
		req = httptest.NewRequest(method, target, nil)
	} else {
		if s.csrf != "" && form.Get(auth.CSRFFieldName) == "" {
			form.Set(auth.CSRFFieldName, s.csrf)
		}
		req = httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, c := range s.cookies {
		req.AddCookie(c)
	}

	rec := httptest.NewRecorder()
	s.h.handler.ServeHTTP(rec, req)
	s.storeCookies(rec.Result().Cookies())
	return rec
}

func (s *session) get(target string) *httptest.ResponseRecorder {
	return s.do(http.MethodGet, target, nil)
}

func (s *session) post(target string, form url.Values) *httptest.ResponseRecorder {
	return s.do(http.MethodPost, target, form)
}

// storeCookies merges the response cookies into the session jar.
func (s *session) storeCookies(cookies []*http.Cookie) {
	for _, c := range cookies {
		// A cookie cleared by the server is dropped from the jar.
		if c.MaxAge < 0 || c.Value == "" {
			s.removeCookie(c.Name)
			continue
		}
		s.setCookie(c)
	}
}

func (s *session) setCookie(c *http.Cookie) {
	for i, existing := range s.cookies {
		if existing.Name == c.Name {
			s.cookies[i] = c
			return
		}
	}
	s.cookies = append(s.cookies, c)
}

func (s *session) removeCookie(name string) {
	for i, c := range s.cookies {
		if c.Name == name {
			s.cookies = append(s.cookies[:i], s.cookies[i+1:]...)
			return
		}
	}
}

// refreshCSRF reads the current session's CSRF token straight from the
// database, which is simpler and less brittle than scraping the HTML.
func (s *session) refreshCSRF(aud auth.Audience) {
	s.h.t.Helper()
	for _, c := range s.cookies {
		if c.Name != aud.CookieName {
			continue
		}
		sess, err := s.h.db.SessionByHash(context.Background(), auth.HashToken(c.Value))
		if err != nil {
			s.h.t.Fatalf("read session: %v", err)
		}
		s.csrf = sess.CSRFToken
		return
	}
	s.h.t.Fatalf("no %s cookie in the session", aud.CookieName)
}

// loginUser logs a salesperson in and captures their CSRF token.
func (h *harness) loginUser(email string) *session {
	h.t.Helper()
	s := h.newSession()

	rec := s.post("/login", url.Values{"email": {email}, "password": {testPassword}})
	if rec.Code != http.StatusSeeOther {
		h.t.Fatalf("login as %s: status %d, want 303\n%s", email, rec.Code, rec.Body.String())
	}
	s.refreshCSRF(auth.UserAudience)
	return s
}

// loginAdmin logs an admin in. There is no second factor - see DECISIONS.md
// on removing 2FA.
func (h *harness) loginAdmin(admin *store.Admin) *session {
	h.t.Helper()
	s := h.newSession()

	rec := s.post("/admin/login", url.Values{"email": {admin.Email}, "password": {testPassword}})
	if rec.Code != http.StatusSeeOther {
		h.t.Fatalf("admin password step: status %d, want 303\n%s", rec.Code, rec.Body.String())
	}
	s.refreshCSRF(auth.AdminAudience)

	// With 2FA disabled (see newHarnessNo2FA), the password step already
	// completes the login.
	if loc := rec.Header().Get("Location"); loc == "/admin" {
		return s
	}

	// Otherwise the session still owes a second factor: reload the admin, the
	// stored TOTP secret is what the code must match.
	fresh, err := h.db.AdminByID(context.Background(), admin.ID)
	if err != nil {
		h.t.Fatalf("reload admin: %v", err)
	}
	secret, err := h.app.Enc.Decrypt(fresh.TOTPSecretEnc)
	if err != nil {
		h.t.Fatalf("decrypt TOTP secret: %v", err)
	}
	code := totpCode(h.t, secret, time.Now())

	rec = s.post("/admin/login/2fa", url.Values{"code": {code}})
	if rec.Code != http.StatusSeeOther {
		h.t.Fatalf("admin 2FA step: status %d, want 303\n%s", rec.Code, rec.Body.String())
	}
	s.refreshCSRF(auth.AdminAudience)
	return s
}

// followEntryLink visits the entry link so the request form has a client.
func (s *session) followEntryLink(code, login string) *httptest.ResponseRecorder {
	s.h.t.Helper()
	return s.get("/r/" + code + "/" + login)
}

// assertStatus fails the test unless the recorder carries the wanted status.
func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d\nbody:\n%s", rec.Code, want, truncateBody(rec.Body.String()))
	}
}

// assertContains fails the test unless the body contains every fragment.
func assertContains(t *testing.T, rec *httptest.ResponseRecorder, fragments ...string) {
	t.Helper()
	body := rec.Body.String()
	for _, f := range fragments {
		if !strings.Contains(body, f) {
			t.Errorf("response does not contain %q\nbody:\n%s", f, truncateBody(body))
		}
	}
}

// assertNotContains fails the test if the body contains any fragment.
func assertNotContains(t *testing.T, rec *httptest.ResponseRecorder, fragments ...string) {
	t.Helper()
	body := rec.Body.String()
	for _, f := range fragments {
		if strings.Contains(body, f) {
			t.Errorf("response unexpectedly contains %q\nbody:\n%s", f, truncateBody(body))
		}
	}
}

func truncateBody(s string) string {
	const max = 4000
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n… (truncated)"
}

// firstMockClient returns a seeded client of the given external store.
func (h *harness) firstMockClient(storeValue string) external.Client {
	h.t.Helper()
	clients, err := h.dir.ListClientsByStores(context.Background(), []string{storeValue})
	if err != nil || len(clients) == 0 {
		h.t.Fatalf("no mock clients for store %s: %v", storeValue, err)
	}
	return clients[0]
}

// seedActivation inserts one active activation directly, for tests that need
// a client to show up in the "Clients of my stores" list without going
// through the full request/approve flow.
func (h *harness) seedActivation(c external.Client, username string) {
	h.t.Helper()
	end, err := dates.EndDate(dates.Today(), 3)
	if err != nil {
		h.t.Fatalf("EndDate: %v", err)
	}
	_, err = h.db.InsertActivation(context.Background(), h.db.DB, store.Activation{
		ClientCode: c.Code, ClientName: c.Name, ClientObject: c.Object, ClientStore: c.Store,
		Username: username, Module: modules.FastCalculator,
		StartDate: dates.Today(), EndDate: end,
	})
	if err != nil {
		h.t.Fatalf("seed activation for %s: %v", c.Code, err)
	}
}

// clientLogins returns a client's usernames from the mock directory, keyed by
// the 9-digit client code.
func (h *harness) clientLogins(clientCode string) []string {
	h.t.Helper()
	logins, err := h.dir.ListClientLogins(context.Background(), clientCode)
	if err != nil {
		h.t.Fatalf("list client logins: %v", err)
	}
	return logins
}
