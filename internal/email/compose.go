package email

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"haynesproform/internal/dates"
	"haynesproform/internal/store"
)

// Composer renders templates and queues the result in the outbox.
type Composer struct {
	db      *store.DB
	baseURL string
}

// NewComposer builds a composer. baseURL is used for {{request_link}}.
func NewComposer(db *store.DB, baseURL string) *Composer {
	return &Composer{db: db, baseURL: strings.TrimRight(baseURL, "/")}
}

// RequestValues builds the placeholder values for a request.
func (c *Composer) RequestValues(r *store.Request) map[string]string {
	mods := make([]string, 0, len(r.Modules))
	for _, m := range r.Modules {
		mods = append(mods, m.Describe())
	}

	testPeriod := "Не"
	if r.TestPeriod {
		testPeriod = "Да"
	}

	return map[string]string{
		PHClientCode:     r.ClientCode,
		PHClientName:     r.ClientName,
		PHClientObject:   r.ClientObject,
		PHClientStore:    r.ClientStore,
		PHUsernames:      strings.Join(r.Usernames, ", "),
		PHModules:        strings.Join(mods, ", "),
		PHTestPeriod:     testPeriod,
		PHStartDate:      dates.FormatDisplay(r.StartDate),
		PHEndDate:        dates.FormatDisplay(r.EndDate()),
		PHDuration:       r.DurationLabel(),
		PHSubmitterEmail: r.SubmitterEmail,
		PHRequestID:      strconv.FormatInt(r.ID, 10),
		PHRequestDate:    dates.DisplayTimestamp(r.CreatedAt),
		PHAdminComment:   r.AdminComment,
		PHDecidedBy:      r.DecidedByEmail,
		PHRequestLink:    fmt.Sprintf("%s/admin/requests/%d", c.baseURL, r.ID),
	}
}

// ExportValues builds the placeholder values for the scheduled export.
func (c *Composer) ExportValues(exportDate string, activeCount int) map[string]string {
	return map[string]string{
		PHExportDate:  dates.FormatDisplay(exportDate),
		PHActiveCount: strconv.Itoa(activeCount),
	}
}

// Rendered is a template with its placeholders substituted.
type Rendered struct {
	Subject  string
	BodyHTML string
	BodyText string
}

// Render loads a template and substitutes the given values.
func (c *Composer) Render(ctx context.Context, templateKey string, values map[string]string) (*Rendered, error) {
	t, err := c.db.EmailTemplateByKey(ctx, templateKey)
	if err != nil {
		return nil, fmt.Errorf("шаблон %q не е намерен: %w", templateKey, err)
	}
	return RenderTemplate(t.Subject, t.BodyHTML, values), nil
}

// RenderTemplate substitutes values into a subject and HTML body. The body is
// HTML-escaped on substitution; the subject is not HTML, so it is not.
func RenderTemplate(subject, bodyHTML string, values map[string]string) *Rendered {
	html := Render(bodyHTML, values)
	return &Rendered{
		Subject:  RenderPlain(subject, values),
		BodyHTML: html,
		BodyText: PlainText(html),
	}
}

// Queue renders a template and appends the message to the outbox.
//
// Queueing rather than sending inline is what keeps a broken SMTP server from
// failing a salesperson's submission.
func (c *Composer) Queue(ctx context.Context, q store.Querier, templateKey string, recipients []string, values map[string]string, att *Attachment) (int64, error) {
	if len(recipients) == 0 {
		return 0, fmt.Errorf("шаблон %q: няма получатели", templateKey)
	}
	r, err := c.Render(ctx, templateKey, values)
	if err != nil {
		return 0, err
	}

	m := store.OutboxMessage{
		TemplateKey: templateKey,
		Recipients:  strings.Join(recipients, ", "),
		Subject:     r.Subject,
		BodyHTML:    r.BodyHTML,
	}
	if att != nil {
		m.AttachmentName.String, m.AttachmentName.Valid = att.Filename, true
		m.AttachmentBlob = att.Content
	}
	return c.db.QueueEmail(ctx, q, m)
}
