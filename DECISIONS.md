# Decisions

Choices made where the specification was silent or where the implementation
departs from it. Each records what was chosen and why, so a later reader can
disagree with the reasoning rather than guess at the intent.

---

## The external database (PLACEHOLDER-B and PLACEHOLDER-C, resolved)

The two open external-database placeholders were resolved against production
and are no longer placeholders. What changed and why is recorded here rather
than just in the diff, because it corrects an assumption the original
specification made.

### `MANDANT_NAME` is the store, not the client's name

Section 4.1 of the specification states "`MANDANT_NAME` – the client's name."
Production data shows this is wrong: `MANDANT_NAME` is the **store** the row
belongs to, for both a salesperson row and a client row. This was confirmed by
querying `V_CATALOG_USERS` directly against the live `STORE_IT_APL_PROD`
database:

- `LOGIN = 'YordanVuchkov'`, `IS_WHO_SALER = 1` → `MANDANT_NAME = 'Магазин Лозен'`
  — a plausible store name, not a person or a company.
- `CUSTOMER_NUMBER = '000000329'`, `IS_WHO_SALER = 0` → `CUSTOMER_NAME =
  'ГИНКОВ - КАР - ЕООД'` (the client's actual name) and separately
  `MANDANT_NAME = 'Варна Склад'` (a store name, distinct from the client name).

Had `MANDANT_NAME` really been the client's name, `GetSaler`'s only job — a
store value to compare against `stores.external_value` — would have had no
column to read at all. This is also why PLACEHOLDER-C turned out not to need
a new table or column: the store was already the value the original
`CATALOG_USERS` query design would have used, just under a misleading name.

### The "Clients table" is the same view, not a separate one

Section 4.2 described a not-yet-identified table holding the client's code,
name, object and store. It turned out to be `V_CATALOG_USERS` itself,
filtered by `CUSTOMER_NUMBER` and `IS_WHO_SALER = 0`: the same view the
original `CATALOG_USERS` query in section 4.1 pointed at, carrying more
columns than that section described (`CUSTOMER_NUMBER`, `CUSTOMER_NAME`,
`ACTIVE`, in addition to `LOGIN`, `MANDANT_NAME`, `IS_WHO_SALER`).

### `Client.Object` reads from `MANDANT_NAME`, the same column as `Store`

**This was wrong at first and got corrected.** The view has no column named
anything like "object", so the first implementation left `Client.Object`
empty and documented that as a limitation. The project owner corrected this:
`MANDANT_NAME` is what the business considers a client's "Обект на клиента"
as well as their store — the two are the same concept here, not separate
data. `GetClientByCode` and `ListClientsByStores` now set `Object` equal to
`Store` after scanning. The admin-editable client fields on requests and
activations (PLACEHOLDER-D) still let an admin override it by hand if a
particular client ever needs something different there.

Before landing on this, a different column was floated and rejected:
`VERSART_NAME`/`VERSART_DESCRIPTION` looked promising at a glance (present,
non-null, plausible-sounding), but sampling across stores showed they repeat
identically across many unrelated clients under the same store - a delivery
zone or route grouping, not a per-client value - so they were not used.

### `ListClientLogins` is now keyed by client code, not by name

The interface originally took `mandantName` on the theory that `MANDANT_NAME`
was the client's name and so identified the client. With `MANDANT_NAME`
actually meaning store, that key would have returned every username at a
whole store rather than one client's usernames. The parameter is now the
client's 9-digit code (`CUSTOMER_NUMBER`), which the confirmed query filters
on directly — a client's own username rows all share both `CUSTOMER_NUMBER`
and `CUSTOMER_NAME`, but only the code is guaranteed unique. Every caller
(`internal/http/customer.go`, `admin_requests.go`, `internal/demo/seed.go`,
and the mock) was updated to pass `client.Code` rather than `client.Name`.

### Every query filters `ACTIVE = 1`

Confirmed present on both salesperson and client rows in the real view; a row
without it is not usable. None of this was speculative — all four queries in
`internal/external/mssql/queries.go` were run against the live
`STORE_IT_APL_PROD` database during development and returned real, sane data
(a real client name, real usernames, 478 clients for one store), not just
compiled successfully.

### The connection needed `encrypt=disable`

The production SQL Server endpoint tested against did not accept a TLS
handshake at the time of testing, so `encrypt=true` failed to connect and
`encrypt=disable` was required. `.env.example` documents both forms and
recommends checking with whoever administers that server whether TLS can be
turned on before falling back to the plain-text option, since it sends the
read-only credentials and every row unencrypted.

---

## The entry link (PLACEHOLDER-A, resolved)

**Spec:** "The exact URL format and any signature/expiry validation will be
specified later," behind a pluggable `EntryLinkParser`.

**Done:** `GET /r/{code}/{login}?exp={unix_seconds}&sig={hex_hmac_sha256}`,
where `sig` is `HMAC-SHA256(ENTRY_LINK_SECRET, code + "\n" + login + "\n" +
exp)`. See README.md, "Entry link format", for the full contract the parent
application must implement, and `internal/entrylink/entrylink.go` for
`SignedParser`.

**Why this shape:**
- The three fields are signed together, newline-joined, so the signature
  cannot be satisfied by mixing a valid code with a valid login taken from two
  different links, and so a boundary shift (`code="1", login="23"` vs.
  `code="12", login="3"`) cannot produce a colliding signature.
- `exp` is a timestamp chosen by the caller, not a server-side TTL, because
  the server has no way to know how long ago the parent application generated
  the link before redirecting the browser to it.
- Query parameters rather than another path segment or a single opaque token,
  because it keeps `{code}` and `{login}` human-readable in logs and in the
  existing route pattern, and keeps the parent application's job to one
  well-known primitive (HMAC-SHA256) rather than a bespoke token format.
- A toggle, `ENTRY_LINK_SIGNING_ENABLED`, exists only so local development and
  the mock directory can keep using plain unsigned links without hand-signing
  every URL; it follows the exact same safety rail as `ADMIN_2FA_ENABLED`
  (refused when `EXTERNAL_DB_MODE=mssql`), for the same reason: a convenience
  meant for a laptop must be structurally unable to reach a real deployment.
- The `app sign-link` subcommand (`cmd/app/main.go`) exists so a valid signed
  link can be produced for manual testing or ops without writing the HMAC
  logic a second time anywhere.

This is this project's own recommendation, not a contract the parent
application's team has confirmed. If their actual constraints differ (for
example, if links must be single-use, or if `exp` needs to be an opaque
server-side token instead of a client-supplied timestamp), only
`internal/entrylink/` and the one place `SignedParser` is constructed in
`cmd/app/main.go` need to change — nothing else in the application depends on
the URL shape.

