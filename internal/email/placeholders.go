package email

import (
	"html"
	"regexp"
	"sort"
	"strings"
)

// Placeholder is one substitution available in a template.
type Placeholder struct {
	// Name is the token without braces, e.g. "client_code".
	Name string
	// Description is the Bulgarian explanation shown in the info panel.
	Description string
	// Example is a sample value shown in the info panel and used by the
	// preview.
	Example string
}

// Token renders the placeholder as it is written in a template.
func (p Placeholder) Token() string { return "{{" + p.Name + "}}" }

// Placeholder names.
const (
	PHClientCode     = "client_code"
	PHClientName     = "client_name"
	PHClientObject   = "client_object"
	PHClientStore    = "client_store"
	PHUsernames      = "usernames"
	PHModules        = "modules"
	PHTestPeriod     = "test_period"
	PHStartDate      = "start_date"
	PHEndDate        = "end_date"
	PHDuration       = "duration"
	PHSubmitterEmail = "submitter_email"
	PHRequestID      = "request_id"
	PHRequestDate    = "request_date"
	PHAdminComment   = "admin_comment"
	PHDecidedBy      = "decided_by"
	PHRequestLink    = "request_link"
	PHExportDate     = "export_date"
	PHActiveCount    = "active_count"
)

// commonPlaceholders are available in the three request templates.
var commonPlaceholders = []Placeholder{
	{PHClientCode, "Клиентски номер", "100000001"},
	{PHClientName, "Име на клиент", "Автосервиз Балкан ЕООД"},
	{PHClientObject, "Обект на клиента", "Сервиз Люлин"},
	{PHClientStore, "Магазин", "Магазин София"},
	{PHUsernames, "Избрани потребители (списък)", "office_100000001, servis_100000001"},
	{PHModules, "Модули и нива", "Fast Calculator, HaynesPro (Business)"},
	{PHTestPeriod, "Да / Не", "Не"},
	{PHStartDate, "Активация от дата", "01.10.2026"},
	{PHEndDate, "Активация до дата", "31.12.2026"},
	{PHDuration, "Активация за (напр. „3 месеца“)", "3 месеца"},
	{PHSubmitterEmail, "Потребител, подал запитването", "prodavach@example.com"},
	{PHRequestID, "Номер на запитването", "42"},
	{PHRequestDate, "Дата на подаване", "17.09.2026"},
}

var decisionPlaceholders = []Placeholder{
	{PHAdminComment, "Коментар на администратора (одобрение/отказ)", "Одобрено по договор."},
	{PHDecidedBy, "Администратор, взел решението", "admin@example.com"},
}

// placeholdersByTemplate lists what each template may use. Anything outside
// this list is left untouched in the body and reported as a warning on save.
var placeholdersByTemplate = map[string][]Placeholder{
	TemplateRequestCreated: append(append([]Placeholder{}, commonPlaceholders...),
		Placeholder{PHRequestLink, "Линк към запитването в админ панела", "https://example.com/admin/requests/42"},
	),
	TemplateRequestApproved: append(append([]Placeholder{}, commonPlaceholders...), decisionPlaceholders...),
	TemplateRequestDenied:   append(append([]Placeholder{}, commonPlaceholders...), decisionPlaceholders...),
	TemplateScheduledExport: {
		{PHExportDate, "Дата на справката", "01.10.2026"},
		{PHActiveCount, "Брой активни модули в справката", "128"},
	},
}

// PlaceholdersFor returns the placeholders a template may use.
func PlaceholdersFor(templateKey string) []Placeholder {
	return placeholdersByTemplate[templateKey]
}

// SampleValues returns the example values of a template's placeholders, for
// the preview.
func SampleValues(templateKey string) map[string]string {
	out := map[string]string{}
	for _, p := range PlaceholdersFor(templateKey) {
		out[p.Name] = p.Example
	}
	return out
}

// placeholderPattern matches a well-formed token: letters, digits and
// underscores between double braces, with optional inner spacing.
var placeholderPattern = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_]+)\s*\}\}`)

// Render substitutes the placeholders in s with HTML-escaped values.
//
// Admin-authored templates are never executed as Go templates: substitution
// is plain string replacement and every value is escaped, so neither the
// template nor the data can inject markup or execute anything.
func Render(s string, values map[string]string) string {
	return placeholderPattern.ReplaceAllStringFunc(s, func(match string) string {
		name := placeholderPattern.FindStringSubmatch(match)[1]
		v, ok := values[name]
		if !ok {
			// An unknown placeholder is left exactly as written, so a typo is
			// visible rather than silently swallowed.
			return match
		}
		return html.EscapeString(v)
	})
}

// RenderPlain substitutes placeholders without HTML escaping, for the subject
// line, which is not HTML.
func RenderPlain(s string, values map[string]string) string {
	return placeholderPattern.ReplaceAllStringFunc(s, func(match string) string {
		name := placeholderPattern.FindStringSubmatch(match)[1]
		if v, ok := values[name]; ok {
			return v
		}
		return match
	})
}

// UnknownPlaceholders returns the placeholder names used in the text that the
// given template does not support, sorted and deduplicated. The template
// editor shows these as a warning on save.
func UnknownPlaceholders(templateKey string, texts ...string) []string {
	known := map[string]bool{}
	for _, p := range PlaceholdersFor(templateKey) {
		known[p.Name] = true
	}

	seen := map[string]bool{}
	var out []string
	for _, text := range texts {
		for _, m := range placeholderPattern.FindAllStringSubmatch(text, -1) {
			name := m[1]
			if known[name] || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// htmlTagPattern is used to derive the plain-text alternative.
var (
	htmlTagPattern    = regexp.MustCompile(`(?is)<[^>]+>`)
	blockBreakPattern = regexp.MustCompile(`(?i)</(p|div|tr|h[1-6]|li|table)>|<br\s*/?>`)
	// RE2 has no backreferences, so each element is spelled out.
	dropContentTags = regexp.MustCompile(
		`(?is)<script\b[^>]*>.*?</script>|<style\b[^>]*>.*?</style>|<head\b[^>]*>.*?</head>`)
	multiBlankPattern = regexp.MustCompile(`\n{3,}`)
)

// PlainText derives a readable plain-text alternative from an HTML body, so
// every message goes out as multipart with a usable text part.
func PlainText(bodyHTML string) string {
	s := dropContentTags.ReplaceAllString(bodyHTML, "")
	s = blockBreakPattern.ReplaceAllString(s, "\n")
	s = htmlTagPattern.ReplaceAllString(s, "")
	s = html.UnescapeString(s)

	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(strings.ReplaceAll(l, " ", " "))
	}
	s = strings.Join(lines, "\n")
	s = multiBlankPattern.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
