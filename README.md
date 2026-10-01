# Module Activation Request Portal

A small Go web application with two parts in one binary:

- a **customer part** where a salesperson files a request to activate modules
  (Fast Calculator, HaynesPro) for a client's usernames, and
- an **admin part** where administrators approve or deny those requests, manage
  activations, accounts, stores, email settings and the monthly Excel report.

The **user interface is Bulgarian only**. Code, comments, logs and this README
are in English. Dates are shown as `DD.MM.YYYY` and all business logic runs in
`Europe/Sofia`.

Everything ships as one static binary in a distroless container: templates,
stylesheet, htmx and Alpine are embedded, there is no build step for the
front end and nothing is fetched from a CDN at runtime.

---

## Contents

- [Quick start (local, no external dependencies)](#quick-start-local-no-external-dependencies)
- [Running with Docker](#running-with-docker)
- [Configuration](#configuration)
- [First administrator and 2FA](#first-administrator-and-2fa)
- [How a request flows through the system](#how-a-request-flows-through-the-system)
- [The external database](#the-external-database)
- [Placeholders — all resolved](#placeholders--all-resolved)
- [Entry link format](#entry-link-format)
- [Reverse proxy](#reverse-proxy)
- [Backups](#backups)
- [Email](#email)
- [The monthly report](#the-monthly-report)
- [Development](#development)
- [Project layout](#project-layout)

---

## Quick start (local, no external dependencies)

The application runs end to end with a mock external directory and no SMTP
server, which is enough to walk through the whole flow.

```bash
export APP_BASE_URL=http://localhost:8080
export APP_ADDR=:8080
export DATA_DIR=./data
export EXTERNAL_DB_MODE=mock
export COOKIE_SECURE=false
export APP_ENCRYPTION_KEY="$(openssl rand -base64 32)"
export BOOTSTRAP_ADMIN_EMAIL=admin@example.com
export BOOTSTRAP_ADMIN_PASSWORD=admin-parola-2026

go run ./cmd/app
```

On Windows PowerShell:

```powershell
$env:APP_BASE_URL       = "http://localhost:8080"
$env:APP_ADDR           = ":8080"
$env:DATA_DIR           = "./data"
$env:EXTERNAL_DB_MODE   = "mock"
$env:COOKIE_SECURE      = "false"
$env:APP_ENCRYPTION_KEY = [Convert]::ToBase64String((1..32 | ForEach-Object { Get-Random -Max 256 }))
$env:BOOTSTRAP_ADMIN_EMAIL    = "admin@example.com"
$env:BOOTSTRAP_ADMIN_PASSWORD = "admin-parola-2026"

go run ./cmd/app
```

With `EXTERNAL_DB_MODE=mock` and an empty database, demo data is seeded on
first start: 3 stores, 2 salespeople, ~20 clients with several usernames each,
and activations and requests in every status. The demo data never loads
against a real external database.

Then:

| What | Where |
|---|---|
| Salesperson login | <http://localhost:8080/login> — `ivan.petrov@example.com` / `demo-parola-2026` |
| | or `maria.dimitrova@example.com` / `demo-parola-2026` |
| A request form | <http://localhost:8080/r/100000001/ivan.petrov> |
| Admin login | <http://localhost:8080/admin/login> — the bootstrap admin above |
| Health | <http://localhost:8080/healthz> |

`ivan.petrov` covers Sofia (client codes `100000001`–`100000007`);
`maria.dimitrova` covers Varna and Plovdiv (`100000008`–`100000020`).

Try `/r/100000008/ivan.petrov` to see the authorization refusal: the client
belongs to a store the account does not cover.

---

## Running with Docker

**Production runs the image GitHub Actions already built and tested, not a
local build.** `.github/workflows/docker-publish.yml` builds and pushes
`ghcr.io/dani5665/apl_modules` on every push to `master` (after `go vet`,
staticcheck, govulncheck and `go test` all pass — the image is never
published otherwise), and `docker-compose.yml`'s `app` service runs that
image by name. A production host only ever needs to:

```bash
cp .env.example .env
# Edit .env. At minimum set APP_BASE_URL, APP_ENCRYPTION_KEY, ENTRY_LINK_SECRET,
# MSSQL_DSN, TRUSTED_PROXY_CIDRS and the two BOOTSTRAP_ADMIN_* variables.

docker compose pull
docker compose up -d
docker compose logs -f app
```

The `ghcr.io/dani5665/apl_modules` package is private, same as the repo:
`docker compose pull` needs `docker login ghcr.io` first with a personal
access token that has the `read:packages` scope, and that account needs to
actually be granted access to the package (repo collaborators usually
inherit it; otherwise grant it directly under the package's own settings).

`docker-compose.yml` also attaches the app to an external Docker network
named `proxy`, which Traefik's own compose setup is expected to create (see
[Reverse proxy](#reverse-proxy)) — `docker compose up` fails with a clear
"network proxy declared as external, but could not be found" error if that
network does not exist yet. Create it once yourself
(`docker network create proxy`) if Traefik isn't already providing it.

**For local development against this same `Dockerfile`** (no registry login
needed), `build:` is still in `docker-compose.yml` alongside `image:`, so
`docker compose up -d --build` builds and tags locally under that same image
name instead of pulling it — this is how this project's own local testing
works. Never pass `--build` on a production host; that would silently start
running a locally-built image instead of the one CI tested.

The image builds on `golang:1.27-alpine` with `CGO_ENABLED=0` and runs on
`gcr.io/distroless/static-debian12:nonroot` as a non-root user. The binary is
about 19 MB, so the image lands around 21 MB. `docker-compose.yml` hardens it
further at the container level: a read-only root filesystem (with `/tmp`
writable, in case the Go runtime ever wants scratch space — the application
itself writes nothing outside `/data`), every Linux capability dropped,
`no-new-privileges`, and a 256 MB / 1 CPU resource limit.

Because distroless has no shell or `curl`, the binary health-checks itself:

```bash
docker compose exec app /app healthcheck
```

`GET /healthz` returns JSON reporting SQLite and the external database
separately. **The external database being unreachable does not mark the
container unhealthy** — the admin part and every local page keep working
without it.

---

## Configuration

All configuration is environment variables. Startup fails with a combined
message listing every problem at once.

| Variable | Default | Notes |
|---|---|---|
| `APP_ADDR` | `:8080` | Listen address inside the container. Plain HTTP. |
| `APP_BASE_URL` | — | **Required.** Public URL, used by `{{request_link}}`. |
| `DATA_DIR` | `/data` | SQLite database and backups. |
| `EXTERNAL_DB_MODE` | `mock` | `mssql` or `mock`. |
| `MSSQL_DSN` | — | Required when `EXTERNAL_DB_MODE=mssql`. |
| `APP_ENCRYPTION_KEY` | — | **Required.** 32 random bytes, base64. |
| `ENTRY_LINK_SECRET` | — | **Required** unless `ENTRY_LINK_SIGNING_ENABLED=false`. At least 16 random bytes, base64. See [Entry link format](#entry-link-format). |
| `ENTRY_LINK_SIGNING_ENABLED` | `true` | Same safety rail as `ADMIN_2FA_ENABLED`: may only be `false` with `EXTERNAL_DB_MODE=mock`. |
| `SESSION_IDLE_TIMEOUT` | `2h` | |
| `SESSION_ABSOLUTE_TIMEOUT` | `12h` | Must be at least the idle timeout. |
| `COOKIE_SECURE` | `true` | Set `false` only for local plain HTTP. See [Reverse proxy](#reverse-proxy). |
| `TRUSTED_PROXY_CIDRS` | empty | See [Reverse proxy](#reverse-proxy). |
| `BOOTSTRAP_ADMIN_EMAIL` | empty | Used only when no admin exists. |
| `BOOTSTRAP_ADMIN_PASSWORD` | empty | At least 10 characters. Must be changed at first login (see below). |
| `ADMIN_2FA_ENABLED` | `true` | See [First administrator and 2FA](#first-administrator-and-2fa). |
| `CLIENT_LIST_CACHE_TTL` | `5m` | Cache for the store-wide client list. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error`. |

Generate the encryption key with:

```bash
openssl rand -base64 32
```

> **Changing `APP_ENCRYPTION_KEY` makes every stored secret unreadable.** The
> SMTP password would have to be re-entered and every administrator would have
> to re-enroll their 2FA. Back the key up with your other secrets.

SMTP settings, notification recipients, email templates and the report
schedule are **not** environment variables: administrators edit them in the
admin panel, and they are stored in the database.

---

## First administrator and 2FA

1. Start the application with `BOOTSTRAP_ADMIN_EMAIL` and
   `BOOTSTRAP_ADMIN_PASSWORD` set. If no administrator exists, one is created
   and the fact is logged. The bootstrap password is treated as provisional:
   the new admin is required to change it (`must_change_password`), and
   **every page but the change form itself and logout redirects there** until
   they do — this holds for the whole session, not only right after login.
2. Open `/admin/login` and sign in with that email and password.
3. You are sent to enrollment: scan the QR code with an authenticator app
   (Google Authenticator, Authy, 1Password …) or type the shown secret, then
   enter the six-digit code to confirm.
4. **Ten single-use recovery codes are shown once.** Save them; they are stored
   only as hashes and cannot be shown again.
5. Set your own password from **Смяна на парола**.
6. Remove `BOOTSTRAP_ADMIN_EMAIL` and `BOOTSTRAP_ADMIN_PASSWORD` from the
   environment and restart. While they remain set with an admin already
   present, the application logs a warning on every start.

Afterwards, every admin login is password → TOTP code (or a recovery code).
Codes are accepted within ±1 time step, and a code that was already used is
refused inside its own window.

Any administrator can reset another's password or 2FA from
**Администратори**. A reset drops that administrator's sessions, so the change
takes effect at once. An administrator cannot delete themselves, and the last
active administrator cannot be removed.

There is **no self-service registration and no "forgot password"** for anyone.
Passwords are set by administrators. The minimum length is 10 characters.

### Disabling 2FA for local development

Set `ADMIN_2FA_ENABLED=false` to skip the TOTP step entirely and log admins in
on password alone. This is meant for local development and testing only:
**the application refuses to start with it set while
`EXTERNAL_DB_MODE=mssql`**, so it cannot be switched off against a real
deployment by mistake. Startup also logs a loud warning whenever it is off.
Leave the variable unset (or `true`) everywhere else — this is the default and
is what a production deployment should run with.

---

## How a request flows through the system

1. The parent application shows a button linking to this app with a 9-digit
   client code and the salesperson's external `LOGIN`, signed and
   time-limited — see [Entry link format](#entry-link-format).
2. The link is parked server-side against a short-lived pre-session cookie and
   the browser is sent to the login page (or straight to the form if already
   signed in).
3. **Authorization is by store, not by name.** The app account and the external
   salesperson are never matched by their names:
   - the `LOGIN` must exist in the external directory with `IS_WHO_SALER = 1`;
   - that salesperson's store must match the `external_value` of one of the
     signed-in account's stores;
   - the client's store must also be one of the account's stores.

   Any failure shows *Нямате достъп до този клиент.* and writes an audit entry.
   No client data is revealed on refusal.
4. The salesperson picks usernames and modules, a start date and a duration,
   and submits. The request is stored as `pending` with a full snapshot of the
   client data, and the notification email is **queued**, not sent inline — a
   broken SMTP server can never fail a submission.
5. An administrator opens the request, may edit it, then approves or denies it.
   On approval, one activation row per (username × module) is created in a
   single transaction, and the decision email goes to the submitting account
   only.

### The test period rule

A test period may be used **once per client** (per 9-digit code). Selecting it
forces Fast Calculator on, HaynesPro on at **Business**, and the duration to
**1 month**. The form disables those inputs; the server re-applies the rule
regardless of what is posted, because a disabled input is not submitted at all
and a form can be tampered with.

It counts as used while a test-period request for that client is `pending` or
`approved`. **A denied test period is not consumed** and can be requested
again. The rule is re-checked inside the approval transaction.

### Activation dates

```
end = start + N months − 1 day      (inclusive)
```

Month arithmetic clamps to the end of the target month, so 31 January plus one
month ends on 27 February, not on 2 March. An activation is **active** when
`start ≤ today ≤ end` and it has not been revoked. Status is computed from the
dates, so expiry needs no scheduled job.

---

## The external database

All access is **read-only** and isolated behind one interface in
`internal/external/`. Two implementations are selected by `EXTERNAL_DB_MODE`:

- `mssql` — the real SQL Server directory. Pool capped at 5 connections, 5 s
  query timeout, context cancellation, parameterized queries only.
- `mock` — seeded in-memory data for development, tests and the demo.

**Recommended:** give the SQL login read-only permissions. The application
never writes to this database, and a read-only login makes that guarantee
structural rather than a matter of trust.

When the external database is unreachable, the customer part shows
*Външната база данни не е достъпна. Опитайте отново по-късно.*, the error is
logged, and the application keeps running.

The store-wide client list is cached for `CLIENT_LIST_CACHE_TTL` (default
5 minutes). Per-client data used on the request form is cached for at most
60 seconds, so an external change shows up quickly.

### Confirmed schema

All four queries are confirmed against production and are what
`internal/external/mssql/queries.go` uses. They read a single view,
`[STORE_IT_APL_PROD].[dbo].[V_CATALOG_USERS]`, which carries more than the
`CATALOG_USERS` table named in the original specification:

| Column | Meaning |
|---|---|
| `LOGIN` | a username — the salesperson's for `IS_WHO_SALER = 1`, one of a client's own for `IS_WHO_SALER = 0` |
| `CUSTOMER_NUMBER` | the 9-digit client code (client rows only) |
| `CUSTOMER_NAME` | the client's name (client rows only) |
| `MANDANT_NAME` | the **store** the row belongs to, for both salesperson and client rows |
| `IS_WHO_SALER` | `1` for a salesperson row, `0` for a client's own username |
| `ACTIVE` | `1` for a currently active row — every query here filters to `ACTIVE = 1` |

**`MANDANT_NAME` is the store, not the client's name**, despite its name and
despite what the original specification assumed — this was established by
testing against production (`YordanVuchkov` → store `Магазин Лозен`; client
`000000329` → store `Варна Склад`). See `DECISIONS.md` for how.

```sql
-- A salesperson's store, for GetSaler.
SELECT TOP (1) [LOGIN], [MANDANT_NAME]
FROM [STORE_IT_APL_PROD].[dbo].[V_CATALOG_USERS]
WHERE [LOGIN] = @p1 AND [IS_WHO_SALER] = 1 AND [ACTIVE] = 1

-- A client's name and store by its 9-digit code, for GetClientByCode.
SELECT TOP (1) [CUSTOMER_NUMBER], [CUSTOMER_NAME], [MANDANT_NAME]
FROM [STORE_IT_APL_PROD].[dbo].[V_CATALOG_USERS]
WHERE [CUSTOMER_NUMBER] = @p1 AND [IS_WHO_SALER] = 0 AND [ACTIVE] = 1

-- A client's own usernames, for ListClientLogins.
SELECT [LOGIN]
FROM [STORE_IT_APL_PROD].[dbo].[V_CATALOG_USERS]
WHERE [CUSTOMER_NUMBER] = @p1 AND [IS_WHO_SALER] = 0 AND [ACTIVE] = 1
ORDER BY [LOGIN]

-- Every client of a set of stores, for ListClientsByStores.
SELECT DISTINCT [CUSTOMER_NUMBER], [CUSTOMER_NAME], [MANDANT_NAME]
FROM [STORE_IT_APL_PROD].[dbo].[V_CATALOG_USERS]
WHERE [IS_WHO_SALER] = 0 AND [ACTIVE] = 1 AND [MANDANT_NAME] IN (...)
ORDER BY [CUSTOMER_NAME], [CUSTOMER_NUMBER]
```

The view has no column of its own for the client's "Обект" (object) — by the
project owner's confirmation, `MANDANT_NAME` is what the business considers a
client's object as well as their store, so `Client.Object` is set equal to
`Client.Store` after each of the queries above.

---

## Placeholders — all resolved

The original specification left four things open (PLACEHOLDER-A through -D).
All four are now resolved; nothing is left pending. See `DECISIONS.md` for how
each one was settled, and the note above for -B and -C (the external queries).
-A (the entry link format) is documented in the next section, and -D
(standalone client records) turned out not to be needed: the one thing this
application must remember per client — whether its single test period is
used — is already derived from `client_code` on the `requests` table
(`store.DB.TestPeriodUsed`), not from a client entity, so a client is
identified everywhere by its 9-digit code alone. `grep -rn PLACEHOLDER
--include="*.go" .` finds only historical comments pointing at this section.

---

## Entry link format

The parent application links here with a 9-digit client code and the
salesperson's external `LOGIN`, signed so a captured or guessed link cannot be
replayed indefinitely:

```
GET /r/{code}/{login}?exp={unix_seconds}&sig={hex_hmac_sha256}
```

- `code` — the 9-digit client code, in the path, exactly as `CUSTOMER_NUMBER`.
- `login` — the salesperson's external `LOGIN`, in the path, URL-encoded if it
  contains characters that need it.
- `exp` — a Unix timestamp (seconds) after which the link is refused. The
  parent application chooses how far in the future this is (a few minutes is
  plenty for a button click); the server only checks `exp` against the clock,
  it does not enforce a maximum lifetime of its own.
- `sig` — lowercase hex of `HMAC-SHA256(secret, code + "\n" + login + "\n" + exp)`,
  where `exp` is signed as the exact same decimal string that appears in the
  URL, and `secret` is the value of `ENTRY_LINK_SECRET`. Joining the three
  fields with `\n` (rather than concatenating them directly) is what stops
  `code="1", login="23"` from signing the same as `code="12", login="3"`.

A request that is missing `exp` or `sig`, whose signature does not match, or
whose `exp` has passed, is rejected the same way as a malformed link — the
Bulgarian error page *Линкът не е валиден* — and the specific reason is never
revealed to the browser (only logged), so a link cannot be probed for which
part of it was wrong.

**`ENTRY_LINK_SECRET`** is a separate secret from `APP_ENCRYPTION_KEY` (at
least 16 random bytes, base64-encoded — generate one the same way, e.g.
`openssl rand -base64 32`) and must be shared with whoever generates links in
the parent application. Rotating it invalidates every link signed with the
old value; there is no overlap period.

**For local testing without the parent application**, either:
- set `ENTRY_LINK_SIGNING_ENABLED=false` (only permitted with
  `EXTERNAL_DB_MODE=mock`, enforced the same way as `ADMIN_2FA_ENABLED`) to
  fall back to plain, unsigned `GET /r/{code}/{login}` links, or
- keep signing on and generate a real link with the bundled tool:
  ```bash
  docker compose exec app /app sign-link 000050431 YordanVuchkov 10m
  ```
  (or `go run ./cmd/app sign-link 000050431 YordanVuchkov 10m` outside Docker,
  with `ENTRY_LINK_SECRET` and `APP_BASE_URL` set in the environment). It
  prints a full, ready-to-open URL that expires after the given duration
  (default 5 minutes).

---

## Reverse proxy

TLS is terminated by **Traefik**, which this project assumes is already
running elsewhere on the host (its own `docker-compose.yml`, not part of this
one) with an entrypoint named `websecure` on 443 and a certresolver named
`letsencrypt` configured for ACME. **This application does not terminate TLS
itself** and serves plain HTTP only.

`docker-compose.yml` has no `ports:` entry at all: Traefik reaches the `app`
container over a shared Docker network, declared there as an `external`
network named `proxy`, rather than through a published host port — so the
container is not reachable from the host, only through Traefik. Routing is
configured entirely through the `traefik.*` labels already on the `app`
service; if your Traefik instance's entrypoint or certresolver are named
differently, edit those two labels to match.

**Set `TRUSTED_PROXY_CIDRS`.** Because Traefik is itself a container, the app
sees every request arriving from Traefik's own address *on the `proxy`
network*, not from `127.0.0.1`. Find that network's subnet once it exists
with:
```
docker network inspect proxy
```
(look for `"Subnet"`) and put it in `.env`:
```
TRUSTED_PROXY_CIDRS=<that subnet>
```
This matters for exactly one thing: the per-IP login lockout (5 failures →
15-minute lockout). The real client address is only known through the
`X-Forwarded-For` header Traefik sets (it does this correctly by default,
nothing to configure there), and that header is only honoured when it is
known to come from somewhere trustworthy — otherwise anyone could put an
arbitrary value in it and either evade the lockout or lock out someone else's
address. With `TRUSTED_PROXY_CIDRS` left at its empty default, the header is
ignored outright and every request is treated as coming from Traefik's own
address instead — safe, but it makes the lockout apply to *all* logins
through Traefik collectively rather than to each real client separately, so a
handful of failed attempts from anyone could briefly lock out everyone.

**What `COOKIE_SECURE` does:** it sets the `Secure` attribute on the session
cookie, which tells the browser to attach that cookie only to `https://`
requests, never to a plain `http://` one. Leave it at its default `true` once
Traefik with a real certificate is in front of the application; only set it
`false` for local development without HTTPS and without Traefik at all, e.g.
testing directly against a container with a published port on `localhost`.

The application sets `Content-Security-Policy` (same-origin only, no
`unsafe-eval` — Alpine ships in its CSP build),
`X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY` and
`Referrer-Policy: same-origin`. Do not let the proxy strip or weaken them.

---

## Backups

A backup runs at startup and every 24 hours using SQLite's `VACUUM INTO`, which
produces a consistent copy while the application keeps serving. Files land in
`$DATA_DIR/backups/` as `app-YYYY-MM-DDTHHMMSS.db`, and the **newest 14 are
kept**.

Backups on the same volume protect against corruption, not against losing the
server. Copy them off the machine, for example nightly:

```bash
# From another host
rsync -az --delete \
  user@server:/var/lib/docker/volumes/<project>_app-data/_data/backups/ \
  /backup/moduli/

# Or through the container's volume mount point
docker compose cp app:/data/backups ./backups
```

To restore: stop the container, replace `/data/app.db` with a backup file
(remove any `app.db-wal` and `app.db-shm` beside it), and start it again.

---

## Email

Email is configured in the admin panel under **SMTP**, not in the environment.
Set the server, port, security (`Няма` / `STARTTLS` / `SSL/TLS`), credentials,
the sender, and the addresses that receive new-request notifications. The
password is encrypted at rest with `APP_ENCRYPTION_KEY` and is never rendered
back into the form — leaving the field blank keeps the stored one.

**Изпрати тест** sends a message immediately, bypassing the queue, and shows
the SMTP error verbatim if it fails.

Everything else goes through an outbox table with a background worker and
exponential backoff (1, 2, 4, 8, 16, 32 minutes, up to 6 attempts). Failures
are visible under **Имейли**, with a badge in the navigation, and can be
retried by hand. With no SMTP configured at all, messages simply pile up as
failures and nothing else breaks.

### Templates

Four editable templates (subject plus HTML body; the plain-text part is
derived automatically): `request_created`, `request_approved`,
`request_denied`, `scheduled_export`.

Placeholders are written `{{client_code}}` and are replaced by plain string
substitution with **HTML-escaped** values. An admin's template is never
executed as a Go template, so neither the template nor the data can inject
markup or run anything. Unknown placeholders are left untouched in the text
and reported as a warning on save.

Each editor has an **ⓘ** button listing every placeholder available to that
template with a Bulgarian description and an example, a **Преглед** that
renders it with sample data, and a **Върни по подразбиране** action.

---

## The monthly report

The Excel export contains **only currently active activations**, sorted by
client name, client code, username and module, with Bulgarian headers, a bold
frozen header row, an auto-filter, sensible column widths and real Excel dates
formatted `DD.MM.YYYY`. The filename is `aktivni_moduli_YYYY-MM-DD.xlsx`.

The same generator serves the manual **Експорт в Excel** button and the
scheduled email, so the two can never differ.

Ticking **Групирай по обекти** next to the button (`?group=1`) downloads a
grouped layout instead, named `aktivni_moduli_po_klienti_YYYY-MM-DD.xlsx`. It
has one bold heading row per client (number, name and object), followed by
that client's activations as username / module / tier / dates / status. The
activation rows are outlined under their heading, so Excel can collapse each
client.

The same checkbox appears on the **График** page and controls the scheduled
email and the **Изпрати сега** button there. Its state is stored on the
schedule row (`group_by_client`) and carries over to the next send, so it is
set once rather than on every send.

The schedule is monthly: a day of the month (1–31) and a time in
`Europe/Sofia`. **If the month is shorter than the chosen day, the report runs
on that month's last day.** A single in-process goroutine ticks every minute.

If the container was down at the scheduled time, the first tick after startup
sees the missed run and executes it **once** — claiming the run advances the
next-run marker in the same statement, so a long outage never produces a
backlog.

---

## Development

```bash
go test ./...                 # unit and handler tests
go vet ./...
staticcheck ./...
govulncheck ./...
gofmt -l ./cmd ./internal
```

Tests cover email validation, the test-period rules including the server-side
forcing, end-date calculation with month-end edge cases, active-status
computation, schedule next-run calculation with short months, placeholder
replacement and escaping, authorization by store, TOTP verification and replay
prevention, and the full form-submission and approve/deny flows against the
mock directory.

`govulncheck` flags two things, both without a fixed version to move to yet
(see `DECISIONS.md` for the full reasoning):

- `golang.org/x/crypto/openpgp`, at the module level, as unmaintained — that
  package is not imported here (only `bcrypt` is).
- **GO-2026-6452** in excelize, reachable through the Excel export. It is a
  *read*-path bug (a malicious `.xlsx`'s cell with a negative shared-string
  index); this application only ever writes a workbook it builds itself, so
  the vulnerable path should not be reachable through it. All three
  background goroutines got panic recovery as a defensive measure regardless.

### Front end

htmx 2.0.10 and Alpine.js 3.15.x are **vendored** into `web/static/` and
embedded in the binary. There is no npm and no build step.

Alpine ships in its **CSP build**, which evaluates no expressions at runtime.
That is what keeps the Content-Security-Policy at `script-src 'self'` with no
`unsafe-eval`, and it means every `x-*` attribute may only name a property or
method of a component registered in `web/static/app.js` — never inline
JavaScript. Keep it that way when editing the templates.

To upgrade either library, replace the file and update the pinned version in
this README. Stay on the htmx 2.x line: htmx 4 changes attribute inheritance
and event names.

---

## Project layout

```
cmd/app/            entry point, wiring, background workers, graceful shutdown
internal/audit/     the audit log
internal/auth/      sessions, passwords, CSRF, TOTP, encryption, throttling
internal/config/    environment configuration and validation
internal/dates/     Europe/Sofia calendar arithmetic
internal/demo/      seeded demo data (mock mode only)
internal/email/     SMTP, templates, placeholders, the outbox worker
internal/entrylink/ the entry-link parser              (PLACEHOLDER-A)
internal/export/    the Excel generator
internal/external/  the read-only external directory: interface, mssql/, mock/
internal/http/      router, middleware, customer and admin handlers
internal/modules/   the fixed module catalogue
internal/requests/  business rules: validation, test period, approve/deny
internal/scheduler/ the monthly export schedule
internal/store/     SQLite: connection, migrations/, repositories
web/templates/      layouts, partials, customer/, admin/
web/static/         htmx, Alpine (CSP build), app.css, app.js, favicon
```

Modules are **fixed in code** and cannot be added by administrators; the
catalogue lives in `internal/modules/modules.go`.

Migrations are numbered `.sql` files embedded in the binary and applied at
startup inside a transaction each, tracked in `schema_migrations`.
