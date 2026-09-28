package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"haynesproform/internal/audit"
	"haynesproform/internal/auth"
	"haynesproform/internal/dates"
	"haynesproform/internal/external"
	"haynesproform/internal/modules"
	"haynesproform/internal/requests"
	"haynesproform/internal/store"
)

// handleEntryLink receives the link from the parent application, parks the
// payload against a pre-session cookie and sends the salesperson to the login
// page (or straight to the form if they are already logged in).
//
// TODO(PLACEHOLDER-A): the final URL format and any signature or expiry
// verification belong in the EntryLinkParser, not here.
func (a *App) handleEntryLink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	link, err := a.EntryLink.Parse(r)
	if err != nil {
		a.Log.Warn("entry link rejected", "ip", a.clientIP(r), "error", err)
		a.Audit.Record(ctx, audit.AnonActor(""), audit.EntryDenied, "", a.clientIP(r),
			map[string]any{"reason": "malformed entry link"})
		a.renderError(w, r, http.StatusBadRequest, "Невалиден линк",
			"Линкът не е валиден. Върнете се в основното приложение и опитайте отново.")
		return
	}

	if err := a.Sessions.ParkEntryLink(ctx, w, link.ClientCode, link.SalerLogin); err != nil {
		a.serverError(w, r, err)
		return
	}

	// An active session goes straight on to the form.
	if _, err := a.Sessions.Load(ctx, r, auth.UserAudience); err == nil {
		http.Redirect(w, r, "/request", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

type loginView struct {
	view
	Email string
	Error string
}

func (a *App) handleUserLoginForm(w http.ResponseWriter, r *http.Request) {
	// Already logged in: go where the entry link points, or home.
	if _, err := a.Sessions.Load(r.Context(), r, auth.UserAudience); err == nil {
		http.Redirect(w, r, a.postLoginTarget(r), http.StatusSeeOther)
		return
	}
	a.renderUserLogin(w, r, "", "", http.StatusOK)
}

func (a *App) renderUserLogin(w http.ResponseWriter, r *http.Request, email, errMsg string, status int) {
	v := loginView{view: a.newView(r, "Вход", "login"), Email: email, Error: errMsg}
	// The login page has no session yet, so it carries no CSRF token: the
	// form is protected by SameSite=Lax and by the credentials themselves.
	if err := a.render.render(w, status, LayoutBare, "customer/login", v); err != nil {
		a.serverError(w, r, err)
	}
}

func (a *App) handleUserLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := parseForm(w, r); err != nil {
		a.renderError(w, r, http.StatusBadRequest, "Невалидна заявка", "Заявката не може да бъде прочетена.")
		return
	}

	rawEmail := strings.TrimSpace(r.PostFormValue("email"))
	password := r.PostFormValue("password")
	ip := a.clientIP(r)

	const genericError = "Грешен имейл или парола."

	lock, err := a.Throttle.Locked(ctx, auth.UserAudience.Name, ip, rawEmail)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	if lock.Locked {
		a.Audit.Record(ctx, audit.AnonActor(rawEmail), audit.LoginLocked, rawEmail, ip, nil)
		a.renderUserLogin(w, r, rawEmail,
			"Твърде много неуспешни опити. Опитайте отново след 15 минути.", http.StatusTooManyRequests)
		return
	}

	email, emailErr := auth.NormalizeEmail(rawEmail)
	var user *store.User
	if emailErr == nil {
		user, err = a.DB.UserByEmail(ctx, email)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			a.serverError(w, r, err)
			return
		}
	}

	// The same generic message covers an unknown account, a wrong password
	// and a deactivated account, so the form reveals nothing.
	if user == nil || !user.Active || !auth.CheckPassword(user.PasswordHash, password) {
		if err := a.Throttle.RegisterFailure(ctx, auth.UserAudience.Name, ip, rawEmail); err != nil {
			a.Log.Error("login failure could not be recorded", "error", err)
		}
		a.Audit.Record(ctx, audit.AnonActor(rawEmail), audit.LoginFailure, rawEmail, ip, nil)
		a.renderUserLogin(w, r, rawEmail, genericError, http.StatusUnauthorized)
		return
	}

	if err := a.Throttle.Clear(ctx, auth.UserAudience.Name, ip, rawEmail); err != nil {
		a.Log.Error("login failures could not be cleared", "error", err)
	}
	if _, err := a.Sessions.Start(ctx, w, auth.UserAudience, user.ID, store.StageActive); err != nil {
		a.serverError(w, r, err)
		return
	}
	a.Audit.Record(ctx, audit.UserActor(user.ID, user.Email), audit.LoginSuccess, user.Email, ip, nil)

	if user.MustChangePassword {
		http.Redirect(w, r, "/password", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, a.postLoginTarget(r), http.StatusSeeOther)
}

// postLoginTarget sends a salesperson who arrived through an entry link to
// the request form, and everyone else to the informational home page.
func (a *App) postLoginTarget(r *http.Request) string {
	if _, ok := a.Sessions.PeekEntryLink(r.Context(), r); ok {
		return "/request"
	}
	return "/"
}

func (a *App) handleUserLogout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if u := userFrom(ctx); u != nil {
		a.Audit.Record(ctx, audit.UserActor(u.ID, u.Email), audit.Logout, u.Email, a.clientIP(r), nil)
	}
	if err := a.Sessions.Destroy(ctx, w, r, auth.UserAudience); err != nil {
		a.serverError(w, r, err)
		return
	}
	a.Sessions.ClearEntryCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

type homeView struct {
	view
	Clients    []store.ClientSummary
	ClientPage pagination
	Search     string
	// ClientsUnavailable is set when the client list could not be loaded, so
	// the page still renders with an inline notice.
	ClientsUnavailable bool
}

// handleHome shows the informational client list. Requests always start from
// the parent application, which the page says explicitly.
func (a *App) handleHome(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)

	if u.MustChangePassword {
		http.Redirect(w, r, "/password", http.StatusSeeOther)
		return
	}
	// An entry link waiting for this browser means the salesperson came here
	// to file a request.
	if _, ok := a.Sessions.PeekEntryLink(ctx, r); ok {
		http.Redirect(w, r, "/request", http.StatusSeeOther)
		return
	}

	v := homeView{view: a.newView(r, "Клиенти", "home")}
	clients, page, search, err := a.loadClientPage(r, u)
	if err != nil {
		a.Log.Error("client list could not be loaded", "error", err)
		v.ClientsUnavailable = true
	}
	v.Clients, v.ClientPage, v.Search = clients, page, search

	if err := a.render.render(w, http.StatusOK, LayoutCustomer, "customer/home", v); err != nil {
		a.serverError(w, r, err)
	}
}

