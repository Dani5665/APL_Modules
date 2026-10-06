// Package requests holds the business rules of an activation request:
// authorization by store, form validation, the once-per-client test period,
// and the approval that turns a request into activations.
package requests

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"haynesproform/internal/dates"
	"haynesproform/internal/modules"
	"haynesproform/internal/store"
)

// Selection is the module part of a submitted form, before the test-period
// rules are applied.
type Selection struct {
	TestPeriod     bool
	FastCalculator bool
	HaynesPro      bool
	HaynesProTier  modules.Tier
	StartDate      string
	Months         int
	Usernames      []string
}

// ValidationError is a list of Bulgarian messages to show beside the form.
type ValidationError struct {
	Messages []string
}

func (e *ValidationError) Error() string { return strings.Join(e.Messages, "; ") }

// Add appends a message.
func (e *ValidationError) Add(format string, a ...any) {
	e.Messages = append(e.Messages, fmt.Sprintf(format, a...))
}

// HasErrors reports whether anything was rejected.
func (e *ValidationError) HasErrors() bool { return len(e.Messages) > 0 }

// OrNil returns the error, or nil when nothing was rejected.
func (e *ValidationError) OrNil() error {
	if e.HasErrors() {
		return e
	}
	return nil
}

// TestPeriodDefaults are the values a test period forces, regardless of what
// the browser posted. Disabled inputs are not submitted at all, so these are
// applied server-side rather than trusted from the request.
const (
	TestPeriodMonths = 1
	TestPeriodTier   = modules.TierBusiness
)

// ApplyTestPeriod forces the fixed test-period selection.
//
// The form disables these inputs in the browser for clarity, but the server
// is the only place the rule is actually enforced.
func ApplyTestPeriod(s Selection) Selection {
	if !s.TestPeriod {
		return s
	}
	s.FastCalculator = true
	s.HaynesPro = true
	s.HaynesProTier = TestPeriodTier
	s.Months = TestPeriodMonths
	return s
}

// Modules converts a selection into the module rows to store, after the
// test-period rules have been applied.
func (s Selection) Modules() []store.RequestModule {
	var out []store.RequestModule
	if s.FastCalculator {
		out = append(out, store.RequestModule{Module: modules.FastCalculator})
	}
	if s.HaynesPro {
		out = append(out, store.RequestModule{Module: modules.HaynesPro, Tier: s.HaynesProTier})
	}
	return out
}

// ValidateInput checks a selection that has already been passed through
// ApplyTestPeriod. availableUsernames is the client's current username list
// from the external directory; testPeriodUsed says whether this client has
// already consumed its single test period.
//
// today is passed in rather than read from the clock so the caller decides
// what "today" means and the rule stays testable.
func ValidateInput(s Selection, availableUsernames []string, testPeriodUsed bool, today string) ([]string, error) {
	v := &ValidationError{}

	usernames := normalizeUsernames(s.Usernames, availableUsernames, v)

	if s.TestPeriod && testPeriodUsed {
		v.Add("Тестовият период вече е използван за този клиент.")
	}

	if !s.FastCalculator && !s.HaynesPro {
		v.Add("Изберете поне един модул.")
	}
	if s.HaynesPro && !modules.ValidTier(modules.HaynesPro, s.HaynesProTier) {
		v.Add("Изберете ниво за HaynesPro.")
	}
	if s.Months < 0 || s.Months > 12 { // 0 = no end date
		v.Add("Изберете период на активация.")
	}

	switch {
	case s.StartDate == "":
		v.Add("Изберете дата на активация.")
	default:
		if _, err := dates.ParseISO(s.StartDate); err != nil {
			v.Add("Невалидна дата на активация.")
		} else if s.StartDate < today {
			v.Add("Датата на активация не може да е в миналото.")
		}
	}

	return usernames, v.OrNil()
}

// normalizeUsernames keeps only usernames the client actually has, so a
// tampered form cannot grant modules to an arbitrary login.
func normalizeUsernames(selected, available []string, v *ValidationError) []string {
	allowed := make(map[string]string, len(available))
	for _, a := range available {
		allowed[strings.ToLower(strings.TrimSpace(a))] = a
	}

	seen := map[string]bool{}
	var out []string
	var unknown []string
	for _, u := range selected {
		key := strings.ToLower(strings.TrimSpace(u))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		canonical, ok := allowed[key]
		if !ok {
			unknown = append(unknown, strings.TrimSpace(u))
			continue
		}
		out = append(out, canonical)
	}

	if len(unknown) > 0 {
		v.Add("Избрани са потребители, които не принадлежат на този клиент.")
	}
	if len(out) == 0 && len(unknown) == 0 {
		v.Add("Изберете поне един потребител.")
	}
	sort.Strings(out)
	return out
}

// Authorization errors. They are deliberately indistinguishable to the user:
// every failure renders the same page and reveals no client data.
var (
	// ErrNoAccess means the check failed for a reason the user must not learn.
	ErrNoAccess = errors.New("requests: not authorized for this client")
)

// CheckStoreAccess is the authorization rule for opening a client: the
// client's store must be one of the logged-in account's stores. The caller
// has already fetched the client.
func CheckStoreAccess(user *store.User, clientStore string) error {
	if user == nil || !user.Active {
		return fmt.Errorf("%w: account is not active", ErrNoAccess)
	}
	if len(user.Stores) == 0 {
		return fmt.Errorf("%w: account has no stores assigned", ErrNoAccess)
	}
	if !user.HasStoreValue(clientStore) {
		return fmt.Errorf("%w: client store %q is not one of the account's stores", ErrNoAccess, clientStore)
	}
	return nil
}