## Standalone client records (PLACEHOLDER-D, resolved)

**Spec:** "Whether administrators may create client records that do not exist
in the external database is undecided," to be marked as a `TODO` if left open.

**Resolved: no standalone client entity is needed.** The only thing this
application must remember about a client independent of any single request or
activation is whether its one-time test period has been used — and that is
already answered by a query over `client_code` on the `requests` table
(`store.DB.TestPeriodUsed`; see the test period rule in README.md), not by a
property of a client row. Every other client fact (name, object, store) is
either read fresh from the external directory (for a request against a real
client) or entered by hand on a manual activation, and is stored as a
snapshot on that activation/request precisely because it does not need to
outlive it or be shared across activations.

The client fields on `activations` and `requests` (`client_code`,
`client_name`, `client_object`, `client_store`) therefore stay exactly as they
were: editable per-row, with no separate `clients` table.

---

## Deviations from the specification

### Email templates are seeded from Go, not from a migration

**Spec:** "Provide sensible default Bulgarian templates seeded by migration."

**Done instead:** the defaults live in `internal/email/templates.go` and are
inserted at every startup with `INSERT … ON CONFLICT DO NOTHING`.

**Why:** the admin panel also has a *Върни по подразбиране* action, which needs
the defaults available in Go. Keeping a copy in SQL as well would mean two
sources of truth that drift the first time someone edits one and not the other.
The seeding is idempotent and never overwrites an administrator's edits, so it
behaves exactly like a migration from the operator's point of view.

### An extra package: `internal/dates`

The layout in the specification has no place for calendar arithmetic, but the
end-date rule, the "is it active today" rule and the scheduler all need the
same `Europe/Sofia` handling. Duplicating it three times is how the 31 January
edge case gets fixed in two places and missed in the third, so it lives in one
tested package.

### An extra package: `internal/demo`

Seeded demo data is a deliverable, but it needs both `store` (to write) and
`auth` (to hash passwords), and `auth` already imports `store`. It therefore
cannot live in `store` without an import cycle. It runs only when
`EXTERNAL_DB_MODE=mock` **and** the database has no salespeople yet, so it can
never touch a real deployment.

---

## Data model

### Timestamps and dates as TEXT

