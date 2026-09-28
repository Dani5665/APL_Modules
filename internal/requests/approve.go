package requests

import (
	"context"
	"database/sql"
	"fmt"

	"haynesproform/internal/dates"
	"haynesproform/internal/modules"
	"haynesproform/internal/store"
)

// Service applies decisions to requests.
type Service struct {
	db *store.DB
}

// NewService builds the request service.
func NewService(db *store.DB) *Service { return &Service{db: db} }

// ApprovalResult reports what an approval produced.
type ApprovalResult struct {
	Request       *store.Request
	ActivationIDs []int64
	StartDate     string
	EndDate       string
}

// Overlaps returns the existing activations that would overlap if the request
// were approved as it currently stands. The admin sees these as a warning and
// may still proceed.
func (s *Service) Overlaps(ctx context.Context, r *store.Request) ([]store.Overlap, error) {
	end, err := dates.EndDate(r.StartDate, r.Months)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(r.Modules))
	for _, m := range r.Modules {
		keys = append(keys, string(m.Module))
	}
	return s.db.FindOverlaps(ctx, s.db.DB, r.ClientCode, r.Usernames, keys, r.StartDate, end)
}

// Approve marks a request approved and creates one activation per
// (username x module), all in a single transaction: either the decision and
// every activation land together, or nothing does.
func (s *Service) Approve(ctx context.Context, requestID, adminID int64, comment string) (*ApprovalResult, error) {
	r, err := s.db.RequestByID(ctx, requestID)
	if err != nil {
		return nil, err
	}
	if r.Status != store.StatusPending {
		return nil, fmt.Errorf("заявка %d вече е обработена", requestID)
	}
	if len(r.Usernames) == 0 || len(r.Modules) == 0 {
		return nil, fmt.Errorf("заявка %d няма избрани потребители или модули", requestID)
	}

	end, err := dates.EndDate(r.StartDate, r.Months)
	if err != nil {
		return nil, err
	}

	result := &ApprovalResult{StartDate: r.StartDate, EndDate: end}

	err = s.db.InTx(ctx, func(tx *sql.Tx) error {
		// Re-check the test-period rule inside the transaction: a second
		// pending test-period request must not slip through concurrently.
		if r.TestPeriod {
			used, err := s.testPeriodUsedTx(ctx, tx, r.ClientCode, r.ID)
			if err != nil {
				return err
			}
			if used {
				return fmt.Errorf("тестовият период вече е използван за клиент %s", r.ClientCode)
			}
		}

		if err := s.db.DecideRequest(ctx, tx, r.ID, store.StatusApproved, adminID, comment); err != nil {
			return err
		}

		for _, username := range r.Usernames {
			for _, m := range r.Modules {
				if _, ok := modules.Get(m.Module); !ok {
					return fmt.Errorf("непознат модул %q", m.Module)
				}
				id, err := s.db.InsertActivation(ctx, tx, store.Activation{
					ClientCode:       r.ClientCode,
					ClientName:       r.ClientName,
					ClientObject:     r.ClientObject,
					ClientStore:      r.ClientStore,
					Username:         username,
					Module:           m.Module,
					Tier:             m.Tier,
					StartDate:        r.StartDate,
					EndDate:          end,
					SourceRequestID:  sql.NullInt64{Int64: r.ID, Valid: true},
					CreatedByAdminID: sql.NullInt64{Int64: adminID, Valid: adminID > 0},
				})
				if err != nil {
					return err
				}
				result.ActivationIDs = append(result.ActivationIDs, id)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if result.Request, err = s.db.RequestByID(ctx, requestID); err != nil {
		return nil, err
	}
	return result, nil
}

// Deny marks a request denied. A denied test-period request does not consume
// the client's single test period, which follows from the status filter in
// TestPeriodUsed rather than from anything done here.
func (s *Service) Deny(ctx context.Context, requestID, adminID int64, comment string) (*store.Request, error) {
	r, err := s.db.RequestByID(ctx, requestID)
	if err != nil {
		return nil, err
	}
	if r.Status != store.StatusPending {
		return nil, fmt.Errorf("заявка %d вече е обработена", requestID)
	}
	if err := s.db.DecideRequest(ctx, s.db.DB, requestID, store.StatusDenied, adminID, comment); err != nil {
		return nil, err
	}
	return s.db.RequestByID(ctx, requestID)
}

func (s *Service) testPeriodUsedTx(ctx context.Context, tx *sql.Tx, clientCode string, excludeID int64) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM requests
		 WHERE client_code = ? AND test_period = 1 AND status IN (?, ?) AND id <> ?`,
		clientCode, store.StatusPending, store.StatusApproved, excludeID).Scan(&n)
	return n > 0, err
}
