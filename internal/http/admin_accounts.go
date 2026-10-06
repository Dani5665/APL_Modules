package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"haynesproform/internal/audit"
	"haynesproform/internal/auth"
	"haynesproform/internal/store"
)

type adminUsersView struct {
	view
	Users   []store.User
	Stores  []store.Store
	Filter  store.UserFilter
	StoreID int64
}

func (a *App) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	storeID, _ := strconv.ParseInt(q.Get("store"), 10, 64)
	f := store.UserFilter{Search: strings.TrimSpace(q.Get("q")), StoreID: storeID}

	users, err := a.DB.ListUsers(ctx, f)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	stores, err := a.DB.ListStores(ctx, false)
	if err != nil {
		a.serverError(w, r, err)
		return
	}

	v := adminUsersView{
		view:    a.newView(r, "Потребители", "users"),
		Users:   users,
		Stores:  stores,
		Filter:  f,
		StoreID: storeID,
	}

	if isHTMX(r) && q.Get("partial") == "1" {
		if err := a.render.renderPartial(w, http.StatusOK, "admin/users", "user_table", v); err != nil {
			a.serverError(w, r, err)
		}
		return
	}
	if err := a.render.render(w, http.StatusOK, LayoutAdmin, "admin/users", v); err != nil {
		a.serverError(w, r, err)
	}
}

type userFormView struct {
	view
	Target   *store.User
	Stores   []store.Store
	Selected map[int64]bool
	Errors   []string
	IsNew    bool
	Email    string
}

func (a *App) handleAdminUserNew(w http.ResponseWriter, r *http.Request) {
	stores, err := a.DB.ListStores(r.Context(), true)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	v := userFormView{
		view:     a.newView(r, "Нов потребител", "users"),
		Stores:   stores,
		Selected: map[int64]bool{},
		IsNew:    true,
	}
	if err := a.render.render(w, http.StatusOK, LayoutAdmin, "admin/user_form", v); err != nil {
		a.serverError(w, r, err)
	}
}

func (a *App) handleAdminUserCreate(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	admin := adminFrom(c)

	rawEmail := strings.TrimSpace(r.PostFormValue("email"))
	password := r.PostFormValue("password")
	storeIDs := parseIDs(r.PostForm["stores"])

	var errs []string
	email, err := auth.NormalizeEmail(rawEmail)
	if err != nil {
		errs = append(errs, "Потребителското име трябва да е валиден имейл адрес.")
	}
	hash, herr := auth.HashPassword(password)
	if herr != nil {
		errs = append(errs, herr.Error())
	}
	if len(storeIDs) == 0 {
		errs = append(errs, "Изберете поне един магазин.")
	}

	if len(errs) == 0 {
		_, err = a.DB.CreateUser(c, email, hash, storeIDs, false)
		if errors.Is(err, store.ErrConflict) {
			errs = append(errs, "Вече съществува потребител с този имейл.")
		} else if err != nil {
			a.serverError(w, r, err)
			return
		}
	}

	if len(errs) > 0 {
		stores, serr := a.DB.ListStores(c, true)
		if serr != nil {
			a.serverError(w, r, serr)
			return
		}
		v := userFormView{
			view:     a.newView(r, "Нов потребител", "users"),
			Stores:   stores,
			Selected: idSet(storeIDs),
			Errors:   errs,
			IsNew:    true,
			Email:    rawEmail,
		}
		if err := a.render.render(w, http.StatusUnprocessableEntity, LayoutAdmin, "admin/user_form", v); err != nil {
			a.serverError(w, r, err)
		}
		return
	}

	a.Audit.Record(c, audit.AdminActor(admin.ID, admin.Email), audit.UserCreated,
		email, a.clientIP(r), map[string]any{"stores": storeIDs})
	redirectWithFlash(w, r, "/admin/users", "user_created")
}

