// Package demo seeds the local database with data for development and for
// the end-to-end demo described in the README.
//
// It runs only against the mock external directory, and only when the
// database has no salespeople yet, so it can never touch a real deployment.
package demo

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"haynesproform/internal/auth"
	"haynesproform/internal/dates"
	"haynesproform/internal/external/mock"
	"haynesproform/internal/modules"
	"haynesproform/internal/store"
)

// DemoPassword is the password of every seeded salesperson account. It is
// printed at startup and is only ever used with the mock directory.
const DemoPassword = "demo-parola-2026"

// storeSeed pairs a display name with the value the mock directory uses.
type storeSeed struct {
	name     string
	external string
}

var storeSeeds = []storeSeed{
	{"Магазин София", mock.StoreSofia},
	{"Магазин Варна", mock.StoreVarna},
	{"Магазин Пловдив", mock.StorePlovdiv},
}

// userSeed is a salesperson and the stores they cover.
type userSeed struct {
	email  string
	stores []string
}

var userSeeds = []userSeed{
	{"ivan.petrov@example.com", []string{mock.StoreSofia}},
	{"maria.dimitrova@example.com", []string{mock.StoreVarna, mock.StorePlovdiv}},
}

// Seed populates stores, salespeople, activations and requests if the
// database is still empty. It reports whether anything was written.
func Seed(ctx context.Context, db *store.DB, log *slog.Logger) (bool, error) {
	users, err := db.ListUsers(ctx, store.UserFilter{})
	if err != nil {
		return false, err
	}
	if len(users) > 0 {
		return false, nil
	}

	storeIDs, err := seedStores(ctx, db)
	if err != nil {
		return false, err
	}
	if err := seedUsers(ctx, db, storeIDs); err != nil {
		return false, err
	}
	if err := seedActivations(ctx, db); err != nil {
		return false, err
	}
	if err := seedRequests(ctx, db); err != nil {
		return false, err
	}

	log.Warn("demo data seeded; salespeople can log in with the demo password",
		"password", DemoPassword,
		"users", len(userSeeds))
	return true, nil
}

// seedStores creates the three stores and returns their ids by external value.
func seedStores(ctx context.Context, db *store.DB) (map[string]int64, error) {
	ids := make(map[string]int64, len(storeSeeds))
	for _, s := range storeSeeds {
		id, err := db.CreateStore(ctx, s.name, s.external)
		if err != nil {
			return nil, fmt.Errorf("seed store %s: %w", s.name, err)
		}
		ids[s.external] = id
	}
	return ids, nil
}

func seedUsers(ctx context.Context, db *store.DB, storeIDs map[string]int64) error {
	hash, err := auth.HashPassword(DemoPassword)
	if err != nil {
		return err
	}
	for _, u := range userSeeds {
		ids := make([]int64, 0, len(u.stores))
		for _, ext := range u.stores {
			ids = append(ids, storeIDs[ext])
		}
		// The demo accounts skip the forced password change so the flow can be
		// walked through without an extra step.
		if _, err := db.CreateUser(ctx, u.email, hash, ids, false); err != nil {
			return fmt.Errorf("seed user %s: %w", u.email, err)
		}
	}
	return nil
}

// activationSeed describes one seeded activation relative to today.
type activationSeed struct {
	clientIndex int
	userIndex   int
	module      modules.Key
	tier        modules.Tier
	startOffset int // days from today
	months      int
	revoked     bool
}

var activationSeeds = []activationSeed{
	// Currently active.
	{0, 0, modules.FastCalculator, "", -30, 6, false},
	{0, 0, modules.HaynesPro, modules.TierBusiness, -30, 6, false},
	{0, 1, modules.FastCalculator, "", -10, 3, false},
	{1, 0, modules.HaynesPro, modules.TierUltra, -60, 12, false},
	{8, 0, modules.FastCalculator, "", -5, 1, false},
	{8, 1, modules.HaynesPro, modules.TierPro, -5, 12, false},
	{13, 0, modules.HaynesPro, modules.TierBusiness, -1, 3, false},
	// Expired.
	{2, 0, modules.FastCalculator, "", -400, 6, false},
	{2, 1, modules.HaynesPro, modules.TierBusiness, -400, 3, false},
	{10, 0, modules.FastCalculator, "", -200, 1, false},
	// Revoked while still inside its period.
	{14, 0, modules.HaynesPro, modules.TierPro, -20, 12, true},
}

func seedActivations(ctx context.Context, db *store.DB) error {
	dir := mock.New()
	clients := dir.Clients()
	today := dates.Now()

	for _, s := range activationSeeds {
		if s.clientIndex >= len(clients) {
			continue
		}
		c := clients[s.clientIndex]

		usernames, err := dir.ListClientLogins(ctx, c.Code)
		if err != nil {
			return err
		}
		if s.userIndex >= len(usernames) {
			continue
		}

		start := today.AddDate(0, 0, s.startOffset).Format(dates.ISO)
		end, err := dates.EndDate(start, s.months)
		if err != nil {
			return err
		}

		a := store.Activation{
			ClientCode:   c.Code,
			ClientName:   c.Name,
			ClientObject: c.Object,
			ClientStore:  storeNameFor(c.Store),
			Username:     usernames[s.userIndex],
			Module:       s.module,
			Tier:         s.tier,
			StartDate:    start,
			EndDate:      end,
		}
		if s.revoked {
			a.RevokedAt = sql.NullString{String: dates.NowUTC(), Valid: true}
		}

		id, err := db.InsertActivation(ctx, db.DB, a)
		if err != nil {
			return fmt.Errorf("seed activation for %s: %w", c.Code, err)
		}
		if s.revoked {
			if err := db.RevokeActivation(ctx, id); err != nil {
				return err
			}
		}
	}
	return nil
}

