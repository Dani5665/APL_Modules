-- A salesperson has a display name, shown as the sender of requests.
ALTER TABLE users ADD COLUMN name TEXT NOT NULL DEFAULT '';

-- Requests to create a customer's profile in the NEXT catalog, filed from the
-- customer information page. The requested password is stored encrypted.
CREATE TABLE profile_requests (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    client_code     TEXT    NOT NULL,
    client_name     TEXT    NOT NULL,
    store_name      TEXT    NOT NULL,
    username        TEXT    NOT NULL,
    password_enc    BLOB    NOT NULL,
    transport       TEXT    NOT NULL,
    submitter_id    INTEGER REFERENCES users(id) ON DELETE SET NULL,
    submitter_name  TEXT    NOT NULL,
    submitter_email TEXT    NOT NULL,
    submitter_stores TEXT   NOT NULL,
    comment         TEXT    NOT NULL DEFAULT '',
    created_at      TEXT    NOT NULL
);
CREATE INDEX idx_profile_requests_client ON profile_requests(client_code);
