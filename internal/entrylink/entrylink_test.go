package entrylink

import (
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestValidateRejectsBadCodesAndLogins(t *testing.T) {
	cases := []struct {
		name, code, login string
	}{
		{"short code", "12345", "u"},
		{"non-digit code", "00005043a", "u"},
		{"empty login", "000050431", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Validate(c.code, c.login); err == nil {
				t.Errorf("Validate(%q, %q) accepted an invalid link", c.code, c.login)
			}
		})
	}
}

func TestValidateAcceptsAWellFormedLink(t *testing.T) {
	link, err := Validate("000050431", "YordanVuchkov")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if link.ClientCode != "000050431" || link.SalerLogin != "YordanVuchkov" {
		t.Errorf("link = %+v", link)
	}
}

// signedRequest builds a GET /r/{code}/{login}?exp=...&sig=... request the
// way a real mux with path patterns would populate it, signed with secret.
func signedRequest(t *testing.T, secret []byte, code, login string, exp time.Time) *http.Request {
	t.Helper()
	expRaw := strconv.FormatInt(exp.Unix(), 10)
	sig := hex.EncodeToString(Sign(secret, code, login, expRaw))

	r := httptest.NewRequest(http.MethodGet, "/r/"+code+"/"+login+"?exp="+expRaw+"&sig="+sig, nil)
	r.SetPathValue("code", code)
	r.SetPathValue("login", login)
	return r
}

func TestSignedParserAcceptsAValidLink(t *testing.T) {
	secret := []byte("0123456789abcdef")
	p := SignedParser{Secret: secret}

	r := signedRequest(t, secret, "000050431", "YordanVuchkov", time.Now().Add(5*time.Minute))
	link, err := p.Parse(r)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if link.ClientCode != "000050431" || link.SalerLogin != "YordanVuchkov" {
		t.Errorf("link = %+v", link)
	}
}

func TestSignedParserRejectsAnExpiredLink(t *testing.T) {
	secret := []byte("0123456789abcdef")
	p := SignedParser{Secret: secret}

	r := signedRequest(t, secret, "000050431", "YordanVuchkov", time.Now().Add(-1*time.Minute))
	if _, err := p.Parse(r); err == nil {
		t.Error("Parse accepted an expired link")
	}
}

func TestSignedParserRejectsATamperedLink(t *testing.T) {
	secret := []byte("0123456789abcdef")
	p := SignedParser{Secret: secret}

	// Signed for one client code, but the path is read for another: the
	// signature no longer matches what SignedParser recomputes.
	r := signedRequest(t, secret, "000050431", "YordanVuchkov", time.Now().Add(5*time.Minute))
	r.SetPathValue("code", "000099999")
	if _, err := p.Parse(r); err == nil {
		t.Error("Parse accepted a link whose client code was changed after signing")
	}
}

func TestSignedParserRejectsAWrongSecret(t *testing.T) {
	p := SignedParser{Secret: []byte("0123456789abcdef")}

	r := signedRequest(t, []byte("different-secret"), "000050431", "YordanVuchkov", time.Now().Add(5*time.Minute))
	if _, err := p.Parse(r); err == nil {
		t.Error("Parse accepted a link signed with a different secret")
	}
}

func TestSignedParserRejectsAMissingSignature(t *testing.T) {
	p := SignedParser{Secret: []byte("0123456789abcdef")}

	r := httptest.NewRequest(http.MethodGet, "/r/000050431/YordanVuchkov", nil)
	r.SetPathValue("code", "000050431")
	r.SetPathValue("login", "YordanVuchkov")
	if _, err := p.Parse(r); err == nil {
		t.Error("Parse accepted a link with no exp or sig")
	}
}

func TestSignCoversAllThreeFieldsSeparately(t *testing.T) {
	secret := []byte("0123456789abcdef")
	// Concatenating "1" + "23" must not sign the same as "12" + "3": the
	// newline join in Sign is what prevents this ambiguity.
	a := Sign(secret, "1", "23", "100")
	b := Sign(secret, "12", "3", "100")
	if hex.EncodeToString(a) == hex.EncodeToString(b) {
		t.Error("Sign produced the same signature for differently-split code/login")
	}
}
