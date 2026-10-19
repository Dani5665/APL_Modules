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
)

func TestValidLatinAlnum(t *testing.T) {
	tests := []struct {
		s    string
		want bool
	}{
		{"abcde", true}, {"Abc12XYZ", true}, {"12345", true},
		{"abcd", false},                  // too short
		{"abcdefghijklmnopqrstu", false}, // 21
		{"abcdefghijklmnopqrst", true},   // 20
		{"abc de", false}, {"abc_de", false}, {"abc-de", false}, {"abc.de", false},
		{"абвгд", false}, // Cyrillic
		{"abcdé", false}, {"", false}, {"abc😀e", false},
	}
	for _, tt := range tests {
		if got := validLatinAlnum(tt.s, 5, 20); got != tt.want {
			t.Errorf("validLatinAlnum(%q) = %v, want %v", tt.s, got, tt.want)
		}
	}
	if validLatinAlnum("abcdefghijklmnop", 5, 15) {
		t.Error("16 characters passed a 15 character limit")
	}
}

func TestInfoDetailHasCopyButtonsAndActions(t *testing.T) {
	h := newHarness(t)
	email, code := sofiaSetup(t, h)
	s := h.loginUser(email)

	client := h.firstMockClient(mock.StoreSofia)
	rec := s.get("/info/" + code)
	assertStatus(t, rec, http.StatusOK)
	assertContains(t, rec,
		`data-copy="`+client.Code+`"`, `data-copy="`+client.Name+`"`,
		"Нов профил в NEXT", `data-dialog-open="profile-dialog"`,
		"Към Активация - модули", `href="/?q=`+client.Code+`"`,
		"Заявка за профил в NEXT",
	)
	// The mandant id is loaded for the transport list but never shown.
	assertNotContains(t, rec, "MandantID", "mandant_id")

	// Without a NEXT profile the button is grayed and has no link.
	rec = s.get("/info/100000099")
	assertContains(t, rec, `aria-disabled="true"`)
	assertNotContains(t, rec, `href="/?q=100000099"`)
	// Empty fields have a disabled copy button.
	assertContains(t, rec, `data-copy="" disabled`)
}

func TestActivationPageAcceptsTheCustomerNumber(t *testing.T) {
	h := newHarness(t)
	email, code := sofiaSetup(t, h)
	s := h.loginUser(email)

	rec := s.get("/?q=" + code)
	assertStatus(t, rec, http.StatusOK)
	assertContains(t, rec, `value="`+code+`"`, `hx-get="/clients/search?q=`+code)
}

func TestProfileFormIsPrefilled(t *testing.T) {
	h := newHarness(t)
	sofia := h.seedStore(mock.StoreSofia, mock.MandantSofia)
	h.seedStore(mock.StoreVarna, mock.MandantVarna)
	const email = "prodavach@example.com"
	h.seedUser(email, sofia)
	s := h.loginUser(email)

	client := h.firstMockClient(mock.StoreSofia)
	rec := s.get("/info/" + client.Code)
	assertStatus(t, rec, http.StatusOK)
	assertContains(t, rec,
		`value="`+client.Code+`" readonly`, `value="`+client.Name+`" readonly`,
		// the customer's own store is fixed
		`id="pf-store" value="`+mock.StoreSofia+`" readonly`,
		// transports of that store
		"Куриер Спиди", "Лична доставка",
		// the sender
		`value="Тестов Потребител" readonly`, "Магазин София",
		`maxlength="150"`,
	)
	assertNotContains(t, rec, "Куриер Еконт")
}

func TestUsernameCheck(t *testing.T) {
	h := newHarness(t)
	email, _ := sofiaSetup(t, h)
	s := h.loginUser(email)

	taken := h.clientLogins(h.firstMockClient(mock.StoreSofia).Code)[0]
	rec := s.get("/info/username-check?username=" + url.QueryEscape(taken))
	assertStatus(t, rec, http.StatusOK)
	assertContains(t, rec, "Вече съществува потребител с това име", "✗")

	rec = s.get("/info/username-check?username=freeName1")
	assertContains(t, rec, "✓")
	assertNotContains(t, rec, "Вече съществува")

	// A badly formed name is not even looked up.
	for _, bad := range []string{"ab", "has space1", "special!1", "кирилица1"} {
		rec = s.get("/info/username-check?username=" + url.QueryEscape(bad))
		assertContains(t, rec, "Само латински букви и цифри")
		assertNotContains(t, rec, "✓")
	}

	assertStatus(t, s.get("/info/username-check?username="), http.StatusOK)
}