// clientPageSize is how many clients one page of the informational list holds.
const clientPageSize = 50

// loadClientPage returns the filtered, paginated list of clients belonging to
// the account's stores that currently have at least one active module
// activation. This is sourced from the local database, not the external
// directory: a client the external directory knows about but that has
// nothing activated here does not appear.
func (a *App) loadClientPage(r *http.Request, u *store.User) ([]store.ClientSummary, pagination, string, error) {
	ctx := r.Context()
	search := strings.TrimSpace(r.URL.Query().Get("q"))

	p := pagination{
		Page:     pageParam(r),
		PageSize: clientPageSize,
		Query:    map[string]string{"q": search},
	}

	clients, total, err := a.DB.ListActiveClientsByStores(ctx, u.StoreExternalValues(), search,
		p.PageSize, p.Offset())
	if err != nil {
		return nil, pagination{}, search, err
	}
	p.Total = total

	return clients, p, search, nil
}

// handleClientList serves the htmx fragment behind the search box.
func (a *App) handleClientList(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())

	clients, page, search, err := a.loadClientPage(r, u)
	if err != nil {
		a.serverError(w, r, err)
		return
	}

	data := homeView{view: a.newView(r, "Клиенти", "home"), Clients: clients, ClientPage: page, Search: search}
	if err := a.render.renderPartial(w, http.StatusOK, "customer/home", "client_list", data); err != nil {
		a.serverError(w, r, err)
	}
}

// requestFormView drives the request form and everything below it.
type requestFormView struct {
	view
	Client          external.Client
	SalerLogin      string
	ClientUsernames []string
	Modules         []modules.Def
	Durations       []store.DurationOption
	Today           string
	TestPeriodUsed  bool
	Activations     []store.Activation
	PendingRequests []store.Request
	// Errors holds Bulgarian validation messages from a rejected submission.
	Errors []string
	// Submitted carries back what the user chose, so a rejected form is not
	// cleared.
	Submitted  requests.Selection
	Clients    []store.ClientSummary
	ClientPage pagination
	Search     string
}

// handleRequestForm renders the request form for the parked entry link.
func (a *App) handleRequestForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)

	if u.MustChangePassword {
		http.Redirect(w, r, "/password", http.StatusSeeOther)
		return
	}

	link, ok := a.Sessions.PeekEntryLink(ctx, r)
	if !ok {
		// No entry link: the informational page explains where to start.
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	client, err := a.authorizeEntry(w, r, u, link.ClientCode, link.SalerLogin)
	if err != nil {
		return // authorizeEntry has already written the response
	}

	v, err := a.buildRequestForm(r, u, link.SalerLogin, client)
	if err != nil {
		a.externalError(w, r, err)
		return
	}
	v.Submitted = requests.Selection{StartDate: dates.Today(), Months: 1}

	if err := a.render.render(w, http.StatusOK, LayoutCustomer, "customer/request", v); err != nil {
		a.serverError(w, r, err)
	}
}

