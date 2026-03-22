# Containerization Plan

## Services

Three services that need to talk to each other:

| Service | Language | Listens on |
|---------|----------|-----------|
| `go-backend` | Go | `:8080` (HTTP + WS) |
| `py-renderer` | Python | `:9000` (TCP) |
| `db` | PostgreSQL | `:5432` |

The Go backend talks to both `py-renderer` (TCP) and `db` (Postgres).
The browser talks only to Go.

---

## Step 1 — Write the DB schema file

Create `db/schema.sql` with the tables the app assumes exist:

```sql
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
```

Docker's official Postgres image runs any `.sql` files placed in
`/docker-entrypoint-initdb.d/` automatically on first boot. This file
gets mounted there via `docker-compose.yml`.

---

## Step 2 — Make the DB connection string configurable

`connectPostgresDB()` in `auth.go` has a hardcoded connection string.
Replace it with an env var:

```go
connString := os.Getenv("DATABASE_URL")
if connString == "" {
    connString = "user=myuser dbname=myapp password=strongpassword host=localhost port=5432 sslmode=disable"
}
```

The fallback keeps local dev working without Docker.

---

## Step 3 — Make the Python renderer address configurable

`handleWS` in `main.go` hardcodes `"127.0.0.1:9000"`. Replace with an env var:

```go
pyAddr := os.Getenv("RENDERER_ADDR")
if pyAddr == "" {
    pyAddr = "127.0.0.1:9000"
}
pythonClient, err := NewPythonClient(pyAddr)
```

In Docker Compose the value will be `"py-renderer:9000"` (the service
name, not 127.0.0.1).

---

## Step 4 — Write `Dockerfile.go`

```dockerfile
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o server .

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/server .
COPY static/ ./static/
EXPOSE 8080
CMD ["./server"]
```

Two-stage build: the `builder` stage compiles, the final image is
just Alpine with the binary + static files. No Go toolchain ships to
production.

---

## Step 5 — Create `renderer/requirements.txt`

Based on the imports in `renderer/main.py`:

```
Pillow
numpy
```

---

## Step 6 — Write `Dockerfile.py`

```dockerfile
FROM python:3.13-slim
WORKDIR /app
COPY renderer/requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY renderer/ ./renderer/
COPY hatman.gif .
COPY frames/ ./frames/
COPY textures/ ./textures/
WORKDIR /app/renderer
CMD ["python", "main.py"]
```

---

## Step 7 — Write `docker-compose.yml`

```yaml
services:
  db:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: myuser
      POSTGRES_PASSWORD: strongpassword
      POSTGRES_DB: myapp
    volumes:
      - db-data:/var/lib/postgresql/data
      - ./db/schema.sql:/docker-entrypoint-initdb.d/schema.sql
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U myuser -d myapp"]
      interval: 5s
      retries: 5

  py-renderer:
    build:
      context: .
      dockerfile: Dockerfile.py

  go-backend:
    build:
      context: .
      dockerfile: Dockerfile.go
    ports:
      - "8080:8080"
    environment:
      DATABASE_URL: "user=myuser dbname=myapp password=strongpassword host=db port=5432 sslmode=disable"
      RENDERER_ADDR: "py-renderer:9000"
    depends_on:
      db:
        condition: service_healthy
      py-renderer:
        condition: service_started

volumes:
  db-data:
```

Key points:
- `host=db` in `DATABASE_URL` is the Docker Compose service name, not `127.0.0.1`
- `depends_on` with `service_healthy` on `db` prevents Go from crashing on startup before Postgres is ready
- `db-data` volume persists the database across container restarts

---

## File checklist

| File to create | What it is |
|----------------|-----------|
| `db/schema.sql` | Table definitions, auto-run by Postgres on first boot |
| `Dockerfile.go` | Two-stage Go build |
| `Dockerfile.py` | Python renderer image |
| `renderer/requirements.txt` | `Pillow` and `numpy` |
| `docker-compose.yml` | Wires all three services together |

| Code to change | What to change |
|----------------|---------------|
| `auth.go` `connectPostgresDB` | Read `DATABASE_URL` from env with hardcoded fallback |
| `main.go` `handleWS` | Read `RENDERER_ADDR` from env with hardcoded fallback |
