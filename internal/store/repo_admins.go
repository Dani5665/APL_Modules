package store

import (
	"context"
	"database/sql"
	"fmt"

	"haynesproform/internal/dates"
)

const adminColumns = `id, email, password_hash, totp_secret_enc, totp_enabled,
	last_totp_step, must_change_password, active, created_at, updated_at`

func scanAdmin(s interface{ Scan(...any) error }) (*Admin, error) {
	var a Admin
	err := s.Scan(&a.ID, &a.Email, &a.PasswordHash, &a.TOTPSecretEnc, &a.TOTPEnabled,
		&a.LastTOTPStep, &a.MustChangePassword, &a.Active, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, classify(err)
	}
	return &a, nil
}

// AdminByID loads one admin.
func (db *DB) AdminByID(ctx context.Context, id int64) (*Admin, error) {
	return scanAdmin(db.QueryRowContext(ctx,
		`SELECT `+adminColumns+` FROM admins WHERE id = ?`, id))
}

// AdminByEmail loads one admin by email.
func (db *DB) AdminByEmail(ctx context.Context, email string) (*Admin, error) {
	return scanAdmin(db.QueryRowContext(ctx,
		`SELECT `+adminColumns+` FROM admins WHERE email = ?`, email))
}

// ListAdmins returns every admin, ordered by email.
func (db *DB) ListAdmins(ctx context.Context) ([]Admin, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+adminColumns+` FROM admins ORDER BY email`)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()

	var out []Admin
	for rows.Next() {
		a, err := scanAdmin(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, classify(rows.Err())
}

// CountActiveAdmins returns how many admins can still log in.
func (db *DB) CountActiveAdmins(ctx context.Context) (int, error) {
	return db.countActiveAdmins(ctx, db.DB)
}

func (db *DB) countActiveAdmins(ctx context.Context, q Querier) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM admins WHERE active = 1`).Scan(&n)
	return n, classify(err)
}

// CreateAdmin inserts a new admin and returns its id. mustChangePassword
// forces the change-password redirect (in requireAdmin) before the admin can
// use anything else, in addition to the always-mandatory 2FA enrollment.
func (db *DB) CreateAdmin(ctx context.Context, email, passwordHash string, mustChangePassword bool) (int64, error) {
	now := dates.NowUTC()
	res, err := db.ExecContext(ctx,
		`INSERT INTO admins (email, password_hash, must_change_password, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)`,
		email, passwordHash, mustChangePassword, now, now)
	if err != nil {
		return 0, classify(err)
	}
	return res.LastInsertId()
}

// SetAdminPassword replaces an admin's password hash.
func (db *DB) SetAdminPassword(ctx context.Context, id int64, hash string, mustChange bool) error {
	_, err := db.ExecContext(ctx,
		`UPDATE admins SET password_hash = ?, must_change_password = ?, updated_at = ? WHERE id = ?`,
		hash, mustChange, dates.NowUTC(), id)
	return classify(err)
}

// EnrollAdminTOTP stores a confirmed TOTP secret and enables 2FA.
func (db *DB) EnrollAdminTOTP(ctx context.Context, id int64, secretEnc []byte, step int64) error {
	_, err := db.ExecContext(ctx,
		`UPDATE admins SET totp_secret_enc = ?, totp_enabled = 1, last_totp_step = ?, updated_at = ?
		 WHERE id = ?`,
		secretEnc, step, dates.NowUTC(), id)
	return classify(err)
}

// ResetAdminTOTP clears 2FA and every recovery code, forcing re-enrollment.
func (db *DB) ResetAdminTOTP(ctx context.Context, id int64) error {
	return db.InTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE admins SET totp_secret_enc = NULL, totp_enabled = 0, last_totp_step = 0, updated_at = ?
			 WHERE id = ?`, dates.NowUTC(), id); err != nil {
			return classify(err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM admin_recovery_codes WHERE admin_id = ?`, id); err != nil {
			return classify(err)
		}
		// Any session of that admin must not survive a 2FA reset.
		_, err := tx.ExecContext(ctx,
			`DELETE FROM sessions WHERE subject_type = ? AND subject_id = ?`, SubjectAdmin, id)
		return classify(err)
	})
}

// ClaimTOTPStep records the time step of an accepted code, but only if it is
// newer than the last one used. A false result means the code was replayed.
func (db *DB) ClaimTOTPStep(ctx context.Context, id int64, step int64) (bool, error) {
	res, err := db.ExecContext(ctx,
		`UPDATE admins SET last_totp_step = ?, updated_at = ? WHERE id = ? AND last_totp_step < ?`,
		step, dates.NowUTC(), id, step)
	if err != nil {
		return false, classify(err)
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// DeleteAdmin removes an admin, refusing to remove the last active one.
func (db *DB) DeleteAdmin(ctx context.Context, id int64) error {
	return db.InTx(ctx, func(tx *sql.Tx) error {
		var active bool
		switch err := tx.QueryRowContext(ctx, `SELECT active FROM admins WHERE id = ?`, id).Scan(&active); {
		case err != nil:
			return classify(err)
		}
		if active {
			n, err := db.countActiveAdmins(ctx, tx)
			if err != nil {
				return err
			}
			if n <= 1 {
				return fmt.Errorf("%w: the last active administrator cannot be removed", ErrInUse)
			}
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM sessions WHERE subject_type = ? AND subject_id = ?`, SubjectAdmin, id); err != nil {
			return classify(err)
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM admins WHERE id = ?`, id)
		return classify(err)
	})
}

// ReplaceRecoveryCodes stores a freshly generated set of hashed codes,
// discarding any previous ones.
func (db *DB) ReplaceRecoveryCodes(ctx context.Context, adminID int64, hashes []string) error {
	return db.InTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM admin_recovery_codes WHERE admin_id = ?`, adminID); err != nil {
			return classify(err)
		}
		now := dates.NowUTC()
		for _, h := range hashes {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO admin_recovery_codes (admin_id, code_hash, created_at) VALUES (?, ?, ?)`,
				adminID, h, now); err != nil {
				return classify(err)
			}
		}
		return nil
	})
}

// UseRecoveryCode consumes an unused recovery code matching one of the given
// candidate hashes. It reports whether a code was consumed.
func (db *DB) UseRecoveryCode(ctx context.Context, adminID int64, hash string) (bool, error) {
	res, err := db.ExecContext(ctx,
		`UPDATE admin_recovery_codes SET used_at = ?
		 WHERE admin_id = ? AND code_hash = ? AND used_at IS NULL`,
		dates.NowUTC(), adminID, hash)
	if err != nil {
		return false, classify(err)
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// CountUnusedRecoveryCodes reports how many recovery codes remain.
func (db *DB) CountUnusedRecoveryCodes(ctx context.Context, adminID int64) (int, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM admin_recovery_codes WHERE admin_id = ? AND used_at IS NULL`,
		adminID).Scan(&n)
	return n, classify(err)
}
