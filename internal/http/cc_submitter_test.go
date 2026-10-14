package http

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"haynesproform/internal/external/mock"
)

func ccOf(t *testing.T, h *harness, key string) (to, cc string, n int) {
	t.Helper()
	msgs, _, err := h.db.ListOutbox(context.Background(), "", 20, 0)
	if err != nil {
		t.Fatalf("list outbox: %v", err)
	}
	for _, m := range msgs {
		if m.TemplateKey == key {
			to, cc = m.Recipients, m.Cc
			n++
		}
	}
	return to, cc, n
}

func TestCCCheckboxIsOnlyOnTheTwoRequestTemplates(t *testing.T) {
	h := newHarness(t)
	s, _ := adminSetup(t, h)

	for _, key := range []string{"request_created", "profile_request_created"} {
		assertContains(t, s.get("/admin/settings/templates/"+key), `name="cc_submitter"`, "Изпращай CC до подателя")
	}
	for _, key := range []string{"scheduled_export", "request_approved", "request_denied"} {
		assertNotContains(t, s.get("/admin/settings/templates/"+key), `name="cc_submitter"`)
	}
}

func TestCCSubmitterIsSavedAndShown(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	s, _ := adminSetup(t, h)

	form := url.Values{"subject": {"Тема"}, "body_html": {"<p>Тяло</p>"}, "recipients": {"a@example.com"}, "cc_submitter": {"1"}}
	assertStatus(t, s.post("/admin/settings/templates/request_created", form), http.StatusSeeOther)
	if tpl, _ := h.db.EmailTemplateByKey(ctx, "request_created"); !tpl.CCSubmitter {
		t.Fatal("the option was not saved")
	}
	rec := s.get("/admin/settings/templates/request_created")
	assertContains(t, rec, `id="cc_submitter" name="cc_submitter" value="1" checked`)
	assertContains(t, s.get("/admin/settings/templates"), "CC до подателя")

	// Saving without the box turns it off, and the other template is untouched.
	form.Del("cc_submitter")
	assertStatus(t, s.post("/admin/settings/templates/request_created", form), http.StatusSeeOther)
	if tpl, _ := h.db.EmailTemplateByKey(ctx, "request_created"); tpl.CCSubmitter {
		t.Error("the option stayed on after saving without it")
	}
	if tpl, _ := h.db.EmailTemplateByKey(ctx, "profile_request_created"); tpl.CCSubmitter {
		t.Error("another template got the option")
	}

	// A template that cannot copy the submitter ignores the field.
	assertStatus(t, s.post("/admin/settings/templates/scheduled_export",
		url.Values{"subject": {"Т"}, "body_html": {"<p>Т</p>"}, "recipients": {"a@example.com"}, "cc_submitter": {"1"}}),
		http.StatusSeeOther)
	if tpl, _ := h.db.EmailTemplateByKey(ctx, "scheduled_export"); tpl.CCSubmitter {
		t.Error("the export template accepted the option")
	}
}

func TestModuleRequestCopiesTheSubmitterWhenAsked(t *testing.T) {
	h := newHarness(t)
	email, code := sofiaSetup(t, h)
	h.setTemplateRecipients("request_created", "admins@example.com")
	s := h.loginUser(email)
	client := h.firstMockClient(mock.StoreSofia)

	// Off: no copy.
	assertStatus(t, s.post("/clients/"+code+"/request", submitForm(h, client.Code)), http.StatusOK)
	if to, cc, n := ccOf(t, h, "request_created"); n != 1 || to != "admins@example.com" || cc != "" {
		t.Fatalf("with the option off: to=%q cc=%q n=%d; want no copy", to, cc, n)
	}

	// On: the submitter is copied, the recipients are unchanged.
	if err := h.db.SetTemplateCCSubmitter(context.Background(), "request_created", true, "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	form := submitForm(h, client.Code)
	form.Set("months", "6")
	assertStatus(t, s.post("/clients/"+code+"/request", form), http.StatusOK)
	msgs, _, _ := h.db.ListOutbox(context.Background(), "", 20, 0)
	if len(msgs) != 2 || msgs[0].Recipients != "admins@example.com" || msgs[0].Cc != email {
		t.Fatalf("with the option on: %+v; want To admins and Cc %s", msgs, email)
	}
}

func TestProfileRequestCopiesTheSubmitterWhenAsked(t *testing.T) {
	h := newHarness(t)
	sofia := h.seedStore(mock.StoreSofia, mock.MandantSofia)
	const email = "prodavach@example.com"
	h.seedUser(email, sofia)
	h.setTemplateRecipients("profile_request_created", "catalog@example.com")
	if err := h.db.SetTemplateCCSubmitter(context.Background(), "profile_request_created", true, "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	s := h.loginUser(email)
	client := h.firstMockClient(mock.StoreSofia)

	assertStatus(t, s.post("/info/"+client.Code+"/profile-request", profileForm(sofia)), http.StatusOK)
	if to, cc, n := ccOf(t, h, "profile_request_created"); n != 1 || to != "catalog@example.com" || cc != email {
		t.Errorf("to=%q cc=%q n=%d; want To catalog and Cc %s", to, cc, n, email)
	}
}

// The submitter is not copied on a message that already goes to them.
func TestSubmitterAlreadyARecipientIsNotCopiedAgain(t *testing.T) {
	h := newHarness(t)
	email, code := sofiaSetup(t, h)
	h.setTemplateRecipients("request_created", "Admin@Example.com, "+email)
	if err := h.db.SetTemplateCCSubmitter(context.Background(), "request_created", true, "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	s := h.loginUser(email)
	client := h.firstMockClient(mock.StoreSofia)

	assertStatus(t, s.post("/clients/"+code+"/request", submitForm(h, client.Code)), http.StatusOK)
	if _, cc, n := ccOf(t, h, "request_created"); n != 1 || cc != "" {
		t.Errorf("cc = %q (n=%d); want none because the submitter is already a recipient", cc, n)
	}
}
