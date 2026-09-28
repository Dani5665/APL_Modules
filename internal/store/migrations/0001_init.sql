-- Initial schema.
--
-- Conventions:
--   * timestamps      TEXT, RFC3339 in UTC ("2006-01-02T15:04:05Z") - fixed
--                     width, so lexicographic comparison is chronological.
--   * calendar dates  TEXT, "YYYY-MM-DD" in Europe/Sofia business time.
--   * booleans        INTEGER 0/1.

CREATE TABLE admins (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    email                TEXT    NOT NULL UNIQUE,
    password_hash        TEXT    NOT NULL,
    totp_secret_enc      BLOB,
    totp_enabled         INTEGER NOT NULL DEFAULT 0,
    last_totp_step       INTEGER NOT NULL DEFAULT 0,
    must_change_password INTEGER NOT NULL DEFAULT 0,
    active               INTEGER NOT NULL DEFAULT 1,
    created_at           TEXT    NOT NULL,
    updated_at           TEXT    NOT NULL
);

CREATE TABLE admin_recovery_codes (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    admin_id   INTEGER NOT NULL REFERENCES admins(id) ON DELETE CASCADE,
    code_hash  TEXT    NOT NULL,
    used_at    TEXT,
    created_at TEXT    NOT NULL
);
CREATE INDEX idx_recovery_codes_admin ON admin_recovery_codes(admin_id);

CREATE TABLE users (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    email                TEXT    NOT NULL UNIQUE,
    password_hash        TEXT    NOT NULL,
    must_change_password INTEGER NOT NULL DEFAULT 0,
    active               INTEGER NOT NULL DEFAULT 1,
    created_at           TEXT    NOT NULL,
    updated_at           TEXT    NOT NULL
);

CREATE TABLE stores (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    name           TEXT    NOT NULL UNIQUE,
    external_value TEXT    NOT NULL,
    active         INTEGER NOT NULL DEFAULT 1,
    created_at     TEXT    NOT NULL,
    updated_at     TEXT    NOT NULL
);

CREATE TABLE user_stores (
    user_id  INTEGER NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
    store_id INTEGER NOT NULL REFERENCES stores(id) ON DELETE RESTRICT,
    PRIMARY KEY (user_id, store_id)
);
CREATE INDEX idx_user_stores_store ON user_stores(store_id);

-- Server-side sessions. Only the SHA-256 of the cookie token is stored.
-- subject_type is 'user' or 'admin'; stage is 'active' or 'awaiting_totp'.
CREATE TABLE sessions (
    token_hash   TEXT    PRIMARY KEY,
    subject_type TEXT    NOT NULL,
    subject_id   INTEGER NOT NULL,
    csrf_token   TEXT    NOT NULL,
    stage        TEXT    NOT NULL DEFAULT 'active',
    created_at   TEXT    NOT NULL,
    last_seen_at TEXT    NOT NULL,
    expires_at   TEXT    NOT NULL
);
CREATE INDEX idx_sessions_expires ON sessions(expires_at);
CREATE INDEX idx_sessions_subject ON sessions(subject_type, subject_id);

-- Entry link parked before login, tied to a short-lived pre-session cookie.
CREATE TABLE pending_entry_links (
    token_hash  TEXT PRIMARY KEY,
    client_code TEXT NOT NULL,
    saler_login TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    expires_at  TEXT NOT NULL
);
CREATE INDEX idx_pending_entry_links_expires ON pending_entry_links(expires_at);

-- Login throttling. scope is 'ip' or 'user'; key is the address or username.
CREATE TABLE login_attempts (
    scope         TEXT    NOT NULL,
    key           TEXT    NOT NULL,
    audience      TEXT    NOT NULL,
    failures      INTEGER NOT NULL DEFAULT 0,
    first_fail_at TEXT    NOT NULL,
    locked_until  TEXT,
    PRIMARY KEY (scope, key, audience)
);

