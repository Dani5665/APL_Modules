package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"haynesproform/internal/audit"
	"haynesproform/internal/auth"
	"haynesproform/internal/dates"
	"haynesproform/internal/email"
	"haynesproform/internal/scheduler"
	"haynesproform/internal/store"
)

type smtpView struct {
	view
	Settings          email.Settings
	SecurityOptions   []email.Security
	RecipientsRaw     string
	HasStoredPassword bool
	Errors            []string
	TestAddress       string
	TestError         string
}

func (a *App) handleAdminSMTP(w http.ResponseWriter, r *http.Request) {
	a.renderSMTP(w, r, nil, "", "", http.StatusOK)
}

func (a *App) renderSMTP(w http.ResponseWriter, r *http.Request, errs []string, testAddress, testErr string, status int) {
	ctx := r.Context()

	s, err := email.LoadSettings(ctx, a.DB, a.Enc)
	if err != nil {
		// A key change makes the password unreadable; the rest of the form
		// must still be usable so the admin can re-enter it.
		a.Log.Error("SMTP settings could not be fully read", "error", err)
		errs = append(errs, "Запазената SMTP парола не може да бъде прочетена. Въведете я отново.")
	}

	sealed, _ := a.DB.Setting(ctx, store.KeySMTPPasswordEnc, "")

	v := smtpView{
		view:              a.newView(r, "SMTP настройки", "smtp"),
		Settings:          s,
		SecurityOptions:   email.SecurityOptions,
		RecipientsRaw:     strings.Join(s.NotifyRecipients, "\n"),
		HasStoredPassword: sealed != "",
		Errors:            errs,
		TestAddress:       testAddress,
		TestError:         testErr,
	}
	// The stored password is never sent back to the browser.
	v.Settings.Password = ""

	if err := a.render.render(w, status, LayoutAdmin, "admin/smtp", v); err != nil {
		a.serverError(w, r, err)
	}
}

func (a *App) handleAdminSMTPSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	admin := adminFrom(ctx)

	host := strings.TrimSpace(r.PostFormValue("host"))
	portRaw := strings.TrimSpace(r.PostFormValue("port"))
	security := email.Security(strings.TrimSpace(r.PostFormValue("security")))
	username := strings.TrimSpace(r.PostFormValue("username"))
	password := r.PostFormValue("password")
	fromAddress := strings.TrimSpace(r.PostFormValue("from_address"))
	fromName := strings.TrimSpace(r.PostFormValue("from_name"))
	recipientsRaw := r.PostFormValue("notify_recipients")

	var errs []string

	port, err := strconv.Atoi(portRaw)
	if err != nil || port < 1 || port > 65535 {
		errs = append(errs, "Въведете валиден порт (1-65535).")
	}
	if host == "" {
		errs = append(errs, "Въведете SMTP сървър.")
	}
	switch security {
	case email.SecurityNone, email.SecuritySTARTTLS, email.SecurityTLS:
	default:
		errs = append(errs, "Изберете тип на защитата.")
	}
	if fromAddress == "" || !auth.ValidEmail(fromAddress) {
		errs = append(errs, "Въведете валиден адрес на подателя.")
	}

	recipients, invalid := auth.NormalizeEmailList(recipientsRaw)
	if len(invalid) > 0 {
		errs = append(errs, "Невалидни адреси за известия: "+strings.Join(invalid, ", "))
	}

	if len(errs) > 0 {
		a.renderSMTP(w, r, errs, "", "", http.StatusUnprocessableEntity)
		return
	}

	values := map[string]string{
		store.KeySMTPHost:         host,
		store.KeySMTPPort:         strconv.Itoa(port),
		store.KeySMTPSecurity:     string(security),
		store.KeySMTPUsername:     username,
		store.KeySMTPFromAddress:  fromAddress,
		store.KeySMTPFromName:     fromName,
		store.KeyNotifyRecipients: strings.Join(recipients, ", "),
	}

	// An empty password field means "leave the stored password unchanged",
	// which is why the current one is never rendered back into the form.
	if password != "" {
		sealed, err := a.Enc.Encrypt(password)
		if err != nil {
			a.serverError(w, r, err)
			return
		}
		values[store.KeySMTPPasswordEnc] = string(sealed)
	}

	if err := a.DB.SetSettings(ctx, values); err != nil {
		a.serverError(w, r, err)
		return
	}

	a.Audit.Record(ctx, audit.AdminActor(admin.ID, admin.Email), audit.SMTPUpdated,
		host, a.clientIP(r), map[string]any{
			"port":              port,
			"security":          string(security),
			"username_set":      username != "",
			"password_changed":  password != "",
			"from_address":      fromAddress,
			"notify_recipients": recipients,
		})
	redirectWithFlash(w, r, "/admin/settings/smtp", "smtp_saved")
}

