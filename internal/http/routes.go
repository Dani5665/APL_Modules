package http

import (
	"io/fs"
	"net/http"

	"haynesproform/web"
)

// routeCommon registers the routes that belong to neither part.
func (a *App) routeCommon(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", a.healthz)

	static, err := fs.Sub(web.Static, "static")
	if err != nil {
		panic("embedded static assets are missing: " + err.Error())
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", staticHandler(http.FS(static))))
}

// routeCustomer registers the salesperson-facing routes.
func (a *App) routeCustomer(mux *http.ServeMux) {
	mux.HandleFunc("GET /login", a.handleUserLoginForm)
	mux.HandleFunc("POST /login", a.handleUserLogin)
	mux.HandleFunc("POST /logout", a.requireUser(a.handleUserLogout))

	// Passwords are set by administrators only; there is no user-facing
	// password change or reset.
	mux.HandleFunc("GET /{$}", a.requireUser(a.handleHome))
	mux.HandleFunc("GET /clients/search", a.requireUser(a.handleClientSearch))
	mux.HandleFunc("GET /clients/{code}", a.requireUser(a.handleClient))
	mux.HandleFunc("POST /clients/{code}/request", a.requireUser(a.handleRequestSubmit))
	mux.HandleFunc("GET /modules", a.requireUser(a.handleModules))
	mux.HandleFunc("GET /modules/list", a.requireUser(a.handleModulesList))

	// Anything else under the customer part renders the Bulgarian 404 page.
	mux.HandleFunc("/", a.notFoundOrAdmin)
}

// routeAdmin registers the administrator routes.
func (a *App) routeAdmin(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/login", a.handleAdminLoginForm)
	mux.HandleFunc("POST /admin/login", a.handleAdminLogin)
	mux.HandleFunc("GET /admin/login/2fa", a.requirePendingAdmin(a.handleAdminTOTPForm))
	mux.HandleFunc("POST /admin/login/2fa", a.requirePendingAdmin(a.handleAdminTOTPVerify))
	mux.HandleFunc("GET /admin/login/enroll", a.requirePendingAdmin(a.handleAdminEnrollForm))
	mux.HandleFunc("POST /admin/login/enroll", a.requirePendingAdmin(a.handleAdminEnrollConfirm))
	mux.HandleFunc("POST /admin/logout", a.requireAdmin(a.handleAdminLogout))

	mux.HandleFunc("GET /admin", a.requireAdmin(a.handleAdminHome))
	mux.HandleFunc("GET /admin/{$}", a.requireAdmin(a.handleAdminHome))

	// Requests
	mux.HandleFunc("GET /admin/requests", a.requireAdmin(a.handleAdminRequests))
	mux.HandleFunc("GET /admin/requests/{id}", a.requireAdmin(a.handleAdminRequestDetail))
	mux.HandleFunc("POST /admin/requests/{id}/edit", a.requireAdmin(a.handleAdminRequestEdit))
	mux.HandleFunc("POST /admin/requests/{id}/approve", a.requireAdmin(a.handleAdminRequestApprove))
	mux.HandleFunc("POST /admin/requests/{id}/deny", a.requireAdmin(a.handleAdminRequestDeny))

	// Activations
	mux.HandleFunc("GET /admin/activations", a.requireAdmin(a.handleAdminActivations))
	mux.HandleFunc("GET /admin/activations/new", a.requireAdmin(a.handleAdminActivationNew))
	mux.HandleFunc("POST /admin/activations", a.requireAdmin(a.handleAdminActivationCreate))
	mux.HandleFunc("GET /admin/activations/export.xlsx", a.requireAdmin(a.handleAdminExport))
	mux.HandleFunc("GET /admin/activations/{id}", a.requireAdmin(a.handleAdminActivationEdit))
	mux.HandleFunc("POST /admin/activations/{id}", a.requireAdmin(a.handleAdminActivationUpdate))
	mux.HandleFunc("POST /admin/activations/{id}/revoke", a.requireAdmin(a.handleAdminActivationRevoke))
	mux.HandleFunc("POST /admin/activations/{id}/restore", a.requireAdmin(a.handleAdminActivationRestore))
	mux.HandleFunc("POST /admin/activations/{id}/delete", a.requireAdmin(a.handleAdminActivationDelete))

	// Salespeople
	mux.HandleFunc("GET /admin/users", a.requireAdmin(a.handleAdminUsers))
	mux.HandleFunc("GET /admin/users/new", a.requireAdmin(a.handleAdminUserNew))
	mux.HandleFunc("POST /admin/users", a.requireAdmin(a.handleAdminUserCreate))
	mux.HandleFunc("GET /admin/users/{id}", a.requireAdmin(a.handleAdminUserEdit))
	mux.HandleFunc("POST /admin/users/{id}", a.requireAdmin(a.handleAdminUserUpdate))
	mux.HandleFunc("POST /admin/users/{id}/password", a.requireAdmin(a.handleAdminUserPassword))
	mux.HandleFunc("POST /admin/users/{id}/active", a.requireAdmin(a.handleAdminUserSetActive))
	mux.HandleFunc("POST /admin/users/{id}/delete", a.requireAdmin(a.handleAdminUserDelete))

	// Administrators
	mux.HandleFunc("GET /admin/admins", a.requireAdmin(a.handleAdminAdmins))
	mux.HandleFunc("POST /admin/admins", a.requireAdmin(a.handleAdminAdminCreate))
	mux.HandleFunc("POST /admin/admins/{id}/password", a.requireAdmin(a.handleAdminAdminPassword))
	mux.HandleFunc("POST /admin/admins/{id}/reset-2fa", a.requireAdmin(a.handleAdminAdminReset2FA))
	mux.HandleFunc("POST /admin/admins/{id}/delete", a.requireAdmin(a.handleAdminAdminDelete))

	// Stores
	mux.HandleFunc("GET /admin/stores", a.requireAdmin(a.handleAdminStores))
	mux.HandleFunc("POST /admin/stores", a.requireAdmin(a.handleAdminStoreCreate))
	mux.HandleFunc("POST /admin/stores/{id}", a.requireAdmin(a.handleAdminStoreUpdate))
	mux.HandleFunc("POST /admin/stores/{id}/delete", a.requireAdmin(a.handleAdminStoreDelete))

	// Settings
	mux.HandleFunc("GET /admin/settings/smtp", a.requireAdmin(a.handleAdminSMTP))
	mux.HandleFunc("POST /admin/settings/smtp", a.requireAdmin(a.handleAdminSMTPSave))
	mux.HandleFunc("POST /admin/settings/smtp/test", a.requireAdmin(a.handleAdminSMTPTest))

	mux.HandleFunc("GET /admin/settings/templates", a.requireAdmin(a.handleAdminTemplates))
	mux.HandleFunc("GET /admin/settings/templates/{key}", a.requireAdmin(a.handleAdminTemplateEdit))
	mux.HandleFunc("POST /admin/settings/templates/{key}", a.requireAdmin(a.handleAdminTemplateSave))
	mux.HandleFunc("POST /admin/settings/templates/{key}/preview", a.requireAdmin(a.handleAdminTemplatePreview))
	mux.HandleFunc("POST /admin/settings/templates/{key}/restore", a.requireAdmin(a.handleAdminTemplateRestore))

	mux.HandleFunc("GET /admin/settings/schedule", a.requireAdmin(a.handleAdminSchedule))
	mux.HandleFunc("POST /admin/settings/schedule", a.requireAdmin(a.handleAdminScheduleSave))
	mux.HandleFunc("POST /admin/settings/schedule/send-now", a.requireAdmin(a.handleAdminScheduleSendNow))

	// Outbox and audit
	mux.HandleFunc("GET /admin/outbox", a.requireAdmin(a.handleAdminOutbox))
	mux.HandleFunc("POST /admin/outbox/{id}/retry", a.requireAdmin(a.handleAdminOutboxRetry))
	mux.HandleFunc("POST /admin/outbox/{id}/delete", a.requireAdmin(a.handleAdminOutboxDelete))
	mux.HandleFunc("GET /admin/audit", a.requireAdmin(a.handleAdminAudit))

	mux.HandleFunc("GET /admin/password", a.requireAdmin(a.handleAdminPasswordForm))
	mux.HandleFunc("POST /admin/password", a.requireAdmin(a.handleAdminPasswordChange))
}

// notFoundOrAdmin renders the 404 page for anything unmatched.
func (a *App) notFoundOrAdmin(w http.ResponseWriter, r *http.Request) {
	a.notFound(w, r)
}