CREATE TABLE requests (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    client_code         TEXT    NOT NULL,
    client_name         TEXT    NOT NULL,
    client_object       TEXT    NOT NULL,
    client_store        TEXT    NOT NULL,
    submitter_user_id   INTEGER REFERENCES users(id) ON DELETE SET NULL,
    submitter_email     TEXT    NOT NULL,
    saler_login         TEXT    NOT NULL,
    test_period         INTEGER NOT NULL DEFAULT 0,
    start_date          TEXT    NOT NULL,
    months              INTEGER NOT NULL,
    status              TEXT    NOT NULL DEFAULT 'pending',
    admin_comment       TEXT    NOT NULL DEFAULT '',
    decided_by_admin_id INTEGER REFERENCES admins(id) ON DELETE SET NULL,
    decided_at          TEXT,
    created_at          TEXT    NOT NULL,
    updated_at          TEXT    NOT NULL,
    CHECK (status IN ('pending', 'approved', 'denied')),
    CHECK (months BETWEEN 1 AND 12)
);
CREATE INDEX idx_requests_status_created ON requests(status, created_at);
CREATE INDEX idx_requests_client_code    ON requests(client_code);

CREATE TABLE request_usernames (
    request_id INTEGER NOT NULL REFERENCES requests(id) ON DELETE CASCADE,
    username   TEXT    NOT NULL,
    PRIMARY KEY (request_id, username)
);

CREATE TABLE request_modules (
    request_id INTEGER NOT NULL REFERENCES requests(id) ON DELETE CASCADE,
    module     TEXT    NOT NULL,
    tier       TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (request_id, module)
);

-- One row per (username x module) granted. Client fields are snapshotted so
-- the local record stays readable even if the external DB changes.
CREATE TABLE activations (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    client_code         TEXT    NOT NULL,
    client_name         TEXT    NOT NULL,
    client_object       TEXT    NOT NULL,
    client_store        TEXT    NOT NULL,
    username            TEXT    NOT NULL,
    module              TEXT    NOT NULL,
    tier                TEXT    NOT NULL DEFAULT '',
    start_date          TEXT    NOT NULL,
    end_date            TEXT    NOT NULL,
    revoked_at          TEXT,
    source_request_id   INTEGER REFERENCES requests(id) ON DELETE SET NULL,
    created_by_admin_id INTEGER REFERENCES admins(id) ON DELETE SET NULL,
    created_at          TEXT    NOT NULL,
    updated_at          TEXT    NOT NULL
);
CREATE INDEX idx_activations_client_code ON activations(client_code);
CREATE INDEX idx_activations_username    ON activations(username);
CREATE INDEX idx_activations_end_date    ON activations(end_date);

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE email_templates (
    key        TEXT PRIMARY KEY,
    subject    TEXT NOT NULL,
    body_html  TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE email_outbox (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    template_key    TEXT    NOT NULL,
    recipients      TEXT    NOT NULL,
    subject         TEXT    NOT NULL,
    body_html       TEXT    NOT NULL,
    attachment_name TEXT,
    attachment_blob BLOB,
    status          TEXT    NOT NULL DEFAULT 'pending',
    attempts        INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT    NOT NULL DEFAULT '',
    next_attempt_at TEXT    NOT NULL,
    sent_at         TEXT,
    created_at      TEXT    NOT NULL,
    CHECK (status IN ('pending', 'sent', 'failed'))
);
CREATE INDEX idx_email_outbox_pending ON email_outbox(status, next_attempt_at);

CREATE TABLE export_schedule (
    id           INTEGER PRIMARY KEY CHECK (id = 1),
    enabled      INTEGER NOT NULL DEFAULT 0,
    day_of_month INTEGER NOT NULL DEFAULT 1,
    time_hhmm    TEXT    NOT NULL DEFAULT '08:00',
    recipients   TEXT    NOT NULL DEFAULT '',
    last_run_at  TEXT,
    last_result  TEXT    NOT NULL DEFAULT '',
    next_run_at  TEXT,
    CHECK (day_of_month BETWEEN 1 AND 31)
);
INSERT INTO export_schedule (id) VALUES (1);

CREATE TABLE audit_log (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    actor_type   TEXT    NOT NULL,
    actor_id     INTEGER,
    actor_label  TEXT    NOT NULL DEFAULT '',
    action       TEXT    NOT NULL,
    target       TEXT    NOT NULL DEFAULT '',
    ip           TEXT    NOT NULL DEFAULT '',
    details_json TEXT    NOT NULL DEFAULT '{}',
    created_at   TEXT    NOT NULL
);
CREATE INDEX idx_audit_log_created ON audit_log(created_at);
CREATE INDEX idx_audit_log_action  ON audit_log(action);
