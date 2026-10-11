-- Migration 0002: accounts move to 629fm (629Manager).
--
-- db/schema.sql only runs on a FRESH Postgres volume, so apply this by hand to
-- an existing database, e.g.:
--   docker compose exec -T db psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" \
--       < db/migrations/0002_629fm_accounts.sql
-- Admin is now the 629fm account's `admin` role; users.is_admin is unused.
--
-- The old local `users`/`sessions` tables are left alone (their avatars could
-- still be copied over by hand). Once nobody needs them:
--   DROP TABLE sessions; DROP TABLE users;

CREATE TABLE IF NOT EXISTS players (
    id               SERIAL PRIMARY KEY,
    principal_id     UUID UNIQUE NOT NULL,
    username         TEXT NOT NULL,
    avatar           BYTEA,
    avatar_mime      TEXT,
    avatar_filename  TEXT
);
