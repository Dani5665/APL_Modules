package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"haynesproform/internal/audit"
	"haynesproform/internal/auth"
	"haynesproform/internal/config"
	"haynesproform/internal/email"
	"haynesproform/internal/entrylink"
	"haynesproform/internal/export"
	"haynesproform/internal/external"
	"haynesproform/internal/requests"
	"haynesproform/internal/scheduler"
	"haynesproform/internal/store"
)

// App carries everything the handlers need. It is built once at startup.
type App struct {
	Cfg       config.Config
	DB        *store.DB
	Directory external.Directory
	Sessions  *auth.Manager
	Throttle  *auth.Throttle
	Enc       *auth.Encrypter
	Audit     *audit.Logger
	Requests  *requests.Service
	Composer  *email.Composer
	Outbox    *email.Worker
	Export    *export.Generator
	Scheduler *scheduler.Runner
	EntryLink entrylink.EntryLinkParser
	Log       *slog.Logger

	render *renderer
	// assetVersion is appended as a query string to every embedded static
	// asset URL, so a redeploy is picked up immediately instead of sitting
	// behind the long Cache-Control this application sets on /static/ (see
	// staticHandler): otherwise a browser that already has app.js or app.css
	// cached keeps using the stale copy for the rest of that cache lifetime,
	// even across an ordinary page reload, since the URL never changes.
	assetVersion string
}

// NewHandler builds the complete routing tree with its middleware.
func NewHandler(app *App) (http.Handler, error) {
	r, err := newRenderer()
	if err != nil {
		return nil, err
	}
	app.render = r
	app.assetVersion = strconv.FormatInt(time.Now().Unix(), 36)

	mux := http.NewServeMux()
	app.routeCustomer(mux)
	app.routeAdmin(mux)
	app.routeCommon(mux)

	// Middleware runs outermost first: recover, then log, then headers.
	var h http.Handler = mux
	h = app.securityHeaders(h)
	h = app.requestLogger(h)
	h = app.recoverPanics(h)
	return h, nil
}

// clientIP returns the caller's address, honouring X-Forwarded-For only from
// a trusted proxy.
func (a *App) clientIP(r *http.Request) string {
	return auth.ClientIP(r, a.Cfg.TrustedProxyCIDRs)
}

// serverError logs an unexpected failure and shows the generic Bulgarian
// error page. The underlying error is never shown to the user.
func (a *App) serverError(w http.ResponseWriter, r *http.Request, err error) {
	a.Log.Error("request failed",
		"method", r.Method, "path", r.URL.Path, "ip", a.clientIP(r), "error", err)

	if isHTMX(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`<div class="alert alert-error" role="alert">Възникна грешка. Опитайте отново.</div>`))
		return
	}
	a.renderError(w, r, http.StatusInternalServerError,
		"Възникна грешка",
		"Възникна неочаквана грешка. Опитайте отново по-късно.")
}

// externalError renders the friendly page shown when the external database
// cannot answer. It never crashes the application.
func (a *App) externalError(w http.ResponseWriter, r *http.Request, err error) {
	a.Log.Error("external directory unavailable",
		"method", r.Method, "path", r.URL.Path, "error", err)

	message := "Външната база данни не е достъпна. Опитайте отново по-късно."
	if errors.Is(err, external.ErrNotConfigured) {
		message = "Външната база данни не е конфигурирана докрай. Свържете се с администратор."
	}

	if isHTMX(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`<div class="alert alert-error" role="alert">` + escapeHTML(message) + `</div>`))
		return
	}
	a.renderError(w, r, http.StatusServiceUnavailable, "Външната база данни не е достъпна", message)
}

// renderError shows a standalone error page.
func (a *App) renderError(w http.ResponseWriter, r *http.Request, status int, title, message string) {
	data := map[string]any{
		"Title":   title,
		"Message": message,
		"IsAdmin": isAdminPath(r),
	}
	if err := a.render.render(w, status, LayoutBare, "customer/error", data); err != nil {
		a.Log.Error("error page could not be rendered", "error", err)
		http.Error(w, message, status)
	}
}

// notFound renders the Bulgarian 404 page.
func (a *App) notFound(w http.ResponseWriter, r *http.Request) {
	a.renderError(w, r, http.StatusNotFound, "Страницата не е намерена",
		"Търсената страница не съществува.")
}

// Close releases resources owned by the application.
func (a *App) Close() error {
	if a.Directory != nil {
		return a.Directory.Close()
	}
	return nil
}

// Healthz reports the health of the local database and, separately, whether
// the external database is reachable.
//
// MSSQL being down must not mark the container unhealthy: the admin part and
// every local page keep working without it.
func (a *App) healthz(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	type health struct {
		Status   string `json:"status"`
		Database string `json:"database"`
		External string `json:"external"`
	}
	out := health{Status: "ok", Database: "ok", External: "ok"}
	status := http.StatusOK

	if err := a.DB.Healthy(ctx); err != nil {
		out.Status, out.Database = "error", err.Error()
		status = http.StatusServiceUnavailable
	}
	if err := a.Directory.Ping(ctx); err != nil {
		out.External = err.Error()
	}

	writeJSON(w, status, out)
}

func isAdminPath(r *http.Request) bool {
	return len(r.URL.Path) >= 6 && r.URL.Path[:6] == "/admin"
}

// ctxKey is the private type of the request-context keys below.
type ctxKey int

const (
	ctxSession ctxKey = iota
	ctxUser
	ctxAdmin
)

// sessionFrom returns the session attached by the authentication middleware.
func sessionFrom(ctx context.Context) *store.Session {
	s, _ := ctx.Value(ctxSession).(*store.Session)
	return s
}

// userFrom returns the logged-in salesperson, or nil.
func userFrom(ctx context.Context) *store.User {
	u, _ := ctx.Value(ctxUser).(*store.User)
	return u
}

// adminFrom returns the logged-in administrator, or nil.
func adminFrom(ctx context.Context) *store.Admin {
	a, _ := ctx.Value(ctxAdmin).(*store.Admin)
	return a
}

// csrfFrom returns the CSRF token of the current session.
func csrfFrom(ctx context.Context) string {
	if s := sessionFrom(ctx); s != nil {
		return s.CSRFToken
	}
	return ""
}
