# Initial deployment — the content-pipeline switchover

This is the **one-time** checklist for the deploy that moves game content out of
the Docker image and into a git-versioned `content/` directory, and adds the
`/admin` panel. After this first deploy, content changes are made entirely
through `/admin` (sync from Sheets, upload assets, roll back) and hot-reload on
the running server — **no image rebuild, no `docker compose pull`, no SSH.** Only
*code* changes still go through build → push → pull.

For the full VPS walkthrough (AzuraCast nginx block, TLS, smoke tests) see
[`deployment.md`](deployment.md); this doc is only the extra steps this
switchover introduces.

---

## Why it isn't zero-touch this once

This deploy is not "fetch + `docker compose up`" because it:

1. **Changes the DB schema** — adds `users.is_admin` and a `content_sources`
   table. `db/schema.sql` only runs on a *fresh* Postgres volume, so an existing
   database needs the migration applied by hand.
2. **Introduces a mounted content dir** — content is no longer baked into the
   image; it's a git repo bind-mounted at `/app/content`. That directory must
   exist and be seeded, or the server can't load content and exits.
3. **Needs an admin user** — the `/admin` panel is gated on `is_admin`.

On the small VPS, keep building on the laptop and pulling on the VPS (don't
`--build` on the box).

---

## Steps (in order)

### 0. Laptop — build & push the new image

```bash
GHCR_USER=you ./push.sh
```

### 1. VPS — apply the DB migration **before** starting the new binary

> Do this first. The new code's session lookup selects `is_admin`; if that
> column is missing, **every** login / `/me` / `/ws` query errors — not just
> admin, the whole site.

```bash
# with your .env values in scope (POSTGRES_USER / POSTGRES_DB)
docker compose -f docker-compose.yml -f docker-compose.prod.yml exec -T db \
  psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" < db/migrations/0001_admin_and_content_sources.sql
```

### 2. VPS — seed the content git repo

`docker-compose.prod.yml` bind-mounts `/srv/raycast-content` → `/app/content`.
It must exist and be a git repo (that repo *is* your version history). Seed it
**as root** so its ownership matches the container's user (avoids git's
"dubious ownership" refusal):

```bash
sudo mkdir -p /srv/raycast-content
sudo cp -a content/. /srv/raycast-content/          # or: git clone <content-repo> /srv/raycast-content
sudo git -C /srv/raycast-content init -q
sudo git -C /srv/raycast-content add -A
sudo git -C /srv/raycast-content -c user.name=seed -c user.email=seed@local commit -qm "seed content"
```

### 3. VPS — become admin, then start

Set `ADMIN_USERNAME=you` in `.env` (promoted on every boot), **or** run once:

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml exec db \
  psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "UPDATE users SET is_admin = true WHERE username = 'you';"
```

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml pull
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d
```

Open `/admin`, set each file's spreadsheet ID + tab `gid` under **Sources**,
then click **Sync**. Done — from here on, content is a browser button.

---

## How content is versioned

`content/` is a git repo and the running server commits to it. Every **Sync**,
**Upload**, and **Rollback** is a commit authored by the admin who clicked it;
history is plain `git log`, rendered in the panel's *Revisions* tab.

**Versioning is only active when the mounted dir is itself a git repo** (has its
own `.git` at the root). `/srv/raycast-content` is, so it's on. The code repo's
`content/` subdirectory is *not* a standalone repo — if you ever bind-mount it
directly into a container, Sync/Upload still work and reload, but there are no
commits or rollback. **`/admin/status` shows which** (`git: on/off`) and the
current HEAD.

---

## Rolling back

From `/admin` → **Revisions** → **Roll back** on any older commit. It restores
that revision's exact tree (deletions included), records the restore as a new
commit on top, and **hot-reloads the live game**.

Prefer the panel over editing on the host: a manual host-side `git checkout`
does **not** reload the running engine — the server keeps serving the old
content until the panel triggers a reload (or the container restarts).

Inspect from the host any time:

```bash
git -C /srv/raycast-content log --oneline      # history
git -C /srv/raycast-content show --stat <sha>  # what a revision changed
```

---

## File structure

Content is consolidated under `content/`; everything else is code/config.

```
backend-raycast/
├── main.go  auth.go  admin.go  sheets.go  session.go   ← server
├── game/            ← engine (world, render, content.go = load+reload, music, script…)
├── templates/admin.html   ← admin panel (embedded in the binary)
├── static/          ← index / menu / game.html (app shell, baked into image)
├── db/
│   ├── schema.sql          (fresh-volume init)
│   └── migrations/0001_…    (hand-applied to existing DBs)
├── content/         ←──── THE VERSIONED GAME DATA (git repo at runtime) ────
│   ├── definitions.csv  TILES.csv  MUSIC.csv  MUSIC_DEFS.csv  hatman.gif
│   ├── textures/{walls,floors,floors+ceilings,door,sprites,misc}/
│   ├── ost/  (M0001–M0006.mp3, + originals/)   → served at /ost/
│   └── scripts/  (*.js hot-reloaded, README.md, blessed.js)
├── Dockerfile.backend   (binary + static/ only — content is NOT baked)
├── docker-compose.yml         (local: bind-mounts ./content)
└── docker-compose.prod.yml    (prod: mounts /srv/raycast-content)
```

Mental model: **`static/` + the Go binary ship in the image (code); everything
under `content/` is a git-versioned volume the admin panel edits live.**

---

## Troubleshooting

- **All logins fail after deploy** → step 1 (migration) wasn't applied; the
  `is_admin` column is missing.
- **Server exits on boot, "loading game assets"** → `/srv/raycast-content` is
  empty or missing; re-seed (step 2).
- **`/admin/status` shows `git: off`** → the mounted content dir isn't a git
  repo; `git init` it (step 2).
- **git "dubious ownership" in logs** → the content dir is owned by a different
  user than the container (root); `sudo chown -R root:root /srv/raycast-content`.
- **`/admin` redirects to `/`** → you're logged in but not admin; see step 3.
