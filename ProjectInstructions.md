# Build Prompt: Module Activation Request Portal

You are a senior Go engineer. Build the complete, production-ready web application described below. Read the whole document before writing code. Where a section is marked **[PLACEHOLDER]**, implement the surrounding code behind a clean interface with a working mock implementation, and leave a clearly marked `TODO(PLACEHOLDER-X)` so the real logic can be dropped in later. Do not invent business rules beyond what is written; if something is truly ambiguous, choose the simplest option and list it in `DECISIONS.md`.

---

## 1. Goals and constraints

- Two parts in one application: a **customer (salesperson) part** and an **admin part**.
- Delivered as **one small, fast Docker container**.
- Absolute maximum load: 2,000 active salespeople with at most 2 accounts each (≈4,000 accounts), a few hundred requests per day. Design for simplicity, not horizontal scale. Single instance.
- **UI language: Bulgarian only.** All labels, messages, validation errors, emails and Excel headers are in Bulgarian. Code, comments, logs and README are in English.
- Dates shown as `DD.MM.YYYY`. Time zone: `Europe/Sofia` for all business logic (activation dates, scheduler).
- TLS is terminated by an existing reverse proxy. The app listens on plain HTTP inside the container.
- Runs on the owner's own server.

## 2. Technology stack (use exactly this)

| Concern | Choice | Notes |
|---|---|---|
| Language | **Go 1.27.x** (latest patch) | Single static binary, `CGO_ENABLED=0`. |
| HTTP | Standard library `net/http` with Go 1.22+ routing patterns (`GET /admin/users/{id}`) | No web framework. Small hand-written middleware chain. |
| Templates | `html/template`, templates and static files embedded with `embed` | Server-rendered pages. |
| Interactivity | **htmx 2.0.10** (pinned, vendored into `static/`) + **Alpine.js 3.15.x** (pinned, vendored) | Use htmx 2, not htmx 4 (htmx 4 was released on 28 Aug 2026 and changes attribute inheritance and event names; stay on the stable 2.x line). No CDN at runtime; no npm build step. |
| CSS | One small hand-written CSS file (or vendored classless Pico CSS) | No Tailwind build. Clean, responsive, accessible. |
| Local database | **SQLite** via `modernc.org/sqlite` (pure Go, CGO-free; latest v1.59.x) | WAL mode, `busy_timeout`, foreign keys ON. File on a Docker volume at `/data/app.db`. |
| External database | **`github.com/microsoft/go-mssqldb`** (latest v1.11.x) | **Read-only.** Parameterized queries only. |
| Excel | **`github.com/xuri/excelize/v2` v2.11.0 or newer** | v2.11.0 fixes a published DoS vulnerability (GO-2026-5960); do not use older versions. |
| Email | `github.com/wneessen/go-mail` (latest) | SMTP with none/STARTTLS/implicit TLS. |
| TOTP 2FA | `github.com/pquerna/otp` (latest) | QR code as inline PNG data URI. |
| Password hashing | `golang.org/x/crypto/bcrypt` (cost 12) or argon2id | |
| Migrations | Embedded numbered `.sql` files + a `schema_migrations` table, applied at startup | No external migration tool. |
| Time zone data | `import _ "time/tzdata"` | Container has no system tzdata. |

Keep dependencies to this list plus the Go standard library unless something is truly unavoidable (justify in `DECISIONS.md`).

## 3. Roles and authentication

There are two completely separate account types stored in separate tables, with separate login pages and separate session cookies:

1. **User (salesperson)** – uses the customer part.
2. **Admin** – uses the admin part. There can be multiple admins. All admins are equal and can see and manage everything.