func (a *App) handleAdminUserEdit(w http.ResponseWriter, r *http.Request) {
	c := r.Context()

	id, ok := idParam(r, "id")
	if !ok {
		a.notFound(w, r)
		return
	}
	u, err := a.DB.UserByID(c, id)
	if errors.Is(err, store.ErrNotFound) {
		a.notFound(w, r)
		return
	}
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	stores, err := a.DB.ListStores(c, false)
	if err != nil {
		a.serverError(w, r, err)
		return
	}

	selected := map[int64]bool{}
	for _, s := range u.Stores {
		selected[s.ID] = true
	}

	v := userFormView{
		view:     a.newView(r, u.Email, "users"),
		Target:   u,
		Stores:   stores,
		Selected: selected,
		Email:    u.Email,
	}
	if err := a.render.render(w, http.StatusOK, LayoutAdmin, "admin/user_form", v); err != nil {
		a.serverError(w, r, err)
	}
}

func (a *App) handleAdminUserUpdate(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	admin := adminFrom(c)

	id, ok := idParam(r, "id")
	if !ok {
		a.notFound(w, r)
		return
	}
	storeIDs := parseIDs(r.PostForm["stores"])
	if len(storeIDs) == 0 {
		redirectWithError(w, r, "/admin/users/"+strconv.FormatInt(id, 10), "not_found")
		return
	}
	if err := a.DB.SetUserStores(c, id, storeIDs); err != nil {
		a.serverError(w, r, err)
		return
	}

	a.Audit.Record(c, audit.AdminActor(admin.ID, admin.Email), audit.UserUpdated,
		strconv.FormatInt(id, 10), a.clientIP(r), map[string]any{"stores": storeIDs})
	redirectWithFlash(w, r, "/admin/users", "user_updated")
}

func (a *App) handleAdminUserPassword(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	admin := adminFrom(c)

	id, ok := idParam(r, "id")
	if !ok {
		a.notFound(w, r)
		return
	}
	hash, err := auth.HashPassword(r.PostFormValue("password"))
	if err != nil {
		a.renderError(w, r, http.StatusUnprocessableEntity, "Невалидна парола", err.Error())
		return
	}
	if err := a.DB.SetUserPassword(c, id, hash, false); err != nil {
		a.serverError(w, r, err)
		return
	}
	// Existing sessions must not survive a password reset.
	if err := a.DB.DeleteSessionsFor(c, store.SubjectUser, id); err != nil {
		a.Log.Error("sessions could not be cleared after a password reset", "user_id", id, "error", err)
	}

	a.Audit.Record(c, audit.AdminActor(admin.ID, admin.Email), audit.PasswordReset,
		strconv.FormatInt(id, 10), a.clientIP(r), map[string]any{"subject": "user"})
	redirectWithFlash(w, r, "/admin/users/"+strconv.FormatInt(id, 10), "password_reset")
}

func (a *App) handleAdminUserSetActive(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	admin := adminFrom(c)

	id, ok := idParam(r, "id")
	if !ok {
		a.notFound(w, r)
		return
	}
	active := checkboxValue(r, "active")
	if err := a.DB.SetUserActive(c, id, active); err != nil {
		a.serverError(w, r, err)
		return
	}

	a.Audit.Record(c, audit.AdminActor(admin.ID, admin.Email), audit.UserUpdated,
		strconv.FormatInt(id, 10), a.clientIP(r), map[string]any{"active": active})
	redirectWithFlash(w, r, "/admin/users", "user_updated")
}

func (a *App) handleAdminUserDelete(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	admin := adminFrom(c)

	id, ok := idParam(r, "id")
	if !ok {
		a.notFound(w, r)
		return
	}
	u, err := a.DB.UserByID(c, id)
	if errors.Is(err, store.ErrNotFound) {
		redirectWithError(w, r, "/admin/users", "not_found")
		return
	}
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	if err := a.DB.DeleteUser(c, id); err != nil {
		a.serverError(w, r, err)
		return
	}

	a.Audit.Record(c, audit.AdminActor(admin.ID, admin.Email), audit.UserDeleted,
		u.Email, a.clientIP(r), nil)
	redirectWithFlash(w, r, "/admin/users", "user_deleted")
}

type adminAdminsView struct {
	view
	Admins []store.Admin
	Errors []string
}

func (a *App) handleAdminAdmins(w http.ResponseWriter, r *http.Request) {
	a.renderAdmins(w, r, nil, http.StatusOK)
}

