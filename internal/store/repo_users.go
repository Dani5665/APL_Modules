package store

import (
	"context"
	"database/sql"
	"strings"

	"haynesproform/internal/dates"
)

const userColumns = `id, email, password_hash, must_change_password, active, created_at, updated_at`

func scanUser(s interface{ Scan(...any) error }) (*User, error) {
	var u User
	err := s.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.MustChangePassword,
		&u.Active, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, classify(err)
	}
	return &u, nil
}

// UserByID loads one salesperson together with their stores.
func (db *DB) UserByID(ctx context.Context, id int64) (*User, error) {
	u, err := scanUser(db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	if u.Stores, err = db.storesForUser(ctx, db.DB, u.ID); err != nil {
		return nil, err
	}
	return u, nil
}

// UserByEmail loads one salesperson by email, together with their stores.
func (db *DB) UserByEmail(ctx context.Context, email string) (*User, error) {
	u, err := scanUser(db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE email = ?`, email))
	if err != nil {
		return nil, err
	}
	if u.Stores, err = db.storesForUser(ctx, db.DB, u.ID); err != nil {
		return nil, err
	}
	return u, nil
}

// UserFilter narrows the salesperson listing.
type UserFilter struct {
	Search  string
	StoreID int64
}

// ListUsers returns salespeople matching the filter, each with their stores.
func (db *DB) ListUsers(ctx context.Context, f UserFilter) ([]User, error) {
	query := `SELECT ` + prefixColumns("u.", userColumns) + ` FROM users u`
	var args []any

	if f.StoreID > 0 {
		query += ` JOIN user_stores us ON us.user_id = u.id AND us.store_id = ?`
		args = append(args, f.StoreID)
	}
	if s := strings.TrimSpace(f.Search); s != "" {
		query += ` WHERE u.email LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(strings.ToLower(s))+"%")
	}
	query += ` ORDER BY u.email`

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()

	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	if err := classify(rows.Err()); err != nil {
		return nil, err
	}

	for i := range out {
		if out[i].Stores, err = db.storesForUser(ctx, db.DB, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// CreateUser inserts a salesperson and assigns their stores.
func (db *DB) CreateUser(ctx context.Context, email, passwordHash string, storeIDs []int64, mustChange bool) (int64, error) {
	var id int64
	err := db.InTx(ctx, func(tx *sql.Tx) error {
		now := dates.NowUTC()
		res, err := tx.ExecContext(ctx,
			`INSERT INTO users (email, password_hash, must_change_password, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?)`,
			email, passwordHash, mustChange, now, now)
		if err != nil {
			return classify(err)
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		return db.setUserStores(ctx, tx, id, storeIDs)
	})
	return id, err
}

// SetUserStores replaces a salesperson's store assignments.
func (db *DB) SetUserStores(ctx context.Context, userID int64, storeIDs []int64) error {
	return db.InTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET updated_at = ? WHERE id = ?`,
			dates.NowUTC(), userID); err != nil {
			return classify(err)
		}
		return db.setUserStores(ctx, tx, userID, storeIDs)
	})
}

func (db *DB) setUserStores(ctx context.Context, q Querier, userID int64, storeIDs []int64) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM user_stores WHERE user_id = ?`, userID); err != nil {
		return classify(err)
	}
	seen := map[int64]bool{}
	for _, sid := range storeIDs {
		if sid <= 0 || seen[sid] {
			continue
		}
		seen[sid] = true
		if _, err := q.ExecContext(ctx,
			`INSERT INTO user_stores (user_id, store_id) VALUES (?, ?)`, userID, sid); err != nil {
			return classify(err)
		}
	}
	return nil
}

// SetUserPassword replaces a salesperson's password hash.
func (db *DB) SetUserPassword(ctx context.Context, id int64, hash string, mustChange bool) error {
	_, err := db.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, must_change_password = ?, updated_at = ? WHERE id = ?`,
		hash, mustChange, dates.NowUTC(), id)
	return classify(err)
}

// SetUserActive activates or deactivates a salesperson. Deactivating drops
// their sessions so the block takes effect immediately.
func (db *DB) SetUserActive(ctx context.Context, id int64, active bool) error {
	return db.InTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET active = ?, updated_at = ? WHERE id = ?`,
			active, dates.NowUTC(), id); err != nil {
			return classify(err)
		}
		if active {
			return nil
		}
		_, err := tx.ExecContext(ctx,
			`DELETE FROM sessions WHERE subject_type = ? AND subject_id = ?`, SubjectUser, id)
		return classify(err)
	})
}

// DeleteUser removes a salesperson. Submitted requests keep their snapshotted
// submitter email, so history survives the deletion.
func (db *DB) DeleteUser(ctx context.Context, id int64) error {
	return db.InTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM sessions WHERE subject_type = ? AND subject_id = ?`, SubjectUser, id); err != nil {
			return classify(err)
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
		return classify(err)
	})
}

// prefixColumns qualifies a comma-separated column list with a table alias.
func prefixColumns(prefix, columns string) string {
	parts := strings.Split(columns, ",")
	for i, p := range parts {
		parts[i] = prefix + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}

// escapeLike neutralises the wildcards in a user-supplied LIKE pattern. The
// queries using it declare ESCAPE '\'.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}