Timestamps are RFC3339 in UTC (`2006-01-02T15:04:05Z`) and calendar dates are
`YYYY-MM-DD`. Both are fixed-width, so lexicographic comparison is
chronological and SQLite's own date functions are not needed. It also makes the
database readable with any SQLite browser during support work, which matters
more here than the few bytes an integer would save.

The split is deliberate: timestamps are instants and belong in UTC; activation
dates are *calendar days in Sofia* and would be wrong as instants.

### Activation status is computed, never stored

`active` / `pending` / `expired` / `revoked` follow from the dates and the
revocation, so nothing has to run at midnight to keep the data honest, and the
database cannot hold a row whose stored status contradicts its own dates.

### Client data is snapshotted onto requests and activations

Each request and activation carries the client's code, name, object and store
as they were at the time. The external database is not ours and may change; the
local record must stay readable regardless. It is also what let PLACEHOLDER-D
be resolved as "no standalone client entity needed" rather than a blocker -
see that section above.

### `submitter_email` is stored alongside `submitter_user_id`

The foreign key is `ON DELETE SET NULL`, but the email is kept as text, so
deleting a salesperson does not erase who filed a historical request.

### The SQLite pool is capped at one connection

SQLite takes one writer at a time. A larger pool would trade queueing inside
`database/sql` for `SQLITE_BUSY` errors, which is a worse deal at a few hundred
requests a day. WAL, a 5 s busy timeout and foreign keys are all on.

---

## Authentication and sessions

### Sessions in the database, not signed cookies

Server-side sessions can be revoked. Deactivating a salesperson, resetting a
password or resetting 2FA all drop that account's sessions immediately, which a
self-contained signed cookie cannot offer. Only the SHA-256 of the cookie token
is stored.

### The session token is rotated when the admin login completes

The password step and the TOTP step are one login, so the token issued after
the password is replaced once the second factor succeeds. A token observed
during the first step is then worthless.

### The login form carries no CSRF token

There is no session yet, so there is nothing to bind a token to. The cookies
are `SameSite=Lax`, and a forged login would only log the victim into an
account the attacker already controls. Every authenticated state-changing
request does carry a synchronizer token, in a hidden field or in the
`X-CSRF-Token` header that htmx sends from `hx-headers`.

### Login throttling lives in SQLite

Lockouts survive a restart. An in-memory counter would be reset by anyone who
can make the process restart.

### TOTP replay prevention stores the accepted time step

`VerifyTOTP` returns which step matched, and the step is claimed with
`UPDATE … WHERE last_totp_step < ?`. The comparison and the write are one
statement, so two simultaneous logins with the same code cannot both win. This
is why the skew window is walked explicitly instead of using the library's
`Skew` option, which does not report the matching step.

### Recovery codes are SHA-256, not bcrypt

They carry 50 bits of entropy from a uniform alphabet. bcrypt exists to slow
down guessing at low-entropy human passwords; there is no such guess to slow
down here. Passwords themselves use bcrypt at cost 12.

### Admin 2FA can be disabled, but only for local development

**Ask:** turn off the mandatory TOTP step for local/dev testing, while keeping
it required in production — a scoped exception to section 3.3 of the original
specification, not a removal of it.

**Done:** a new `ADMIN_2FA_ENABLED` environment variable (default `true`)
gates the second factor in `handleAdminLogin`. When `false`, the session
starts at `store.StageActive` directly instead of `StageAwaitingTOTP`, and the
password alone completes the login - the rest of the TOTP machinery (secret
storage, enrollment, recovery codes, replay prevention) is untouched and still
exercised by its own tests whenever the flag is at its default.

**The safety rail is enforced, not just documented:** `config.Load` refuses to
start with `ADMIN_2FA_ENABLED=false` while `EXTERNAL_DB_MODE=mssql`, so the
flag cannot silently disable 2FA against a real deployment - only against the
mock directory, which is what every local and CI run already uses. Startup
also logs a loud warning whenever the flag is off, so a misconfigured
environment is visible in the logs even if someone missed the `.env` comment.

### Passwords longer than 72 bytes are rejected

bcrypt silently truncates there. Refusing is honest; accepting would quietly
ignore the rest of what the user typed.

### The minimum length counts characters, not bytes

Ten Cyrillic characters are twenty bytes. Counting bytes would impose a
different rule on Bulgarian passwords than on English ones.

---

## The customer part

### The entry link is consumed on submit

Once a request is filed the parked link is deleted, so a refresh cannot file it
again. Re-opening `/request` without a new link goes to the informational page.
Combined with the server-side re-validation, this is what "prevent double
submission" means beyond disabling the button.

