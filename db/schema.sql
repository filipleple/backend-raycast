-- Accounts are 629fm's (629Manager); this only maps a principal to the int
-- player ID the engine uses, plus game-only data.
CREATE TABLE IF NOT EXISTS players (
    id               SERIAL PRIMARY KEY,
    principal_id     UUID UNIQUE NOT NULL,
    username         TEXT NOT NULL,   -- copy of the 629fm username, refreshed on login
    avatar           BYTEA,
    avatar_mime      TEXT,
    avatar_filename  TEXT
);

-- Per-file Google Sheets source: which spreadsheet tab feeds each content CSV.
-- Edited from the admin panel; consumed by the /admin/sync/sheets handler.
CREATE TABLE IF NOT EXISTS content_sources (
    filename  TEXT PRIMARY KEY,
    sheet_id  TEXT NOT NULL,
    gid       TEXT NOT NULL
);
