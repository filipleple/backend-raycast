# browser-raycast

A multiplayer browser game rendered entirely on the server. A single Go
process owns everything: auth, sessions, timing, the shared world, a
software raycasting renderer, a per-tile music system, an embedded
JavaScript layer that lets you script the map without a rebuild, and an admin
panel that syncs content from a Google Sheet and hot-reloads it live. The
browser is a thin client — it sends key state and paints the JPEG frames the
server sends back. PostgreSQL stores users and sessions.

All game content — maps, tile definitions, music, textures, scripts — lives
under **`content/`**, a git-versioned directory read at runtime and *not* baked
into the image, so content changes never require a rebuild or redeploy (see
[Admin panel & content pipeline](#admin-panel--content-pipeline)).

The renderer (the `game/` package) is a faithful Go port of a retired
Python/Pillow renderer, verified pixel-identical on deterministic input. See
[Design notes](#design-notes) for the two quirks that port deliberately
preserves.

---

## Architecture

```
┌──────────────────────────────────────────────────────────────┐
│  Browser  (static/game.html)                                   │
│                                                                │
│  keydown/keyup → inputState{}  ───────────► ws.send() 20Hz     │
│                                                                │
│  ws.onmessage:                                                 │
│    binary frame → createImageBitmap → <canvas>   (video)       │
│    text frame   → JSON events → HANDLERS{}       (control)     │
│                   music / sound / tone / popup / eval / custom │
│    AudioEngine: fetches /ost/ tracks, crossfades, WebAudio     │
└───────────────────────────┬────────────────────────────────────┘
                            │  HTTP: auth API, static files, /ost/
                            │  WebSocket (:8080):
                            │    ▲ JSON key state (in)
                            │    ▼ binary JPEG frames + text JSON events (out)
┌───────────────────────────┴────────────────────────────────────┐
│  Go server                                                     │
│                                                                │
│  main.go  — routing, WS upgrade, session guard, /ost/ static   │
│  auth.go  — register/login/logout, bcrypt, avatars, sessions   │
│                                                                │
│  Session (session.go)                                          │
│    ├── readWS()   [goroutine] JSON input → shared input map     │
│    └── tickLoop() [100ms] snapshot input → engine.Tick()        │
│          → text frame (drained events) then binary frame (JPEG) │
│                                                                │
│  game/ — shared world, one sync.RWMutex                        │
│    Step()    input → movement, doors, portals   [write lock]    │
│              ├─ scripts: onEnter / onUse / onTick / onJoin      │
│              └─ music:   MUSIC.csv zone → crossfade event       │
│    Render()  DDA raycasting → JPEG              [read lock]      │
│    scripts/  hot-reloaded *.js (goja interpreter)              │
└───────────────────────────────┬─────────────────────────────────┘
                                │  postgres (:5432)
                  ┌─────────────┴───────────────┐
                  │  PostgreSQL                  │
                  │  users     — bcrypt, avatars │
                  │  sessions  — token → user_id │
                  └──────────────────────────────┘
```

![](ss.png)

---

## Running

`docker compose up --build` starts three services: PostgreSQL, the Go server
(`:8080`), and a Caddy reverse proxy (`:80`/`:443`). For local play you hit
the Go server on `:8080` directly.

```bash
cp .env.example .env    # set POSTGRES_USER, POSTGRES_PASSWORD, POSTGRES_DB
docker compose up --build
```

Open `http://localhost:8080`, register an account, upload an optional avatar,
then **join game**. The compose file bind-mounts `./content` into the container
and sets `CONTENT_DIR=/app/content`, so the game reads content straight from
the repo — edits are picked up on the next reload, no rebuild.

Bare `go run .` (or `./run.sh`) also works for iterating on server code; it
reads content from `./content` (override with `CONTENT_DIR`) and expects a
Postgres reachable at `DATABASE_URL`.

To use the admin panel, make yourself an admin — set `ADMIN_USERNAME=you` (it's
promoted on boot) or run `UPDATE users SET is_admin = true WHERE username='you'`
— then open `/admin`.

### Production

`docker-compose.prod.yml` overlays the base file for deployment: it pulls the
pre-built image from GHCR, binds the backend to `127.0.0.1:18080` behind a
reverse proxy (nginx, Caddy, …), and bind-mounts a **dedicated content git
checkout** (`/srv/raycast-content`) that the admin panel commits into. Seed it
once (`git clone <content-repo> /srv/raycast-content`, or copy the repo's
`content/`).

```bash
GHCR_USER=you ./push.sh                                              # build + push image
docker compose -f docker-compose.yml -f docker-compose.prod.yml pull
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d
```

Because content is a mounted volume, a **code** change still goes build → push →
pull, but a **content** change is just the admin panel — no image rebuild, no
SSH. `deployment.md` has the full VPS walkthrough (WebSocket-aware nginx block,
the `db/migrations/` step, and content seeding).

---

## The WebSocket protocol

One connection at `/ws`, authenticated by the session cookie (401 otherwise),
carries three streams distinguished by frame type — the browser tells them
apart for free in `onmessage`:

| Direction | Frame | Payload |
|---|---|---|
| browser → server | text | `{"ArrowUp": true, ...}` key state, sent 20Hz |
| server → browser | binary | one JPEG video frame per tick (10Hz) |
| server → browser | text | JSON array of control events (music, sound, tone, popup, eval, or any custom type) |

Input rate (20Hz) and render rate (10Hz) are intentionally decoupled: input
is merged into a shared map as it arrives; the tick loop snapshots whatever is
current. Control events are queued per-player during a tick (`emit`) and
drained onto the wire right before that tick's video frame.

---

## Auth API

All endpoints return JSON.

| Method | Path | Description |
|--------|------|-------------|
| POST | `/register` | Create account `{username, password}` |
| POST | `/login` | Authenticate, set `HttpOnly` `session_token` cookie |
| POST | `/logout` | Invalidate session, close any active WS |
| POST | `/unregister` | Delete account (requires password confirmation) |
| GET | `/me` | Current username if logged in |
| GET | `/avatar` | Current user's avatar image |
| POST | `/avatar/upload` | Upload a PNG or GIF avatar (5 MB max) |

---

## Maps & tiles

The world is CSV-driven. Two files define it, plus a definitions table (all
paths below are under `content/`):

- **`definitions.csv`** — the tile dictionary. Each row maps a four-digit ID
  to behaviour: `texture_name, transparency, walk_through, wall, floor, door`.
  A texture name resolves to a file under `textures/<kind>/` (e.g. wall
  `stonewall` → `textures/walls/stonewall.gif`). Unknown IDs fall back to
  invisible + walkable.
- **`TILES.csv`** — the map grid. Every cell is a quoted **three-line** value
  — `ceiling`, `wall`, `floor` tile IDs, top to bottom, matching what you
  see looking at the cell. Rendering keys off the wall layer's `wall` flag; a
  cell is walkable only if the wall **and** floor layers are `walk_through`.
  A floor-type tile in the wall slot (water, rug, lava) decorates the floor
  instead of drawing a pane.
- **`MUSIC.csv`** — a parallel grid, same dimensions, one music-zone ID per
  cell (see below).

Special wall IDs: `0001` is the spawn tile; `door=1` tiles teleport between
their two walkable neighbours; portal tiles build a fresh map lazily on first
use and move the player across. World scale is fixed at 64 world px per tile.

---

## Music

Music is data, not code. `MUSIC.csv` tags each cell with a zone ID; stand in a
zone for ~300 ms (a short hysteresis stops boundary-strafing from stuttering)
and the browser crossfades to that zone's track. `M0000` is silence.

`MUSIC_DEFS.csv` (`id,file,loop,volume,fade_ms`) tunes each zone; IDs without
a row fall back to `/ost/<id>.wav`, looping, full volume. Tracks live in
`ost/` and are served at `/ost/` — the server only ever sends *"play M0002,
fade 1500 ms"*; the browser's `AudioEngine` fetches and plays. Any format the
browser can decode works (MP3/OGG/WAV; PCM WAV yes, ADPCM no).

Scripts can pin a player's track with `ctx.music("M0006")` and hand control
back to the zones with `ctx.musicAuto()`.

---

## Scripting

Drop a `.js` file in **`scripts/`** and the map starts doing things — no
rebuild, no restart. The server watches the folder and hot-reloads within a
second of any change; a script that throws is logged and the game keeps
running. Scripts run server-side in an embedded interpreter
([goja](https://github.com/dop251/goja)) under the engine write lock, so they
can mutate the world freely.

```js
// scripts/splash.js
onEnter("0204", ctx => ctx.tone(180, 300, 0.4, 0));   // blub on any water tile
```

Handlers register at load time and the engine fires them: `onEnter(spec, fn)`,
`onUse(spec, fn)` (space pressed while looking at a cell), `onJoin(fn)`,
`onTick(every, fn)`. The `ctx` object exposes messages to the browser
(`popup`, `tone`, `sound`, `music`, `js`, `send`), world mutation
(`setWall`/`setFloor`/`setCeiling`, live `cell()`, `teleport`) and queries
(`players`, `map`).

This is **deliberately not a sandbox** — scripts are repo code, same trust
level as `main.go`, and the API hands out live pointers on purpose (you can
noclip, teleport into a wall, or `eval` arbitrary JS in a player's browser).
The one guarantee: a broken or malicious-looking script can crash its own
player's session but never brings the server down. `scripts/blessed.js` is the
reference example; **[`scripts/README.md`](scripts/README.md) is the full API
reference and cookbook.**

---

## Admin panel & content pipeline

`/admin` (admin users only) turns a content change from *edit sheet → download
CSVs → commit → drop assets → push → SSH → rebuild → redeploy* into one button.
Content lives in the `content/` git repo the server reads; the panel edits and
reloads it in place.

- **Sync from Sheets.** Configure, once, the spreadsheet ID + per-tab `gid` for
  each of `definitions.csv`, `TILES.csv`, `MUSIC.csv`, `MUSIC_DEFS.csv` (the
  sheet must be shared *anyone with the link can view* — no OAuth). *Sync*
  fetches every tab, **validates** it by loading it in a throwaway staging tree,
  and only if it parses cleanly writes it, commits, and hot-reloads the live
  game. Broken content is rejected and nothing changes.
- **Upload assets.** Textures (PNG/GIF) and music/sound (`ost/`) upload straight
  from the browser (same multipart path as avatars), land in `content/`, get
  committed, and reload.
- **Versioning & rollback.** Every sync/upload is a git commit (authored by the
  admin). *Revisions* lists the history; *Roll back* restores any revision's
  exact tree — deletions included — as a new commit and reloads. So you can pin
  or revert "what broke what" with real diffs.

Under the hood: `game.LoadContent` (the validation gate — a parse error means
nothing changed) and `Engine.ReloadContent`, which swaps defs/map/music/sprite/
scripts under the write lock and **respawns live players** onto the fresh map
(their cached map pointer and absolute position would otherwise be invalid). A
content change reaches players in-place, no reconnect.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/admin` | The panel (admins only; others redirect to `/`) |
| GET | `/admin/status` | Content dir, git HEAD, players online |
| GET / POST | `/admin/sources` | Read / set the per-file sheet ID + gid |
| POST | `/admin/sync/sheets` | Fetch → validate → commit → reload |
| POST | `/admin/assets/upload` | Upload a texture or track (multipart) |
| GET | `/admin/revisions` | Commit history of `content/` |
| POST | `/admin/rollback` | Restore a revision `{sha}` and reload |

State-changing admin routes require the admin session cookie and reject
cross-origin requests (Origin/Referer check).

---

## Extending

Content edits (below) are picked up by a **reload** — click *Sync* / upload in
`/admin`, or restart. They never need an image rebuild. All paths are under
`content/`.

| I want to… | Do this |
|---|---|
| Edit maps/defs/music together | Edit the Google Sheet, then click **Sync** in `/admin` (fetch → validate → commit → live reload) |
| Add a texture | Upload it in `/admin`, or drop it in `content/textures/<walls\|floors+ceilings\|door\|sprites>/` and reference the bare name from a `definitions.csv` row |
| Add a tile type | Add a row to `content/definitions.csv` (ID + flags), then use the ID in `TILES.csv` |
| Edit the map | Edit `content/TILES.csv` cells — three IDs per cell (ceiling/wall/floor) |
| Add a music zone | Tag cells in `content/MUSIC.csv`, add a row to `MUSIC_DEFS.csv`, upload/drop the track in `content/ost/` |
| Make the map interactive | Write a `content/scripts/*.js` file — hot-reloads, see `content/scripts/README.md` |
| Add a new client event type | `ctx.send({type:"shake", ...})` server-side + a `shake:` entry in the `HANDLERS` table in `static/game.html` (unknown types are ignored, so either side can ship first) |
| Undo a bad content change | *Roll back* to an earlier revision in `/admin` |

### Dev workflow

```bash
go fmt ./...
go vet ./...
go test ./...          # engine + music/script tests (game/) and git/validation tests (main)
```

`game/*_test.go` drive the engine headlessly — walking a player onto a trigger
tile and asserting the emitted events, and exercising `LoadContent`/
`ReloadContent`. `admin_test.go` covers the sync validation gate and git
rollback. Together they check a script, map, or pipeline change without a
browser.

---

## Design notes

- **One lock, two access patterns.** The `game.Engine` guards the whole world
  with a single `sync.RWMutex`: `Step` (input, movement, per-axis collision,
  doors, scripts, music) takes the write lock; `RenderFrame` takes the read
  lock, so any number of sessions render concurrently.
- **Session lifecycle.** Each connection runs `readWS()` as a goroutine
  (blocks on reads, merges input) alongside `tickLoop()` on the handler
  goroutine (drives the 100 ms render clock). A `done chan struct{}` closed by
  `readWS` on disconnect lets `tickLoop` exit cleanly. `tickLoop` is the sole
  WS writer, so writes need no lock. It also `recover()`s: a script that
  panics the renderer drops that one player, not the server.
- **Session-cookie auth.** Login issues an `HttpOnly` `session_token` backed
  by the `sessions` table; `/ws` and every protected endpoint validate it per
  request. Only one live WS per user (`activeSessions` guard). `/admin/*` adds
  an `is_admin` check.
- **Content is data, not image.** `LoadContent` parses and validates a whole
  content directory before anything goes live (a bad CSV fails the load, so the
  running game is untouched); `ReloadContent` swaps it in under the write lock
  and respawns players. That's what lets the admin panel change content on a
  running server, and what keeps a broken sync from taking the game down.
- **Renderer fidelity.** DDA wall raycasting (120 rays across 60° FOV,
  transparent panes accumulated back-to-front), per-pixel floor/ceiling
  casting, billboard sprites with per-column depth testing, optional minimap,
  then `image/jpeg` at quality 60. Two Python-era quirks are preserved on
  purpose: nearest-neighbour texture coords accumulate by repeated addition
  (Pillow's incremental affine transform), and the floor caster evaluates
  `(d*x)/width` not `d*(x/width)` (numpy's order). **Don't "simplify" these
  without re-running a frame comparison.**

---

## File map

| Path | Role |
|---|---|
| `main.go` | HTTP routing, WS upgrade, session guard, engine + admin startup, `CONTENT_DIR`, `/ost/` static |
| `auth.go` | Register/login/logout/unregister, avatar upload/fetch, bcrypt, Postgres, `is_admin` |
| `admin.go` | `/admin/*` handlers: sheets sync, asset upload, git commit/log/rollback, validation staging |
| `sheets.go` | Public-link Google Sheets CSV fetch (the single sheet-access seam) |
| `session.go` | `Session`: `readWS` goroutine, `tickLoop`, text+binary frame writes, panic recovery |
| `game/world.go` | `Engine`, `Map`, `Player`; locking; `Join`/`Leave`/`Step`/`Tick`; scripted cell edits |
| `game/content.go` | `LoadContent` (load + validate a content dir) and `Engine.ReloadContent` (live hot-swap + player respawn) |
| `game/mapload.go` | `TILES.csv` + `definitions.csv` loader, `Symbol`, spawn, doors |
| `game/defs.go` | `definitions.csv` parser and tile `Def` table |
| `game/update.go` | Per-tick input: movement, collision, doors, portals; fires scripts + music |
| `game/dda.go` | DDA raycasting through the tile grid |
| `game/fov.go` | Fires 120 rays across the 60° FOV |
| `game/render.go` | Floor/ceiling casting, wall panes, sprites, minimap, JPEG encode |
| `game/texture.go` | PNG/GIF texture loading and sampling |
| `game/music.go` | `MUSIC.csv`/`MUSIC_DEFS.csv` loading, zone state machine, crossfade events |
| `game/script.go` | goja host: load + hot-reload `scripts/*.js`, handler registry, dispatch |
| `game/scriptctx.go` | `ScriptCtx` — the API surface handed to every script |
| `game/events.go` | `Event` type and the per-player outgoing event queue |
| `game/*_test.go` | Headless engine, music-zone, script, and content-reload tests |
| `admin_test.go` | Sync validation gate + git rollback tests |
| `templates/admin.html` | Admin panel UI (embedded into the binary via `go:embed`) |
| `content/` | **Git-versioned game content**, mounted at runtime — see below |
| `content/scripts/` | Hot-reloaded `*.js` game logic (`README.md` = API reference, `blessed.js` = example) |
| `content/ost/` | Music/sound assets served at `/ost/` |
| `content/{definitions,TILES,MUSIC,MUSIC_DEFS}.csv`, `textures/`, `hatman.gif` | Tile dictionary, map, music zones/defs, art |
| `db/schema.sql` | Postgres `users`, `sessions`, `content_sources` tables |
| `db/migrations/` | Hand-applied SQL for existing databases (schema.sql only runs on a fresh volume) |
| `static/index.html` | Login / register / avatar management |
| `static/game.html` | Gameplay canvas: input, WS loop, frame paint, `AudioEngine`, event `HANDLERS` |
| `Dockerfile.backend` | Go server image (binary + `static/`; content is mounted, not baked) |
| `docker-compose.yml` | Local: Postgres + go-backend + Caddy; bind-mounts `./content` |
| `docker-compose.prod.yml` | Production overlay: GHCR image, localhost binding, `/srv/raycast-content` mount |
</content>