### Authorization is re-checked on submit

The form being rendered is not treated as permission to post. Store
assignments, the client's store and the salesperson's store are all checked
again when the submission arrives.

### Usernames are canonicalised against the client's current list

A posted username that does not belong to the client is rejected rather than
ignored, so a tampered form cannot grant modules to an arbitrary login.
Comparison is case-insensitive and the stored value is the one the external
directory returned.

### "Clients of my stores" is sourced from local activations, not the external directory

**Spec:** section 6.3.C describes this list as "all clients tied to the
account's stores (from external DB)".

**Changed, at the project owner's explicit request:** the list now shows only
clients that currently have at least one active module activation, read from
the local database (`store.ListActiveClientsByStores`) instead of
`external.Directory.ListClientsByStores`. A client the external directory
knows about but that has nothing activated here does not appear - a store
with accounts and stores configured but no approved requests yet correctly
shows an empty list, which is the behaviour this was asked for. The external
directory's `ListClientsByStores` method and its `mssql`/`mock`
implementations are untouched; they are simply no longer called from this one
place, since the entry-link flow (`GetClientByCode`, `GetSaler`,
`ListClientLogins`) still reads live from the external directory as before -
only this one informational, non-authoritative list changed source.

### The client list is filtered and paged in Go

The store filter runs in SQL; the search and the paging run in Go, over the
full set of the account's activated clients (a few hundred rows at the
application's stated scale). This is a correctness requirement, not just a
convenience: SQLite's `LIKE` only case-folds ASCII, so a SQL-side search would
make a Cyrillic client name case-sensitive, while Go's `strings.ToLower` is
Unicode-aware. This was caught by a test (`LIKE '%балкан%'` failing to match
`"Балкан"`) before it shipped.

### Store matching ignores case and surrounding space

`external_value` is maintained by hand against a third-party system, so
` Sofia ` and `SOFIA` matching is a kindness that costs nothing. The values
still have to correspond; there is no fuzzy matching.

---

## The admin part

### Approval is one transaction

The status change and every activation row commit together, and the
test-period rule is re-checked inside it. Either the whole decision lands or
none of it does.

### Overlaps warn but never block

The specification asks for a warning that the admin may override, so the
overlap check is presentational only. It is computed for pending requests and
shown above the decision buttons.

### Editing a request keeps usernames that vanished externally

If a username on a pending request no longer exists in the external directory,
it is still offered in the edit form. Silently dropping it would change the
request without telling anyone.

### A deciding admin sees a conflict, not an error page

Deciding an already-decided request returns 409 with the reason on the detail
page. Two administrators clicking at once is an ordinary race, not a fault.

### Manual activations accept either an end date or a month count

The month count wins when both are given, because it applies the same
`start + N months − 1 day` rule as an approval and is the easier thing to get
right by hand.

### Flash messages travel as keys, not text

A redirect carries `?msg=saved`, and the Bulgarian text is looked up
server-side. Nothing a caller puts in the URL can be rendered onto the page.

### Store deletion is refused while assigned

The specification asks for deactivation instead of hard deletion for a store in
use. Deletion is offered only at zero assignments; otherwise the UI points at
the active flag.

---

## Email

### Sending is always queued, except the test message

A request is stored before anything is sent, and the email is appended to an
outbox that a worker drains with exponential backoff. A broken SMTP server can
never fail a salesperson's submission. The **Изпрати тест** button is the one
exception: it sends inline so the admin sees the server's own error text.

### The attachment blob is dropped once sent

A monthly workbook kept forever would dominate the database. The report can be
regenerated from the activations at any time.

### Placeholder substitution is plain text replacement with escaping

Admin-authored templates are never parsed as Go templates. Values are
HTML-escaped on substitution, the subject is substituted without escaping
because it is not HTML, and unknown placeholders are left in place and reported
as a warning. An administrator cannot inject markup or execute anything, even
by accident.

### The template preview shows escaped source

The rendered body is displayed as text rather than injected as live markup, so
the preview cannot script the admin panel.

---

## Front end

### htmx is told to swap error responses too

**A real bug, found from a user report:** submitting the request form with a
validation problem (or an expired CSRF token, or a server error) looked like
nothing happened at all - the submit button flashed disabled and the page
stayed exactly as it was, with the Bulgarian error message the server had
already rendered nowhere to be seen.

