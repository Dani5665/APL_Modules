package http

import (
	"errors"
	"html/template"
	"net/http"
	"strings"
	"time"

	"haynesproform/internal/audit"
	"haynesproform/internal/auth"
	"haynesproform/internal/store"
)

// recoveryCodeCount is how many single-use codes an enrollment produces.
const recoveryCodeCount = 10

type adminLoginView struct {
	view
	Email string
	Error string
}

func (a *App) handleAdminLoginForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if s, err := a.Sessions.Load(ctx, r, auth.AdminAudience); err == nil {
		if s.Stage == store.StageActive {
			http.Redirect(w, r, "/admin", http.StatusSeeOther)
		} else {
			http.Redirect(w, r, "/admin/login/2fa", http.StatusSeeOther)
		}
		return
	}
	a.renderAdminLogin(w, r, "", "", http.StatusOK)
}

func (a *App) renderAdminLogin(w http.ResponseWriter, r *http.Request, email, errMsg string, status int) {
	v := adminLoginView{view: a.newView(r, "Административен вход", "login"), Email: email, Error: errMsg}
	if err := a.render.render(w, status, LayoutBare, "admin/login", v); err != nil {
		a.serverError(w, r, err)
	}
}

func (a *App) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := parseForm(w, r); err != nil {
		a.renderError(w, r, http.StatusBadRequest, "Невалидна заявка", "Заявката не може да бъде прочетена.")
		return
	}

	rawEmail := strings.TrimSpace(r.PostFormValue("email"))
	password := r.PostFormValue("password")
	ip := a.clientIP(r)

	lock, err := a.Throttle.Locked(ctx, auth.AdminAudience.Name, ip, rawEmail)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	if lock.Locked {
		a.Audit.Record(ctx, audit.AnonActor(rawEmail), audit.LoginLocked, rawEmail, ip, nil)
		a.renderAdminLogin(w, r, rawEmail,
			"Твърде много неуспешни опити. Опитайте отново след 15 минути.", http.StatusTooManyRequests)
		return
	}

	email, emailErr := auth.NormalizeEmail(rawEmail)
	var admin *store.Admin
	if emailErr == nil {
		admin, err = a.DB.AdminByEmail(ctx, email)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			a.serverError(w, r, err)
			return
		}
	}

	if admin == nil || !admin.Active || !auth.CheckPassword(admin.PasswordHash, password) {
		if err := a.Throttle.RegisterFailure(ctx, auth.AdminAudience.Name, ip, rawEmail); err != nil {
			a.Log.Error("login failure could not be recorded", "error", err)
		}
		a.Audit.Record(ctx, audit.AnonActor(rawEmail), audit.LoginFailure, rawEmail, ip,
			map[string]any{"area": "admin"})
		a.renderAdminLogin(w, r, rawEmail, "Грешен имейл или парола.", http.StatusUnauthorized)
		return
	}

	if err := a.Throttle.Clear(ctx, auth.AdminAudience.Name, ip, rawEmail); err != nil {
		a.Log.Error("login failures could not be cleared", "error", err)
	}

	// With 2FA disabled (dev only, EXTERNAL_DB_MODE=mock; see DECISIONS.md and
	// config.Admin2FAEnabled's validation), the password alone completes the
	// login. In production this stays mandatory: the session starts owing
	// TOTP and only becomes active once the second factor succeeds.
	if !a.Cfg.Admin2FAEnabled {
		if _, err := a.Sessions.Start(ctx, w, auth.AdminAudience, admin.ID, store.StageActive); err != nil {
			a.serverError(w, r, err)
			return
		}
		a.Audit.Record(ctx, audit.AdminActor(admin.ID, admin.Email), audit.LoginSuccess, admin.Email, ip,
			map[string]any{"factor": "password", "2fa": "disabled"})

		if admin.MustChangePassword {
			http.Redirect(w, r, "/admin/password", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}

	// The password is only the first factor: the session starts owing TOTP.
	if _, err := a.Sessions.Start(ctx, w, auth.AdminAudience, admin.ID, store.StageAwaitingTOTP); err != nil {
		a.serverError(w, r, err)
		return
	}
	a.Audit.Record(ctx, audit.AdminActor(admin.ID, admin.Email), audit.LoginSuccess, admin.Email, ip,
		map[string]any{"factor": "password"})

	if !admin.TOTPEnabled {
		http.Redirect(w, r, "/admin/login/enroll", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/login/2fa", http.StatusSeeOther)
}

type totpView struct {
	view
	Error string
}

func (a *App) handleAdminTOTPForm(w http.ResponseWriter, r *http.Request) {
	admin := adminFrom(r.Context())
	if !admin.TOTPEnabled {
		http.Redirect(w, r, "/admin/login/enroll", http.StatusSeeOther)
		return
	}
	a.renderTOTP(w, r, "", http.StatusOK)
}

func (a *App) renderTOTP(w http.ResponseWriter, r *http.Request, errMsg string, status int) {
	v := totpView{view: a.newView(r, "Двуфакторна автентикация", "login"), Error: errMsg}
	if err := a.render.render(w, status, LayoutBare, "admin/totp", v); err != nil {
		a.serverError(w, r, err)
	}
}

func (a *App) handleAdminTOTPVerify(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	admin := adminFrom(ctx)
	ip := a.clientIP(r)

	if !admin.TOTPEnabled {
		http.Redirect(w, r, "/admin/login/enroll", http.StatusSeeOther)
		return
	}

	code := strings.TrimSpace(r.PostFormValue("code"))
	recovery := strings.TrimSpace(r.PostFormValue("recovery_code"))

	lock, err := a.Throttle.Locked(ctx, "admin_totp", ip, admin.Email)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	if lock.Locked {
		a.renderTOTP(w, r, "Твърде много неуспешни опити. Опитайте отново след 15 минути.",
			http.StatusTooManyRequests)
		return
	}

	ok, usedRecovery, err := a.verifySecondFactor(r, admin, code, recovery)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	if !ok {
		if err := a.Throttle.RegisterFailure(ctx, "admin_totp", ip, admin.Email); err != nil {
			a.Log.Error("2FA failure could not be recorded", "error", err)
		}
		a.Audit.Record(ctx, audit.AdminActor(admin.ID, admin.Email), audit.TOTPFailure, admin.Email, ip, nil)
		a.renderTOTP(w, r, "Невалиден код.", http.StatusUnauthorized)
		return
	}

	if err := a.Throttle.Clear(ctx, "admin_totp", ip, admin.Email); err != nil {
		a.Log.Error("2FA failures could not be cleared", "error", err)
	}

	action := audit.TOTPSuccess
	if usedRecovery {
		action = audit.TOTPRecoveryUsed
	}
	a.Audit.Record(ctx, audit.AdminActor(admin.ID, admin.Email), action, admin.Email, ip, nil)

	// Rotate the session token now that the login is complete, so a token
	// observed during the first factor cannot be reused with full rights.
	if _, err := a.Sessions.Rotate(ctx, w, r, auth.AdminAudience, store.StageActive); err != nil {
		a.serverError(w, r, err)
		return
	}

	if admin.MustChangePassword {
		http.Redirect(w, r, "/admin/password", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// verifySecondFactor checks a TOTP code or a recovery code. It reports
// whether the factor was accepted and whether a recovery code was consumed.
func (a *App) verifySecondFactor(r *http.Request, admin *store.Admin, code, recovery string) (ok bool, usedRecovery bool, err error) {
	ctx := r.Context()

	if recovery != "" {
		used, err := a.DB.UseRecoveryCode(ctx, admin.ID, auth.HashRecoveryCode(recovery))
		return used, used, err
	}
	if code == "" {
		return false, false, nil
	}

	secret, err := a.Enc.Decrypt(admin.TOTPSecretEnc)
	if err != nil {
		return false, false, err
	}
	step, err := auth.VerifyTOTP(secret, code, time.Now())
	if err != nil {
		if errors.Is(err, auth.ErrInvalidTOTPCode) {
			return false, false, nil
		}
		return false, false, err
	}

	// Claiming the step rejects a code that was already used in its window.
	claimed, err := a.DB.ClaimTOTPStep(ctx, admin.ID, step)
	if err != nil {
		return false, false, err
	}
	if !claimed {
		a.Log.Warn("a TOTP code was replayed", "admin", admin.Email, "step", step)
	}
	return claimed, false, nil
}

type enrollView struct {
	view
	// QRDataURI is a data: URI this application builds itself from a PNG it
	// just encoded, so it is marked trusted; html/template would otherwise
	// strip a data: URI out of the src attribute.
	QRDataURI     template.URL
	ManualEntry   string
	Secret        string
	Error         string
	RecoveryCodes []string
}

// handleAdminEnrollForm shows the QR code and secret for first enrollment.
//
// The candidate secret travels in a hidden field rather than being stored, so
// an abandoned enrollment leaves nothing behind. It is written to the
// database only once a valid code confirms it.
func (a *App) handleAdminEnrollForm(w http.ResponseWriter, r *http.Request) {
	admin := adminFrom(r.Context())
	if admin.TOTPEnabled {
		http.Redirect(w, r, "/admin/login/2fa", http.StatusSeeOther)
		return
	}

	enrollment, err := auth.NewEnrollment(admin.Email)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	a.renderEnroll(w, r, enrollment.Secret, enrollment.QRDataURI, enrollment.ManualEntry, "", http.StatusOK)
}

func (a *App) renderEnroll(w http.ResponseWriter, r *http.Request, secret, qr, manual, errMsg string, status int) {
	v := enrollView{
		view:        a.newView(r, "Настройка на двуфакторна автентикация", "login"),
		Secret:      secret,
		QRDataURI:   template.URL(qr),
		ManualEntry: manual,
		Error:       errMsg,
	}
	if err := a.render.render(w, status, LayoutBare, "admin/enroll", v); err != nil {
		a.serverError(w, r, err)
	}
}

func (a *App) handleAdminEnrollConfirm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	admin := adminFrom(ctx)

	if admin.TOTPEnabled {
		http.Redirect(w, r, "/admin/login/2fa", http.StatusSeeOther)
		return
	}

	secret := strings.TrimSpace(r.PostFormValue("secret"))
	code := strings.TrimSpace(r.PostFormValue("code"))
	if secret == "" {
		http.Redirect(w, r, "/admin/login/enroll", http.StatusSeeOther)
		return
	}

	// Re-render the same secret on failure so the admin does not have to scan
	// a new QR code after a mistyped digit.
	reshow := func(msg string) {
		enrollment, err := auth.EnrollmentFromSecret(admin.Email, secret)
		if err != nil {
			a.serverError(w, r, err)
			return
		}
		a.renderEnroll(w, r, secret, enrollment.QRDataURI, enrollment.ManualEntry, msg,
			http.StatusUnprocessableEntity)
	}

	step, err := auth.VerifyTOTP(secret, code, time.Now())
	if err != nil {
		if errors.Is(err, auth.ErrInvalidTOTPCode) {
			reshow("Невалиден код. Опитайте отново.")
			return
		}
		a.serverError(w, r, err)
		return
	}

	sealed, err := a.Enc.Encrypt(secret)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	if err := a.DB.EnrollAdminTOTP(ctx, admin.ID, sealed, step); err != nil {
		a.serverError(w, r, err)
		return
	}

	codes, err := auth.NewRecoveryCodes(recoveryCodeCount)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	hashes := make([]string, len(codes))
	for i, c := range codes {
		hashes[i] = auth.HashRecoveryCode(c)
	}
	if err := a.DB.ReplaceRecoveryCodes(ctx, admin.ID, hashes); err != nil {
		a.serverError(w, r, err)
		return
	}

	a.Audit.Record(ctx, audit.AdminActor(admin.ID, admin.Email), audit.TOTPEnrolled,
		admin.Email, a.clientIP(r), map[string]any{"recovery_codes": len(codes)})

	// Enrollment completes the login, so the session is promoted and its
	// token rotated.
	if _, err := a.Sessions.Rotate(ctx, w, r, auth.AdminAudience, store.StageActive); err != nil {
		a.serverError(w, r, err)
		return
	}

	// The codes are shown exactly once, here.
	v := enrollView{
		view:          a.newView(r, "Кодове за възстановяване", "login"),
		RecoveryCodes: codes,
	}
	v.CSRF = csrfFrom(ctx)
	if err := a.render.render(w, http.StatusOK, LayoutBare, "admin/recovery_codes", v); err != nil {
		a.serverError(w, r, err)
	}
}

func (a *App) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if admin := adminFrom(ctx); admin != nil {
		a.Audit.Record(ctx, audit.AdminActor(admin.ID, admin.Email), audit.Logout,
			admin.Email, a.clientIP(r), nil)
	}
	if err := a.Sessions.Destroy(ctx, w, r, auth.AdminAudience); err != nil {
		a.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

func (a *App) handleAdminPasswordForm(w http.ResponseWriter, r *http.Request) {
	admin := adminFrom(r.Context())
	v := passwordView{view: a.newView(r, "Смяна на парола", "password"), Forced: admin.MustChangePassword}
	if err := a.render.render(w, http.StatusOK, LayoutAdmin, "admin/password", v); err != nil {
		a.serverError(w, r, err)
	}
}

func (a *App) handleAdminPasswordChange(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	admin := adminFrom(ctx)

	renderErr := func(msg string) {
		v := passwordView{
			view:   a.newView(r, "Смяна на парола", "password"),
			Error:  msg,
			Forced: admin.MustChangePassword,
		}
		if err := a.render.render(w, http.StatusUnprocessableEntity, LayoutAdmin, "admin/password", v); err != nil {
			a.serverError(w, r, err)
		}
	}

	if !auth.CheckPassword(admin.PasswordHash, r.PostFormValue("current_password")) {
		renderErr("Текущата парола е грешна.")
		return
	}
	next := r.PostFormValue("new_password")
	if next != r.PostFormValue("confirm_password") {
		renderErr("Двете нови пароли не съвпадат.")
		return
	}
	hash, err := auth.HashPassword(next)
	if err != nil {
		renderErr(err.Error())
		return
	}
	if err := a.DB.SetAdminPassword(ctx, admin.ID, hash, false); err != nil {
		a.serverError(w, r, err)
		return
	}

	a.Audit.Record(ctx, audit.AdminActor(admin.ID, admin.Email), audit.PasswordChanged,
		admin.Email, a.clientIP(r), nil)
	redirectWithFlash(w, r, "/admin", "password_changed")
}
