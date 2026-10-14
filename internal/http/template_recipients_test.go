package http

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"haynesproform/internal/external/mock"
)

func TestOnlyNonReplyTemplatesHaveAssignableRecipients(t *testing.T) {
	h := newHarness(t)
	s, _ := adminSetup(t, h)

	for _, key := range []string{"request_created", "scheduled_export", "profile_request_created"} {
		rec := s.get("/admin/settings/templates/" + key)
		assertStatus(t, rec, http.StatusOK)
		assertContains(t, rec, `name="recipients"`)
	}
	// Approval and denial go back to the submitter.
	for _, key := range []string{"request_approved", "request_denied"} {
		rec := s.get("/admin/settings/templates/" + key)
		assertStatus(t, rec, http.StatusOK)
		assertNotContains(t, rec, `name="recipients"`)
		assertContains(t, rec, "изпраща до подателя на запитването")
	}
}

func TestTemplateRecipientsAreSavedAndValidated(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	s, _ := adminSetup(t, h)

	post := func(key, recipients string) *url.Values {
		return &url.Values{"subject": {"Тема"}, "body_html": {"<p>Тяло</p>"}, "recipients": {recipients}}
	}

	assertStatus(t, s.post("/admin/settings/templates/request_created",
		*post("request_created", "a@example.com\nb@example.com, a@example.com")), http.StatusSeeOther)
	got, err := h.db.TemplateRecipients(ctx, "request_created")
	if err != nil || len(got) != 2 || got[0] != "a@example.com" || got[1] != "b@example.com" {
		t.Fatalf("recipients = %v, %v; want the two distinct addresses", got, err)
	}
	assertContains(t, s.get("/admin/settings/templates/request_created"), "a@example.com", "b@example.com")
	assertContains(t, s.get("/admin/settings/templates"), "a@example.com")

	// An invalid address is refused and the stored list is untouched.
	rec := s.post("/admin/settings/templates/request_created", *post("request_created", "ok@example.com, broken@, nope"))
	assertStatus(t, rec, http.StatusUnprocessableEntity)
	assertContains(t, rec, "Невалидни адреси на получатели", "ok@example.com")
	if got, _ := h.db.TemplateRecipients(ctx, "request_created"); len(got) != 2 {
		t.Errorf("a rejected save changed the recipients: %v", got)
	}

	// Each template keeps its own list.
	if got, _ := h.db.TemplateRecipients(ctx, "profile_request_created"); len(got) != 0 {
		t.Errorf("another template received the addresses: %v", got)
	}

	// Restoring the default text keeps the recipients.
	assertStatus(t, s.post("/admin/settings/templates/request_created/restore", url.Values{}), http.StatusSeeOther)
	if got, _ := h.db.TemplateRecipients(ctx, "request_created"); len(got) != 2 {
		t.Errorf("restoring the default text cleared the recipients: %v", got)
	}

	// A template that goes back to the sender ignores posted recipients.
	assertStatus(t, s.post("/admin/settings/templates/request_approved",
		*post("request_approved", "x@example.com")), http.StatusSeeOther)
	if got, _ := h.db.TemplateRecipients(ctx, "request_approved"); len(got) != 0 {
		t.Errorf("an approval template got recipients: %v", got)
	}
}

// A new request is mailed to the request template's recipients, and a profile
// request to its own template's - one list does not stand in for the other.
func TestEachRequestKindUsesItsOwnTemplateRecipients(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	sofia := h.seedStore(mock.StoreSofia, mock.MandantSofia)
	const email = "prodavach@example.com"
	h.seedUser(email, sofia)
	h.setTemplateRecipients("request_created", "module-admins@example.com")
	s := h.loginUser(email)
	client := h.firstMockClient(mock.StoreSofia)

	// No recipients on the profile template: the request is stored, no email.
	assertStatus(t, s.post("/info/"+client.Code+"/profile-request", profileForm(sofia)), http.StatusOK)
	if _, total, _ := h.db.ListOutbox(ctx, "", 10, 0); total != 0 {
		t.Fatalf("%d emails queued, want none: the profile template has no recipients", total)
	}

	h.setTemplateRecipients("profile_request_created", "catalog-admins@example.com")
	form := profileForm(sofia)
	form.Set("username", "seconduser1")
	assertStatus(t, s.post("/info/"+client.Code+"/profile-request", form), http.StatusOK)
	assertStatus(t, s.post("/clients/"+client.Code+"/request", submitForm(h, client.Code)), http.StatusOK)

	msgs, total, err := h.db.ListOutbox(ctx, "", 10, 0)
	if err != nil || total != 2 {
		t.Fatalf("outbox = %d, %v; want 2", total, err)
	}
	to := map[string]string{}
	for _, m := range msgs {
		to[m.TemplateKey] = m.Recipients
	}
	if to["profile_request_created"] != "catalog-admins@example.com" ||
		to["request_created"] != "module-admins@example.com" {
		t.Errorf("recipients by template = %v", to)
	}
}

func TestAdminMenuIsGrouped(t *testing.T) {
	h := newHarness(t)
	s, _ := adminSetup(t, h)

	rec := s.get("/admin/requests")
	assertStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	order := []string{
		"Модули - NEXT", "Запитвания", "Активации",
		"Акаунти", "Потребители", "Администратори",
		"Настройки", "Магазини", "SMTP", "Шаблони", "График", "Имейли", "Одит",
	}
	pos := 0
	for _, want := range order {
		i := indexFrom(body, want, pos)
		if i < 0 {
			t.Fatalf("%q is missing from the menu, or out of order", want)
		}
		pos = i
	}
}

func indexFrom(s, sub string, from int) int {
	for i := from; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestSMTPAndSchedulePagesNoLongerHoldRecipients(t *testing.T) {
	h := newHarness(t)
	s, _ := adminSetup(t, h)
	h.setTemplateRecipients("scheduled_export", "report@example.com")

	assertNotContains(t, s.get("/admin/settings/smtp"), `name="notify_recipients"`)

	rec := s.get("/admin/settings/schedule")
	assertNotContains(t, rec, `name="recipients"`)
	assertContains(t, rec, "report@example.com", "/admin/settings/templates/scheduled_export")
}