func (a *App) handleAdminSMTPTest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	admin := adminFrom(ctx)

	to := strings.TrimSpace(r.PostFormValue("test_address"))
	if !auth.ValidEmail(to) {
		a.renderSMTP(w, r, nil, to, "Въведете валиден имейл адрес.", http.StatusUnprocessableEntity)
		return
	}

	settings, err := email.LoadSettings(ctx, a.DB, a.Enc)
	if err != nil {
		a.renderSMTP(w, r, nil, to, "Настройките не могат да бъдат прочетени: "+err.Error(),
			http.StatusUnprocessableEntity)
		return
	}

	// The test is sent directly rather than through the outbox, so the admin
	// sees the SMTP error immediately.
	sender := email.NewSender(settings)
	msg := email.Message{
		To:      []string{to},
		Subject: "Тестов имейл - Модули: заявки",
		BodyHTML: "<p>Това е тестов имейл от приложението за заявки за активация на модули.</p>" +
			"<p>Ако го получавате, SMTP настройките са коректни.</p>",
	}
	if err := sender.Send(ctx, msg); err != nil {
		a.Log.Warn("SMTP test failed", "to", to, "error", err)
		a.renderSMTP(w, r, nil, to,
			"Изпращането е неуспешно. Съобщение от сървъра: "+err.Error(),
			http.StatusUnprocessableEntity)
		return
	}

	a.Audit.Record(ctx, audit.AdminActor(admin.ID, admin.Email), audit.SMTPTestSent,
		to, a.clientIP(r), nil)
	redirectWithFlash(w, r, "/admin/settings/smtp", "test_sent")
}

type templatesView struct {
	view
	Templates []templateRow
}

type templateRow struct {
	Def      email.TemplateDef
	Stored   *store.EmailTemplate
	IsCustom bool
}

func (a *App) handleAdminTemplates(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	stored, err := a.DB.ListEmailTemplates(ctx)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	byKey := make(map[string]store.EmailTemplate, len(stored))
	for _, t := range stored {
		byKey[t.Key] = t
	}

	rows := make([]templateRow, 0, len(email.Defaults))
	for _, d := range email.Defaults {
		row := templateRow{Def: d}
		if t, ok := byKey[d.Key]; ok {
			tt := t
			row.Stored = &tt
			row.IsCustom = t.Subject != d.DefaultSubject || t.BodyHTML != d.DefaultBodyHTML
		}
		rows = append(rows, row)
	}

	v := templatesView{view: a.newView(r, "Имейл шаблони", "templates"), Templates: rows}
	if err := a.render.render(w, http.StatusOK, LayoutAdmin, "admin/templates", v); err != nil {
		a.serverError(w, r, err)
	}
}

type templateEditView struct {
	view
	Def          email.TemplateDef
	Template     store.EmailTemplate
	Placeholders []email.Placeholder
	Warnings     []string
	Errors       []string
	IsCustom     bool
}

func (a *App) handleAdminTemplateEdit(w http.ResponseWriter, r *http.Request) {
	v, ok := a.loadTemplateEdit(w, r)
	if !ok {
		return
	}
	if err := a.render.render(w, http.StatusOK, LayoutAdmin, "admin/template_edit", v); err != nil {
		a.serverError(w, r, err)
	}
}

