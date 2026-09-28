package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"haynesproform/internal/auth"
	"haynesproform/internal/store"
)

// maxFormBytes bounds a posted form. The largest form here is a handful of
// checkboxes and an email template body.
const maxFormBytes = 1 << 20 // 1 MiB

// contentSecurityPolicy allows only same-origin resources.
//
// Alpine ships in its CSP build, which evaluates no expressions at runtime,
// so no 'unsafe-eval' is needed. Inline styles are allowed because the TOTP
// QR code and a few progress widths are set inline; no inline script is.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; " +
	"font-src 'self'; " +
	"connect-src 'self'; " +
	"form-action 'self'; " +
	"frame-ancestors 'none'; " +
	"base-uri 'none'; " +
	"object-src 'none'"

func (a *App) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// statusRecorder captures the status code for the access log.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

func (a *App) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The health check runs every few seconds; logging it would drown the
		// log in noise.
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		a.Log.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"bytes", rec.bytes,
			"duration_ms", time.Since(start).Milliseconds(),
			"ip", a.clientIP(r),
		)
	})
}

func (a *App) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if p := recover(); p != nil {
				a.Log.Error("panic recovered",
					"method", r.Method, "path", r.URL.Path,
					"panic", fmt.Sprint(p), "stack", string(debug.Stack()))
				a.renderError(w, r, http.StatusInternalServerError,
					"Възникна грешка",
					"Възникна неочаквана грешка. Опитайте отново по-късно.")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// requireUser admits only a fully logged-in salesperson.
func (a *App) requireUser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		s, err := a.Sessions.Load(ctx, r, auth.UserAudience)
		if err != nil {
			if !errors.Is(err, auth.ErrNoSession) {
				a.serverError(w, r, err)
				return
			}
			a.redirectToLogin(w, r, "/login")
			return
		}

		u, err := a.DB.UserByID(ctx, s.SubjectID)
		if err != nil || !u.Active {
			_ = a.Sessions.Destroy(ctx, w, r, auth.UserAudience)
			a.redirectToLogin(w, r, "/login")
			return
		}

		ctx = context.WithValue(ctx, ctxSession, s)
		ctx = context.WithValue(ctx, ctxUser, u)
		r = r.WithContext(ctx)

		if !a.enforceCSRF(w, r, s.CSRFToken) {
			return
		}
		// A forced password change blocks every page but the change form and
		// logout, not only the handlers that happen to check it after login -
		// otherwise a bookmark or a stale link lets the account skip it.
		if u.MustChangePassword && r.URL.Path != "/password" && r.URL.Path != "/logout" {
			a.redirectToPage(w, r, "/password")
			return
		}
		next(w, r)
	}
}

// requireAdmin admits only a logged-in, active administrator. There is no
// second factor - see DECISIONS.md on removing 2FA.
func (a *App) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		s, err := a.Sessions.Load(ctx, r, auth.AdminAudience)
		if err != nil {
			if !errors.Is(err, auth.ErrNoSession) {
				a.serverError(w, r, err)
				return
			}
			a.redirectToLogin(w, r, "/admin/login")
			return
		}

		admin, err := a.DB.AdminByID(ctx, s.SubjectID)
		if err != nil || !admin.Active {
			_ = a.Sessions.Destroy(ctx, w, r, auth.AdminAudience)
			a.redirectToLogin(w, r, "/admin/login")
			return
		}

		// A session that still owes a second factor may only reach the TOTP
		// pages, which are routed outside this middleware. With 2FA disabled
		// (dev only; see DECISIONS.md), Start never creates such a session,
		// so this branch is simply never taken.
		if s.Stage != store.StageActive {
			a.redirectToLogin(w, r, "/admin/login/2fa")
			return
		}

		ctx = context.WithValue(ctx, ctxSession, s)
		ctx = context.WithValue(ctx, ctxAdmin, admin)
		r = r.WithContext(ctx)

		if !a.enforceCSRF(w, r, s.CSRFToken) {
			return
		}
		// A forced password change (bootstrap admin, or reset by another
		// admin) blocks every page but the change form and logout, not only
		// the handlers that happen to check it right after login - otherwise
		// a bookmark or the recovery-codes page's own link back to /admin
		// lets the account skip it indefinitely.
		if admin.MustChangePassword && r.URL.Path != "/admin/password" && r.URL.Path != "/admin/logout" {
			a.redirectToPage(w, r, "/admin/password")
			return
		}
		next(w, r)
	}
}

