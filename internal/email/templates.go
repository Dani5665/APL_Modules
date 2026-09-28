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
)

// TemplateDef is a template's identity and its shipped default content.
type TemplateDef struct {
	Key             string
	Label           string
	Description     string
	DefaultSubject  string
	DefaultBodyHTML string
}

// Defaults are the shipped Bulgarian templates, in the order the admin page
// lists them.
//
// They are the single source of truth for both the initial seed and the
// "restore default" action, so the two can never drift apart.
var Defaults = []TemplateDef{
	{
		Key:            TemplateRequestCreated,
		Label:          "Ново запитване (към администраторите)",
		Description:    "Изпраща се до администраторите при подадено ново запитване.",
		DefaultSubject: "Ново запитване №{{request_id}} - {{client_name}}",
		DefaultBodyHTML: `<p>Подадено е ново запитване за активация на модули.</p>
<table>
  <tr><td><strong>Номер на запитване:</strong></td><td>{{request_id}}</td></tr>
  <tr><td><strong>Дата на подаване:</strong></td><td>{{request_date}}</td></tr>
  <tr><td><strong>Клиентски номер:</strong></td><td>{{client_code}}</td></tr>
  <tr><td><strong>Име на клиент:</strong></td><td>{{client_name}}</td></tr>
  <tr><td><strong>Обект на клиента:</strong></td><td>{{client_object}}</td></tr>
  <tr><td><strong>Магазин:</strong></td><td>{{client_store}}</td></tr>
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
		Label:          "Одобрено запитване (към подателя)",
		Description:    "Изпраща се до потребителя, подал запитването, при одобрение.",
		DefaultSubject: "Запитване №{{request_id}} е одобрено",
		DefaultBodyHTML: `<p>Здравейте,</p>
<p>Вашето запитване №{{request_id}} от {{request_date}} за клиент <strong>{{client_name}}</strong> ({{client_code}}) е <strong>одобрено</strong>.</p>
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
		Label:          "Отказано запитване (към подателя)",
		Description:    "Изпраща се до потребителя, подал запитването, при отказ.",
		DefaultSubject: "Запитване №{{request_id}} е отказано",
		DefaultBodyHTML: `<p>Здравейте,</p>
<p>Вашето запитване №{{request_id}} от {{request_date}} за клиент <strong>{{client_name}}</strong> ({{client_code}}) е <strong>отказано</strong>.</p>
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
		Key:            TemplateScheduledExport,
		Label:          "Месечна справка (към получателите на справката)",
		Description:    "Изпраща се по график, с прикачен Excel файл с активните модули.",
		DefaultSubject: "Справка за активни модули - {{export_date}}",
		DefaultBodyHTML: `<p>Здравейте,</p>
<p>Прилагаме справката за активните модули към {{export_date}}.</p>
<p>Общо активни модули: <strong>{{active_count}}</strong>.</p>
<p>Файлът е прикачен към това съобщение.</p>`,
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
	for _, d := range Defaults {
		if err := db.EnsureEmailTemplate(ctx, d.Key, d.DefaultSubject, d.DefaultBodyHTML, now); err != nil {
			return fmt.Errorf("seed email template %s: %w", d.Key, err)
		}
	}
	return nil
}
