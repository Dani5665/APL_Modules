package email

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"runtime/debug"
	"time"

	"haynesproform/internal/auth"
	"haynesproform/internal/store"
)

// Outbox worker tuning.
const (
	// MaxAttempts is how many times a message is retried before it is parked
	// as failed and shown to admins.
	MaxAttempts = 6
	// baseBackoff is doubled on each attempt: 1, 2, 4, 8, 16, 32 minutes.
	baseBackoff = time.Minute
	maxBackoff  = time.Hour
	// batchSize bounds how many messages one tick delivers.
	batchSize = 10
	// tickInterval is how often the worker looks for due messages.
	tickInterval = 30 * time.Second
)

// Worker delivers queued messages in the background.
type Worker struct {
	db  *store.DB
	enc *auth.Encrypter
	log *slog.Logger
	// wake lets a fresh submission trigger a delivery attempt without waiting
	// for the next tick.
	wake chan struct{}
}

// NewWorker builds the outbox worker.
func NewWorker(db *store.DB, enc *auth.Encrypter, log *slog.Logger) *Worker {
	return &Worker{db: db, enc: enc, log: log, wake: make(chan struct{}, 1)}
}

// Notify asks the worker to look for work now. It never blocks.
func (w *Worker) Notify() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Run delivers queued messages until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	w.safeProcessBatch(ctx)
	for {
		select {
		case <-ctx.Done():
			w.log.Info("outbox worker stopped")
			return
		case <-ticker.C:
			w.safeProcessBatch(ctx)
		case <-w.wake:
			w.safeProcessBatch(ctx)
		}
	}
}

// safeProcessBatch runs one batch with panic recovery: this goroutine has no
// HTTP middleware to catch a panic for it, and one unrecovered panic here
// would take the whole process down, not just a delivery attempt.
func (w *Worker) safeProcessBatch(ctx context.Context) {
	defer func() {
		if p := recover(); p != nil {
			w.log.Error("panic recovered in outbox worker",
				"panic", fmt.Sprint(p), "stack", string(debug.Stack()))
		}
	}()
	w.processBatch(ctx)
}

func (w *Worker) processBatch(ctx context.Context) {
	msgs, err := w.db.ClaimDueEmails(ctx, batchSize)
	if err != nil {
		w.log.Error("outbox could not be read", "error", err)
		return
	}
	if len(msgs) == 0 {
		return
	}

	// Settings are read once per batch: they change rarely, and a send that
	// fails because SMTP is unset must still record the failure per message.
	settings, err := LoadSettings(ctx, w.db, w.enc)
	if err != nil {
		w.log.Error("SMTP settings could not be read", "error", err)
	}
	sender := NewSender(settings)

	for _, m := range msgs {
		if ctx.Err() != nil {
			return
		}
		w.deliver(ctx, sender, m)
	}
}

func (w *Worker) deliver(ctx context.Context, sender *Sender, m store.OutboxMessage) {
	msg := Message{
		To:       m.RecipientList(),
		Subject:  m.Subject,
		BodyHTML: m.BodyHTML,
	}
	if m.AttachmentName.Valid && len(m.AttachmentBlob) > 0 {
		msg.Attachment = &Attachment{Filename: m.AttachmentName.String, Content: m.AttachmentBlob}
	}

	err := sender.Send(ctx, msg)
	if err == nil {
		if err := w.db.MarkEmailSent(ctx, m.ID); err != nil {
			w.log.Error("outbox message could not be marked sent", "id", m.ID, "error", err)
			return
		}
		w.log.Info("email sent", "id", m.ID, "template", m.TemplateKey, "recipients", m.Recipients)
		return
	}

	attempts := m.Attempts + 1
	next := time.Now().Add(backoff(attempts))
	if markErr := w.db.MarkEmailFailed(ctx, m.ID, attempts, MaxAttempts, err.Error(), next); markErr != nil {
		w.log.Error("outbox failure could not be recorded", "id", m.ID, "error", markErr)
	}

	level := slog.LevelWarn
	if attempts >= MaxAttempts {
		level = slog.LevelError
	}
	w.log.Log(ctx, level, "email delivery failed",
		"id", m.ID, "template", m.TemplateKey, "attempt", attempts, "max_attempts", MaxAttempts,
		"error", err)
}

// backoff returns the delay before attempt n, doubling each time and capped
// so a long-broken SMTP server is retried hourly rather than never.
func backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := time.Duration(math.Pow(2, float64(attempt-1))) * baseBackoff
	if d > maxBackoff || d <= 0 {
		return maxBackoff
	}
	return d
}
