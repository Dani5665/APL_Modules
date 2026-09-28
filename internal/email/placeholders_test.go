package email

import (
	"strings"
	"testing"
)

func TestRenderEscapesValues(t *testing.T) {
	values := map[string]string{
		"client_name": `Ауто & Ко <script>alert("x")</script>`,
		"client_code": "100000001",
	}
	got := Render("<p>{{client_name}} ({{client_code}})</p>", values)

	if strings.Contains(got, "<script>") {
		t.Errorf("Render did not escape markup in a value: %q", got)
	}
	for _, want := range []string{"&amp;", "&lt;script&gt;", "&#34;", "100000001"} {
		if !strings.Contains(got, want) {
			t.Errorf("Render output %q is missing %q", got, want)
		}
	}
	// The template's own markup must survive.
	if !strings.HasPrefix(got, "<p>") || !strings.HasSuffix(got, "</p>") {
		t.Errorf("Render damaged the template markup: %q", got)
	}
}

func TestRenderLeavesUnknownPlaceholdersUntouched(t *testing.T) {
	got := Render("{{client_code}} / {{does_not_exist}}", map[string]string{"client_code": "123"})
	if got != "123 / {{does_not_exist}}" {
		t.Errorf("Render = %q, want %q", got, "123 / {{does_not_exist}}")
	}
}

func TestRenderToleratesInnerSpacing(t *testing.T) {
	got := Render("{{ client_code }}", map[string]string{"client_code": "123"})
	if got != "123" {
		t.Errorf("Render = %q, want %q", got, "123")
	}
}

func TestRenderIgnoresMalformedTokens(t *testing.T) {
	// A single brace, or a token with punctuation, is not a placeholder.
	in := "{client_code} {{client-code}} {{}}"
	if got := Render(in, map[string]string{"client_code": "123"}); got != in {
		t.Errorf("Render = %q, want it unchanged", got)
	}
}

func TestRenderPlainDoesNotEscape(t *testing.T) {
	got := RenderPlain("Тема: {{client_name}}", map[string]string{"client_name": "Ауто & Ко"})
	if got != "Тема: Ауто & Ко" {
		t.Errorf("RenderPlain = %q, want the raw value", got)
	}
}

func TestUnknownPlaceholders(t *testing.T) {
	subject := "{{client_code}} {{typo_here}}"
	body := "<p>{{client_name}} {{another_typo}} {{typo_here}} {{request_link}}</p>"

	got := UnknownPlaceholders(TemplateRequestCreated, subject, body)
	want := []string{"another_typo", "typo_here"}
	if len(got) != len(want) {
		t.Fatalf("UnknownPlaceholders = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("UnknownPlaceholders[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// {{request_link}} belongs to request_created only.
	if got := UnknownPlaceholders(TemplateRequestApproved, "{{request_link}}"); len(got) != 1 {
		t.Errorf("UnknownPlaceholders for request_approved = %v, want [request_link]", got)
	}
	if got := UnknownPlaceholders(TemplateRequestCreated, "{{request_link}}"); len(got) != 0 {
		t.Errorf("UnknownPlaceholders for request_created = %v, want none", got)
	}
}

func TestPlainText(t *testing.T) {
	html := `<style>p{color:red}</style><p>Здравейте,</p>
<table><tr><td><strong>Клиент:</strong></td><td>Ауто &amp; Ко</td></tr></table>
<p>Ред едно<br>Ред две</p>`

	got := PlainText(html)
	for _, want := range []string{"Здравейте,", "Клиент:", "Ауто & Ко", "Ред едно", "Ред две"} {
		if !strings.Contains(got, want) {
			t.Errorf("PlainText output is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "<") || strings.Contains(got, "color:red") {
		t.Errorf("PlainText left markup or style content behind:\n%s", got)
	}
	if strings.Contains(got, "\n\n\n") {
		t.Errorf("PlainText left a run of blank lines:\n%q", got)
	}
}

func TestDefaultTemplatesUseOnlyKnownPlaceholders(t *testing.T) {
	if len(Defaults) != 4 {
		t.Fatalf("got %d default templates, want 4", len(Defaults))
	}
	for _, d := range Defaults {
		if unknown := UnknownPlaceholders(d.Key, d.DefaultSubject, d.DefaultBodyHTML); len(unknown) > 0 {
			t.Errorf("default template %s uses unknown placeholders %v", d.Key, unknown)
		}
		if d.DefaultSubject == "" || d.DefaultBodyHTML == "" {
			t.Errorf("default template %s has empty content", d.Key)
		}
	}
}

func TestRenderTemplateWithSampleValues(t *testing.T) {
	for _, d := range Defaults {
		r := RenderTemplate(d.DefaultSubject, d.DefaultBodyHTML, SampleValues(d.Key))
		if strings.Contains(r.Subject, "{{") || strings.Contains(r.BodyHTML, "{{") {
			t.Errorf("template %s still contains an unreplaced placeholder:\nsubject: %s\nbody: %s",
				d.Key, r.Subject, r.BodyHTML)
		}
		if strings.TrimSpace(r.BodyText) == "" {
			t.Errorf("template %s produced an empty plain-text body", d.Key)
		}
	}
}