// requestSeed describes one seeded request.
type requestSeed struct {
	clientIndex int
	userCount   int
	submitter   string
	testPeriod  bool
	months      int
	status      store.RequestStatus
	mods        []store.RequestModule
}

var requestSeeds = []requestSeed{
	{
		clientIndex: 3, userCount: 1, submitter: userSeeds[0].email,
		months: 3, status: store.StatusPending,
		mods: []store.RequestModule{{Module: modules.FastCalculator}},
	},
	{
		clientIndex: 4, userCount: 2, submitter: userSeeds[0].email,
		months: 12, status: store.StatusPending,
		mods: []store.RequestModule{
			{Module: modules.FastCalculator},
			{Module: modules.HaynesPro, Tier: modules.TierUltra},
		},
	},
	{
		// A used test period, so the rule is visible in the demo.
		clientIndex: 5, userCount: 1, submitter: userSeeds[0].email,
		testPeriod: true, months: 1, status: store.StatusApproved,
		mods: []store.RequestModule{
			{Module: modules.FastCalculator},
			{Module: modules.HaynesPro, Tier: modules.TierBusiness},
		},
	},
	{
		clientIndex: 9, userCount: 1, submitter: userSeeds[1].email,
		months: 6, status: store.StatusApproved,
		mods: []store.RequestModule{{Module: modules.HaynesPro, Tier: modules.TierPro}},
	},
	{
		clientIndex: 11, userCount: 1, submitter: userSeeds[1].email,
		months: 2, status: store.StatusDenied,
		mods: []store.RequestModule{{Module: modules.FastCalculator}},
	},
	{
		// A denied test period does not consume the client's single one.
		clientIndex: 15, userCount: 1, submitter: userSeeds[1].email,
		testPeriod: true, months: 1, status: store.StatusDenied,
		mods: []store.RequestModule{
			{Module: modules.FastCalculator},
			{Module: modules.HaynesPro, Tier: modules.TierBusiness},
		},
	},
}

func seedRequests(ctx context.Context, db *store.DB) error {
	dir := mock.New()
	clients := dir.Clients()
	today := dates.Now()

	for i, s := range requestSeeds {
		if s.clientIndex >= len(clients) {
			continue
		}
		c := clients[s.clientIndex]

		usernames, err := dir.ListClientLogins(ctx, c.Code)
		if err != nil {
			return err
		}
		if len(usernames) == 0 {
			continue
		}
		if s.userCount > len(usernames) {
			s.userCount = len(usernames)
		}

		user, err := db.UserByEmail(ctx, s.submitter)
		if err != nil {
			return fmt.Errorf("seed request %d: %w", i, err)
		}

		id, err := db.CreateRequest(ctx, store.NewRequest{
			ClientCode:      c.Code,
			ClientName:      c.Name,
			ClientObject:    c.Object,
			ClientStore:     storeNameFor(c.Store),
			SubmitterUserID: user.ID,
			SubmitterEmail:  user.Email,
			SalerLogin:      salerLoginFor(c.Store),
			TestPeriod:      s.testPeriod,
			StartDate:       today.AddDate(0, 0, 1).Format(dates.ISO),
			Months:          s.months,
			Usernames:       usernames[:s.userCount],
			Modules:         s.mods,
		})
		if err != nil {
			return fmt.Errorf("seed request %d: %w", i, err)
		}

		// Decided requests are moved out of "pending" directly, so the seed
		// does not depend on an administrator existing yet.
		if s.status != store.StatusPending {
			if err := markDecided(ctx, db, id, s.status); err != nil {
				return err
			}
		}
	}
	return nil
}

func markDecided(ctx context.Context, db *store.DB, id int64, status store.RequestStatus) error {
	comment := "Одобрено при първоначалното зареждане на примерните данни."
	if status == store.StatusDenied {
		comment = "Отказано при първоначалното зареждане на примерните данни."
	}
	_, err := db.ExecContext(ctx,
		`UPDATE requests SET status = ?, admin_comment = ?, decided_at = ?, updated_at = ?
		 WHERE id = ?`,
		status, comment, dates.NowUTC(), dates.NowUTC(), id)
	return err
}

// storeNameFor maps an external store value to the local display name.
func storeNameFor(external string) string {
	for _, s := range storeSeeds {
		if s.external == external {
			return s.name
		}
	}
	return external
}

// salerLoginFor returns the mock salesperson covering a store.
func salerLoginFor(externalStore string) string {
	if externalStore == mock.StoreSofia {
		return "ivan.petrov"
	}
	return "maria.dimitrova"
}