// requirePendingAdmin admits an administrator who has passed the password
// step but not yet the second factor.
func (a *App) requirePendingAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		s, err := a.Sessions.Load(ctx, r, auth.AdminAudience)
		if err != nil {
			a.redirectToLogin(w, r, "/admin/login")
			return
		}
		admin, err := a.DB.AdminByID(ctx, s.SubjectID)
		if err != nil || !admin.Active {
			_ = a.Sessions.Destroy(ctx, w, r, auth.AdminAudience)
			a.redirectToLogin(w, r, "/admin/login")
			return
		}
		if s.Stage == store.StageActive {
			http.Redirect(w, r, "/admin", http.StatusSeeOther)
			return
		}

		ctx = context.WithValue(ctx, ctxSession, s)
		ctx = context.WithValue(ctx, ctxAdmin, admin)
		r = r.WithContext(ctx)

		if !a.enforceCSRF(w, r, s.CSRFToken) {
			return
		}
		next(w, r)
	}
}

// enforceCSRF parses the form and verifies the synchronizer token on every
// state-changing request. It reports whether the request may continue.
func (a *App) enforceCSRF(w http.ResponseWriter, r *http.Request, token string) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		a.Log.Warn("form could not be parsed", "path", r.URL.Path, "error", err)
		a.renderError(w, r, http.StatusBadRequest, "Невалидна заявка",
			"Заявката не може да бъде прочетена.")
		return false
	}

	if !auth.CheckCSRF(r, token) {
		a.Log.Warn("CSRF token rejected", "path", r.URL.Path, "ip", a.clientIP(r))
		if isHTMX(r) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`<div class="alert alert-error" role="alert">Сесията е изтекла. Презаредете страницата.</div>`))
			return false
		}
		a.renderError(w, r, http.StatusForbidden, "Изтекла сесия",
			"Сесията е изтекла или заявката е невалидна. Влезте отново и опитайте пак.")
		return false
	}
	return true
}

// redirectToLogin sends the browser to a login page.
func (a *App) redirectToLogin(w http.ResponseWriter, r *http.Request, loginPath string) {
	a.redirectToPage(w, r, loginPath)
}

// redirectToPage sends the browser to any full page - a login page, or a
// forced password-change page - in place of the current one. An htmx request
// gets the redirect as a header instead, since the browser would otherwise
// swap that page into a fragment.
func (a *App) redirectToPage(w http.ResponseWriter, r *http.Request, path string) {
	if isHTMX(r) {
		w.Header().Set("HX-Redirect", path)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, path, http.StatusSeeOther)
}

// isHTMX reports whether the request came from htmx.
func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// parseForm parses a form on a handler that was not routed through the CSRF
// middleware (a GET with query parameters, say).
func parseForm(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	return r.ParseForm()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// escapeHTML escapes text inserted into a small inline HTML fragment.
func escapeHTML(s string) string { return html.EscapeString(s) }

// staticHandler serves the embedded assets with a long cache lifetime.
func staticHandler(fsys http.FileSystem) http.Handler {
	fileServer := http.FileServer(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Assets are versioned by the build, so a long max-age is safe; the
		// HTML that references them is never cached.
		if strings.HasSuffix(r.URL.Path, ".js") || strings.HasSuffix(r.URL.Path, ".css") ||
			strings.HasSuffix(r.URL.Path, ".svg") || strings.HasSuffix(r.URL.Path, ".ico") {
			w.Header().Set("Cache-Control", "public, max-age=86400")
		}
		fileServer.ServeHTTP(w, r)
	})
}
