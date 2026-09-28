package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"haynesproform/internal/audit"
	"haynesproform/internal/dates"
	"haynesproform/internal/export"
	"haynesproform/internal/modules"
	"haynesproform/internal/store"
)

type adminActivationsView struct {
	view
	Activations   []store.Activation
	Page          pagination
	Filter        store.ActivationFilter
	Stores        []string
	Modules       []modules.Def
	Today         string
	StatusOptions []statusOption
}

type statusOption struct {
	Value string
	Label string
}

var activationStatusOptions = []statusOption{
	{"", "Всички"},
	{"active", "Активни"},
	{"pending", "Предстоящи"},
	{"expired", "Изтекли"},
	{"revoked", "Прекратени"},
}

func (a *App) handleAdminActivations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	f := store.ActivationFilter{
		Status: q.Get("status"),
		Search: strings.TrimSpace(q.Get("q")),
		Store:  q.Get("store"),
		Module: q.Get("module"),
		Limit:  adminPageSize,
	}
	page := pagination{
		Page:     pageParam(r),
		PageSize: adminPageSize,
		Query: map[string]string{
			"status": f.Status, "q": f.Search, "store": f.Store, "module": f.Module,
		},
	}
	f.Offset = page.Offset()

	rows, total, err := a.DB.ListActivations(ctx, f)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	page.Total = total

	storeValues, err := a.DB.DistinctActivationStores(ctx)
	if err != nil {
		a.serverError(w, r, err)
		return
	}

	v := adminActivationsView{
		view:          a.newView(r, "Клиенти и активации", "activations"),
		Activations:   rows,
		Page:          page,
		Filter:        f,
		Stores:        storeValues,
		Modules:       modules.All,
		Today:         dates.Today(),
		StatusOptions: activationStatusOptions,
	}

	if isHTMX(r) && q.Get("partial") == "1" {
		if err := a.render.renderPartial(w, http.StatusOK, "admin/activations", "activation_table", v); err != nil {
			a.serverError(w, r, err)
		}
		return
	}
	if err := a.render.render(w, http.StatusOK, LayoutAdmin, "admin/activations", v); err != nil {
		a.serverError(w, r, err)
	}
}

type activationFormView struct {
	view
	Activation store.Activation
	Modules    []modules.Def
	Durations  []store.DurationOption
	Today      string
	Errors     []string
	IsNew      bool
}

func (a *App) handleAdminActivationNew(w http.ResponseWriter, r *http.Request) {
	v := activationFormView{
		view:      a.newView(r, "Нова активация", "activations"),
		Modules:   modules.All,
		Durations: store.DurationOptions(),
		Today:     dates.Today(),
		IsNew:     true,
		Activation: store.Activation{
			Module:    modules.FastCalculator,
			StartDate: dates.Today(),
		},
	}
	if err := a.render.render(w, http.StatusOK, LayoutAdmin, "admin/activation_form", v); err != nil {
		a.serverError(w, r, err)
	}
}