// authorizeEntry performs the section 6.2 checks and, on failure, writes the
// error page and an audit entry. A nil error means the caller may proceed.
func (a *App) authorizeEntry(w http.ResponseWriter, r *http.Request, u *store.User, clientCode, salerLogin string) (external.Client, error) {
	ctx := r.Context()
	ip := a.clientIP(r)

	deny := func(reason string) (external.Client, error) {
		a.Audit.Record(ctx, audit.UserActor(u.ID, u.Email), audit.EntryDenied, clientCode, ip,
			map[string]any{"reason": reason, "saler_login": salerLogin})
		a.renderError(w, r, http.StatusForbidden, "Нямате достъп",
			"Нямате достъп до този клиент.")
		return external.Client{}, errors.New(reason)
	}

	saler, err := a.Directory.GetSaler(ctx, salerLogin)
	switch {
	case errors.Is(err, external.ErrNotFound):
		return deny("salesperson not found in the external directory")
	case err != nil:
		a.externalError(w, r, err)
		return external.Client{}, err
	}

	client, err := a.Directory.GetClientByCode(ctx, clientCode)
	switch {
	case errors.Is(err, external.ErrNotFound):
		return deny("client not found in the external directory")
	case err != nil:
		a.externalError(w, r, err)
		return external.Client{}, err
	}

	if err := requests.CheckStoreAccess(u, saler.Store, client.Store); err != nil {
		return deny(err.Error())
	}
	return *client, nil
}

// buildRequestForm gathers everything the form page shows.
func (a *App) buildRequestForm(r *http.Request, u *store.User, salerLogin string, client external.Client) (requestFormView, error) {
	ctx := r.Context()

	v := requestFormView{
		view:       a.newView(r, "Ново запитване", "request"),
		Client:     client,
		SalerLogin: salerLogin,
		Modules:    modules.All,
		Durations:  store.DurationOptions(),
		Today:      dates.Today(),
	}

	usernames, err := a.Directory.ListClientLogins(ctx, client.Code)
	if err != nil {
		return v, err
	}
	v.ClientUsernames = usernames

	if v.TestPeriodUsed, err = a.DB.TestPeriodUsed(ctx, client.Code, 0); err != nil {
		return v, err
	}
	if v.Activations, err = a.DB.ListActivationsForClient(ctx, client.Code); err != nil {
		return v, err
	}
	if v.PendingRequests, err = a.DB.ListRequestsForClient(ctx, client.Code, store.StatusPending); err != nil {
		return v, err
	}

	clients, page, search, err := a.loadClientPage(r, u)
	if err != nil {
		// The client list is informational; its absence must not block the
		// form itself.
		a.Log.Warn("client list could not be loaded for the request form", "error", err)
	}
	v.Clients, v.ClientPage, v.Search = clients, page, search
	return v, nil
}

// handleRequestSubmit validates and stores a submission, then queues the
// notification email.
func (a *App) handleRequestSubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)

	link, ok := a.Sessions.PeekEntryLink(ctx, r)
	if !ok {
		a.renderError(w, r, http.StatusBadRequest, "Липсва клиент",
			"Запитванията се започват от основното приложение.")
		return
	}

	// Authorization is re-checked on submit, not only when the form was shown.
	client, err := a.authorizeEntry(w, r, u, link.ClientCode, link.SalerLogin)
	if err != nil {
		return
	}

	sel := parseSelection(r)
	sel = requests.ApplyTestPeriod(sel)

	usernames, err := a.Directory.ListClientLogins(ctx, client.Code)
	if err != nil {
		a.externalError(w, r, err)
		return
	}
	testUsed, err := a.DB.TestPeriodUsed(ctx, client.Code, 0)
	if err != nil {
		a.serverError(w, r, err)
		return
	}

	selected, verr := requests.ValidateInput(sel, usernames, testUsed, dates.Today())
	if verr != nil {
		a.renderFormErrors(w, r, u, link.SalerLogin, client, sel, verr)
		return
	}

	id, err := a.DB.CreateRequest(ctx, store.NewRequest{
		ClientCode:      client.Code,
		ClientName:      client.Name,
		ClientObject:    client.Object,
		ClientStore:     client.Store,
		SubmitterUserID: u.ID,
		SubmitterEmail:  u.Email,
		SalerLogin:      link.SalerLogin,
		TestPeriod:      sel.TestPeriod,
		StartDate:       sel.StartDate,
		Months:          sel.Months,
		Usernames:       selected,
		Modules:         sel.Modules(),
	})
	if err != nil {
		a.serverError(w, r, err)
		return
	}

	// The entry link is consumed once the request is filed, so a refresh
	// cannot silently submit it again.
	a.Sessions.ClearEntryCookie(w)
	_, _ = a.Sessions.TakeEntryLink(ctx, w, r)

	a.Audit.Record(ctx, audit.UserActor(u.ID, u.Email), audit.RequestSubmitted,
		strconv.FormatInt(id, 10), a.clientIP(r), map[string]any{
			"client_code": client.Code,
			"usernames":   selected,
			"test_period": sel.TestPeriod,
			"months":      sel.Months,
			"start_date":  sel.StartDate,
		})

	a.queueRequestCreated(ctx, id)

	data := map[string]any{"RequestID": id, "ClientName": client.Name}
	if err := a.render.renderPartial(w, http.StatusOK, "customer/request", "submit_success", data); err != nil {
		a.serverError(w, r, err)
	}
}