func (a *App) loadTemplateEdit(w http.ResponseWriter, r *http.Request) (templateEditView, bool) {
	ctx := r.Context()
	var v templateEditView

	key := r.PathValue("key")
	def, ok := email.DefaultByKey(key)
	if !ok {
		a.notFound(w, r)
		return v, false
	}

	t, err := a.DB.EmailTemplateByKey(ctx, key)
	if errors.Is(err, store.ErrNotFound) {
		t = &store.EmailTemplate{Key: key, Subject: def.DefaultSubject, BodyHTML: def.DefaultBodyHTML}
	} else if err != nil {
		a.serverError(w, r, err)
		return v, false
	}

	v = templateEditView{
		view:         a.newView(r, def.Label, "templates"),
		Def:          def,
		Template:     *t,
		Placeholders: email.PlaceholdersFor(key),
		IsCustom:     t.Subject != def.DefaultSubject || t.BodyHTML != def.DefaultBodyHTML,
	}
	return v, true
}

func (a *App) handleAdminTemplateSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	admin := adminFrom(ctx)

	key := r.PathValue("key")
	def, ok := email.DefaultByKey(key)
	if !ok {
		a.notFound(w, r)
		return
	}

	subject := strings.TrimSpace(r.PostFormValue("subject"))
	body := r.PostFormValue("body_html")

	var errs []string
	if subject == "" {
		errs = append(errs, "Въведете тема на имейла.")
	}
	if strings.TrimSpace(body) == "" {
		errs = append(errs, "Въведете съдържание на имейла.")
	}

	if len(errs) > 0 {
		v, ok := a.loadTemplateEdit(w, r)
		if !ok {
			return
		}
		v.Template.Subject, v.Template.BodyHTML = subject, body
		v.Errors = errs
		if err := a.render.render(w, http.StatusUnprocessableEntity, LayoutAdmin, "admin/template_edit", v); err != nil {
			a.serverError(w, r, err)
		}
		return
	}

	if err := a.DB.SaveEmailTemplate(ctx, key, subject, body, dates.NowUTC()); err != nil {
		a.serverError(w, r, err)
		return
	}

	a.Audit.Record(ctx, audit.AdminActor(admin.ID, admin.Email), audit.TemplateUpdated,
		key, a.clientIP(r), nil)

	// An unknown placeholder is not an error: it is left untouched in the
	// body, so the admin is warned rather than blocked.
	unknown := email.UnknownPlaceholders(key, subject, body)
	if len(unknown) == 0 {
		redirectWithFlash(w, r, "/admin/settings/templates/"+key, "template_saved")
		return
	}

	v, ok := a.loadTemplateEdit(w, r)
	if !ok {
		return
	}
	v.Flash = flashMessages["template_saved"]
	v.Warnings = []string{
		"Непознати променливи, които ще останат непроменени в текста: {{" +
			strings.Join(unknown, "}}, {{") + "}}",
	}
	_ = def
	if err := a.render.render(w, http.StatusOK, LayoutAdmin, "admin/template_edit", v); err != nil {
		a.serverError(w, r, err)
	}
}

// handleAdminTemplatePreview renders the current editor content with sample
// values, as an htmx fragment.
func (a *App) handleAdminTemplatePreview(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if _, ok := email.DefaultByKey(key); !ok {
		a.notFound(w, r)
		return
	}

	rendered := email.RenderTemplate(
		r.PostFormValue("subject"),
		r.PostFormValue("body_html"),
		email.SampleValues(key),
	)

	data := map[string]any{
		"Subject": rendered.Subject,
		// The preview is shown as escaped source, never injected as live
		// markup, so an admin's template cannot script the admin panel.
		"BodyHTML": rendered.BodyHTML,
		"BodyText": rendered.BodyText,
	}
	if err := a.render.renderPartial(w, http.StatusOK, "admin/template_edit", "template_preview", data); err != nil {
		a.serverError(w, r, err)
	}
}

func (a *App) handleAdminTemplateRestore(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	admin := adminFrom(ctx)

	key := r.PathValue("key")
	def, ok := email.DefaultByKey(key)
	if !ok {
		a.notFound(w, r)
		return
	}
	if err := a.DB.SaveEmailTemplate(ctx, key, def.DefaultSubject, def.DefaultBodyHTML, dates.NowUTC()); err != nil {
		a.serverError(w, r, err)
		return
	}

	a.Audit.Record(ctx, audit.AdminActor(admin.ID, admin.Email), audit.TemplateRestored,
		key, a.clientIP(r), nil)
	redirectWithFlash(w, r, "/admin/settings/templates/"+key, "template_restored")
}

