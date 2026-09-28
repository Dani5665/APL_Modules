package http

import (
	"context"

	"haynesproform/internal/email"
	"haynesproform/internal/store"
)

// queueRequestCreated queues the "new request" notification to the configured
// admin recipients.
//
// A failure here is logged but never surfaced: the request has already been
// stored, and losing the notification must not lose the request.
func (a *App) queueRequestCreated(ctx context.Context, requestID int64) {
	req, err := a.DB.RequestByID(ctx, requestID)
	if err != nil {
		a.Log.Error("request could not be read for its notification email",
			"request_id", requestID, "error", err)
		return
	}

	recipients, err := a.notifyRecipients(ctx)
	if err != nil {
		a.Log.Error("notification recipients could not be read", "error", err)
		return
	}
	if len(recipients) == 0 {
		a.Log.Warn("no administrator notification recipients are configured; "+
			"the new-request email was not queued", "request_id", requestID)
		return
	}

	if _, err := a.Composer.Queue(ctx, nil, email.TemplateRequestCreated, recipients,
		a.Composer.RequestValues(req), nil); err != nil {
		a.Log.Error("new-request email could not be queued",
			"request_id", requestID, "error", err)
		return
	}
	a.Outbox.Notify()
}

// queueDecision queues the approval or denial email to the submitting account
// only.
func (a *App) queueDecision(ctx context.Context, req *store.Request, templateKey string) {
	if req.SubmitterEmail == "" {
		a.Log.Warn("request has no submitter address; the decision email was not queued",
			"request_id", req.ID)
		return
	}

	if _, err := a.Composer.Queue(ctx, nil, templateKey, []string{req.SubmitterEmail},
		a.Composer.RequestValues(req), nil); err != nil {
		a.Log.Error("decision email could not be queued",
			"request_id", req.ID, "template", templateKey, "error", err)
		return
	}
	a.Outbox.Notify()
}

// notifyRecipients returns the configured administrator notification
// addresses.
func (a *App) notifyRecipients(ctx context.Context) ([]string, error) {
	raw, err := a.DB.Setting(ctx, store.KeyNotifyRecipients, "")
	if err != nil {
		return nil, err
	}
	return store.SplitEmails(raw), nil
}
