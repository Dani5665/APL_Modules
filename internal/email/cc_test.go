package email

import (
	"bytes"
	"strings"
	"testing"
)

func TestMessageCarriesTheCCHeader(t *testing.T) {
	s := NewSender(Settings{FromAddress: "no-reply@example.com"})
	msg, err := s.buildMsg(Message{
		To:       []string{"admin@example.com"},
		Cc:       []string{"submitter@example.com"},
		Subject:  "Тема",
		BodyHTML: "<p>Тяло</p>",
	})
	if err != nil {
		t.Fatalf("buildMsg: %v", err)
	}
	var buf bytes.Buffer
	if _, err := msg.WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	raw := buf.String()
	if !strings.Contains(raw, "Cc: <submitter@example.com>") && !strings.Contains(raw, "Cc: submitter@example.com") {
		t.Errorf("no Cc header in the message:\n%s", raw)
	}
	if !strings.Contains(raw, "admin@example.com") {
		t.Error("the To address is missing")
	}

	// Without a CC there is no header at all.
	msg, _ = s.buildMsg(Message{To: []string{"admin@example.com"}, Subject: "Т", BodyHTML: "<p>Т</p>"})
	buf.Reset()
	_, _ = msg.WriteTo(&buf)
	if strings.Contains(buf.String(), "Cc:") {
		t.Error("a Cc header appears although nobody is copied")
	}
}

func TestOnlyRequestTemplatesCanCopyTheSubmitter(t *testing.T) {
	for _, d := range Defaults {
		want := d.Key == TemplateRequestCreated || d.Key == TemplateProfileRequest
		if d.AllowCCSubmitter != want {
			t.Errorf("%s: AllowCCSubmitter = %v, want %v", d.Key, d.AllowCCSubmitter, want)
		}
	}
}