func profileForm(storeID int64) url.Values {
	return url.Values{
		"store":     {strconv.FormatInt(storeID, 10)},
		"username":  {"newuser01"},
		"password":  {"Secret123"},
		"transport": {"Лична доставка"},
		"comment":   {"Моля, бързо."},
	}
}

func TestProfileRequestIsStoredAndEmailed(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	sofia := h.seedStore(mock.StoreSofia, mock.MandantSofia)
	const email = "prodavach@example.com"
	h.seedUser(email, sofia)
	if err := h.db.SetTemplateRecipients(ctx, "profile_request_created", "admin@example.com", dates.NowUTC()); err != nil {
		t.Fatalf("set recipients: %v", err)
	}
	s := h.loginUser(email)
	client := h.firstMockClient(mock.StoreSofia)

	form := profileForm(sofia)
	// The posted number and name are ignored: they come from the directory.
	form.Set("client_code", "999999999")
	// So is the serving store: a posted one is ignored.
	form.Set("store", "99999")
	rec := s.post("/info/"+client.Code+"/profile-request", form)
	assertStatus(t, rec, http.StatusOK)
	assertContains(t, rec, "Заявката е изпратена.", "newuser01")

	var code, name, storeName, user, transport, sender, comment string
	var enc []byte
	err := h.db.QueryRowContext(ctx,
		`SELECT client_code, client_name, store_name, username, password_enc, transport, submitter_name, comment
		 FROM profile_requests`).Scan(&code, &name, &storeName, &user, &enc, &transport, &sender, &comment)
	if err != nil {
		t.Fatalf("no stored request: %v", err)
	}
	if code != client.Code || name != client.Name || storeName != "Магазин София" ||
		user != "newuser01" || transport != "Лична доставка" || sender != "Тестов Потребител" ||
		comment != "Моля, бързо." {
		t.Errorf("stored = %q %q %q %q %q %q %q", code, name, storeName, user, transport, sender, comment)
	}
	if pw, err := h.app.Enc.Decrypt(enc); err != nil || pw != "Secret123" {
		t.Errorf("password at rest = %q, %v; want it encrypted and recoverable", pw, err)
	}
	if strings.Contains(string(enc), "Secret123") {
		t.Error("the password is stored in plain text")
	}

	msgs, total, err := h.db.ListOutbox(ctx, "", 10, 0)
	if err != nil || total != 1 {
		t.Fatalf("outbox = %d, %v; want 1 email", total, err)
	}
	var body string
	if err := h.db.QueryRowContext(ctx,
		`SELECT body_html FROM email_outbox WHERE template_key = 'profile_request_created'`).Scan(&body); err != nil {
		t.Fatalf("no profile request email body: %v", err)
	}
	if msgs[0].TemplateKey != "profile_request_created" ||
		!strings.Contains(body, "newuser01") || !strings.Contains(body, "Secret123") {
		t.Errorf("email = %q\n%s", msgs[0].TemplateKey, body)
	}
}