// renderFormErrors re-renders the form with the validation messages, as an
// htmx fragment replacing the form.
func (a *App) renderFormErrors(w http.ResponseWriter, r *http.Request, u *store.User, salerLogin string, client external.Client, sel requests.Selection, verr error) {
	v, err := a.buildRequestForm(r, u, salerLogin, client)
	if err != nil {
		a.externalError(w, r, err)
		return
	}
	v.Submitted = sel

	var ve *requests.ValidationError
	if errors.As(verr, &ve) {
		v.Errors = ve.Messages
	} else {
		v.Errors = []string{verr.Error()}
	}

	if err := a.render.renderPartial(w, http.StatusUnprocessableEntity, "customer/request", "request_form", v); err != nil {
		a.serverError(w, r, err)
	}
}

// parseSelection reads the posted form into a Selection. Every value is
// re-validated afterwards; nothing here is trusted.
func parseSelection(r *http.Request) requests.Selection {
	months, _ := strconv.Atoi(r.PostFormValue("months"))
	return requests.Selection{
		TestPeriod:     checkboxValue(r, "test_period"),
		FastCalculator: checkboxValue(r, "module_fast_calculator"),
		HaynesPro:      checkboxValue(r, "module_haynespro"),
		HaynesProTier:  modules.Tier(strings.TrimSpace(r.PostFormValue("haynespro_tier"))),
		StartDate:      strings.TrimSpace(r.PostFormValue("start_date")),
		Months:         months,
		Usernames:      r.PostForm["usernames"],
	}
}

// checkboxValue reads a checkbox that is mirrored into a hidden input.
//
// A disabled checkbox is not submitted at all, so the form mirrors its state
// into a hidden field; either spelling counts as checked.
func checkboxValue(r *http.Request, name string) bool {
	for _, v := range r.PostForm[name] {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "on", "yes":
			return true
		}
	}
	return false
}

type passwordView struct {
	view
	Error  string
	Forced bool
}

func (a *App) handleUserPasswordForm(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	v := passwordView{view: a.newView(r, "Смяна на парола", "password"), Forced: u.MustChangePassword}
	if err := a.render.render(w, http.StatusOK, LayoutCustomer, "customer/password", v); err != nil {
		a.serverError(w, r, err)
	}
}

func (a *App) handleUserPasswordChange(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)

	current := r.PostFormValue("current_password")
	next := r.PostFormValue("new_password")
	confirm := r.PostFormValue("confirm_password")

	renderErr := func(msg string) {
		v := passwordView{
			view:   a.newView(r, "Смяна на парола", "password"),
			Error:  msg,
			Forced: u.MustChangePassword,
		}
		if err := a.render.render(w, http.StatusUnprocessableEntity, LayoutCustomer, "customer/password", v); err != nil {
			a.serverError(w, r, err)
		}
	}

	if !auth.CheckPassword(u.PasswordHash, current) {
		renderErr("Текущата парола е грешна.")
		return
	}
	if next != confirm {
		renderErr("Двете нови пароли не съвпадат.")
		return
	}
	hash, err := auth.HashPassword(next)
	if err != nil {
		renderErr(err.Error())
		return
	}
	if err := a.DB.SetUserPassword(ctx, u.ID, hash, false); err != nil {
		a.serverError(w, r, err)
		return
	}

	a.Audit.Record(ctx, audit.UserActor(u.ID, u.Email), audit.PasswordChanged, u.Email, a.clientIP(r), nil)
	redirectWithFlash(w, r, "/", "password_changed")
}
