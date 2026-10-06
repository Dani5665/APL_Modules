package http

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"haynesproform/internal/audit"
	"haynesproform/internal/auth"
	"haynesproform/internal/dates"
	"haynesproform/internal/external"
	"haynesproform/internal/modules"
	"haynesproform/internal/requests"
	"haynesproform/internal/store"
)

type loginView struct {
	view
	Email string
	Error string
}

func (a *App) handleUserLoginForm(w http.ResponseWriter, r *http.Request) {
	if _, err := a.Sessions.Load(r.Context(), r, auth.UserAudience); err == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
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

	a.warmClientCache(user)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// warmClientCache loads the clients of the user's stores from the external
// database in the background, so the client list is already cached by the time
// the first page after login asks for it. A failure is only logged: the page
// that needs the list reports the error itself.
func (a *App) warmClientCache(u *store.User) {
	stores := u.StoreExternalValues()
	if len(stores) == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := a.Directory.ListClientsByStores(ctx, stores); err != nil {
			a.Log.Warn("client list could not be preloaded", "user_id", u.ID, "error", err)
		}
	}()
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
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// clientSearchPageSize is how many clients one page of search results holds.
const clientSearchPageSize = 50

type homeView struct {
	view
	Clients    []external.Client
	ClientPage pagination
	Search     string
	// LoadURL is where the page fetches its results from.
	LoadURL     string
	Unavailable bool
}

// handleHome shows the search over the clients of the user's stores. The list
// itself is fetched by htmx right after the page loads, so the page appears
// at once even while the external database is still answering.
func (a *App) handleHome(w http.ResponseWriter, r *http.Request) {
	v := homeView{view: a.newView(r, "Клиенти", "home")}
	v.Search = strings.TrimSpace(r.URL.Query().Get("q"))

	// The page links of the results point back here, so the first load of the
	// results must honour the same filter and page.
	q := url.Values{}
	if v.Search != "" {
		q.Set("q", v.Search)
	}
	if p := pageParam(r); p > 1 {
		q.Set("page", strconv.Itoa(p))
	}
	v.LoadURL = "/clients/search"
	if len(q) > 0 {
		v.LoadURL += "?" + q.Encode()
	}

	if err := a.render.render(w, http.StatusOK, LayoutCustomer, "customer/home", v); err != nil {
		a.serverError(w, r, err)
	}
}

// handleClientSearch serves the htmx fragment with the matching clients.
func (a *App) handleClientSearch(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	search := strings.TrimSpace(r.URL.Query().Get("q"))

	v := homeView{view: a.newView(r, "Клиенти", "home"), Search: search}
	v.ClientPage = pagination{
		Page:     pageParam(r),
		PageSize: clientSearchPageSize,
		Query:    map[string]string{"q": search},
	}

	all, err := a.Directory.ListClientsByStores(r.Context(), u.StoreExternalValues())
	if err != nil {
		a.Log.Error("clients could not be loaded from the external directory", "error", err)
		v.Unavailable = true
	} else {
		matches := filterClients(all, search)
		v.ClientPage.Total = len(matches)
		from := min(v.ClientPage.Offset(), len(matches))
		to := min(from+v.ClientPage.PageSize, len(matches))
		v.Clients = matches[from:to]
	}

	if err := a.render.renderPartial(w, http.StatusOK, "customer/home", "client_results", v); err != nil {
		a.serverError(w, r, err)
	}
}

// filterClients keeps the clients whose code, name or object contains every
// word of the search. Matching is Unicode-aware, so Cyrillic is case-folded.
func filterClients(all []external.Client, search string) []external.Client {
	words := strings.Fields(strings.ToLower(search))
	if len(words) == 0 {
		return all
	}
	var out []external.Client
	for _, c := range all {
		hay := strings.ToLower(c.Code + " " + c.Name + " " + c.Object)
		match := true
		for _, w := range words {
			if !strings.Contains(hay, w) {
				match = false
				break
			}
		}
		if match {
			out = append(out, c)
		}
	}
	return out
}

// modulesPageSize is how many clients one page of the modules list holds.
const modulesPageSize = 50

type modulesView struct {
	view
	Clients    []store.ClientSummary
	ClientPage pagination
	Search     string
	// Unavailable is set when the list could not be loaded, so the page still
	// renders with an inline notice.
	Unavailable bool
}

// handleModules shows every client of the user's stores that has an active
// module, with those modules, in a searchable table.
func (a *App) handleModules(w http.ResponseWriter, r *http.Request) {
	v := modulesView{view: a.newView(r, "Активни модули", "modules")}
	v.Clients, v.ClientPage, v.Search, v.Unavailable = a.loadModulesPage(r)
	if err := a.render.render(w, http.StatusOK, LayoutCustomer, "customer/modules", v); err != nil {
		a.serverError(w, r, err)
	}
}

// handleModulesList serves the htmx fragment behind the search box.
func (a *App) handleModulesList(w http.ResponseWriter, r *http.Request) {
	v := modulesView{view: a.newView(r, "Активни модули", "modules")}
	v.Clients, v.ClientPage, v.Search, v.Unavailable = a.loadModulesPage(r)
	if err := a.render.renderPartial(w, http.StatusOK, "customer/modules", "client_list", v); err != nil {
		a.serverError(w, r, err)
	}
}

// loadModulesPage returns the filtered, paginated list of clients belonging to
// the account's stores that currently have at least one active module
// activation. This is sourced from the local database: a client the external
// directory knows about but that has nothing activated here does not appear.
func (a *App) loadModulesPage(r *http.Request) (clients []store.ClientSummary, p pagination, search string, unavailable bool) {
	u := userFrom(r.Context())
	search = strings.TrimSpace(r.URL.Query().Get("q"))
	p = pagination{
		Page:     pageParam(r),
		PageSize: modulesPageSize,
		Query:    map[string]string{"q": search},
	}

	clients, total, err := a.DB.ListActiveClientsByStores(r.Context(), u.StoreExternalValues(), search,
		p.PageSize, p.Offset())
	if err != nil {
		a.Log.Error("modules list could not be loaded", "error", err)
		return nil, p, search, true
	}
	p.Total = total
	return clients, p, search, false
}

// clientView drives the client page: the client's information and the
// request form.
type clientView struct {
	view
	Client          external.Client
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
	Submitted requests.Selection
}

// handleClient shows one client: its users with their modules, the pending
// requests, and the form to ask for an activation.
func (a *App) handleClient(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())

	client, err := a.authorizeClient(w, r, u, r.PathValue("code"))
	if err != nil {
		return // authorizeClient has already written the response
	}

	v, err := a.buildClientView(r, client)
	if err != nil {
		a.externalError(w, r, err)
		return
	}
	v.Submitted = requests.Selection{StartDate: dates.Today(), Months: 1}

	if err := a.render.render(w, http.StatusOK, LayoutCustomer, "customer/client", v); err != nil {
		a.serverError(w, r, err)
	}
}

