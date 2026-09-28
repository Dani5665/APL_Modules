package store

import (
	"context"
	"fmt"

	"haynesproform/internal/dates"
)

const storeColumns = `id, name, external_value, active, created_at, updated_at`

func scanStore(s interface{ Scan(...any) error }) (*Store, error) {
	var st Store
	if err := s.Scan(&st.ID, &st.Name, &st.ExternalValue, &st.Active, &st.CreatedAt, &st.UpdatedAt); err != nil {
		return nil, classify(err)
	}
	return &st, nil
}

// StoreByID loads one store.
func (db *DB) StoreByID(ctx context.Context, id int64) (*Store, error) {
	return scanStore(db.QueryRowContext(ctx, `SELECT `+storeColumns+` FROM stores WHERE id = ?`, id))
}

// ListStores returns stores ordered by name. When activeOnly is set, only
// stores available for new assignments are returned.
func (db *DB) ListStores(ctx context.Context, activeOnly bool) ([]Store, error) {
	query := `SELECT ` + prefixColumns("s.", storeColumns) + `,
		(SELECT count(*) FROM user_stores us WHERE us.store_id = s.id) AS user_count
		FROM stores s`
	if activeOnly {
		query += ` WHERE s.active = 1`
	}
	query += ` ORDER BY s.name`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()

	var out []Store
	for rows.Next() {
		var s Store
		if err := rows.Scan(&s.ID, &s.Name, &s.ExternalValue, &s.Active,
			&s.CreatedAt, &s.UpdatedAt, &s.UserCount); err != nil {
			return nil, classify(err)
		}
		out = append(out, s)
	}
	return out, classify(rows.Err())
}

// CreateStore inserts a store. An empty externalValue defaults to the name.
func (db *DB) CreateStore(ctx context.Context, name, externalValue string) (int64, error) {
	if externalValue == "" {
		externalValue = name
	}
	now := dates.NowUTC()
	res, err := db.ExecContext(ctx,
		`INSERT INTO stores (name, external_value, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		name, externalValue, now, now)
	if err != nil {
		return 0, classify(err)
	}
	return res.LastInsertId()
}

// UpdateStore changes a store's name, external value and active flag.
func (db *DB) UpdateStore(ctx context.Context, id int64, name, externalValue string, active bool) error {
	if externalValue == "" {
		externalValue = name
	}
	_, err := db.ExecContext(ctx,
		`UPDATE stores SET name = ?, external_value = ?, active = ?, updated_at = ? WHERE id = ?`,
		name, externalValue, active, dates.NowUTC(), id)
	return classify(err)
}

// DeleteStore removes a store that no salesperson is assigned to. A store in
// use must be deactivated instead.
func (db *DB) DeleteStore(ctx context.Context, id int64) error {
	var assigned int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM user_stores WHERE store_id = ?`, id).Scan(&assigned); err != nil {
		return classify(err)
	}
	if assigned > 0 {
		return fmt.Errorf("%w: %d salespeople are assigned to this store", ErrInUse, assigned)
	}
	_, err := db.ExecContext(ctx, `DELETE FROM stores WHERE id = ?`, id)
	return classify(err)
}

// storesForUser loads a salesperson's stores, including deactivated ones: an
// existing assignment keeps working, it is only hidden from new assignments.
func (db *DB) storesForUser(ctx context.Context, q Querier, userID int64) ([]Store, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT `+prefixColumns("s.", storeColumns)+`
		 FROM stores s JOIN user_stores us ON us.store_id = s.id
		 WHERE us.user_id = ?
		 ORDER BY s.name`, userID)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()

	var out []Store
	for rows.Next() {
		s, err := scanStore(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, classify(rows.Err())
}
