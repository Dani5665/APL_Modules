package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"haynesproform/internal/audit"
	"haynesproform/internal/dates"
	"haynesproform/internal/email"
	"haynesproform/internal/modules"
	"haynesproform/internal/requests"
	"haynesproform/internal/store"
)

// adminPageSize is the row count of the admin listings.
const adminPageSize = 50

// handleAdminHome is the admin landing page: the pending requests.
func (a *App) handleAdminHome(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/admin/requests?status=pending", http.StatusSeeOther)
}

type adminRequestsView struct {
	view
	Requests []store.Request
	Page     pagination
	Filter   store.RequestFilter
	Statuses []store.RequestStatus
}

func (a *App) handleAdminRequests(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	f := store.RequestFilter{
		Status:   store.RequestStatus(q.Get("status")),
		Search:   strings.TrimSpace(q.Get("q")),
		FromDate: strings.TrimSpace(q.Get("from")),
		ToDate:   strings.TrimSpace(q.Get("to")),
		Limit:    adminPageSize,
	}
	switch f.Status {
	case store.StatusPending, store.StatusApproved, store.StatusDenied, "":
	default:
		f.Status = ""
	}

	page := pagination{
		Page:     pageParam(r),
		PageSize: adminPageSize,
		Query: map[string]string{
			"status": string(f.Status), "q": f.Search, "from": f.FromDate, "to": f.ToDate,
		},
	}
	f.Offset = page.Offset()

	rows, total, err := a.DB.ListRequests(ctx, f)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	page.Total = total

	v := adminRequestsView{
		view:     a.newView(r, "Запитвания", "requests"),
		Requests: rows,
		Page:     page,
		Filter:   f,
		Statuses: []store.RequestStatus{store.StatusPending, store.StatusApproved, store.StatusDenied},
	}

	// The search box swaps only the table.
	if isHTMX(r) && r.URL.Query().Get("partial") == "1" {
		if err := a.render.renderPartial(w, http.StatusOK, "admin/requests", "request_table", v); err != nil {
			a.serverError(w, r, err)
		}
		return
	}
	if err := a.render.render(w, http.StatusOK, LayoutAdmin, "admin/requests", v); err != nil {
		a.serverError(w, r, err)
	}
}

type adminRequestDetailView struct {
	view
	Request         *store.Request
	Activations     []store.Activation
	Overlaps        []store.Overlap
	ClientUsernames []string
	Modules         []modules.Def
	Durations       []store.DurationOption
	Today           string
	TestPeriodUsed  bool
	// ExternalDown marks that the client's current username list could not be
	// fetched, so the edit form falls back to the snapshotted usernames.
	ExternalDown bool
	Errors       []string
}

func (a *App) handleAdminRequestDetail(w http.ResponseWriter, r *http.Request) {
	v, ok := a.loadRequestDetail(w, r)
	if !ok {
		return
	}
	if err := a.render.render(w, http.StatusOK, LayoutAdmin, "admin/request_detail", v); err != nil {
		a.serverError(w, r, err)
	}
}

func (a *App) loadRequestDetail(w http.ResponseWriter, r *http.Request) (adminRequestDetailView, bool) {
	ctx := r.Context()

	var v adminRequestDetailView
	id, ok := idParam(r, "id")
	if !ok {
		a.notFound(w, r)
		return v, false
	}

	req, err := a.DB.RequestByID(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		a.notFound(w, r)
		return v, false
	}
	if err != nil {
		a.serverError(w, r, err)
		return v, false
	}

	v = adminRequestDetailView{
		view:      a.newView(r, "Запитване №"+strconv.FormatInt(req.ID, 10), "requests"),
		Request:   req,
		Modules:   modules.All,
		Durations: store.DurationOptions(),
		Today:     dates.Today(),
	}

	if v.Activations, err = a.DB.ListActivationsForClient(ctx, req.ClientCode); err != nil {
		a.serverError(w, r, err)
		return v, false
	}
	if v.TestPeriodUsed, err = a.DB.TestPeriodUsed(ctx, req.ClientCode, req.ID); err != nil {
		a.serverError(w, r, err)
		return v, false
	}

	// The admin edits against the client's current usernames; if the external
	// directory is down, fall back to what was submitted.
	usernames, err := a.Directory.ListClientLogins(ctx, req.ClientCode)
	if err != nil {
		a.Log.Warn("client usernames could not be loaded for the admin view",
			"client", req.ClientName, "error", err)
		v.ExternalDown = true
		usernames = req.Usernames
	}
	v.ClientUsernames = mergeUsernames(usernames, req.Usernames)

	if req.Status == store.StatusPending {
		if v.Overlaps, err = a.Requests.Overlaps(ctx, req); err != nil {
			a.Log.Warn("overlapping activations could not be computed", "request_id", req.ID, "error", err)
		}
	}
	return v, true
}

// mergeUsernames keeps every username the request already carries, even one
// that has since disappeared from the external directory, so an edit cannot
// silently drop it.
func mergeUsernames(current, selected []string) []string {
	seen := make(map[string]bool, len(current))
	out := make([]string, 0, len(current)+len(selected))
	for _, u := range current {
		if key := strings.ToLower(u); !seen[key] {
			seen[key] = true
			out = append(out, u)
		}
	}
	for _, u := range selected {
		if key := strings.ToLower(u); !seen[key] {
			seen[key] = true
			out = append(out, u)
		}
	}
	return out
}