// authorizeClient checks that the client exists and belongs to one of the
// account's stores and, on failure, writes the error page and an audit entry.
// A nil error means the caller may proceed.
func (a *App) authorizeClient(w http.ResponseWriter, r *http.Request, u *store.User, code string) (external.Client, error) {
	ctx := r.Context()

	deny := func(reason string) (external.Client, error) {
		a.Audit.Record(ctx, audit.UserActor(u.ID, u.Email), audit.EntryDenied, code, a.clientIP(r),
			map[string]any{"reason": reason})
		a.renderError(w, r, http.StatusForbidden, "Нямате достъп",
			"Нямате достъп до този клиент.")
		return external.Client{}, errors.New(reason)
	}

	client, err := a.Directory.GetClientByCode(ctx, strings.TrimSpace(code))
	switch {
	case errors.Is(err, external.ErrNotFound):
		return deny("client not found in the external directory")
	case err != nil:
		a.externalError(w, r, err)
		return external.Client{}, err
	}

	if err := requests.CheckStoreAccess(u, client.Store); err != nil {
		return deny(err.Error())
	}
	return *client, nil
}

// buildClientView gathers everything the client page shows.
func (a *App) buildClientView(r *http.Request, client external.Client) (clientView, error) {
	ctx := r.Context()

	v := clientView{
		view:      a.newView(r, client.Name, "home"),
		Client:    client,
		Modules:   modules.All,
		Durations: store.DurationOptions(),
		Today:     dates.Today(),
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
	return v, nil
}

// handleRequestSubmit validates and stores a submission, then queues the
// notification email.
func (a *App) handleRequestSubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)

	// Authorization is re-checked on submit, not only when the page was shown.
	client, err := a.authorizeClient(w, r, u, r.PathValue("code"))
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
		a.renderFormErrors(w, r, client, sel, verr)
		return
	}

	id, err := a.DB.CreateRequest(ctx, store.NewRequest{
		ClientCode:      client.Code,
		ClientName:      client.Name,
		ClientObject:    client.Object,
		ClientStore:     client.Store,
		SubmitterUserID: u.ID,
		SubmitterEmail:  u.Email,
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

	a.Audit.Record(ctx, audit.UserActor(u.ID, u.Email), audit.RequestSubmitted,
		strconv.FormatInt(id, 10), a.clientIP(r), map[string]any{
			"client_code": client.Code,
			"usernames":   selected,
			"test_period": sel.TestPeriod,
			"months":      sel.Months,
			"start_date":  sel.StartDate,
		})

	a.queueRequestCreated(ctx, id)

	data := map[string]any{"RequestID": id, "ClientName": client.Name, "ClientCode": client.Code}
	if err := a.render.renderPartial(w, http.StatusOK, "customer/client", "submit_success", data); err != nil {
		a.serverError(w, r, err)
	}
}

// renderFormErrors re-renders the form with the validation messages, as an
// htmx fragment replacing the form.
func (a *App) renderFormErrors(w http.ResponseWriter, r *http.Request, client external.Client, sel requests.Selection, verr error) {
	v, err := a.buildClientView(r, client)
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

	if err := a.render.renderPartial(w, http.StatusUnprocessableEntity, "customer/client", "request_form", v); err != nil {
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
