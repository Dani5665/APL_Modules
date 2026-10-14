package store

import (
	"context"

	"haynesproform/internal/dates"
)

// NewProfileRequest is a request to create a customer's profile in the NEXT
// catalog.
type NewProfileRequest struct {
	ClientCode      string
	ClientName      string
	StoreName       string
	Username        string
	PasswordEnc     []byte
	Transport       string
	SubmitterID     int64
	SubmitterName   string
	SubmitterEmail  string
	SubmitterStores string
	Comment         string
}

// CreateProfileRequest stores a profile request and returns its id.
func (db *DB) CreateProfileRequest(ctx context.Context, n NewProfileRequest) (int64, error) {
	res, err := db.ExecContext(ctx,
		`INSERT INTO profile_requests
			(client_code, client_name, store_name, username, password_enc, transport,
			 submitter_id, submitter_name, submitter_email, submitter_stores, comment, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.ClientCode, n.ClientName, n.StoreName, n.Username, n.PasswordEnc, n.Transport,
		nullInt64(n.SubmitterID), n.SubmitterName, n.SubmitterEmail, n.SubmitterStores, n.Comment,
		dates.NowUTC())
	if err != nil {
		return 0, classify(err)
	}
	return res.LastInsertId()
}