type scheduleView struct {
	view
	Schedule      *store.ExportSchedule
	RecipientsRaw string
	NextRunAt     string
	LastRunAt     string
	Errors        []string
	SendError     string
}

func (a *App) handleAdminSchedule(w http.ResponseWriter, r *http.Request) {
	a.renderSchedule(w, r, nil, "", http.StatusOK)
}

func (a *App) renderSchedule(w http.ResponseWriter, r *http.Request, errs []string, sendErr string, status int) {
	ctx := r.Context()

	s, err := a.DB.ExportSchedule(ctx)
	if err != nil {
		a.serverError(w, r, err)
		return
	}

	v := scheduleView{
		view:          a.newView(r, "График на справката", "schedule"),
		Schedule:      s,
		RecipientsRaw: strings.Join(s.RecipientList(), "\n"),
		Errors:        errs,
		SendError:     sendErr,
	}
	if s.NextRunAt.Valid {
		v.NextRunAt = dates.DisplayTimestamp(s.NextRunAt.String)
	}
	if s.LastRunAt.Valid {
		v.LastRunAt = dates.DisplayTimestamp(s.LastRunAt.String)
	}

	if err := a.render.render(w, status, LayoutAdmin, "admin/schedule", v); err != nil {
		a.serverError(w, r, err)
	}
}

func (a *App) handleAdminScheduleSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	admin := adminFrom(ctx)

	enabled := checkboxValue(r, "enabled")
	day, dayErr := strconv.Atoi(strings.TrimSpace(r.PostFormValue("day_of_month")))
	hhmm := strings.TrimSpace(r.PostFormValue("time_hhmm"))
	recipientsRaw := r.PostFormValue("recipients")
	groupByClient := checkboxValue(r, "group_by_client")

	var errs []string
	if dayErr != nil || day < 1 || day > 31 {
		errs = append(errs, "Изберете ден от месеца между 1 и 31.")
	}
	if _, _, err := scheduler.ParseHHMM(hhmm); err != nil {
		errs = append(errs, "Въведете час във формат ЧЧ:ММ.")
	}
	recipients, invalid := auth.NormalizeEmailList(recipientsRaw)
	if len(invalid) > 0 {
		errs = append(errs, "Невалидни адреси: "+strings.Join(invalid, ", "))
	}
	if enabled && len(recipients) == 0 {
		errs = append(errs, "Добавете поне един получател, за да включите графика.")
	}

	if len(errs) > 0 {
		a.renderSchedule(w, r, errs, "", http.StatusUnprocessableEntity)
		return
	}

	next, err := scheduler.ComputeNext(&store.ExportSchedule{
		Enabled: enabled, DayOfMonth: day, TimeHHMM: hhmm,
	})
	if err != nil {
		a.renderSchedule(w, r, []string{err.Error()}, "", http.StatusUnprocessableEntity)
		return
	}

	if err := a.DB.SaveExportSchedule(ctx, enabled, day, hhmm, strings.Join(recipients, ", "), groupByClient, next); err != nil {
		a.serverError(w, r, err)
		return
	}

	a.Audit.Record(ctx, audit.AdminActor(admin.ID, admin.Email), audit.ScheduleUpdated,
		"export_schedule", a.clientIP(r), map[string]any{
			"enabled":         enabled,
			"day_of_month":    day,
			"time":            hhmm,
			"recipients":      recipients,
			"group_by_client": groupByClient,
			"next_run_at":     next.String,
		})
	redirectWithFlash(w, r, "/admin/settings/schedule", "schedule_saved")
}

