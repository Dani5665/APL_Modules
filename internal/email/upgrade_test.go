package email

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"haynesproform/internal/store"
)

func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestNewRequestTemplateHasNoStoreLine(t *testing.T) {
	d, ok := DefaultByKey(TemplateRequestCreated)
	if !ok {
		t.Fatal("no default for request_created")
	}
	if strings.Contains(d.DefaultBodyHTML, "Магазин:") || strings.Contains(d.DefaultBodyHTML, "{{client_store}}") {
		t.Error("the default still has the duplicate store line")
	}
	if !strings.Contains(d.DefaultBodyHTML, "{{client_object}}") {
		t.Error("the object line is gone")
	}
}

func TestSeedUpgradesTemplatesThatStillHoldAnOldDefault(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := SeedTemplates(ctx, db); err != nil {
		t.Fatalf("SeedTemplates: %v", err)
	}

	// Stored as an earlier version shipped it, with recipients assigned.
	for _, key := range []string{TemplateRequestCreated, TemplateRequestApproved, TemplateRequestDenied} {
		old := previousDefaults[key][0]
		if err := db.SaveEmailTemplate(ctx, key, old.Subject, old.Body, "2026-01-01T00:00:00Z"); err != nil {
			t.Fatalf("store old %s: %v", key, err)
		}
	}
	if err := db.SetTemplateRecipients(ctx, TemplateRequestCreated, "keep@example.com", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("set recipients: %v", err)
	}

	// An admin's own text in another template.
	if err := db.SaveEmailTemplate(ctx, TemplateScheduledExport, "Мое заглавие", "<p>Мое тяло</p>", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("store custom: %v", err)
	}

	if err := SeedTemplates(ctx, db); err != nil {
		t.Fatalf("second SeedTemplates: %v", err)
	}

	for _, key := range []string{TemplateRequestCreated, TemplateRequestApproved, TemplateRequestDenied} {
		d, _ := DefaultByKey(key)
		got, err := db.EmailTemplateByKey(ctx, key)
		if err != nil || got.Subject != d.DefaultSubject || got.BodyHTML != d.DefaultBodyHTML {
			t.Errorf("%s was not upgraded to the current default: %+v, %v", key, got, err)
		}
	}
	if got, _ := db.EmailTemplateByKey(ctx, TemplateRequestCreated); got.Recipients != "keep@example.com" {
		t.Errorf("recipients after the upgrade = %q, want them kept", got.Recipients)
	}
	if got, _ := db.EmailTemplateByKey(ctx, TemplateScheduledExport); got.Subject != "Мое заглавие" || got.BodyHTML != "<p>Мое тяло</p>" {
		t.Errorf("an admin's edited template was overwritten: %+v", got)
	}
}

func TestSeedLeavesEveryEditedVariantAlone(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := SeedTemplates(ctx, db); err != nil {
		t.Fatalf("SeedTemplates: %v", err)
	}

	// An old default with one word changed is an admin's edit.
	old := previousDefaults[TemplateRequestCreated][0]
	edited := strings.Replace(old.Body, "Подадено е", "Постъпи", 1)
	if err := db.SaveEmailTemplate(ctx, TemplateRequestCreated, old.Subject, edited, "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("store: %v", err)
	}
	if err := SeedTemplates(ctx, db); err != nil {
		t.Fatalf("SeedTemplates: %v", err)
	}
	if got, _ := db.EmailTemplateByKey(ctx, TemplateRequestCreated); got.BodyHTML != edited {
		t.Error("an edited template was upgraded over the admin's changes")
	}
}

func TestDeniedSubjectNamesTheClient(t *testing.T) {
	d, _ := DefaultByKey(TemplateRequestDenied)
	got := RenderPlain(d.DefaultSubject, map[string]string{PHClientName: "Автосервиз Балкан ЕООД", PHRequestID: "7"})
	if want := "Запитване за активация на модули - Автосервиз Балкан ЕООД е отказано"; got != want {
		t.Errorf("subject = %q, want %q", got, want)
	}
}

// The denied template no longer names the request number in its body, and a
// database holding the previous version is upgraded.
func TestDeniedBodyHasNoRequestNumberAndPreviousVersionsUpgrade(t *testing.T) {
	d, _ := DefaultByKey(TemplateRequestDenied)
	if strings.Contains(d.DefaultBodyHTML, "{{request_id}}") || strings.Contains(d.DefaultBodyHTML, "№") {
		t.Error("the denied body still names the request number")
	}
	if !strings.Contains(d.DefaultBodyHTML, "Вашето запитване от {{request_date}} за клиент") {
		t.Error("the denied body is not the requested text")
	}

	ctx := context.Background()
	db := openTestDB(t)
	if err := SeedTemplates(ctx, db); err != nil {
		t.Fatalf("SeedTemplates: %v", err)
	}
	for i, old := range previousDefaults[TemplateRequestDenied] {
		if err := db.SaveEmailTemplate(ctx, TemplateRequestDenied, old.Subject, old.Body, "2026-01-01T00:00:00Z"); err != nil {
			t.Fatalf("store old version %d: %v", i, err)
		}
		if err := SeedTemplates(ctx, db); err != nil {
			t.Fatalf("SeedTemplates: %v", err)
		}
		got, _ := db.EmailTemplateByKey(ctx, TemplateRequestDenied)
		if got.Subject != d.DefaultSubject || got.BodyHTML != d.DefaultBodyHTML {
			t.Errorf("previous version %d was not upgraded", i)
		}
	}
}