func TestProfileRequestValidation(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	sofia := h.seedStore(mock.StoreSofia, mock.MandantSofia)
	const email = "prodavach@example.com"
	h.seedUser(email, sofia)
	s := h.loginUser(email)
	client := h.firstMockClient(mock.StoreSofia)
	taken := h.clientLogins(client.Code)[0]

	tests := []struct {
		name   string
		mutate func(url.Values)
		want   string
	}{
		{"short username", func(v url.Values) { v.Set("username", "abcd") }, "между 5 и 20"},
		{"long username", func(v url.Values) { v.Set("username", strings.Repeat("a", 21)) }, "между 5 и 20"},
		{"username with a space", func(v url.Values) { v.Set("username", "abc def") }, "между 5 и 20"},
		{"username with a symbol", func(v url.Values) { v.Set("username", "abc_def") }, "между 5 и 20"},
		{"cyrillic username", func(v url.Values) { v.Set("username", "кирилица") }, "между 5 и 20"},
		{"username already taken", func(v url.Values) { v.Set("username", taken) }, "Вече съществува потребител с това име"},
		{"short password", func(v url.Values) { v.Set("password", "abcd") }, "между 5 и 15"},
		{"long password", func(v url.Values) { v.Set("password", strings.Repeat("a", 16)) }, "между 5 и 15"},
		{"password with a symbol", func(v url.Values) { v.Set("password", "pass word") }, "между 5 и 15"},
		{"unknown transport", func(v url.Values) { v.Set("transport", "Хеликоптер") }, "Изберете транспорт"},
		{"long comment", func(v url.Values) { v.Set("comment", strings.Repeat("я", 151)) }, "150 символа"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			form := profileForm(sofia)
			tt.mutate(form)
			rec := s.post("/info/"+client.Code+"/profile-request", form)
			assertStatus(t, rec, http.StatusUnprocessableEntity)
			assertContains(t, rec, tt.want)
		})
	}

	var n int
	if err := h.db.QueryRowContext(ctx, `SELECT count(*) FROM profile_requests`).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d requests stored after only rejected submissions (%v)", n, err)
	}

	// A comment of exactly 150 characters is fine.
	form := profileForm(sofia)
	form.Set("comment", strings.Repeat("я", 150))
	assertStatus(t, s.post("/info/"+client.Code+"/profile-request", form), http.StatusOK)
}

func TestProfileRequestNeedsLoginAndCSRFAndAKnownCustomer(t *testing.T) {
	h := newHarness(t)
	sofia := h.seedStore(mock.StoreSofia, mock.MandantSofia)
	const email = "prodavach@example.com"
	h.seedUser(email, sofia)
	client := h.firstMockClient(mock.StoreSofia)

	rec := h.newSession().post("/info/"+client.Code+"/profile-request", profileForm(sofia))
	assertStatus(t, rec, http.StatusSeeOther)

	s := h.loginUser(email)
	form := profileForm(sofia)
	form.Set("csrf_token", "wrong")
	assertStatus(t, s.post("/info/"+client.Code+"/profile-request", form), http.StatusForbidden)

	assertStatus(t, s.post("/info/999999998/profile-request", profileForm(sofia)), http.StatusNotFound)
}

func TestAdminUserNameIsRequiredAndSaved(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	s, _ := adminSetup(t, h)

	stores, err := h.db.ListStores(ctx, true)
	if err != nil || len(stores) == 0 {
		t.Fatalf("no stores: %v", err)
	}
	sid := strconv.FormatInt(stores[0].ID, 10)

	// Without a name the account is refused.
	rec := s.post("/admin/users", url.Values{
		"email": {"bez-ime@example.com"}, "password": {"nova-parola-123"}, "stores": {sid},
	})
	assertStatus(t, rec, http.StatusUnprocessableEntity)
	assertContains(t, rec, "Въведете име.")

	// With one it is stored and listed.
	rec = s.post("/admin/users", url.Values{
		"email": {"s-ime@example.com"}, "name": {"  Георги Георгиев "}, "password": {"nova-parola-123"}, "stores": {sid},
	})
	assertStatus(t, rec, http.StatusSeeOther)
	u, err := h.db.UserByEmail(ctx, "s-ime@example.com")
	if err != nil || u.Name != "Георги Георгиев" {
		t.Fatalf("name = %q, err %v; want the trimmed name", u.Name, err)
	}
	assertContains(t, s.get("/admin/users"), "Георги Георгиев")

	// It can be changed from the edit page, and cannot be emptied.
	id := strconv.FormatInt(u.ID, 10)
	assertStatus(t, s.post("/admin/users/"+id, url.Values{"name": {"Нов Име"}, "stores": {sid}}), http.StatusSeeOther)
	if u, _ = h.db.UserByID(ctx, u.ID); u.Name != "Нов Име" {
		t.Errorf("name after edit = %q", u.Name)
	}
	assertStatus(t, s.post("/admin/users/"+id, url.Values{"name": {" "}, "stores": {sid}}), http.StatusSeeOther)
	if u, _ = h.db.UserByID(ctx, u.ID); u.Name != "Нов Име" {
		t.Errorf("an empty name replaced the stored one: %q", u.Name)
	}
}
