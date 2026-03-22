# Deployment walkthrough

## First time setup

### 1. Credentials

```bash
cp .env.example .env
```

Edit `.env` with real values:

```
POSTGRES_USER=myuser
POSTGRES_PASSWORD=yourpassword
POSTGRES_DB=myapp
GHCR_USER=yourgithubusername
```

Add `.env` to `.gitignore` so it never gets committed.

### 2. GHCR login (laptop and VPS, once each)

Create a GitHub personal access token with `write:packages` scope, then:

```bash
echo "YOUR_TOKEN" | docker login ghcr.io -u YOUR_GITHUB_USERNAME --password-stdin
```

---

## Building and pushing (laptop)

After any code change, run from `backend-raycast/`:

```bash
GHCR_USER=yourusername ./push.sh
```

This builds both images and pushes them to GHCR as `:latest`.

---

## Deploying on the VPS

### Pull and start

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml pull
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d
```

### Check it came up

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml ps
docker compose -f docker-compose.yml -f docker-compose.prod.yml logs -f go-backend
```

### Smoke test (from the VPS, before touching nginx)

```bash
curl -i http://127.0.0.1:18080/
curl -i http://127.0.0.1:18080/me
```

---

## Wiring into AzuraCast nginx

Edit `/var/azuracast/custom.conf` and add:

```nginx
location = /myapp {
    return 301 /myapp/;
}

location /myapp/ {
    proxy_pass http://127.0.0.1:18080/;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_set_header Host $host;
    include proxy_params;
    proxy_cookie_path / /myapp/;
}
```

The three `proxy_http_version` / `proxy_set_header` lines are required for WebSocket to work — without them nginx strips the upgrade headers.

Validate and reload:

```bash
docker exec azuracast nginx -t
docker exec azuracast nginx -s reload
```

Test through nginx:

```bash
curl -i http://127.0.0.1/myapp/
curl -i http://127.0.0.1/myapp/me
```

Then open `https://yourdomain.com/myapp/` in a browser.

---

## Updating after a code change

1. Make changes on laptop
2. `GHCR_USER=yourusername ./push.sh`
3. On VPS:
```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml pull
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d
```

No rebuild on the VPS, no downtime beyond container restart.

---

## Cautions

- Never bind the app to public `8080` — `docker-compose.prod.yml` locks it to `127.0.0.1:18080`.
- Do not add a `server {}` block to `custom.conf` — it is included inside AzuraCast's existing server block, so only `location` rules belong there.
- Schema changes do not apply automatically to an existing Postgres volume. The `db/schema.sql` init script only runs on a fresh volume. If you change the schema, either migrate manually or `docker compose down -v` (wipes all data) and bring it back up.
- `docker compose down` without `-v` is safe — keeps the database volume intact.