The cause: htmx 2.0.10's default `responseHandling` config does not swap a
4xx/5xx response into the page - it only fires `htmx:responseError` and
discards the body (`{code:"[45]..", swap:false, error:true}` in
`htmx.min.js`). Every error path in this application (`serverError`,
`externalError`, the CSRF check, and every validation failure that
re-renders a form) deliberately builds a renderable HTML fragment for
exactly this situation - the discarding was silent, not a missing feature on
the server side.

Fixed with a global `htmx:beforeSwap` listener in `web/static/app.js` that
forces `shouldSwap = true` for any response with `status >= 400`, matching
htmx's own documented pattern for this case. The request form's error partial
is rooted at the same `id="request-form-wrap"` as its `hx-target`, so once
swapping is allowed the existing server-side rendering works with no other
change needed.

### The submit button is disabled by htmx, never by a click handler

**The bug behind "the button greys out and nothing is sent":** the send
button used to disable itself from an Alpine `@click` handler as a
double-submit guard. Alpine applies that change in a microtask, which runs
right after the click listener returns but *before* the browser performs the
button's default action, and browsers skip form submission for a disabled
submit button. The form therefore never submitted and htmx never sent a
request. Checks that post over HTTP directly cannot see this; it was
reproduced and then verified fixed in headless Chrome by clicking through the
real form.

Double submission is now prevented only by `hx-disabled-elt` on the form.
htmx disables the button after the request has started and re-enables it when
the request finishes.

### Alpine's CSP build

The specification asked for it "if feasible". It is, and it is what lets the
Content-Security-Policy stay at `script-src 'self'` with no `unsafe-eval`.

The cost is real: the CSP build evaluates no expressions, so every `x-*`
attribute may only name a property or a method of a registered component. That
is why computed values appear as getters (`fastCalculatorValue`, `locked`,
`showTier`) in `web/static/app.js` rather than as inline expressions, and why
the username search filters the DOM in a method instead of a template filter.

### Disabled inputs are mirrored into hidden fields

Browsers do not submit disabled inputs. Every locked control has a hidden input
bound to a getter holding the effective value, placed **before** the visible
control so `PostFormValue` reads the mirror. This is a convenience for a
correct client; the server applies the same rules to whatever actually arrives,
and the tests post tampered forms to prove it.

### Row forms live outside the table

A `<form>` is not valid inside `<tr>` and browsers hoist it out of the table.
The stores page puts each row's form after the table and joins the inputs with
the `form` attribute.

### Hand-written CSS

One file, roughly 500 lines, no framework and no build step. It covers focus
outlines, a skip link, responsive tables and `prefers-reduced-motion`.

---

## Operations

### `/healthz` reports the external database without failing on it

SQLite failing means the container is broken. The external database being
unreachable means one feature degrades while the admin part and every local
page keep working — so it is reported separately and does not fail the check.

### Backups run in-process

`VACUUM INTO` on a 24-hour timer, plus one at startup, keeping 14 files. A
sidecar container would need its own access to the same volume and its own
schedule; for a single-instance application this is less to go wrong.

### Session purging is a background job, not a query filter

Expired and idle sessions are deleted every 15 minutes. The lookup already
checks both timeouts, so the job is only there to keep the table from growing.

---

## Dependencies

Exactly the libraries the specification names, plus the Go standard library.
No web framework, no router library, no cron library, no ORM.

`govulncheck` flags `golang.org/x/crypto/openpgp` at the module level as
unmaintained. That package is not imported here — only `bcrypt` is — and there
is no fixed version to move to, so the finding is informational.

`govulncheck` also flags **GO-2026-6452** (excelize, "panic via negative
shared-string index") as reachable, through `export.Build`'s call to
`StreamWriter.AddTable`. The excelize v2.11.0 already pinned per the
specification is the newest release, and no fixed version exists yet
(`Fixed in: N/A`). The bug is in the *read* path — parsing a cell whose
shared-string index is negative, reachable via `GetCellValue`, `GetRows` and
similar — and this application only ever *writes* a workbook it builds itself
with `excelize.NewFile()`; it never opens an externally supplied `.xlsx` file,
so the vulnerable code path should not be reachable through this application's
own use of the library. As a defensive measure regardless, panic recovery was
added to all three background goroutines (the export scheduler, the outbox
worker, and housekeeping) in addition to the existing HTTP middleware's
recovery: none of them had it before, and an unrecovered panic in any
goroutine — from this or any other cause — brings down the whole process, not
just that job. Re-run `govulncheck ./...` after any excelize upgrade to check
whether this clears.
