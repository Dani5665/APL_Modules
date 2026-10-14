package email

import (
	"context"
	"fmt"

	"haynesproform/internal/dates"
	"haynesproform/internal/store"
)

// Template keys.
const (
	TemplateRequestCreated  = "request_created"
	TemplateRequestApproved = "request_approved"
	TemplateRequestDenied   = "request_denied"
	TemplateScheduledExport = "scheduled_export"
	TemplateProfileRequest  = "profile_request_created"
)

// TemplateDef is a template's identity and its shipped default content.
type TemplateDef struct {
	Key             string
	Label           string
	Description     string
	DefaultSubject  string
	DefaultBodyHTML string
	// AssignRecipients is true for a template whose recipients an admin sets.
	// The others are sent back to the submitter of a request.
	AssignRecipients bool
	// AllowCCSubmitter is true for a template that can be set to always copy the
	// user who filed the request.
	AllowCCSubmitter bool
}

// Defaults are the shipped Bulgarian templates, in the order the admin page
// lists them.
//
// They are the single source of truth for both the initial seed and the
// "restore default" action, so the two can never drift apart.
var Defaults = []TemplateDef{
	{
		Key:              TemplateRequestCreated,
		Label:            "Ново запитване за NEXT модули",
		Description:      "Изпраща се при ново запитване за активация на модули в каталога.",
		AssignRecipients: true,
		AllowCCSubmitter: true,
		DefaultSubject:   "Ново запитване за NEXT модули - {{client_name}}",
		DefaultBodyHTML: `<p>Подадено е ново запитване за активация на модули в каталога.</p>
<table>
  <tr><td><strong>Номер на запитване:</strong></td><td>{{request_id}}</td></tr>
  <tr><td><strong>Дата на подаване:</strong></td><td>{{request_date}}</td></tr>
  <tr><td><strong>Клиентски номер:</strong></td><td>{{client_code}}</td></tr>
  <tr><td><strong>Име на клиент:</strong></td><td>{{client_name}}</td></tr>
  <tr><td><strong>Обект на клиента:</strong></td><td>{{client_object}}</td></tr>
  <tr><td><strong>Потребители:</strong></td><td>{{usernames}}</td></tr>
  <tr><td><strong>Модули:</strong></td><td>{{modules}}</td></tr>
  <tr><td><strong>Тест период:</strong></td><td>{{test_period}}</td></tr>
  <tr><td><strong>Активация от:</strong></td><td>{{start_date}}</td></tr>
  <tr><td><strong>Активация до:</strong></td><td>{{end_date}}</td></tr>
  <tr><td><strong>Активация за:</strong></td><td>{{duration}}</td></tr>
  <tr><td><strong>Подал запитването:</strong></td><td>{{submitter_email}}</td></tr>
</table>
<p><a href="{{request_link}}">Отвори запитването в админ панела</a></p>`,
	},
	{
		Key:            TemplateRequestApproved,
		Label:          "Обработено запитване за модули в NEXT (към подателя)",
		Description:    "Изпраща се до потребителя, подал запитването, при одобрение.",
		DefaultSubject: "Запитване за NEXT модули към {{client_name}} е обработено",
		DefaultBodyHTML: `<p>Здравейте,</p>
<p>Вашето запитване от {{request_date}} за клиент <strong>{{client_name}}</strong> ({{client_code}}) е <strong>обработено</strong>.</p>
<table>
  <tr><td><strong>Обект на клиента:</strong></td><td>{{client_object}}</td></tr>
  <tr><td><strong>Потребители:</strong></td><td>{{usernames}}</td></tr>
  <tr><td><strong>Модули:</strong></td><td>{{modules}}</td></tr>
  <tr><td><strong>Тест период:</strong></td><td>{{test_period}}</td></tr>
  <tr><td><strong>Активация от:</strong></td><td>{{start_date}}</td></tr>
  <tr><td><strong>Активация до:</strong></td><td>{{end_date}}</td></tr>
  <tr><td><strong>Активация за:</strong></td><td>{{duration}}</td></tr>
</table>
<p><strong>Коментар:</strong> {{admin_comment}}</p>
<p>Поздрави,<br>{{decided_by}}</p>`,
	},
	{
		Key:            TemplateRequestDenied,
		Label:          "Отказано запитване за модули в NEXT (към подателя)",
		Description:    "Изпраща се до потребителя, подал запитването, при отказ.",
		DefaultSubject: "Запитване за активация на модули - {{client_name}} е отказано",
		DefaultBodyHTML: `<p>Здравейте,</p>
<p>Вашето запитване от {{request_date}} за клиент <strong>{{client_name}}</strong> ({{client_code}}) е <strong>отказано</strong>.</p>
<table>
  <tr><td><strong>Потребители:</strong></td><td>{{usernames}}</td></tr>
  <tr><td><strong>Модули:</strong></td><td>{{modules}}</td></tr>
  <tr><td><strong>Активация от:</strong></td><td>{{start_date}}</td></tr>
  <tr><td><strong>Активация за:</strong></td><td>{{duration}}</td></tr>
</table>
<p><strong>Коментар:</strong> {{admin_comment}}</p>
<p>Поздрави,<br>{{decided_by}}</p>`,
	},
	{
		Key:              TemplateScheduledExport,
		Label:            "Месечна справка за модули в каталога",
		Description:      "Изпраща се по график, с прикачен Excel файл с активните модули.",
		AssignRecipients: true,
		DefaultSubject:   "Справка за активни модули - {{export_date}}",
		DefaultBodyHTML: `<p>Здравейте,</p>
<p>Прилагаме справката за активните модули към {{export_date}}.</p>
<p>Общо активни модули: <strong>{{active_count}}</strong>.</p>
<p>Файлът е прикачен към това съобщение.</p>`,
	},
	{
		Key:              TemplateProfileRequest,
		Label:            "Заявка за нов профил в NEXT",
		Description:      "Изпраща се при заявка за нов профил в NEXT.",
		AssignRecipients: true,
		AllowCCSubmitter: true,
		DefaultSubject:   "Заявка за профил в NEXT - {{client_name}}",
		DefaultBodyHTML: `<p>Подадена е заявка за създаване на профил в NEXT.</p>
<table>
  <tr><td><strong>Дата на подаване:</strong></td><td>{{request_date}}</td></tr>
  <tr><td><strong>Клиентски номер:</strong></td><td>{{client_code}}</td></tr>
  <tr><td><strong>Име на клиента:</strong></td><td>{{client_name}}</td></tr>
  <tr><td><strong>Обслужващ обект:</strong></td><td>{{store_name}}</td></tr>
  <tr><td><strong>Потребителско име:</strong></td><td>{{username}}</td></tr>
  <tr><td><strong>Парола:</strong></td><td>{{password}}</td></tr>
  <tr><td><strong>Транспорт:</strong></td><td>{{transport}}</td></tr>
  <tr><td><strong>Изпратил заявката:</strong></td><td>{{submitter_name}} ({{submitter_email}})</td></tr>
  <tr><td><strong>Обекти на подателя:</strong></td><td>{{submitter_stores}}</td></tr>
  <tr><td><strong>Коментар:</strong></td><td>{{comment}}</td></tr>
</table>`,
	},
}

