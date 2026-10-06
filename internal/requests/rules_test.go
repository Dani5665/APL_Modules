package requests

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"haynesproform/internal/modules"
	"haynesproform/internal/store"
)

func TestApplyTestPeriodForcesSelection(t *testing.T) {
	// What a tampered form might post: test period on, everything else off or
	// contradictory. Disabled inputs are not submitted, so this is the shape
	// the server actually receives.
	got := ApplyTestPeriod(Selection{
		TestPeriod:     true,
		FastCalculator: false,
		HaynesPro:      false,
		HaynesProTier:  modules.TierUltra,
		Months:         12,
	})

	if !got.FastCalculator {
		t.Error("test period did not force Fast Calculator on")
	}
	if !got.HaynesPro {
		t.Error("test period did not force HaynesPro on")
	}
	if got.HaynesProTier != modules.TierBusiness {
		t.Errorf("tier = %q, want %q", got.HaynesProTier, modules.TierBusiness)
	}
	if got.Months != 1 {
		t.Errorf("months = %d, want 1", got.Months)
	}
}

func TestApplyTestPeriodLeavesNormalSelectionAlone(t *testing.T) {
	in := Selection{
		TestPeriod:    false,
		HaynesPro:     true,
		HaynesProTier: modules.TierUltra,
		Months:        6,
	}
	got := ApplyTestPeriod(in)
	if !reflect.DeepEqual(got, in) {
		t.Errorf("ApplyTestPeriod changed a non-test-period selection: %+v -> %+v", in, got)
	}
}

func TestSelectionModules(t *testing.T) {
	mods := ApplyTestPeriod(Selection{TestPeriod: true}).Modules()
	if len(mods) != 2 {
		t.Fatalf("got %d modules, want 2", len(mods))
	}
	if mods[0].Module != modules.FastCalculator || mods[0].Tier != "" {
		t.Errorf("first module = %+v, want Fast Calculator with no tier", mods[0])
	}
	if mods[1].Module != modules.HaynesPro || mods[1].Tier != modules.TierBusiness {
		t.Errorf("second module = %+v, want HaynesPro Business", mods[1])
	}
}

const today = "2026-09-17"

var available = []string{"office_100000001", "servis_100000001", "sklad_100000001"}

func validSelection() Selection {
	return Selection{
		FastCalculator: true,
		StartDate:      today,
		Months:         3,
		Usernames:      []string{"office_100000001"},
	}
}

func TestValidateInputAcceptsAValidForm(t *testing.T) {
	users, err := ValidateInput(validSelection(), available, false, today)
	if err != nil {
		t.Fatalf("ValidateInput: %v", err)
	}
	if len(users) != 1 || users[0] != "office_100000001" {
		t.Errorf("usernames = %v, want [office_100000001]", users)
	}
}

