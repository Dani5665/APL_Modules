package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"haynesproform/internal/audit"
	"haynesproform/internal/dates"
	"haynesproform/internal/email"
	"haynesproform/internal/export"
	"haynesproform/internal/store"
)

// tickInterval is how often the schedule is inspected. A minute is fine for a
// job that runs monthly and keeps the loop cheap.
const tickInterval = time.Minute

// Runner owns the scheduled export.
type Runner struct {
	db       *store.DB
	gen      *export.Generator
	composer *email.Composer
	audit    *audit.Logger
	log      *slog.Logger
	// notifyOutbox wakes the email worker as soon as a message is queued.
	notifyOutbox func()
}

// New builds the scheduler.
func New(db *store.DB, gen *export.Generator, composer *email.Composer, auditLog *audit.Logger, log *slog.Logger, notifyOutbox func()) *Runner {
	return &Runner{db: db, gen: gen, composer: composer, audit: auditLog, log: log, notifyOutbox: notifyOutbox}
}

// Run ticks until ctx is cancelled.
//
// A missed run - the container was down at the scheduled time - is handled by
// the same code path: the first tick after startup sees next_run_at in the
// past and runs once. Claiming advances next_run_at into the future, so a
// long outage still produces exactly one catch-up.
func (r *Runner) Run(ctx context.Context) {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	r.safeTick(ctx)
	for {
		select {
		case <-ctx.Done():
			r.log.Info("scheduler stopped")
			return
		case <-ticker.C:
			r.safeTick(ctx)
		}
	}
}

// safeTick runs one tick with panic recovery: this goroutine has no HTTP
// middleware to catch a panic for it, and one unrecovered panic here would
// take the whole process down, not just the export. Generating and attaching
// a spreadsheet touches a third-party library on every run, so a defensive
// boundary here costs little and buys a lot.
func (r *Runner) safeTick(ctx context.Context) {
	defer func() {
		if p := recover(); p != nil {
			r.log.Error("panic recovered in scheduler tick",
				"panic", fmt.Sprint(p), "stack", string(debug.Stack()))
		}
	}()
	r.tick(ctx)
}

func (r *Runner) tick(ctx context.Context) {
	s, err := r.db.ExportSchedule(ctx)
	if err != nil {
		r.log.Error("export schedule could not be read", "error", err)
		return
	}
	if !s.Enabled {
		return
	}
	if !s.NextRunAt.Valid || s.NextRunAt.String == "" {
		// Enabled but never scheduled: compute the first run and wait for it.
		if err := r.Reschedule(ctx); err != nil {
			r.log.Error("next export run could not be computed", "error", err)
		}
		return
	}

	next, err := dates.ParseUTC(s.NextRunAt.String)
	if err != nil {
		r.log.Error("next export run is not a valid timestamp",
			"value", s.NextRunAt.String, "error", err)
		return
	}
	if time.Now().Before(next) {
		return
	}

	// Compute the following run from now rather than from the missed time, so
	// a long outage does not queue a backlog of catch-ups.
	following, err := NextRun(time.Now(), s.DayOfMonth, s.TimeHHMM)
	if err != nil {
		r.log.Error("next export run could not be computed", "error", err)
		return
	}

	// The claim and the advance are one statement: whoever wins it sends.
	won, err := r.db.ClaimExportRun(ctx, s.NextRunAt.String, dates.FormatUTC(following))
	if err != nil {
		r.log.Error("export run could not be claimed", "error", err)
		return
	}
	if !won {
		return
	}

	r.log.Info("running scheduled export",
		"due_at", next.In(dates.Sofia).Format(time.RFC3339),
		"next_at", following.Format(time.RFC3339))

	if err := r.runExport(ctx, s.RecipientList(), export.Options{GroupByClient: s.GroupByClient},
		audit.ExportScheduled, audit.Actor{Type: "system", Label: "scheduler"}, ""); err != nil {
		r.recordResult(ctx, "Грешка: "+err.Error())
		r.log.Error("scheduled export failed", "error", err)
		return
	}
	r.recordResult(ctx, "Успешно")
}

// SendNow runs the export immediately, for the admin's test button. It does
// not touch next_run_at.
func (r *Runner) SendNow(ctx context.Context, actor audit.Actor, ip string) error {
	s, err := r.db.ExportSchedule(ctx)
	if err != nil {
		return err
	}
	recipients := s.RecipientList()
	if len(recipients) == 0 {
		return fmt.Errorf("няма зададени получатели на справката")
	}

	opts := export.Options{GroupByClient: s.GroupByClient}
	if err := r.runExport(ctx, recipients, opts, audit.ExportScheduled, actor, ip); err != nil {
		r.recordResult(ctx, "Грешка: "+err.Error())
		return err
	}
	r.recordResult(ctx, "Успешно (ръчно изпращане)")
	return nil
}

// runExport generates the report and queues it to the recipients.
func (r *Runner) runExport(ctx context.Context, recipients []string, opts export.Options, action string, actor audit.Actor, ip string) error {
	if len(recipients) == 0 {
		return fmt.Errorf("няма зададени получатели на справката")
	}

	res, err := r.gen.Generate(ctx, opts)
	if err != nil {
		return fmt.Errorf("справката не може да бъде генерирана: %w", err)
	}

	today := dates.Today()
	values := r.composer.ExportValues(today, res.RowCount)
	id, err := r.composer.Queue(ctx, nil, email.TemplateScheduledExport, recipients, values,
		&email.Attachment{Filename: res.Filename, Content: res.Content})
	if err != nil {
		return fmt.Errorf("справката не може да бъде поставена в опашката: %w", err)
	}

	r.audit.Record(ctx, actor, action, res.Filename, ip, map[string]any{
		"recipients":      recipients,
		"rows":            res.RowCount,
		"outbox_id":       id,
		"export_date":     today,
		"group_by_client": opts.GroupByClient,
	})
	if r.notifyOutbox != nil {
		r.notifyOutbox()
	}
	return nil
}

func (r *Runner) recordResult(ctx context.Context, result string) {
	if err := r.db.RecordExportRun(ctx, dates.NowUTC(), result); err != nil {
		r.log.Error("export result could not be recorded", "error", err)
	}
}

// Reschedule recomputes next_run_at from the stored schedule. It is called
// after an admin changes the schedule and when an enabled schedule has no
// next run yet.
func (r *Runner) Reschedule(ctx context.Context) error {
	s, err := r.db.ExportSchedule(ctx)
	if err != nil {
		return err
	}
	next, err := ComputeNext(s)
	if err != nil {
		return err
	}
	return r.db.SaveExportSchedule(ctx, s.Enabled, s.DayOfMonth, s.TimeHHMM, s.Recipients, s.GroupByClient, next)
}

// ComputeNext returns the next run timestamp for a schedule, or SQL NULL when
// the schedule is disabled.
func ComputeNext(s *store.ExportSchedule) (sql.NullString, error) {
	if !s.Enabled {
		return sql.NullString{}, nil
	}
	next, err := NextRun(time.Now(), s.DayOfMonth, s.TimeHHMM)
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: dates.FormatUTC(next), Valid: true}, nil
}
