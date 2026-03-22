CREATE TABLE IF NOT EXISTS users (
    id               SERIAL PRIMARY KEY,
    username         TEXT UNIQUE NOT NULL,
    password         TEXT NOT NULL,
    avatar           BYTEA,
    avatar_mime      TEXT,
    avatar_filename  TEXT
);

CREATE TABLE IF NOT EXISTS sessions (
    id      SERIAL PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token   TEXT UNIQUE NOT NULL
);