// parseActivationForm reads and validates the manual activation form.
//
// The client fields are edited here rather than through a standalone client
// record - PLACEHOLDER-D from the original specification, resolved: this
// application never needs to identify a client except by its 9-digit code,
// and the one thing that has to be remembered per client (whether its single
// test period is used) is already derived from client_code on the requests
// table (see store.DB.TestPeriodUsed), not from a client entity. See
// DECISIONS.md.
func parseActivationForm(r *http.Request) (store.Activation, []string) {
	var errs []string
	a := store.Activation{
		ClientCode:   strings.TrimSpace(r.PostFormValue("client_code")),
		ClientName:   strings.TrimSpace(r.PostFormValue("client_name")),
		ClientObject: strings.TrimSpace(r.PostFormValue("client_object")),
		ClientStore:  strings.TrimSpace(r.PostFormValue("client_store")),
		Username:     strings.TrimSpace(r.PostFormValue("username")),
		Module:       modules.Key(strings.TrimSpace(r.PostFormValue("module"))),
		Tier:         modules.Tier(strings.TrimSpace(r.PostFormValue("tier"))),
		StartDate:    strings.TrimSpace(r.PostFormValue("start_date")),
		EndDate:      strings.TrimSpace(r.PostFormValue("end_date")),
	}

	if a.ClientCode == "" {
		errs = append(errs, "Въведете клиентски номер.")
	}
	if a.ClientName == "" {
		errs = append(errs, "Въведете име на клиент.")
	}
	if a.Username == "" {
		errs = append(errs, "Въведете потребител.")
	}

	def, ok := modules.Get(a.Module)
	switch {
	case !ok:
		errs = append(errs, "Изберете модул.")
	case !def.RequiresTier():
		// A tier posted for a module that has none is simply dropped.
		a.Tier = ""
	case !modules.ValidTier(a.Module, a.Tier):
		errs = append(errs, "Изберете ниво за "+def.Label+".")
	}

	if _, err := dates.ParseISO(a.StartDate); err != nil {
		errs = append(errs, "Невалидна начална дата.")
	}

	// The end date can be given directly, or derived from a month count.
	if months, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue("months"))); err == nil && months >= 1 && months <= 12 {
		if end, err := dates.EndDate(a.StartDate, months); err == nil {
			a.EndDate = end
		}
	}
	if _, err := dates.ParseISO(a.EndDate); err != nil {
		errs = append(errs, "Невалидна крайна дата.")
	} else if a.EndDate < a.StartDate {
		errs = append(errs, "Крайната дата не може да е преди началната.")
	}

	return a, errs
}

func (a *App) handleAdminActivationCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	admin := adminFrom(ctx)

	act, errs := parseActivationForm(r)
	if len(errs) > 0 {
		v := activationFormView{
			view:       a.newView(r, "Нова активация", "activations"),
			Activation: act,
			Modules:    modules.All,
			Durations:  store.DurationOptions(),
			Today:      dates.Today(),
			Errors:     errs,
			IsNew:      true,
		}
		if err := a.render.render(w, http.StatusUnprocessableEntity, LayoutAdmin, "admin/activation_form", v); err != nil {
			a.serverError(w, r, err)
		}
		return
	}

	act.CreatedByAdminID.Int64, act.CreatedByAdminID.Valid = admin.ID, true
	id, err := a.DB.InsertActivation(ctx, a.DB.DB, act)
	if err != nil {
		a.serverError(w, r, err)
		return
	}

	a.Audit.Record(ctx, audit.AdminActor(admin.ID, admin.Email), audit.ActivationCreated,
		strconv.FormatInt(id, 10), a.clientIP(r), map[string]any{
			"client_code": act.ClientCode,
			"username":    act.Username,
			"module":      string(act.Module),
			"tier":        string(act.Tier),
			"start_date":  act.StartDate,
			"end_date":    act.EndDate,
		})
	redirectWithFlash(w, r, "/admin/activations", "activation_created")
}

func (a *App) handleAdminActivationEdit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, ok := idParam(r, "id")
	if !ok {
		a.notFound(w, r)
		return
	}
	act, err := a.DB.ActivationByID(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		a.notFound(w, r)
		return
	}
	if err != nil {
		a.serverError(w, r, err)
		return
	}

	v := activationFormView{
		view:       a.newView(r, "Активация", "activations"),
		Activation: *act,
		Modules:    modules.All,
		Durations:  store.DurationOptions(),
		Today:      dates.Today(),
	}
	if err := a.render.render(w, http.StatusOK, LayoutAdmin, "admin/activation_form", v); err != nil {
		a.serverError(w, r, err)
	}
}

