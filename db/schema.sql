CREATE TABLE IF NOT EXISTS users (
    id               SERIAL PRIMARY KEY,
    username         TEXT UNIQUE NOT NULL,
    password         TEXT NOT NULL,
    avatar           BYTEA,
    avatar_mime      TEXT,
    avatar_filename  TEXT,
    is_admin         BOOLEAN NOT NULL DEFAULT false
);

CREATE TABLE IF NOT EXISTS sessions (
    id      SERIAL PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token   TEXT UNIQUE NOT NULL
);

-- Per-file Google Sheets source: which spreadsheet tab feeds each content CSV.
-- Edited from the admin panel; consumed by the /admin/sync/sheets handler.
CREATE TABLE IF NOT EXISTS content_sources (
    filename  TEXT PRIMARY KEY,
    sheet_id  TEXT NOT NULL,
    gid       TEXT NOT NULL
);

