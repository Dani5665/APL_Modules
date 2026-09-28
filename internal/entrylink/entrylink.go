// Package entrylink parses the link that brings a salesperson into this
// application from the parent system.
//
// The link is signed: GET /r/{code}/{login}?exp={unix_seconds}&sig={hex}.
// See SignedParser and README.md ("Entry link format") for the exact
// contract the parent application must implement.
package entrylink

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// EntryLink is the payload the parent application passes: which client the
// request is for, and which salesperson is making it.
type EntryLink struct {
	ClientCode string
	SalerLogin string
}

// EntryLinkParser extracts an entry link from an incoming request.
type EntryLinkParser interface {
	Parse(r *http.Request) (EntryLink, error)
}

// ErrInvalid marks a malformed, unsigned, tampered or expired entry link.
// The handler turns it into a generic Bulgarian error page and never echoes
// the offending value back or distinguishes the reason, so a link cannot be
// probed for which part of it was wrong.
var ErrInvalid = errors.New("entrylink: invalid entry link")

// maxLoginLength bounds the salesperson login taken from the URL.
const maxLoginLength = 200

// clientCodeLength is the fixed width of a client code.
const clientCodeLength = 9

// Validate applies the rules every entry link must satisfy, whatever the URL
// format ends up being.
func Validate(code, login string) (EntryLink, error) {
	if len(code) != clientCodeLength || !allDigits(code) {
		return EntryLink{}, fmt.Errorf("%w: client code must be exactly %d digits", ErrInvalid, clientCodeLength)
	}
	if login == "" {
		return EntryLink{}, fmt.Errorf("%w: the salesperson login is missing", ErrInvalid)
	}
	if len(login) > maxLoginLength {
		return EntryLink{}, fmt.Errorf("%w: the salesperson login exceeds %d characters", ErrInvalid, maxLoginLength)
	}
	return EntryLink{ClientCode: code, SalerLogin: login}, nil
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

// PathParser reads the entry link from GET /r/{code}/{login} with no
// signature or expiry check. It exists for local development against the
// mock external directory (see cmd/app/main.go, which wires it in only when
// ENTRY_LINK_SIGNING_ENABLED=false) and for tests. It must never be used
// against a real deployment: anyone who can guess or observe a valid
// {code}/{login} pair gets an unexpiring link.
type PathParser struct{}

var _ EntryLinkParser = PathParser{}

// Parse implements EntryLinkParser.
func (PathParser) Parse(r *http.Request) (EntryLink, error) {
	code := strings.TrimSpace(r.PathValue("code"))
	login := strings.TrimSpace(r.PathValue("login"))
	return Validate(code, login)
}

// SignedParser reads the entry link from
// GET /r/{code}/{login}?exp={unix_seconds}&sig={hex_hmac_sha256}, the format
// documented in README.md. The signature covers the code, the login and the
// expiry together, so none of the three can be swapped independently, and a
// captured link stops working once it expires.
type SignedParser struct {
	// Secret is the shared HMAC key (ENTRY_LINK_SECRET). It must match what
	// the parent application signs with.
	Secret []byte
	// Now returns the current time; overridable in tests. Defaults to
	// time.Now when nil.
	Now func() time.Time
}

var _ EntryLinkParser = SignedParser{}

// Parse implements EntryLinkParser.
func (p SignedParser) Parse(r *http.Request) (EntryLink, error) {
	code := strings.TrimSpace(r.PathValue("code"))
	login := strings.TrimSpace(r.PathValue("login"))
	link, err := Validate(code, login)
	if err != nil {
		return EntryLink{}, err
	}

	expRaw := r.URL.Query().Get("exp")
	sigRaw := r.URL.Query().Get("sig")
	if expRaw == "" || sigRaw == "" {
		return EntryLink{}, fmt.Errorf("%w: missing exp or sig", ErrInvalid)
	}

	exp, err := strconv.ParseInt(expRaw, 10, 64)
	if err != nil {
		return EntryLink{}, fmt.Errorf("%w: exp is not a Unix timestamp", ErrInvalid)
	}

	// The signature is checked before the expiry, so an attacker learns
	// nothing about whether an otherwise-valid link merely expired.
	want := Sign(p.Secret, code, login, expRaw)
	got, err := hex.DecodeString(sigRaw)
	if err != nil || subtle.ConstantTimeCompare(want, got) != 1 {
		return EntryLink{}, fmt.Errorf("%w: signature mismatch", ErrInvalid)
	}

	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	if now().Unix() > exp {
		return EntryLink{}, fmt.Errorf("%w: link expired", ErrInvalid)
	}
	return link, nil
}

// Sign computes the raw HMAC-SHA256 over a link's code, login and exp (the
// exp already formatted exactly as it appears in the URL, e.g. via
// strconv.FormatInt). Used by SignedParser.Parse and by whatever generates
// links (see the `app sign-link` command).
//
// The three fields are newline-joined before signing so that, for example,
// code="1" login="23" and code="12" login="3" cannot be confused - "\n"
// cannot appear in a validated code or login (codes are all-digit, logins
// are trimmed of surrounding whitespace but not validated character by
// character otherwise, so this join is what actually prevents ambiguity).
func Sign(secret []byte, code, login, exp string) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(code))
	mac.Write([]byte{'\n'})
	mac.Write([]byte(login))
	mac.Write([]byte{'\n'})
	mac.Write([]byte(exp))
	return mac.Sum(nil)
}