func (a *App) handleAdminRequestEdit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	admin := adminFrom(ctx)

	id, ok := idParam(r, "id")
	if !ok {
		a.notFound(w, r)
		return
	}
	req, err := a.DB.RequestByID(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		a.notFound(w, r)
		return
	}
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	if req.Status != store.StatusPending {
		redirectWithError(w, r, "/admin/requests/"+strconv.FormatInt(id, 10), "already_decided")
		return
	}

	// The same rules as the customer form, applied to the admin's edit.
	sel := requests.ApplyTestPeriod(parseSelection(r))

	usernames, err := a.Directory.ListClientLogins(ctx, req.ClientCode)
	if err != nil {
		usernames = mergeUsernames(req.Usernames, sel.Usernames)
	}
	testUsed, err := a.DB.TestPeriodUsed(ctx, req.ClientCode, req.ID)
	if err != nil {
		a.serverError(w, r, err)
		return
	}

	selected, verr := requests.ValidateInput(sel, usernames, testUsed, dates.Today())
	if verr != nil {
		v, ok := a.loadRequestDetail(w, r)
		if !ok {
			return
		}
		var ve *requests.ValidationError
		if errors.As(verr, &ve) {
			v.Errors = ve.Messages
		} else {
			v.Errors = []string{verr.Error()}
		}
		if err := a.render.render(w, http.StatusUnprocessableEntity, LayoutAdmin, "admin/request_detail", v); err != nil {
			a.serverError(w, r, err)
		}
		return
	}

	if err := a.DB.UpdateRequestDetails(ctx, req.ID, sel.TestPeriod, sel.StartDate, sel.Months,
		selected, sel.Modules()); err != nil {
		a.serverError(w, r, err)
		return
	}

	a.Audit.Record(ctx, audit.AdminActor(admin.ID, admin.Email), audit.RequestEdited,
		strconv.FormatInt(req.ID, 10), a.clientIP(r), map[string]any{
			"usernames":   selected,
			"test_period": sel.TestPeriod,
			"start_date":  sel.StartDate,
			"months":      sel.Months,
		})

	redirectWithFlash(w, r, "/admin/requests/"+strconv.FormatInt(req.ID, 10), "request_edited")
}

func (a *App) handleAdminRequestApprove(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	admin := adminFrom(ctx)

	id, ok := idParam(r, "id")
	if !ok {
		a.notFound(w, r)
		return
	}
	comment := strings.TrimSpace(r.PostFormValue("admin_comment"))

	result, err := a.Requests.Approve(ctx, id, admin.ID, comment)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			redirectWithError(w, r, "/admin/requests/"+strconv.FormatInt(id, 10), "already_decided")
			return
		}
		a.renderDecisionError(w, r, id, err)
		return
	}

	a.Audit.Record(ctx, audit.AdminActor(admin.ID, admin.Email), audit.RequestApproved,
		strconv.FormatInt(id, 10), a.clientIP(r), map[string]any{
			"client_code":   result.Request.ClientCode,
			"activations":   len(result.ActivationIDs),
			"start_date":    result.StartDate,
			"end_date":      result.EndDate,
			"comment_given": comment != "",
		})

	a.queueDecision(ctx, result.Request, email.TemplateRequestApproved)
	redirectWithFlash(w, r, "/admin/requests/"+strconv.FormatInt(id, 10), "request_approved")
}

func (a *App) handleAdminRequestDeny(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	admin := adminFrom(ctx)

	id, ok := idParam(r, "id")
	if !ok {
		a.notFound(w, r)
		return
	}
	comment := strings.TrimSpace(r.PostFormValue("admin_comment"))

	req, err := a.Requests.Deny(ctx, id, admin.ID, comment)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			redirectWithError(w, r, "/admin/requests/"+strconv.FormatInt(id, 10), "already_decided")
			return
		}
		a.renderDecisionError(w, r, id, err)
		return
	}

	a.Audit.Record(ctx, audit.AdminActor(admin.ID, admin.Email), audit.RequestDenied,
		strconv.FormatInt(id, 10), a.clientIP(r), map[string]any{
			"client_code":   req.ClientCode,
			"comment_given": comment != "",
		})

	a.queueDecision(ctx, req, email.TemplateRequestDenied)
	redirectWithFlash(w, r, "/admin/requests/"+strconv.FormatInt(id, 10), "request_denied")
}

// renderDecisionError shows a business-rule failure on the detail page rather
// than as a server error.
func (a *App) renderDecisionError(w http.ResponseWriter, r *http.Request, id int64, cause error) {
	a.Log.Warn("request decision refused", "request_id", id, "error", cause)

	v, ok := a.loadRequestDetail(w, r)
	if !ok {
		return
	}
	v.Errors = []string{cause.Error()}
	if err := a.render.render(w, http.StatusConflict, LayoutAdmin, "admin/request_detail", v); err != nil {
		a.serverError(w, r, err)
	}
}