### 3.1 Common rules
- Sessions stored server-side in SQLite (random 32-byte token, only its SHA-256 hash stored). Cookie: `HttpOnly`, `Secure` (configurable off for local dev), `SameSite=Lax`, path-scoped (`/` for users, `/admin` for admins). Idle timeout and absolute timeout configurable via env.
- CSRF protection on every state-changing request (synchronizer token in a hidden field and in an `hx-headers` meta for htmx requests).
- Login rate limiting per IP and per username (e.g. 5 failures → 15-minute lockout). Client IP taken from `X-Forwarded-For` **only** when the request comes from a trusted proxy CIDR set in env.
- Security headers: CSP (self only, no inline scripts except Alpine's requirement — use the Alpine CSP build if feasible), `X-Content-Type-Options`, `Referrer-Policy`, `X-Frame-Options: DENY`.
- No self-service registration and **no "forgot password"** for anyone. Passwords are set by admins.
- Minimum password length 10.

### 3.2 Users (salespeople)
- **Username must be a valid email address.** Validate with `net/mail.ParseAddress`, require that the parsed address equals the trimmed input, that it contains exactly one `@`, and a domain with a dot. Store lowercased. Unique.
- Each user has **one or more stores** (many-to-many), selected by the admin from the store list with checkboxes. A user sees clients of all their stores.
- Users can be created, deleted (soft-delete/deactivate is acceptable, but must block login), and have their password reset by an admin. Optionally force password change on next login after an admin reset.

### 3.3 Admins
- Username is also an email (same validation).
- **Mandatory TOTP 2FA:**
  - On first successful password login, the admin must enroll: show QR code + manual secret, require a valid 6-digit code to confirm.
  - Generate **10 single-use recovery codes**, shown once, stored hashed.
  - Every later login: password, then TOTP code (or a recovery code). Allow ±1 time step skew. Prevent reuse of the same code within its window.
  - Any admin can **reset another admin's 2FA** (forces re-enrollment) and reset their password.
- Admins can create/delete other admins. An admin cannot delete themself, and the system must always keep at least one active admin.
- **Bootstrap:** on first start, if no admin exists, create one from env vars `BOOTSTRAP_ADMIN_EMAIL` and `BOOTSTRAP_ADMIN_PASSWORD` (must enroll 2FA on first login). Log a warning if these env vars remain set after an admin exists.

## 4. External MSSQL database (read-only)

Connection string from env `MSSQL_DSN`. Use a small pool (max ~5 open connections), connection and query timeouts (e.g. 5 s), and `context` cancellation. Recommend in README that the SQL login has read-only permissions.

Isolate **all** external access behind a Go interface in `internal/external/`, e.g.:

```go
type Directory interface {
    // Salesperson from the URL (IS_WHO_SALER = 1).
    GetSaler(ctx context.Context, login string) (*Saler, error)          // includes store
    // Client by 9-digit code.
    GetClientByCode(ctx context.Context, code string) (*Client, error)   // name, object, store
    // Client's own usernames (IS_WHO_SALER = 0).
    ListClientLogins(ctx context.Context, mandantName string) ([]string, error)
    // All clients belonging to the given stores (for the informational list).
    ListClientsByStores(ctx context.Context, storeValues []string) ([]Client, error)
}
```

Provide two implementations: `mssql` (real) and `mock` (seeded in-memory data for development and tests), selected by env `EXTERNAL_DB_MODE=mssql|mock`. Keep all SQL strings for the real implementation in one file so they are easy to adjust.

Cache `ListClientsByStores` results in memory for a short configurable TTL (default 5 min). Do not cache the per-client data used on the request form longer than 60 s.

If MSSQL is unreachable, show a friendly Bulgarian error page (`Външната база данни не е достъпна. Опитайте отново по-късно.`) and log the error; do not crash.

### 4.1 Known table
```sql
SELECT [ID], [LOGIN], [MANDANT_NAME], [IS_WHO_SALER]
FROM [STORE_IT_APL_PROD].[dbo].[CATALOG_USERS]
```
- `LOGIN` – a username. For a salesperson it is the username that arrives in the URL. For a client it is one of the client's usernames.
- `MANDANT_NAME` – the client's name. **A client = one `MANDANT_NAME`.** Multiple rows share the same `MANDANT_NAME`; they are the client's usernames.
- `IS_WHO_SALER` – `1`/true for salespeople (users), `0`/false for client usernames.

Client usernames for the form dropdown:
```sql
SELECT [LOGIN] FROM [STORE_IT_APL_PROD].[dbo].[CATALOG_USERS]
WHERE [MANDANT_NAME] = @p1 AND [IS_WHO_SALER] = 0
ORDER BY [LOGIN]
```

### 4.2 **[PLACEHOLDER-B] Clients table**
A separate Clients table (not yet identified) holds the **9-digit client code**, the **client name** (matches `MANDANT_NAME`), the **client's object (Обект на клиента)** and the **store** the client is tied to. Implement `GetClientByCode` and `ListClientsByStores` in the mock fully; in the MSSQL implementation leave the SQL as `TODO(PLACEHOLDER-B)` returning a clear "not configured" error.

### 4.3 **[PLACEHOLDER-C] Salesperson's store**
The store of the salesperson (the `LOGIN` from the URL) comes from the external DB. The exact column/table is not yet known. Implement in the mock; mark the MSSQL query `TODO(PLACEHOLDER-C)`.

## 5. Stores (local)

- Admin-editable list of stores (e.g. `Магазин София`, `Магазин Варна`).
- Fields: `name` (display, unique) and `external_value` (the exact value as it appears in the external DB; defaults to `name`). The admin is responsible for keeping these matched with the external DB — no automatic sync.
- A store assigned to users cannot be hard-deleted; offer deactivation instead (hidden from new assignments).

## 6. Customer part (salesperson)

### 6.1 Entry link — **[PLACEHOLDER-A]**
The parent application shows a button that opens a link to this app carrying a **9-digit client code** and the **salesperson's username** (the external `LOGIN`). The exact URL format and any signature/expiry validation will be specified later.

Implement it as a pluggable `EntryLinkParser` interface:
```go
type EntryLink struct { ClientCode string; SalerLogin string }
type EntryLinkParser interface { Parse(r *http.Request) (EntryLink, error) }
```
- Provide a temporary implementation for route `GET /r/{code}/{login}` with validation: code is exactly 9 digits; login non-empty, max 200 chars.
- Mark `TODO(PLACEHOLDER-A)` for the final format and any HMAC signature/expiry check.
- Store the parsed `EntryLink` in a short-lived pending state (server-side, tied to a pre-session cookie), then redirect to the login page. After login, continue to the request form. If the user is already logged in, go straight to the form.
- Visiting the app without an entry link, after login, shows only the informational client list (section 6.4) and a message that requests are started from the parent application.

### 6.2 Authorization after login
The app account (email) and the external salesperson `LOGIN` are **not** linked by name. They are linked **by store**:
1. Look up the salesperson by `SalerLogin` in the external DB; it must exist with `IS_WHO_SALER = 1`.
2. Get the salesperson's store (PLACEHOLDER-C).
3. That store's value must match the `external_value` of one of the logged-in account's stores.
4. Look up the client by `ClientCode` (PLACEHOLDER-B). The client's store must also be one of the account's stores.

If any check fails, show a Bulgarian error page (e.g. `Нямате достъп до този клиент.`) and write an audit log entry. Never reveal client data on failure.

### 6.3 Request form page
Layout top to bottom:

**A. Form**

| Field | Behaviour |
|---|---|
| **Клиентски номер** | Read-only, from the URL code. |
| **Име на клиент** | Read-only, from external DB. |
| **Обект на клиента** | Read-only, from external DB. |
| **Потребители** | Multi-select dropdown with checkboxes (searchable if more than ~10 items) listing the client's usernames (`IS_WHO_SALER = 0`). At least one required. Include "select all". |
| **Тест период** (checkbox) | When checked: **Fast Calculator** checked and disabled; **HaynesPro** checked and disabled with tier **Business** selected and disabled; **Активация за** set to **1 месец** and disabled. Unchecking restores the previous editable state. **Disabled if the client already used a test period** (see 6.5), with an explanatory note: `Тестовият период вече е използван за този клиент.` |
| **Fast Calculator** (checkbox) | |
| **HaynesPro** (checkbox) | When checked, a tier dropdown appears: `Business`, `Pro`, `Ultra` (required). Hidden when unchecked. |
| **Активация от дата** (date picker) | Default today (Europe/Sofia). Past dates not allowed (`min` attribute and server-side check). |
| **Активация за** (dropdown) | 12 options: `1 месец`, `2 месеца`, `3 месеца`, `4 месеца`, `5 месеца`, `6 месеца`, `7 месеца`, `8 месеца`, `9 месеца`, `10 месеца`, `11 месеца`, `1 година`. Stored as integer months 1–12. |
| **Изпрати запитване** (button) | Submits via htmx; shows success message in place and resets the form. Prevent double submission. |

Implement the interactive logic with Alpine.js. **Disabled inputs are not submitted by browsers** — mirror values into hidden inputs, and above all **re-apply and enforce every rule on the server**: when `test_period=true`, the server forces Fast Calculator + HaynesPro Business + 1 month regardless of posted values. At least one module must be selected. The selected modules apply equally to all selected usernames.

**B. Client information** (below the form)
- Client name.
- Table of the client's usernames with their **currently active modules** from the local DB: module, tier, from date, to date. Usernames without activations show `Няма активни модули`.
- Also show this client's **pending** requests (date submitted, users, modules) so duplicates are visible.

**C. Clients of my stores** (below that)
- Informational, searchable list of all clients tied to the account's stores (from external DB): client code, client name, client object, store.
- Server-side search (htmx with debounce, `hx-trigger="input changed delay:300ms"`) over code, name and object; paginated (e.g. 50 per page). Not clickable.

### 6.4 On submit
1. Validate (server side, all rules above plus a re-check of authorization from 6.2).
2. Store the request with status `pending`, snapshotting: client code, client name, client object, client store, submitting account, saler login, selected usernames, modules/tiers, test-period flag, start date, months.
3. Send the **"new request" email** to the fixed admin notification recipients (configured in admin settings).
4. Write an audit log entry.
Email sending must not block or fail the request: queue it in a local `email_outbox` table with a background worker and retries (exponential backoff, max attempts), with status visible to admins.

### 6.5 Test period rule
A test period may be used **only once per client** (per 9-digit client code). It counts as used if the client has any test-period request with status `pending` or `approved`. Enforce on the server (and in the DB with a check at approve time). A denied test-period request does not consume it.

## 7. Modules (fixed)

Modules are **fixed in code; admins cannot add modules.**
- `FAST_CALCULATOR` – label `Fast Calculator`, no tier.
- `HAYNESPRO` – label `HaynesPro`, tier required: `BUSINESS`, `PRO`, `ULTRA` (labels `Business`, `Pro`, `Ultra`).

Define them in one Go file so they can be changed in code later.

## 8. Admin part (`/admin`)

Separate login (section 3.3). Not linked anywhere from the customer part. Pages:

### 8.1 Requests
- List with filters (status: `pending` / `approved` / `denied`, client code/name search, date range), newest first, pending count badge in the navigation.
- Detail view showing all snapshotted data and the client's current activations.
- The admin can **edit** the request before deciding: selected usernames (from the client's current external list), modules, tier, start date, months, test period flag (same rules as the form).
- **Approve** (`Одобри`) or **Deny** (`Откажи`) — purely at the admin's judgement; an optional comment field whose value is available as an email placeholder.
- On approve, inside one transaction: create one activation row per (username × module) with start date and end date, set status, record who and when. If a module is already active for that username in the overlapping period, show a warning before approval (`Потребителят вече има активен модул за този период`) but allow the admin to proceed.
- On approve/deny: send the corresponding email **to the submitting account's email only**.
- **End date rule:** `end = start + N months − 1 day` (inclusive). An activation is **active** when `start ≤ today ≤ end` and it is not revoked. Status is computed from dates, so expiry happens automatically without a job.

### 8.2 Clients and module activations (CRUD)
- List all local activations grouped by client (code, name), filterable by status (active / expired / revoked / all), store, module, search.
- Create an activation manually (client code, client name, client object, store, username, module, tier, start date, end date or months), edit, revoke, delete.
- **[PLACEHOLDER-D] Client records:** whether the admin can also create/edit client records that do not exist in the external DB is not decided yet. For now, store client data as fields on activations/requests (code, name, object, store) and allow editing them there. Mark `TODO(PLACEHOLDER-D)`.
- **Export to Excel** button (section 9).

### 8.3 Users (salespeople)
- List (search by email, filter by store), create (email + password + stores checkboxes), edit stores, reset password, deactivate/delete.

### 8.4 Admins
- List, create (email + password), reset password, reset 2FA, delete (with the rules in 3.3).

### 8.5 Stores
- CRUD per section 5.

### 8.6 SMTP settings
- Host, port, security (`Няма`, `STARTTLS`, `SSL/TLS`), username, password, from address, from name.
- **Admin notification recipients** for new requests: list of emails (each validated).
- The SMTP password is encrypted at rest (AES-GCM) with a key from env `APP_ENCRYPTION_KEY` (32 bytes, base64); the password is never shown back in the UI (leave blank = unchanged).
- **Send test email** button to an address entered by the admin, showing the SMTP error in Bulgarian + raw error if it fails.

### 8.7 Email templates
Editable templates (subject + HTML body, with a plain-text body auto-generated from HTML) for these events:
1. `request_created` – to admin recipients.
2. `request_approved` – to submitter.
3. `request_denied` – to submitter.
4. `scheduled_export` – to the export schedule recipients (Excel attached).

Placeholders use the syntax `{{client_code}}` and are replaced by simple string substitution with **HTML-escaped** values (do not execute admin input as Go templates). Unknown placeholders are left untouched and flagged in a validation warning on save.

Each template editor has an **info (ⓘ) button** that opens a panel listing the placeholders available for that template with a Bulgarian description and an example value. At minimum:

| Placeholder | Description |
|---|---|
| `{{client_code}}` | Клиентски номер |
| `{{client_name}}` | Име на клиент |
| `{{client_object}}` | Обект на клиента |
| `{{client_store}}` | Магазин |
| `{{usernames}}` | Избрани потребители (списък) |
| `{{modules}}` | Модули и нива |
| `{{test_period}}` | Да / Не |
| `{{start_date}}` | Активация от дата |
| `{{end_date}}` | Активация до дата |
| `{{duration}}` | Активация за (напр. „3 месеца“) |
| `{{submitter_email}}` | Потребител, подал запитването |
| `{{request_id}}` | Номер на запитването |
| `{{request_date}}` | Дата на подаване |
| `{{admin_comment}}` | Коментар на администратора (одобрение/отказ) |
| `{{decided_by}}` | Администратор, взел решението |
| `{{request_link}}` | Линк към запитването в админ панела (само за `request_created`) |
| `{{export_date}}` | Дата на справката (само за `scheduled_export`) |
| `{{active_count}}` | Брой активни модули в справката (само за `scheduled_export`) |

Provide sensible default Bulgarian templates seeded by migration, a **preview** with sample data, and a "restore default" action.

### 8.8 Export schedule
- Enable/disable toggle.
- **Monthly** only: day of month (1–31) and time (HH:MM, Europe/Sofia). If the month is shorter than the chosen day, run on the last day of that month.
- Recipients: list of emails (each validated).
- Shows last run time, last result (success / error message) and next run time.
- **Send now** button for testing.

### 8.9 Audit log
Read-only, filterable, paginated list of: logins (success/failure) for users and admins, 2FA events, request submissions, approvals/denials/edits, activation CRUD, user/admin/store CRUD, password and 2FA resets, SMTP and template changes, schedule changes, exports (manual and scheduled). Store actor, action, target, timestamp, IP, and a JSON detail field. Never log passwords, TOTP secrets or codes.

## 9. Excel export

- Contains **only currently active activations** (per the rule in 8.1).
- **Sorted by client** (client name, then client code), then username, then module.
- Columns with Bulgarian headers, in this order: `Клиентски номер`, `Име на клиент`, `Обект на клиента`, `Магазин`, `Потребител`, `Модул`, `Ниво`, `От дата`, `До дата`, `Статус`.
- Bold, frozen header row, auto-filter, reasonable column widths, dates as real Excel dates formatted `DD.MM.YYYY`.
- Filename: `aktivni_moduli_YYYY-MM-DD.xlsx`.
- Use excelize's streaming writer. Same generator is used for the manual download and the scheduled email.

## 10. Scheduler

- A single in-process goroutine that ticks every minute, reads the schedule from SQLite, and runs the export when `now >= next_run_at`. After running, compute and store the next `next_run_at`.
- **Missed runs** (container was down at the scheduled time): on startup, if `next_run_at` is in the past, run once, then schedule the next one. Never send more than one catch-up.
- Guard against double sends with a DB-level "claim" (update `next_run_at` in the same transaction before sending).
- No cron library needed.

## 11. Local data model (guide, adjust as needed)

- `admins` (id, email, password_hash, totp_secret_enc, totp_enabled, last_totp_step, must_change_password, active, created_at)
- `admin_recovery_codes` (id, admin_id, code_hash, used_at)
- `users` (id, email, password_hash, must_change_password, active, created_at)
- `stores` (id, name, external_value, active)
- `user_stores` (user_id, store_id)
- `sessions` (token_hash, subject_type, subject_id, csrf_token, created_at, last_seen_at, expires_at, stage — e.g. `awaiting_totp`)
- `pending_entry_links` (token_hash, client_code, saler_login, expires_at)
- `requests` (id, client_code, client_name, client_object, client_store, submitter_user_id, saler_login, test_period, start_date, months, status, admin_comment, decided_by_admin_id, decided_at, created_at, updated_at)
- `request_usernames` (request_id, username)
- `request_modules` (request_id, module, tier)
- `activations` (id, client_code, client_name, client_object, client_store, username, module, tier, start_date, end_date, revoked_at, source_request_id nullable, created_by_admin_id, created_at, updated_at)
- `settings` (key, value) for SMTP and admin notification recipients
- `email_templates` (key, subject, body_html, updated_at)
- `email_outbox` (id, template_key, recipients, subject, body_html, attachment_blob nullable, status, attempts, last_error, next_attempt_at, created_at)
- `export_schedule` (id=1, enabled, day_of_month, time_hhmm, recipients, last_run_at, last_result, next_run_at)
- `audit_log` (id, actor_type, actor_id, action, target, ip, details_json, created_at)

Indexes on `activations(client_code)`, `activations(username)`, `activations(end_date)`, `requests(status, created_at)`, `audit_log(created_at)`.

## 12. Configuration (env vars)

`APP_ADDR` (default `:8080`), `APP_BASE_URL`, `DATA_DIR` (default `/data`), `MSSQL_DSN`, `EXTERNAL_DB_MODE`, `APP_ENCRYPTION_KEY`, `SESSION_IDLE_TIMEOUT`, `SESSION_ABSOLUTE_TIMEOUT`, `COOKIE_SECURE` (default true), `TRUSTED_PROXY_CIDRS`, `BOOTSTRAP_ADMIN_EMAIL`, `BOOTSTRAP_ADMIN_PASSWORD`, `CLIENT_LIST_CACHE_TTL`, `LOG_LEVEL`. Fail fast with a clear message if a required variable is missing or invalid. Provide `.env.example`.

## 13. Docker

- Multi-stage `Dockerfile`: build with `golang:1.27-alpine` (`CGO_ENABLED=0`, `-trimpath`, `-ldflags "-s -w"`), run on `gcr.io/distroless/static-debian12:nonroot`.
- Target image size under ~30 MB; idle memory well under 50 MB.
- `VOLUME /data`; runs as non-root; SQLite file and WAL in `/data`.
- `GET /healthz` endpoint (checks SQLite; reports MSSQL reachability separately without failing health). Because distroless has no shell/curl, the binary supports `app healthcheck` for the Docker `HEALTHCHECK`.
- Graceful shutdown on SIGTERM (finish in-flight requests, stop scheduler and outbox worker).
- `docker-compose.yml` example with the volume, env file and a comment showing a reverse proxy in front.
- Structured JSON logs to stdout (`log/slog`).
- **Backups:** add a daily online SQLite backup (`VACUUM INTO`) to `/data/backups/`, keeping the last 14 files, and document how to copy them off the server.

## 14. Project layout

```
cmd/app/main.go
internal/config/
internal/http/          (router, middleware, handlers: customer/, admin/)
internal/auth/          (sessions, passwords, csrf, totp, ratelimit)
internal/store/         (SQLite repositories, migrations/)
internal/external/      (Directory interface, mssql/, mock/)
internal/entrylink/     (EntryLinkParser — PLACEHOLDER-A)
internal/modules/       (fixed module definitions)
internal/requests/      (business rules: validation, test period, approve/deny)
internal/email/         (smtp sender, templates, placeholders, outbox worker)
internal/export/        (excel generator)
internal/scheduler/
internal/audit/
web/templates/          (layouts, customer, admin, partials)
web/static/             (htmx.min.js, alpine.min.js, app.css, app.js)
Dockerfile, docker-compose.yml, .env.example, README.md, DECISIONS.md
```

## 15. Testing and quality

- Unit tests for: email validation, test-period rules (including server-side forcing), end-date calculation (month-end edge cases, e.g. 31 Jan + 1 month), active-status computation, schedule next-run calculation (short months, catch-up), placeholder replacement and escaping, authorization-by-store logic, TOTP verification and replay prevention.
- Handler tests for form submission and approve/deny using the mock external directory.
- `go vet` and `staticcheck` clean. `govulncheck` clean.
- The app must run fully with `EXTERNAL_DB_MODE=mock` and no SMTP server (outbox shows failures) so it can be demoed end to end.

## 16. Deliverables

1. Complete source code per the layout above.
2. `README.md`: running locally, running with Docker, env vars, first admin bootstrap and 2FA enrollment, reverse proxy notes, backups, where each PLACEHOLDER lives and what must be supplied.
3. `DECISIONS.md`: any choice you made that was not specified.
4. Seeded mock data: 3 stores, 2 salespeople with stores, ~20 clients across stores, several usernames per client, some activations (active, expired) and requests in each status.

## 17. Open placeholders summary

| ID | Topic | Status |
|---|---|---|
| PLACEHOLDER-A | Entry link URL format and signature/expiry validation | To be specified |
| PLACEHOLDER-B | External Clients table: 9-digit code, client name, client object, store | Table not yet identified |
| PLACEHOLDER-C | Source of the salesperson's store in the external DB | To be specified |
| PLACEHOLDER-D | Whether admins can create/edit standalone client records | To be decided |
