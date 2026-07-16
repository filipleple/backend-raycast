-- Migration 0001: admin flag + Google Sheets content sources.
--
-- db/schema.sql only runs on a FRESH Postgres volume, so apply this by hand to
-- an existing database, e.g.:
--   docker compose exec -T db psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" \
--       < db/migrations/0001_admin_and_content_sources.sql
-- Then promote your admin (or set ADMIN_USERNAME and restart the backend):
--   UPDATE users SET is_admin = true WHERE username = 'you';

ALTER TABLE users ADD COLUMN IF NOT EXISTS is_admin BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE IF NOT EXISTS content_sources (
    filename  TEXT PRIMARY KEY,
    sheet_id  TEXT NOT NULL,
    gid       TEXT NOT NULL
);
