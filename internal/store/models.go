package store

import (
	"database/sql"
	"strconv"
	"strings"

	"haynesproform/internal/dates"
	"haynesproform/internal/modules"
)

// SubjectType distinguishes the two account types. They live in separate
// tables and never share a session.
type SubjectType string

const (
	SubjectUser  SubjectType = "user"
	SubjectAdmin SubjectType = "admin"
)

// SessionStage marks how far a login has progressed.
const (
	StageActive       = "active"
	StageAwaitingTOTP = "awaiting_totp"
)

// RequestStatus is the lifecycle of an activation request.
type RequestStatus string

const (
	StatusPending  RequestStatus = "pending"
	StatusApproved RequestStatus = "approved"
	StatusDenied   RequestStatus = "denied"
)

// Label returns the Bulgarian label of a request status.
func (s RequestStatus) Label() string {
	switch s {
	case StatusPending:
		return "Чакащо"
	case StatusApproved:
		return "Одобрено"
	case StatusDenied:
		return "Отказано"
	}
	return string(s)
}

// Admin is an administrator account.
type Admin struct {
	ID                 int64
	Email              string
	PasswordHash       string
	TOTPSecretEnc      []byte
	TOTPEnabled        bool
	LastTOTPStep       int64
	MustChangePassword bool
	Active             bool
	CreatedAt          string
	UpdatedAt          string
}

// User is a salesperson account.
type User struct {
	ID                 int64
	Email              string
	PasswordHash       string
	MustChangePassword bool
	Active             bool
	CreatedAt          string
	UpdatedAt          string
	// Stores is populated by the repository methods that load assignments.
	Stores []Store
}

// StoreExternalValues returns the external values of the user's stores, which
// is what authorization compares against.
func (u User) StoreExternalValues() []string {
	out := make([]string, 0, len(u.Stores))
	for _, s := range u.Stores {
		out = append(out, s.ExternalValue)
	}
	return out
}

// HasStoreValue reports whether the user is assigned a store whose external
// value matches v, compared case-insensitively and ignoring surrounding space.
func (u User) HasStoreValue(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return false
	}
	for _, s := range u.Stores {
		if strings.ToLower(strings.TrimSpace(s.ExternalValue)) == v {
			return true
		}
	}
	return false
}

// Store is a local shop record mapped to a value in the external database.
type Store struct {
	ID            int64
	Name          string
	ExternalValue string
	Active        bool
	CreatedAt     string
	UpdatedAt     string
	// UserCount is populated by listings that need it.
	UserCount int
}

// Session is a server-side session record.
type Session struct {
	TokenHash   string
	SubjectType SubjectType
	SubjectID   int64
	CSRFToken   string
	Stage       string
	CreatedAt   string
	LastSeenAt  string
	ExpiresAt   string
}

// Request is a submitted activation request with its snapshotted client data.
type Request struct {
	ID               int64
	ClientCode       string
	ClientName       string
	ClientObject     string
	ClientStore      string
	SubmitterUserID  sql.NullInt64
	SubmitterEmail   string
	SalerLogin       string
	TestPeriod       bool
	StartDate        string
	Months           int
	Status           RequestStatus
	AdminComment     string
	DecidedByAdminID sql.NullInt64
	DecidedByEmail   string
	DecidedAt        sql.NullString
	CreatedAt        string
	UpdatedAt        string

	Usernames []string
	Modules   []RequestModule
}

// RequestModule is one module (with its tier) selected on a request.
type RequestModule struct {
	Module modules.Key
	Tier   modules.Tier
}

// Describe renders the module and tier, e.g. "HaynesPro (Business)".
func (m RequestModule) Describe() string { return modules.Describe(m.Module, m.Tier) }

// EndDate returns the inclusive last day covered by the request.
func (r Request) EndDate() string {
	end, err := dates.EndDate(r.StartDate, r.Months)
	if err != nil {
		return ""
	}
	return end
}

// DurationLabel renders the requested duration the way the form offers it.
func (r Request) DurationLabel() string { return DurationLabel(r.Months) }

// DurationLabel renders a month count using the Bulgarian labels of the
// "Активация за" dropdown.
func DurationLabel(months int) string {
	switch {
	case months == 1:
		return "1 месец"
	case months == 12:
		return "1 година"
	default:
		return strconv.Itoa(months) + " месеца"
	}
}

// DurationOption is one entry of the "Активация за" dropdown.
type DurationOption struct {
	Months int
	Label  string
}

// DurationOptions returns the twelve selectable durations, in order.
func DurationOptions() []DurationOption {
	out := make([]DurationOption, 0, 12)
	for m := 1; m <= 12; m++ {
		out = append(out, DurationOption{Months: m, Label: DurationLabel(m)})
	}
	return out
}

// Activation is one granted (username x module) row.
type Activation struct {
	ID               int64
	ClientCode       string
	ClientName       string
	ClientObject     string
	ClientStore      string
	Username         string
	Module           modules.Key
	Tier             modules.Tier
	StartDate        string
	EndDate          string
	RevokedAt        sql.NullString
	SourceRequestID  sql.NullInt64
	CreatedByAdminID sql.NullInt64
	CreatedAt        string
	UpdatedAt        string
}

// ActivationStatus is the computed state of an activation.
type ActivationStatus string

const (
	ActivationActive  ActivationStatus = "active"
	ActivationPending ActivationStatus = "pending"
	ActivationExpired ActivationStatus = "expired"
	ActivationRevoked ActivationStatus = "revoked"
)

// Label returns the Bulgarian label of an activation status.
func (s ActivationStatus) Label() string {
	switch s {
	case ActivationActive:
		return "Активен"
	case ActivationPending:
		return "Предстоящ"
	case ActivationExpired:
		return "Изтекъл"
	case ActivationRevoked:
		return "Прекратен"
	}
	return "Неизвестен"
}

// StatusOn computes the activation's state on the given ISO date. Status is
// derived from dates rather than stored, so expiry needs no scheduled job.
func (a Activation) StatusOn(today string) ActivationStatus {
	if a.RevokedAt.Valid && a.RevokedAt.String != "" {
		return ActivationRevoked
	}
	switch {
	case today < a.StartDate:
		return ActivationPending
	case today > a.EndDate:
		return ActivationExpired
	default:
		return ActivationActive
	}
}

// Status computes the activation's state today, in the business time zone.
func (a Activation) Status() ActivationStatus { return a.StatusOn(dates.Today()) }

// IsActive reports whether the activation is in force today.
func (a Activation) IsActive() bool { return a.Status() == ActivationActive }

// Describe renders the module and tier, e.g. "HaynesPro (Business)".
func (a Activation) Describe() string { return modules.Describe(a.Module, a.Tier) }
