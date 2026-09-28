package auth

import (
	"net/http"
	"strings"
)

// CSRFFieldName is the hidden form field carrying the synchronizer token.
const CSRFFieldName = "csrf_token"

// CSRFHeaderName is the header htmx sends, populated from a meta tag via
// hx-headers so every htmx request carries the token without extra markup.
const CSRFHeaderName = "X-CSRF-Token"

// CheckCSRF reports whether the request carries the session's CSRF token.
//
// Safe methods are exempt; every other method must present the token in the
// form field or the header.
func CheckCSRF(r *http.Request, sessionToken string) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	}
	if sessionToken == "" {
		return false
	}

	if got := strings.TrimSpace(r.Header.Get(CSRFHeaderName)); got != "" {
		return ConstantTimeEqual(got, sessionToken)
	}
	// r.FormValue reads the parsed body; the caller parses the form first.
	return ConstantTimeEqual(strings.TrimSpace(r.PostFormValue(CSRFFieldName)), sessionToken)
}