// DefaultByKey returns the shipped definition of a template.
func DefaultByKey(key string) (TemplateDef, bool) {
	for _, d := range Defaults {
		if d.Key == key {
			return d, true
		}
	}
	return TemplateDef{}, false
}

// SeedTemplates inserts any template that does not exist yet.
//
// It runs at every startup rather than in a migration, so the shipped
// defaults and the "restore default" action share one definition in Go. An
// admin's edits are never overwritten: existing rows are left untouched.
func SeedTemplates(ctx context.Context, db *store.DB) error {
	now := dates.NowUTC()

	// A template that is new in this version starts with the addresses that
	// used to receive new-request notifications, so nothing goes quiet.
	legacy, err := db.Setting(ctx, store.KeyNotifyRecipients, "")
	if err != nil {
		return err
	}

	for _, d := range Defaults {
		recipients := ""
		if d.AssignRecipients && d.Key == TemplateProfileRequest {
			recipients = legacy
		}
		if err := db.EnsureEmailTemplate(ctx, d.Key, d.DefaultSubject, d.DefaultBodyHTML, recipients, now); err != nil {
			return fmt.Errorf("seed email template %s: %w", d.Key, err)
		}
		if err := upgradeUnchanged(ctx, db, d, now); err != nil {
			return fmt.Errorf("upgrade email template %s: %w", d.Key, err)
		}
	}
	return nil
}

// upgradeUnchanged replaces a stored template with the current default when it
// still holds a default that an earlier version shipped. Text an admin edited
// matches none of them and is never touched; the recipients are kept either
// way.
func upgradeUnchanged(ctx context.Context, db *store.DB, d TemplateDef, now string) error {
	t, err := db.EmailTemplateByKey(ctx, d.Key)
	if err != nil {
		return err
	}
	if t.Subject == d.DefaultSubject && t.BodyHTML == d.DefaultBodyHTML {
		return nil
	}
	for _, p := range previousDefaults[d.Key] {
		if t.Subject == p.Subject && t.BodyHTML == p.Body {
			return db.SaveEmailTemplate(ctx, d.Key, d.DefaultSubject, d.DefaultBodyHTML, now)
		}
	}
	return nil
}