func (a *App) handleAdminScheduleSendNow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	admin := adminFrom(ctx)

	if err := a.Scheduler.SendNow(ctx, audit.AdminActor(admin.ID, admin.Email), a.clientIP(r)); err != nil {
		a.Log.Warn("manual export failed", "error", err)
		a.renderSchedule(w, r, nil, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	redirectWithFlash(w, r, "/admin/settings/schedule", "export_queued")
}

type outboxView struct {
	view
	Messages []store.OutboxMessage
	Page     pagination
	Status   string
	Statuses []statusOption
}

var outboxStatusOptions = []statusOption{
	{"", "Всички"},
	{"pending", "Чакащи"},
	{"sent", "Изпратени"},
	{"failed", "Неуспешни"},
}

func (a *App) handleAdminOutbox(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	status := r.URL.Query().Get("status")
	switch status {
	case "", "pending", "sent", "failed":
	default:
		status = ""
	}

	page := pagination{
		Page:     pageParam(r),
		PageSize: adminPageSize,
		Query:    map[string]string{"status": status},
	}

	msgs, total, err := a.DB.ListOutbox(ctx, status, adminPageSize, page.Offset())
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	page.Total = total

	v := outboxView{
		view:     a.newView(r, "Изходящи имейли", "outbox"),
		Messages: msgs,
		Page:     page,
		Status:   status,
		Statuses: outboxStatusOptions,
	}
	if err := a.render.render(w, http.StatusOK, LayoutAdmin, "admin/outbox", v); err != nil {
		a.serverError(w, r, err)
	}
}

func (a *App) handleAdminOutboxRetry(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	admin := adminFrom(ctx)

	id, ok := idParam(r, "id")
	if !ok {
		a.notFound(w, r)
		return
	}
	if err := a.DB.RetryEmail(ctx, id); err != nil {
		a.serverError(w, r, err)
		return
	}
	a.Outbox.Notify()

	a.Audit.Record(ctx, audit.AdminActor(admin.ID, admin.Email), audit.EmailRetried,
		strconv.FormatInt(id, 10), a.clientIP(r), nil)
	redirectWithFlash(w, r, "/admin/outbox", "email_retried")
}

func (a *App) handleAdminOutboxDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	admin := adminFrom(ctx)

	id, ok := idParam(r, "id")
	if !ok {
		a.notFound(w, r)
		return
	}
	if err := a.DB.DeleteOutboxMessage(ctx, id); err != nil {
		a.serverError(w, r, err)
		return
	}

	a.Audit.Record(ctx, audit.AdminActor(admin.ID, admin.Email), audit.EmailDeleted,
		strconv.FormatInt(id, 10), a.clientIP(r), nil)
	redirectWithFlash(w, r, "/admin/outbox", "email_deleted")
}

type auditView struct {
	view
	Entries []store.AuditEntry
	Page    pagination
	Filter  store.AuditFilter
	Actions []string
}

func (a *App) handleAdminAudit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	f := store.AuditFilter{
		Action:    q.Get("action"),
		ActorType: q.Get("actor"),
		Search:    strings.TrimSpace(q.Get("q")),
		FromDate:  strings.TrimSpace(q.Get("from")),
		ToDate:    strings.TrimSpace(q.Get("to")),
		Limit:     adminPageSize,
	}
	page := pagination{
		Page:     pageParam(r),
		PageSize: adminPageSize,
		Query: map[string]string{
			"action": f.Action, "actor": f.ActorType, "q": f.Search,
			"from": f.FromDate, "to": f.ToDate,
		},
	}
	f.Offset = page.Offset()

	entries, total, err := a.DB.ListAudit(ctx, f)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	page.Total = total

	actions, err := a.DB.DistinctAuditActions(ctx)
	if err != nil {
		a.serverError(w, r, err)
		return
	}

	v := auditView{
		view:    a.newView(r, "Одит", "audit"),
		Entries: entries,
		Page:    page,
		Filter:  f,
		Actions: actions,
	}

	if isHTMX(r) && q.Get("partial") == "1" {
		if err := a.render.renderPartial(w, http.StatusOK, "admin/audit", "audit_table", v); err != nil {
			a.serverError(w, r, err)
		}
		return
	}
	if err := a.render.render(w, http.StatusOK, LayoutAdmin, "admin/audit", v); err != nil {
		a.serverError(w, r, err)
	}
}