func (a *App) handleAdminActivationUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	admin := adminFrom(ctx)

	id, ok := idParam(r, "id")
	if !ok {
		a.notFound(w, r)
		return
	}
	existing, err := a.DB.ActivationByID(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		a.notFound(w, r)
		return
	}
	if err != nil {
		a.serverError(w, r, err)
		return
	}

	act, errs := parseActivationForm(r)
	act.ID = existing.ID
	act.RevokedAt = existing.RevokedAt

	if len(errs) > 0 {
		v := activationFormView{
			view:       a.newView(r, "Активация", "activations"),
			Activation: act,
			Modules:    modules.All,
			Durations:  store.DurationOptions(),
			Today:      dates.Today(),
			Errors:     errs,
		}
		if err := a.render.render(w, http.StatusUnprocessableEntity, LayoutAdmin, "admin/activation_form", v); err != nil {
			a.serverError(w, r, err)
		}
		return
	}

	if err := a.DB.UpdateActivation(ctx, act); err != nil {
		a.serverError(w, r, err)
		return
	}

	a.Audit.Record(ctx, audit.AdminActor(admin.ID, admin.Email), audit.ActivationUpdated,
		strconv.FormatInt(id, 10), a.clientIP(r), map[string]any{
			"client_code": act.ClientCode,
			"username":    act.Username,
			"module":      string(act.Module),
			"start_date":  act.StartDate,
			"end_date":    act.EndDate,
		})
	redirectWithFlash(w, r, "/admin/activations", "activation_updated")
}

func (a *App) handleAdminActivationRevoke(w http.ResponseWriter, r *http.Request) {
	a.activationStateChange(w, r, "revoke")
}

func (a *App) handleAdminActivationRestore(w http.ResponseWriter, r *http.Request) {
	a.activationStateChange(w, r, "restore")
}

func (a *App) handleAdminActivationDelete(w http.ResponseWriter, r *http.Request) {
	a.activationStateChange(w, r, "delete")
}

// activationStateChange applies revoke, restore or delete to one activation.
func (a *App) activationStateChange(w http.ResponseWriter, r *http.Request, op string) {
	ctx := r.Context()
	admin := adminFrom(ctx)

	id, ok := idParam(r, "id")
	if !ok {
		a.notFound(w, r)
		return
	}
	act, err := a.DB.ActivationByID(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		redirectWithError(w, r, "/admin/activations", "not_found")
		return
	}
	if err != nil {
		a.serverError(w, r, err)
		return
	}

	var action, flash string
	switch op {
	case "revoke":
		err, action, flash = a.DB.RevokeActivation(ctx, id), audit.ActivationRevoked, "activation_revoked"
	case "restore":
		err, action, flash = a.DB.RestoreActivation(ctx, id), audit.ActivationRestored, "activation_restored"
	case "delete":
		err, action, flash = a.DB.DeleteActivation(ctx, id), audit.ActivationDeleted, "activation_deleted"
	}
	if err != nil {
		a.serverError(w, r, err)
		return
	}

	a.Audit.Record(ctx, audit.AdminActor(admin.ID, admin.Email), action,
		strconv.FormatInt(id, 10), a.clientIP(r), map[string]any{
			"client_code": act.ClientCode,
			"username":    act.Username,
			"module":      string(act.Module),
		})
	redirectWithFlash(w, r, "/admin/activations", flash)
}

// handleAdminExport streams the Excel report of active activations. With
// group=1 the activations are grouped under a heading row per client.
func (a *App) handleAdminExport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	admin := adminFrom(ctx)

	opts := export.Options{GroupByClient: r.URL.Query().Get("group") == "1"}
	res, err := a.Export.Generate(ctx, opts)
	if err != nil {
		a.serverError(w, r, err)
		return
	}

	a.Audit.Record(ctx, audit.AdminActor(admin.ID, admin.Email), audit.ExportManual,
		res.Filename, a.clientIP(r), map[string]any{
			"rows":            res.RowCount,
			"group_by_client": opts.GroupByClient,
		})

	w.Header().Set("Content-Type",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="`+res.Filename+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(res.Content)))
	w.Header().Set("Cache-Control", "no-store")
	if _, err := w.Write(res.Content); err != nil {
		a.Log.Error("export could not be written to the response", "error", err)
	}
}