func (a *App) renderAdmins(w http.ResponseWriter, r *http.Request, errs []string, status int) {
	admins, err := a.DB.ListAdmins(r.Context())
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	v := adminAdminsView{
		view:   a.newView(r, "Администратори", "admins"),
		Admins: admins,
		Errors: errs,
	}
	if err := a.render.render(w, status, LayoutAdmin, "admin/admins", v); err != nil {
		a.serverError(w, r, err)
	}
}

func (a *App) handleAdminAdminCreate(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	admin := adminFrom(c)

	rawEmail := strings.TrimSpace(r.PostFormValue("email"))
	var errs []string

	email, err := auth.NormalizeEmail(rawEmail)
	if err != nil {
		errs = append(errs, "Потребителското име трябва да е валиден имейл адрес.")
	}
	hash, herr := auth.HashPassword(r.PostFormValue("password"))
	if herr != nil {
		errs = append(errs, herr.Error())
	}

	if len(errs) == 0 {
		if _, err := a.DB.CreateAdmin(c, email, hash, false); errors.Is(err, store.ErrConflict) {
			errs = append(errs, "Вече съществува администратор с този имейл.")
		} else if err != nil {
			a.serverError(w, r, err)
			return
		}
	}
	if len(errs) > 0 {
		a.renderAdmins(w, r, errs, http.StatusUnprocessableEntity)
		return
	}

	a.Audit.Record(c, audit.AdminActor(admin.ID, admin.Email), audit.AdminCreated,
		email, a.clientIP(r), nil)
	redirectWithFlash(w, r, "/admin/admins", "admin_created")
}

func (a *App) handleAdminAdminPassword(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	admin := adminFrom(c)

	id, ok := idParam(r, "id")
	if !ok {
		a.notFound(w, r)
		return
	}
	hash, err := auth.HashPassword(r.PostFormValue("password"))
	if err != nil {
		a.renderAdmins(w, r, []string{err.Error()}, http.StatusUnprocessableEntity)
		return
	}
	if err := a.DB.SetAdminPassword(c, id, hash, true); err != nil {
		a.serverError(w, r, err)
		return
	}
	if id != admin.ID {
		if err := a.DB.DeleteSessionsFor(c, store.SubjectAdmin, id); err != nil {
			a.Log.Error("admin sessions could not be cleared", "admin_id", id, "error", err)
		}
	}

	a.Audit.Record(c, audit.AdminActor(admin.ID, admin.Email), audit.PasswordReset,
		strconv.FormatInt(id, 10), a.clientIP(r), map[string]any{"subject": "admin"})
	redirectWithFlash(w, r, "/admin/admins", "password_reset")
}

func (a *App) handleAdminAdminReset2FA(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	admin := adminFrom(c)

	id, ok := idParam(r, "id")
	if !ok {
		a.notFound(w, r)
		return
	}
	target, err := a.DB.AdminByID(c, id)
	if errors.Is(err, store.ErrNotFound) {
		redirectWithError(w, r, "/admin/admins", "not_found")
		return
	}
	if err != nil {
		a.serverError(w, r, err)
		return
	}

	// Resetting 2FA also drops the target's sessions, so the next login must
	// re-enroll.
	if err := a.DB.ResetAdminTOTP(c, id); err != nil {
		a.serverError(w, r, err)
		return
	}

	a.Audit.Record(c, audit.AdminActor(admin.ID, admin.Email), audit.TOTPReset,
		target.Email, a.clientIP(r), nil)

	// An admin who reset their own 2FA has just lost their session.
	if id == admin.ID {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}
	redirectWithFlash(w, r, "/admin/admins", "twofa_reset")
}

func (a *App) handleAdminAdminDelete(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	admin := adminFrom(c)

	id, ok := idParam(r, "id")
	if !ok {
		a.notFound(w, r)
		return
	}
	if id == admin.ID {
		redirectWithError(w, r, "/admin/admins", "self_delete")
		return
	}

	target, err := a.DB.AdminByID(c, id)
	if errors.Is(err, store.ErrNotFound) {
		redirectWithError(w, r, "/admin/admins", "not_found")
		return
	}
	if err != nil {
		a.serverError(w, r, err)
		return
	}

	if err := a.DB.DeleteAdmin(c, id); errors.Is(err, store.ErrInUse) {
		redirectWithError(w, r, "/admin/admins", "last_admin")
		return
	} else if err != nil {
		a.serverError(w, r, err)
		return
	}

	a.Audit.Record(c, audit.AdminActor(admin.ID, admin.Email), audit.AdminDeleted,
		target.Email, a.clientIP(r), nil)
	redirectWithFlash(w, r, "/admin/admins", "admin_deleted")
}

