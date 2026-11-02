package http

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"haynesproform/internal/audit"
	"haynesproform/internal/external"
	"haynesproform/internal/store"
)

// Limits of the "Заявка за профил в NEXT" form.
const (
	usernameMin = 5
	usernameMax = 20
	passwordMin = 5
	passwordMax = 15
	commentMax  = 150
)

// validLatinAlnum reports whether s holds only Latin letters and digits, and
// between min and max of them. Anything else - spaces, symbols, Cyrillic - is
// rejected.
func validLatinAlnum(s string, min, max int) bool {
	if len(s) < min || len(s) > max {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

// profileSubmission is what the user typed, carried back when the form is
// rejected.
type profileSubmission struct {
	// Store is the MANDANT_ID of the serving store the user picked.
	Store     string
	Username  string
	Password  string
	Transport string
	Comment   string
}

// profileFormView drives the profile request form.
type profileFormView struct {
	CSRF     string
	Customer external.Customer
	// Stores are the sender's own stores, one of which is picked as the serving
	// store. None is selected at first.
	Stores []store.Store
	// Transports are the options of the picked serving store; none means the
	// field is not applicable.
	Transports      []string
	SubmitterName   string
	SubmitterStores string
	// SubmitterStoreNames lists the sender's stores for the form.
	SubmitterStoreNames []string
	Submitted           profileSubmission
	Errors              []string
	// TransportsUnavailable is set when the options could not be read.
	TransportsUnavailable bool
}

// buildProfileForm gathers what the form offers: the sender's stores to pick
// the serving store from, and the sender's details. Transports are loaded once
// a store is picked.
func (a *App) buildProfileForm(r *http.Request, customer external.Customer) profileFormView {
	ctx := r.Context()
	u := userFrom(ctx)

	f := profileFormView{CSRF: csrfFrom(ctx), Customer: customer, Stores: u.Stores}

	f.SubmitterName = u.Name
	if f.SubmitterName == "" {
		f.SubmitterName = u.Email
	}
	names := make([]string, 0, len(u.Stores))
	for _, s := range u.Stores {
		names = append(names, s.Name)
	}
	f.SubmitterStores = strings.Join(names, ", ")
	f.SubmitterStoreNames = names
	return f
}

// TransportView is the transport field as the form shows it.
func (f profileFormView) TransportView() transportSelectView {
	return transportSelectView{
		Transports:            f.Transports,
		Selected:              f.Submitted.Transport,
		TransportsUnavailable: f.TransportsUnavailable,
		NoStore:               f.Submitted.Store == "",
	}
}

// userStore returns the user's store with the given MANDANT_ID, if assigned.
func userStore(u *store.User, mandantID string) (store.Store, bool) {
	mandantID = strings.TrimSpace(mandantID)
	if mandantID == "" {
		return store.Store{}, false
	}
	for _, s := range u.Stores {
		if strings.EqualFold(strings.TrimSpace(s.MandantID), mandantID) {
			return s, true
		}
	}
	return store.Store{}, false
}

// transportSelectView drives the transport field, which is reloaded whenever the
// serving store changes.
type transportSelectView struct {
	Transports            []string
	Selected              string
	TransportsUnavailable bool
	// NoStore is set while no serving store is picked.
	NoStore bool
}

// handleProfileTransports serves the transport field for the picked serving
// store. Only the user's own stores are accepted.
func (a *App) handleProfileTransports(w http.ResponseWriter, r *http.Request) {
	v := transportSelectView{NoStore: true}
	if st, ok := userStore(userFrom(r.Context()), r.URL.Query().Get("store")); ok {
		v.NoStore = false
		v.Transports, v.TransportsUnavailable = a.transportsByMandant(r.Context(), st.MandantID)
	}
	if err := a.render.renderPartial(w, http.StatusOK, "customer/info_detail", "transport_field", v); err != nil {
		a.serverError(w, r, err)
	}
}

// transportsByMandant reads the transport options of a store from the external
// directory. unavailable is set when they could not be read.
func (a *App) transportsByMandant(ctx context.Context, mandantID string) (transports []string, unavailable bool) {
	transports, err := a.Directory.ListTransports(ctx, mandantID)
	if err != nil {
		a.Log.Error("transports could not be loaded from the external directory",
			"mandant_id", mandantID, "error", err)
		return nil, true
	}
	return transports, false
}

// handleProfileRequest validates and stores a request for a new NEXT profile
// and queues the notification email. The customer's number and name always
// come from the external directory, never from the posted form.
func (a *App) handleProfileRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)

	if err := parseForm(w, r); err != nil {
		a.renderError(w, r, http.StatusBadRequest, "Невалидна заявка", "Заявката не може да бъде прочетена.")
		return
	}

	customer, err := a.Directory.GetCustomer(ctx, r.PathValue("code"))
	switch {
	case errors.Is(err, external.ErrNotFound):
		a.notFound(w, r)
		return
	case err != nil:
		a.externalError(w, r, err)
		return
	}

	form := a.buildProfileForm(r, *customer)

	sub := profileSubmission{
		Store:     strings.TrimSpace(r.PostFormValue("store")),
		Username:  strings.TrimSpace(r.PostFormValue("username")),
		Password:  r.PostFormValue("password"),
		Transport: strings.TrimSpace(r.PostFormValue("transport")),
		Comment:   strings.TrimSpace(r.PostFormValue("comment")),
	}

	var errs []string

	// The serving store must be one of the sender's own stores; the transports
	// are looked up from it here, not taken from the form.
	var storeName string
	if st, ok := userStore(u, sub.Store); ok {
		storeName = st.Name
		sub.Store = st.MandantID
		form.Transports, form.TransportsUnavailable = a.transportsByMandant(ctx, st.MandantID)
	} else {
		errs = append(errs, "Изберете обслужващ обект.")
	}

	validName := validLatinAlnum(sub.Username, usernameMin, usernameMax)
	if !validName {
		errs = append(errs, "Потребителското име трябва да е между 5 и 20 символа - само латински букви и цифри, без интервали и специални символи.")
	}
	if !validLatinAlnum(sub.Password, passwordMin, passwordMax) {
		errs = append(errs, "Паролата трябва да е между 5 и 15 символа - само латински букви и цифри, без интервали и специални символи.")
	}

	if len(form.Transports) > 0 {
		found := false
		for _, t := range form.Transports {
			if t == sub.Transport {
				found = true
			}
		}
		if !found {
			errs = append(errs, "Изберете транспорт.")
		}
	} else {
		sub.Transport = ""
	}

	if utf8.RuneCountInString(sub.Comment) > commentMax {
		errs = append(errs, "Коментарът може да е най-много 150 символа.")
	}

	// Checked again here, not only when the field lost focus: the name may
	// have been taken in the meantime, and the browser check is not trusted.
	if validName {
		taken, err := a.Directory.LoginExists(ctx, sub.Username)
		switch {
		case err != nil:
			a.Log.Error("username could not be checked in the external directory", "error", err)
			errs = append(errs, "Проверката на потребителското име не е възможна в момента. Опитайте отново.")
		case taken:
			errs = append(errs, "Вече съществува потребител с това име")
		}
	}

	form.Submitted = sub
	if len(errs) > 0 {
		form.Errors = errs
		if err := a.render.renderPartial(w, http.StatusUnprocessableEntity, "customer/info_detail", "profile_form", form); err != nil {
			a.serverError(w, r, err)
		}
		return
	}

	enc, err := a.Enc.Encrypt(sub.Password)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	n := store.NewProfileRequest{
		ClientCode:      customer.Code,
		ClientName:      customer.Name,
		StoreName:       storeName,
		Username:        sub.Username,
		PasswordEnc:     enc,
		Transport:       sub.Transport,
		SubmitterID:     u.ID,
		SubmitterName:   form.SubmitterName,
		SubmitterEmail:  u.Email,
		SubmitterStores: form.SubmitterStores,
		Comment:         sub.Comment,
	}
	id, err := a.DB.CreateProfileRequest(ctx, n)
	if err != nil {
		a.serverError(w, r, err)
		return
	}

	// The password is not written to the audit log.
	a.Audit.Record(ctx, audit.UserActor(u.ID, u.Email), audit.ProfileRequestSubmitted,
		strconv.FormatInt(id, 10), a.clientIP(r), map[string]any{
			"client_code": customer.Code,
			"username":    sub.Username,
			"store":       storeName,
			"transport":   sub.Transport,
		})
	a.queueProfileRequest(ctx, id, n, sub.Password)

	if err := a.render.renderPartial(w, http.StatusOK, "customer/info_detail", "profile_success",
		map[string]any{"Username": sub.Username, "ClientName": customer.Name}); err != nil {
		a.serverError(w, r, err)
	}
}

// usernameStatus is what the username check shows beside the field.
type usernameStatus struct {
	// State is "free", "taken", "invalid" or "error".
	State string
}

// handleUsernameCheck answers the check made when the username field loses
// focus: is the name well formed, and is it unused in the catalog?
func (a *App) handleUsernameCheck(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("username"))
	if name == "" {
		// Nothing typed: show nothing.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		return
	}

	st := usernameStatus{State: "invalid"}
	if validLatinAlnum(name, usernameMin, usernameMax) {
		taken, err := a.Directory.LoginExists(r.Context(), name)
		switch {
		case err != nil:
			a.Log.Error("username could not be checked in the external directory", "error", err)
			st.State = "error"
		case taken:
			st.State = "taken"
		default:
			st.State = "free"
		}
	}
	if err := a.render.renderPartial(w, http.StatusOK, "customer/info_detail", "username_status", st); err != nil {
		a.serverError(w, r, err)
	}
}
