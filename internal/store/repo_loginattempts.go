package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"haynesproform/internal/dates"
)

// LockState describes whether a login may proceed.
type LockState struct {
	Locked bool
	Until  time.Time
}

// LoginLocked reports whether any of the given (scope, key) pairs is locked
// out for the audience ("user" or "admin").
func (db *DB) LoginLocked(ctx context.Context, audience string, keys map[string]string) (LockState, error) {
	now := time.Now().UTC()
	var worst LockState

	for scope, key := range keys {
		if key == "" {
			continue
		}
		var until sql.NullString
		err := db.QueryRowContext(ctx,
			`SELECT locked_until FROM login_attempts WHERE scope = ? AND key = ? AND audience = ?`,
			scope, key, audience).Scan(&until)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			continue
		case err != nil:
			return LockState{}, classify(err)
		}
		if !until.Valid {
			continue
		}
		t, err := dates.ParseUTC(until.String)
		if err != nil || !t.After(now) {
			continue
		}
		if !worst.Locked || t.After(worst.Until) {
			worst = LockState{Locked: true, Until: t}
		}
	}
	return worst, nil
}

// RegisterLoginFailure counts a failed attempt for each key and locks the key
// out for lockFor once it reaches maxFailures within the same window.
func (db *DB) RegisterLoginFailure(ctx context.Context, audience string, keys map[string]string, maxFailures int, lockFor time.Duration) error {
	now := time.Now().UTC()
	return db.InTx(ctx, func(tx *sql.Tx) error {
		for scope, key := range keys {
			if key == "" {
				continue
			}
			var failures int
			var firstFail sql.NullString
			var lockedUntil sql.NullString
			err := tx.QueryRowContext(ctx,
				`SELECT failures, first_fail_at, locked_until
				 FROM login_attempts WHERE scope = ? AND key = ? AND audience = ?`,
				scope, key, audience).Scan(&failures, &firstFail, &lockedUntil)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return classify(err)
			}

			// A window that has already elapsed starts counting afresh.
			if err == nil && firstFail.Valid {
				if t, perr := dates.ParseUTC(firstFail.String); perr == nil && now.Sub(t) > lockFor {
					failures = 0
				}
			}
			failures++

			var lock sql.NullString
			if failures >= maxFailures {
				lock = nullString(dates.FormatUTC(now.Add(lockFor)))
			}
			first := dates.FormatUTC(now)
			if failures > 1 && firstFail.Valid {
				first = firstFail.String
			}

			if _, err := tx.ExecContext(ctx,
				`INSERT INTO login_attempts (scope, key, audience, failures, first_fail_at, locked_until)
				 VALUES (?, ?, ?, ?, ?, ?)
				 ON CONFLICT(scope, key, audience) DO UPDATE SET
				     failures      = excluded.failures,
				     first_fail_at = excluded.first_fail_at,
				     locked_until  = excluded.locked_until`,
				scope, key, audience, failures, first, lock); err != nil {
				return classify(err)
			}
		}
		return nil
	})
}

// ClearLoginFailures forgets the failures for the given keys after a success.
func (db *DB) ClearLoginFailures(ctx context.Context, audience string, keys map[string]string) error {
	return db.InTx(ctx, func(tx *sql.Tx) error {
		for scope, key := range keys {
			if key == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM login_attempts WHERE scope = ? AND key = ? AND audience = ?`,
				scope, key, audience); err != nil {
				return classify(err)
			}
		}
		return nil
	})
}