type adminStoresView struct {
	view
	Stores []store.Store
	Errors []string
}

func (a *App) handleAdminStores(w http.ResponseWriter, r *http.Request) {
	a.renderStores(w, r, nil, http.StatusOK)
}

func (a *App) renderStores(w http.ResponseWriter, r *http.Request, errs []string, status int) {
	stores, err := a.DB.ListStores(r.Context(), false)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	v := adminStoresView{
		view:   a.newView(r, "Магазини", "stores"),
		Stores: stores,
		Errors: errs,
	}
	if err := a.render.render(w, status, LayoutAdmin, "admin/stores", v); err != nil {
		a.serverError(w, r, err)
	}
}

func (a *App) handleAdminStoreCreate(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	admin := adminFrom(c)

	name := strings.TrimSpace(r.PostFormValue("name"))
	external := strings.TrimSpace(r.PostFormValue("external_value"))
	if name == "" {
		a.renderStores(w, r, []string{"Въведете име на магазин."}, http.StatusUnprocessableEntity)
		return
	}

	id, err := a.DB.CreateStore(c, name, external)
	if errors.Is(err, store.ErrConflict) {
		a.renderStores(w, r, []string{"Вече съществува магазин с това име."}, http.StatusUnprocessableEntity)
		return
	}
	if err != nil {
		a.serverError(w, r, err)
		return
	}

	a.Audit.Record(c, audit.AdminActor(admin.ID, admin.Email), audit.StoreCreated,
		strconv.FormatInt(id, 10), a.clientIP(r), map[string]any{"name": name, "external_value": external})
	redirectWithFlash(w, r, "/admin/stores", "store_created")
}

func (a *App) handleAdminStoreUpdate(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	admin := adminFrom(c)

	id, ok := idParam(r, "id")
	if !ok {
		a.notFound(w, r)
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	external := strings.TrimSpace(r.PostFormValue("external_value"))
	active := checkboxValue(r, "active")

	if name == "" {
		a.renderStores(w, r, []string{"Въведете име на магазин."}, http.StatusUnprocessableEntity)
		return
	}
	if err := a.DB.UpdateStore(c, id, name, external, active); errors.Is(err, store.ErrConflict) {
		a.renderStores(w, r, []string{"Вече съществува магазин с това име."}, http.StatusUnprocessableEntity)
		return
	} else if err != nil {
		a.serverError(w, r, err)
		return
	}

	a.Audit.Record(c, audit.AdminActor(admin.ID, admin.Email), audit.StoreUpdated,
		strconv.FormatInt(id, 10), a.clientIP(r), map[string]any{
			"name": name, "external_value": external, "active": active,
		})
	redirectWithFlash(w, r, "/admin/stores", "store_updated")
}

func (a *App) handleAdminStoreDelete(w http.ResponseWriter, r *http.Request) {
	c := r.Context()
	admin := adminFrom(c)

	id, ok := idParam(r, "id")
	if !ok {
		a.notFound(w, r)
		return
	}
	if err := a.DB.DeleteStore(c, id); errors.Is(err, store.ErrInUse) {
		redirectWithError(w, r, "/admin/stores", "store_in_use")
		return
	} else if err != nil {
		a.serverError(w, r, err)
		return
	}

	a.Audit.Record(c, audit.AdminActor(admin.ID, admin.Email), audit.StoreDeleted,
		strconv.FormatInt(id, 10), a.clientIP(r), nil)
	redirectWithFlash(w, r, "/admin/stores", "store_deleted")
}

// parseIDs converts posted checkbox values into ids, skipping anything that
// is not a positive number.
func parseIDs(values []string) []int64 {
	out := make([]int64, 0, len(values))
	for _, v := range values {
		if id, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && id > 0 {
			out = append(out, id)
		}
	}
	return out
}

func idSet(ids []int64) map[int64]bool {
	m := make(map[int64]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}
