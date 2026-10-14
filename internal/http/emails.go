package http

import (
	"context"
	"strings"

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

	recipients, cc, err := a.templateDelivery(ctx, email.TemplateRequestCreated, req.SubmitterEmail)
	if err != nil {
		a.Log.Error("notification recipients could not be read", "error", err)
		return
	}
	if len(recipients) == 0 {
		a.Log.Warn("no recipients are assigned to the template; "+
			"the new-request email was not queued", "request_id", requestID)
		return
	}

	if _, err := a.Composer.Queue(ctx, nil, email.TemplateRequestCreated, recipients, cc,
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

	if _, err := a.Composer.Queue(ctx, nil, templateKey, []string{req.SubmitterEmail}, nil,
		a.Composer.RequestValues(req), nil); err != nil {
		a.Log.Error("decision email could not be queued",
			"request_id", req.ID, "template", templateKey, "error", err)
		return
	}
	a.Outbox.Notify()
}

// queueProfileRequest queues the "profile request" notification to the
// configured admin recipients. As with module requests, a failure is logged
// but never surfaced: the request is already stored.
func (a *App) queueProfileRequest(ctx context.Context, id int64, n store.NewProfileRequest, password string) {
	recipients, cc, err := a.templateDelivery(ctx, email.TemplateProfileRequest, n.SubmitterEmail)
	if err != nil {
		a.Log.Error("notification recipients could not be read", "error", err)
		return
	}
	if len(recipients) == 0 {
		a.Log.Warn("no recipients are assigned to the template; "+
			"the profile request email was not queued", "profile_request_id", id)
		return
	}
	if _, err := a.Composer.Queue(ctx, nil, email.TemplateProfileRequest, recipients, cc,
		a.Composer.ProfileRequestValues(n, password), nil); err != nil {
		a.Log.Error("profile request email could not be queued",
			"profile_request_id", id, "error", err)
		return
	}
	a.Outbox.Notify()
}

// templateRecipients returns the addresses assigned to an email template.
func (a *App) templateRecipients(ctx context.Context, key string) ([]string, error) {
	return a.DB.TemplateRecipients(ctx, key)
}

// templateDelivery returns who an email built from the template goes to: its
// assigned recipients, and - when the template is set to always copy the
// submitter - the submitter as CC. The submitter is left out of the copy when
// they are already a recipient, so nobody gets the message twice.
func (a *App) templateDelivery(ctx context.Context, key, submitterEmail string) (to, cc []string, err error) {
	t, err := a.DB.EmailTemplateByKey(ctx, key)
	if err != nil {
		return nil, nil, err
	}
	to = t.RecipientList()
	if !t.CCSubmitter || strings.TrimSpace(submitterEmail) == "" {
		return to, nil, nil
	}
	for _, r := range to {
		if strings.EqualFold(r, strings.TrimSpace(submitterEmail)) {
			return to, nil, nil
		}
	}
	return to, []string{strings.TrimSpace(submitterEmail)}, nil
}
