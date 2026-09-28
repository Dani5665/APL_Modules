// Package audit records who did what, when and from where. Entries are
// append-only and are never allowed to fail the action they describe.
package audit

import (
	"context"
	"encoding/json"
	"log/slog"

	"haynesproform/internal/store"
)

// Action names used across the application. They are stable strings because
// the admin filter lists them.
const (
	LoginSuccess     = "login.success"
	LoginFailure     = "login.failure"
	LoginLocked      = "login.locked"
	Logout           = "logout"
	TOTPEnrolled     = "totp.enrolled"
	TOTPSuccess      = "totp.success"
	TOTPFailure      = "totp.failure"
	TOTPRecoveryUsed = "totp.recovery_used"
	TOTPReset        = "totp.reset"
	PasswordChanged  = "password.changed"
	PasswordReset    = "password.reset"

	EntryDenied = "entry.denied"

	RequestSubmitted = "request.submitted"
	RequestEdited    = "request.edited"
	RequestApproved  = "request.approved"
	RequestDenied    = "request.denied"

	ActivationCreated  = "activation.created"
	ActivationUpdated  = "activation.updated"
	ActivationRevoked  = "activation.revoked"
	ActivationRestored = "activation.restored"
	ActivationDeleted  = "activation.deleted"

	UserCreated  = "user.created"
	UserUpdated  = "user.updated"
	UserDeleted  = "user.deleted"
	AdminCreated = "admin.created"
	AdminDeleted = "admin.deleted"
	StoreCreated = "store.created"
	StoreUpdated = "store.updated"
	StoreDeleted = "store.deleted"

	SMTPUpdated      = "smtp.updated"
	SMTPTestSent     = "smtp.test_sent"
	TemplateUpdated  = "template.updated"
	TemplateRestored = "template.restored"
	ScheduleUpdated  = "schedule.updated"

	ExportManual    = "export.manual"
	ExportScheduled = "export.scheduled"
	EmailRetried    = "email.retried"
	EmailDeleted    = "email.deleted"
)

// Actor identifies who performed an action. An anonymous actor (a failed
// login, say) carries only a label.
type Actor struct {
	Type  string
	ID    int64
	Label string
}

// UserActor builds an actor for a salesperson.
func UserActor(id int64, email string) Actor {
	return Actor{Type: string(store.SubjectUser), ID: id, Label: email}
}

// AdminActor builds an actor for an administrator.
func AdminActor(id int64, email string) Actor {
	return Actor{Type: string(store.SubjectAdmin), ID: id, Label: email}
}

// AnonActor builds an actor for an unauthenticated attempt.
func AnonActor(label string) Actor { return Actor{Type: "anonymous", Label: label} }

// Logger writes audit entries.
type Logger struct {
	db  *store.DB
	log *slog.Logger
}

// New builds an audit logger.
func New(db *store.DB, log *slog.Logger) *Logger { return &Logger{db: db, log: log} }

// Record appends an entry. A failure to write the audit row is logged but
// never propagated: losing an audit line must not undo a completed action.
func (l *Logger) Record(ctx context.Context, actor Actor, action, target, ip string, details map[string]any) {
	l.RecordTx(ctx, nil, actor, action, target, ip, details)
}

// RecordTx appends an entry inside an existing transaction, so an action and
// its audit row commit together. Passing a nil Querier writes outside any
// transaction.
func (l *Logger) RecordTx(ctx context.Context, q store.Querier, actor Actor, action, target, ip string, details map[string]any) {
	payload := "{}"
	if len(details) > 0 {
		if b, err := json.Marshal(details); err == nil {
			payload = string(b)
		} else {
			l.log.Warn("audit details could not be encoded", "action", action, "error", err)
		}
	}

	e := store.AuditEntry{
		ActorType:   actor.Type,
		ActorLabel:  actor.Label,
		Action:      action,
		Target:      target,
		IP:          ip,
		DetailsJSON: payload,
	}
	if actor.ID > 0 {
		e.ActorID.Int64, e.ActorID.Valid = actor.ID, true
	}

	if err := l.db.InsertAudit(ctx, q, e); err != nil {
		l.log.Error("audit entry could not be written",
			"action", action, "actor", actor.Label, "error", err)
	}
}