func TestValidateInputRejections(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(*Selection)
		used        bool
		wantMessage string
	}{
		{
			name:        "no modules",
			mutate:      func(s *Selection) { s.FastCalculator = false },
			wantMessage: "поне един модул",
		},
		{
			name:        "no usernames",
			mutate:      func(s *Selection) { s.Usernames = nil },
			wantMessage: "поне един потребител",
		},
		{
			name:        "username not belonging to the client",
			mutate:      func(s *Selection) { s.Usernames = []string{"office_999999999"} },
			wantMessage: "не принадлежат на този клиент",
		},
		{
			name:        "past start date",
			mutate:      func(s *Selection) { s.StartDate = "2026-09-16" },
			wantMessage: "в миналото",
		},
		{
			name:        "malformed start date",
			mutate:      func(s *Selection) { s.StartDate = "17.09.2026" },
			wantMessage: "Невалидна дата",
		},
		{
			name:        "missing start date",
			mutate:      func(s *Selection) { s.StartDate = "" },
			wantMessage: "Изберете дата",
		},
		{
			name:        "months out of range",
			mutate:      func(s *Selection) { s.Months = 13 },
			wantMessage: "период на активация",
		},
		{
			name:        "negative months",
			mutate:      func(s *Selection) { s.Months = -1 },
			wantMessage: "период на активация",
		},
		{
			name:        "HaynesPro without a tier",
			mutate:      func(s *Selection) { s.HaynesPro = true },
			wantMessage: "ниво за HaynesPro",
		},
		{
			name:        "HaynesPro with an unknown tier",
			mutate:      func(s *Selection) { s.HaynesPro = true; s.HaynesProTier = "PLATINUM" },
			wantMessage: "ниво за HaynesPro",
		},
		{
			name:        "test period already used",
			mutate:      func(s *Selection) { s.TestPeriod = true },
			used:        true,
			wantMessage: "вече е използван",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := validSelection()
			tt.mutate(&s)
			s = ApplyTestPeriod(s)

			_, err := ValidateInput(s, available, tt.used, today)
			if err == nil {
				t.Fatalf("ValidateInput accepted an invalid form")
			}
			if !strings.Contains(err.Error(), tt.wantMessage) {
				t.Errorf("error = %q, want it to mention %q", err.Error(), tt.wantMessage)
			}
		})
	}
}

func TestValidateInputAllowsTestPeriodWhenUnused(t *testing.T) {
	s := ApplyTestPeriod(Selection{
		TestPeriod: true,
		StartDate:  today,
		Usernames:  []string{"office_100000001"},
	})
	if _, err := ValidateInput(s, available, false, today); err != nil {
		t.Fatalf("ValidateInput rejected an unused test period: %v", err)
	}
}

func TestValidateInputDeduplicatesAndCanonicalisesUsernames(t *testing.T) {
	s := validSelection()
	s.Usernames = []string{"OFFICE_100000001", " office_100000001 ", "servis_100000001"}

	users, err := ValidateInput(s, available, false, today)
	if err != nil {
		t.Fatalf("ValidateInput: %v", err)
	}
	want := []string{"office_100000001", "servis_100000001"}
	if len(users) != len(want) {
		t.Fatalf("usernames = %v, want %v", users, want)
	}
	for i := range want {
		if users[i] != want[i] {
			t.Errorf("usernames[%d] = %q, want %q", i, users[i], want[i])
		}
	}
}

func TestCheckStoreAccess(t *testing.T) {
	user := &store.User{
		Active: true,
		Stores: []store.Store{
			{Name: "Магазин София", ExternalValue: "SOFIA"},
			{Name: "Магазин Варна", ExternalValue: "VARNA"},
		},
	}

	if err := CheckStoreAccess(user, "SOFIA"); err != nil {
		t.Errorf("access to an own store was refused: %v", err)
	}
	// Comparison ignores case and surrounding space, because external values
	// are maintained by hand.
	if err := CheckStoreAccess(user, " varna "); err != nil {
		t.Errorf("case-insensitive store match was refused: %v", err)
	}

	tests := []struct {
		name        string
		user        *store.User
		clientStore string
	}{
		{"client of another store", user, "PLOVDIV"},
		{"empty client store", user, ""},
		{"inactive account", &store.User{Active: false, Stores: user.Stores}, "SOFIA"},
		{"account with no stores", &store.User{Active: true}, "SOFIA"},
		{"no account", nil, "SOFIA"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckStoreAccess(tt.user, tt.clientStore)
			if !errors.Is(err, ErrNoAccess) {
				t.Errorf("CheckStoreAccess = %v, want ErrNoAccess", err)
			}
		})
	}
}

func TestValidateInputAcceptsNoEndDate(t *testing.T) {
	s := Selection{FastCalculator: true, StartDate: "2026-10-10", Months: 0, Usernames: []string{"a"}}
	if _, err := ValidateInput(s, []string{"a"}, false, "2026-10-06"); err != nil {
		t.Errorf("months 0 (no end date) was rejected: %v", err)
	}
}
