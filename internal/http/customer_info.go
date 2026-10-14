package http

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"haynesproform/internal/external"
)

// infoPageSize is how many customers each load of the information list adds;
// the list scrolls infinitely, fetching the next batch as the end comes into view.
const infoPageSize = 25

type infoView struct {
	view
	Customers   []external.Customer
	Page        pagination
	Search      string
	LoadURL     string
	Unavailable bool
}

// handleInfo shows the customer information page. As on the activation page,
// the list is fetched by htmx after the page has rendered.
func (a *App) handleInfo(w http.ResponseWriter, r *http.Request) {
	v := infoView{view: a.newView(r, "Информация клиенти", "info")}
	v.Search = strings.TrimSpace(r.URL.Query().Get("q"))

	q := url.Values{}
	if v.Search != "" {
		q.Set("q", v.Search)
	}
	v.LoadURL = "/info/search"
	if len(q) > 0 {
		v.LoadURL += "?" + q.Encode()
	}

	if err := a.render.render(w, http.StatusOK, LayoutCustomer, "customer/info", v); err != nil {
		a.serverError(w, r, err)
	}
}

// handleInfoSearch serves the htmx fragment with the matching customers.
func (a *App) handleInfoSearch(w http.ResponseWriter, r *http.Request) {
	search := strings.TrimSpace(r.URL.Query().Get("q"))

	v := infoView{view: a.newView(r, "Информация клиенти", "info"), Search: search}
	v.Page = pagination{
		Page:     pageParam(r),
		PageSize: infoPageSize,
		Query:    map[string]string{"q": search},
	}

	all, err := a.Directory.ListCustomers(r.Context())
	if err != nil {
		a.Log.Error("customers could not be loaded from the external directory", "error", err)
		v.Unavailable = true
	} else {
		matches := filterCustomers(all, search)
		v.Page.Total = len(matches)
		from := min(v.Page.Offset(), len(matches))
		to := min(from+v.Page.PageSize, len(matches))
		v.Customers = matches[from:to]
	}

	// Later batches are only the extra rows; the first is the whole list.
	fragment := "info_results"
	if v.Page.Page > 1 {
		fragment = "info_more"
	}
	if err := a.render.renderPartial(w, http.StatusOK, "customer/info", fragment, v); err != nil {
		a.serverError(w, r, err)
	}
}

// filterCustomers keeps the customers whose code, name or serving store
// contains every word of the search (Unicode-aware, so Cyrillic is
// case-folded).
func filterCustomers(all []external.Customer, search string) []external.Customer {
	words := strings.Fields(strings.ToLower(search))
	if len(words) == 0 {
		return all
	}
	var out []external.Customer
	for _, c := range all {
		hay := strings.ToLower(c.Code + " " + c.Name + " " + c.Mandant)
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

// profileRow is one catalog profile as shown on the customer page.
type profileRow struct {
	Login     string
	Password  string
	Mandant   string
	LastLogin string
	Active    bool
}

type infoDetailView struct {
	view
	Customer external.Customer
	Profiles []profileRow
	// ProfilesUnavailable is set when the profiles could not be read, so the
	// customer's own information still shows.
	ProfilesUnavailable bool
	// Form drives the "Заявка за профил в NEXT" popup.
	Form profileFormView
}

// handleInfoDetail shows everything known about one customer, together with
// the profiles it has in the catalog.
func (a *App) handleInfoDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	customer, err := a.Directory.GetCustomer(ctx, r.PathValue("code"))
	switch {
	case errors.Is(err, external.ErrNotFound):
		a.notFound(w, r)
		return
	case err != nil:
		a.externalError(w, r, err)
		return
	}

	v := infoDetailView{view: a.newView(r, customer.Name, "info"), Customer: *customer}

	profiles, err := a.Directory.ListProfiles(ctx, customer.Code)
	if err != nil {
		a.Log.Error("profiles could not be loaded from the external directory",
			"client_code", customer.Code, "error", err)
		v.ProfilesUnavailable = true
	}
	for _, p := range profiles {
		row := profileRow{Login: p.Login, Password: p.Password, Mandant: p.Mandant, Active: p.Active}
		// LAST_LOGIN is a wall-clock value without a zone; it is shown as is.
		if !p.LastLogin.IsZero() {
			row.LastLogin = p.LastLogin.Format("02.01.2006 15:04")
		}
		v.Profiles = append(v.Profiles, row)
	}

	form, err := a.buildProfileForm(r, *customer)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	v.Form = form

	if err := a.render.render(w, http.StatusOK, LayoutCustomer, "customer/info_detail", v); err != nil {
		a.serverError(w, r, err)
	}
}
