package http

import (
	"net/http"
	"net/url"
	"strconv"

	"haynesproform/internal/store"
)

// view is the data every page receives. Handlers embed it in their own
// page-specific struct.
type view struct {
	Title      string
	CSRF       string
	User       *store.User
	Admin      *store.Admin
	ActivePage string
	// PendingRequests drives the badge in the admin navigation.
	PendingRequests int
	// FailedEmails drives the outbox warning in the admin navigation.
	FailedEmails int
	Flash        string
	FlashError   string
	MockMode     bool
	// AssetVersion busts the cache on every embedded static asset URL; see
	// App.assetVersion.
	AssetVersion string
}

// newView builds the shared part of a page's data.
func (a *App) newView(r *http.Request, title, activePage string) view {
	ctx := r.Context()
	v := view{
		Title:        title,
		CSRF:         csrfFrom(ctx),
		User:         userFrom(ctx),
		Admin:        adminFrom(ctx),
		ActivePage:   activePage,
		MockMode:     a.Cfg.ExternalMode == "mock",
		AssetVersion: a.assetVersion,
	}
	v.Flash, v.FlashError = flashFrom(r)

	if v.Admin != nil {
		if n, err := a.DB.CountRequestsByStatus(ctx, store.StatusPending); err == nil {
			v.PendingRequests = n
		}
		if n, err := a.DB.CountFailedEmails(ctx); err == nil {
			v.FailedEmails = n
		}
	}
	return v
}

// flashMessages maps the keys carried in a redirect URL to their Bulgarian
// text.
//
// Only a key travels in the URL, never the message itself, so nothing a
// caller supplies can end up rendered on the page.
var flashMessages = map[string]string{
	"saved":               "Промените са запазени.",
	"created":             "Записът е създаден.",
	"deleted":             "Записът е изтрит.",
	"user_created":        "Потребителят е създаден.",
	"user_updated":        "Потребителят е обновен.",
	"user_deleted":        "Потребителят е изтрит.",
	"password_reset":      "Паролата е сменена.",
	"password_changed":    "Паролата е сменена успешно.",
	"admin_created":       "Администраторът е създаден.",
	"admin_deleted":       "Администраторът е изтрит.",
	"twofa_reset":         "Двуфакторната автентикация е нулирана.",
	"store_created":       "Магазинът е създаден.",
	"store_updated":       "Магазинът е обновен.",
	"store_deleted":       "Магазинът е изтрит.",
	"request_approved":    "Запитването е одобрено.",
	"request_denied":      "Запитването е отказано.",
	"request_edited":      "Запитването е обновено.",
	"activation_created":  "Активацията е създадена.",
	"activation_updated":  "Активацията е обновена.",
	"activation_revoked":  "Активацията е прекратена.",
	"activation_restored": "Активацията е възстановена.",
	"activation_deleted":  "Активацията е изтрита.",
	"smtp_saved":          "Настройките за SMTP са запазени.",
	"test_sent":           "Тестовият имейл е изпратен успешно.",
	"template_saved":      "Шаблонът е запазен.",
	"template_restored":   "Шаблонът е върнат към стойността по подразбиране.",
	"schedule_saved":      "Графикът е запазен.",
	"export_queued":       "Справката е генерирана и поставена в опашката за изпращане.",
	"email_retried":       "Имейлът е върнат в опашката.",
	"email_deleted":       "Имейлът е изтрит от опашката.",
}

// flashErrors maps error keys to their Bulgarian text.
var flashErrors = map[string]string{
	"store_in_use":    "Магазинът е зададен на потребители и не може да бъде изтрит. Деактивирайте го вместо това.",
	"last_admin":      "Последният активен администратор не може да бъде изтрит.",
	"self_delete":     "Не можете да изтриете собствения си профил.",
	"not_found":       "Записът не е намерен.",
	"already_decided": "Запитването вече е обработено.",
}

// flashFrom reads the flash keys from the query string.
func flashFrom(r *http.Request) (msg, errMsg string) {
	q := r.URL.Query()
	return flashMessages[q.Get("msg")], flashErrors[q.Get("err")]
}

// redirectWithFlash sends the browser to path with a success flash key.
func redirectWithFlash(w http.ResponseWriter, r *http.Request, path, msgKey string) {
	redirectWithQuery(w, r, path, url.Values{"msg": {msgKey}})
}

// redirectWithError sends the browser to path with an error flash key.
func redirectWithError(w http.ResponseWriter, r *http.Request, path, errKey string) {
	redirectWithQuery(w, r, path, url.Values{"err": {errKey}})
}

func redirectWithQuery(w http.ResponseWriter, r *http.Request, path string, q url.Values) {
	target := path
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	// htmx would otherwise swap the redirect target into a fragment.
	if isHTMX(r) {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// pagination describes one page of a listing.
type pagination struct {
	Page     int
	PageSize int
	Total    int
	// Query carries the current filters so page links keep them.
	Query map[string]string
}

// Pages returns the total number of pages, at least one.
func (p pagination) Pages() int {
	if p.PageSize <= 0 {
		return 1
	}
	n := (p.Total + p.PageSize - 1) / p.PageSize
	if n < 1 {
		return 1
	}
	return n
}

// HasPrev reports whether an earlier page exists.
func (p pagination) HasPrev() bool { return p.Page > 1 }

// HasNext reports whether a later page exists.
func (p pagination) HasNext() bool { return p.Page < p.Pages() }

// Prev and Next return the neighbouring page numbers.
func (p pagination) Prev() int { return max(1, p.Page-1) }
func (p pagination) Next() int { return min(p.Pages(), p.Page+1) }

// From and To are the 1-based bounds of the current page, for "showing X-Y of
// Z".
func (p pagination) From() int {
	if p.Total == 0 {
		return 0
	}
	return (p.Page-1)*p.PageSize + 1
}

func (p pagination) To() int {
	return min(p.Page*p.PageSize, p.Total)
}

// Offset is the SQL offset of the current page.
func (p pagination) Offset() int { return (p.Page - 1) * p.PageSize }

// pageParam reads a 1-based page number from the query string.
func pageParam(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || n < 1 {
		return 1
	}
	return n
}

// idParam reads a numeric path value.
func idParam(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}
