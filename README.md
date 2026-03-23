# browser-raycast

A multiplayer browser game where a Go server bridges a WebSocket frontend and a
Python rendering backend. The Go side manages sessions, auth, and timing; Python
owns the game state and produces frames. PostgreSQL stores users and sessions.

---

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│  Browser                                                │
│                                                         │
│  index.html  — login / register / avatar upload         │
│  game.html   — gameplay canvas                          │
│                                                         │
│  keydown/keyup → inputState{}  ──────► ws.send() 20Hz  │
│  ws.onmessage ──► createImageBitmap ──► canvas          │
└───────────────────────┬─────────────────────────────────┘
                        │  HTTP (auth API + static files)
                        │  WebSocket  (port 8080)
                        │  JSON key state  ▼
                        │  PNG frames      ▲
┌───────────────────────┴─────────────────────────────────┐
│  Go server                                              │
│                                                         │
│  auth.go — register/login/logout, bcrypt, sessions      │
│  main.go — HTTP routing, WS upgrade, session guard      │
│                                                         │
│  handleWS()                                             │
│    └── Session                                          │
│          ├── readWS()  [goroutine]                      │
│          │     reads JSON from WS → updates input map   │
│          │     on disconnect → closes done channel      │
│          │                                              │
│          └── tickLoop()  [main goroutine]               │
│                fires every 50ms                         │
│                snapshots input → SendInput → recv PNG   │
│                sends PNG to browser via WS              │
│                on done / error → Cleanup()              │
└──────────┬────────────────────────┬─────────────────────┘
           │  TCP  (port 9000)      │  postgres (port 5432)
           │  length-prefixed       │  users, sessions
           │  JSON input  ▼         │
           │  PNG frame   ▲         │
┌──────────┴──────────────┐  ┌──────┴──────────────────────┐
│  Python renderer        │  │  PostgreSQL                  │
│  (renderer/main.py)     │  │                              │
│                         │  │  users  — bcrypt passwords,  │
│  accept loop            │  │           avatar blobs        │
│    └── handle_client()  │  │  sessions — token → user_id  │
│          recv JSON →    │  └─────────────────────────────┘
│          update(state)  │
│          → render()     │
│          → PNG bytes    │
│          send PNG back  │
└─────────────────────────┘
```

---

![](ss.png)

---

## Running

The stack runs as three containers. Copy the example env, then bring it up:

```bash
cp .env.example .env   # fill in POSTGRES_USER, POSTGRES_PASSWORD, POSTGRES_DB
docker compose up --build
```

Open `http://localhost:8080`. Register an account, upload an optional avatar,
then click **join game**.

The old `run.sh` (bare `go run . && python3 renderer/main.py`) still works for
local dev but requires a local Postgres instance reachable at the default
`DATABASE_URL`.

### Production

`docker-compose.prod.yml` extends the base compose file for deployment — it
pulls pre-built images from GHCR and binds the backend only to localhost so a
reverse proxy (nginx, Caddy, etc.) can front it:

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d
```

---

## Auth API

All endpoints return JSON. The WebSocket at `/ws` requires a valid session
cookie — unauthenticated connections are rejected with 401.

| Method | Path | Description |
|--------|------|-------------|
| POST | `/register` | Create account `{username, password}` |
| POST | `/login` | Authenticate, set `session_token` cookie |
| POST | `/logout` | Invalidate session, close any active WS |
| POST | `/unregister` | Delete account (requires password confirmation) |
| GET | `/me` | Returns current username if logged in |
| GET | `/avatar` | Fetch current user's avatar image |
| POST | `/avatar/upload` | Upload PNG or GIF avatar (5 MB max) |

---

## Concepts used

### WebSocket
Persistent full-duplex connection between browser and Go server, over HTTP
upgrade. Used here for two separate data streams on one connection: JSON key
state going in, binary PNG frames coming out. The browser sets `binaryType =
"blob"` so received frames can be fed directly to `createImageBitmap`.

### TCP with length-prefixed framing
Raw TCP has no concept of messages — it's a byte stream. To send structured
messages, every payload is prefixed with a 4-byte big-endian length. The
receiver reads the header first, then reads exactly that many bytes. This is
implemented symmetrically in `protocol.go` and `renderer/protocol.py`.
`io.ReadFull` / `recv_exact` ensure partial reads are handled correctly.

### Goroutines and channels
Each browser session spawns two goroutines worth of work:
- `readWS` runs as a goroutine — blocks on WS reads, writes input state
- `tickLoop` runs on the handler goroutine — drives the render clock

Shutdown coordination uses a `done chan struct{}`: `readWS` closes it on
disconnect, `tickLoop` selects on it to exit cleanly. This is idiomatic Go for
one-time signals between goroutines.

### Mutex / shared state
`readWS` and `tickLoop` run concurrently and both touch `input`. A
`sync.RWMutex` protects it: `readWS` takes a write lock when merging new keys,
`tickLoop` takes a read lock to snapshot before each render tick. `tickLoop` is
the sole writer to the WebSocket connection, avoiding the need to lock writes.

### Decoupled tick rate vs. input rate
The browser sends input at 20Hz. The renderer ticks at 20Hz (50ms). These are
intentionally independent: input arrives asynchronously and is merged into a
shared map; the ticker snapshots whatever is current. This avoids coupling the
render clock to network jitter.

### Request-response over TCP
The Go-to-Python protocol is synchronous request/response per tick: send JSON
input, block until PNG comes back. Simple and sufficient — no pipelining needed.

### Session-cookie auth
Login issues an `HttpOnly` `session_token` cookie backed by a Postgres
`sessions` table. The WebSocket handler and all protected endpoints look up the
session on every request. Logout deletes the DB row and closes any open WS
connection for that user.

### Python as a rendering subprocess
Python owns `GameState` and `Renderer`. It uses Pillow to draw to an in-memory
image, encodes it as PNG, and sends the bytes over TCP. This keeps game logic
and rendering in Python while Go handles all the networking and auth.

---

## File map

| File | What it does |
|---|---|
| `main.go` | HTTP routing, WebSocket upgrade, session guard |
| `auth.go` | Register/login/logout handlers, bcrypt, Postgres helpers |
| `session.go` | `Session` type: `readWS` goroutine + `tickLoop`, lifecycle |
| `python.go` | `PythonClient`: TCP connection to renderer, `SendInput` |
| `protocol.go` | Low-level framing: `sendFrame`, `recvBinary`, `recvExact` |
| `db/schema.sql` | Postgres schema: `users`, `sessions` tables |
| `static/index.html` | Login/register page + avatar management |
| `static/game.html` | Gameplay canvas: key capture, WS loop, frame rendering |
| `renderer/main.py` | Python TCP server: accept loop, per-client handler |
| `renderer/render.py` | Game state, update logic, Pillow rendering |
| `renderer/protocol.py` | Python mirror of the length-prefixed framing protocol |
| `Dockerfile.backend` | Go server image |
| `Dockerfile.py` | Python renderer image |
| `docker-compose.yml` | Local dev: builds all three services |
| `docker-compose.prod.yml` | Production overlay: GHCR images, restart policies |
