package auth

import (
	"errors"
	"net/mail"
	"strings"
)

// ErrInvalidEmail is returned when a username is not a plain email address.
var ErrInvalidEmail = errors.New("невалиден имейл адрес")

// NormalizeEmail validates an address and returns it lowercased.
//
// net/mail accepts forms that are valid RFC 5322 but unwanted as usernames,
// such as `"Name" <a@b.c>` or an address with comments. Requiring the parsed
// address to equal the trimmed input rejects those, and the extra checks
// require exactly one "@" and a dotted domain.
func NormalizeEmail(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", ErrInvalidEmail
	}

	addr, err := mail.ParseAddress(trimmed)
	if err != nil || addr.Address != trimmed || addr.Name != "" {
		return "", ErrInvalidEmail
	}

	local, domain, found := strings.Cut(trimmed, "@")
	if !found || local == "" || domain == "" {
		return "", ErrInvalidEmail
	}
	if strings.Contains(domain, "@") {
		return "", ErrInvalidEmail
	}
	// A dotted domain with non-empty labels, so "user@localhost" and
	// "user@example." are both rejected.
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return "", ErrInvalidEmail
	}
	for _, l := range labels {
		if l == "" {
			return "", ErrInvalidEmail
		}
	}

	return strings.ToLower(trimmed), nil
}

// ValidEmail reports whether raw is an acceptable address.
func ValidEmail(raw string) bool {
	_, err := NormalizeEmail(raw)
	return err == nil
}

// NormalizeEmailList validates each address in a comma-, semicolon-,
// whitespace- or newline-separated list, returning the normalised addresses
// and the raw entries that failed.
func NormalizeEmailList(raw string) (valid []string, invalid []string) {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
	})
	seen := map[string]bool{}
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		norm, err := NormalizeEmail(f)
		if err != nil {
			invalid = append(invalid, f)
			continue
		}
		if seen[norm] {
			continue
		}
		seen[norm] = true
		valid = append(valid, norm)
	}
	return valid, invalid
}
