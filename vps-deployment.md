## Deployment note

### 1) What the VPS recon established

* The public web entrypoint is already occupied by **AzuraCast**, running in Docker and publishing `80`, `443`, and a very large range of `8000`-series ports. There is also another container already exposed on `8443`. There is no host-level `nginx`, `apache2`, or `caddy` service to hook into directly.
* AzuraCast bind-mounts `/var/azuracast/custom.conf` into `/etc/nginx/azuracast.conf.d/custom.conf`, and nginx includes `/etc/nginx/azuracast.conf.d/*.conf` **inside its existing public `server { ... }` block**. That means custom integration should be done with extra `location` rules, not a new standalone `server` block.
* The test ports `18080` and `15432` were checked and came back free, so they are suitable for private, non-public testing. 
* The VPS is small: Docker is installed and working, but RAM is only about `1.9 GiB` with **no swap**, and Docker already has some reclaimable cache. That makes “build on laptop, pull on VPS” the safer route.

### 2) How the app should be tested first, then integrated

* First run the app **privately only** on the VPS at `127.0.0.1:18080`, with Postgres kept internal to the Compose network. Do not publish the app on `80`, `443`, or a public `8080`. AzuraCast already owns the public ingress.
* Verify the app locally on the VPS with `curl http://127.0.0.1:18080/...`.
* After that, integrate it through AzuraCast nginx using a **path prefix**, for example `/myapp/`, by editing `/var/azuracast/custom.conf`. Because that file is included inside the existing nginx `server`, the right pattern is a `location /myapp/ { ... }` proxy, not a separate virtual host.

Recommended nginx snippet for `/var/azuracast/custom.conf`:

```nginx
location = /myapp {
    return 301 /myapp/;
}

location /myapp/ {
    proxy_pass http://127.0.0.1:18080/;
    include proxy_params;
    proxy_cookie_path / /myapp/;
}
```

Why this works:

* `/myapp/login` on the public site becomes `/login` on the Go app because of the trailing slash on `proxy_pass`.
* `proxy_cookie_path / /myapp/;` keeps session cookies scoped to `/myapp/` instead of the whole domain.

### 3) How to build and push the custom images to GHCR

You only need to build and push the **custom** images:

* `go-backend`
* `py-renderer`

You do **not** need to push Postgres; keep using `postgres:16-alpine`. Your current compose file already uses local builds for the Go and Python services and the official Postgres image for DB. 

On your laptop:

```bash
export GHCR_USER=YOUR_GITHUB_USERNAME
export GHCR_NS=ghcr.io/$GHCR_USER

echo "$GITHUB_TOKEN" | docker login ghcr.io -u "$GHCR_USER" --password-stdin

docker build -f Dockerfile.backend -t $GHCR_NS/myapp-go-backend:latest .
docker build -f Dockerfile.py -t $GHCR_NS/myapp-py-renderer:latest .

docker push $GHCR_NS/myapp-go-backend:latest
docker push $GHCR_NS/myapp-py-renderer:latest
```

If you want versioned tags too:

```bash
export TAG=2026-03-22

docker tag $GHCR_NS/myapp-go-backend:latest $GHCR_NS/myapp-go-backend:$TAG
docker tag $GHCR_NS/myapp-py-renderer:latest $GHCR_NS/myapp-py-renderer:$TAG

docker push $GHCR_NS/myapp-go-backend:$TAG
docker push $GHCR_NS/myapp-py-renderer:$TAG
```

### 4) How `docker-compose.yml` should change and why

Current key facts from the file:

* `go-backend` is currently published as `8080:8080`, which would expose it publicly.
* `db` already has no public host port.
* `go-backend` depends on `db` and `py-renderer`.
* `db` mounts `./db/schema.sql` into `/docker-entrypoint-initdb.d/schema.sql`. That script only runs automatically on a **fresh** Postgres data directory. 

Use this shape instead:

```yaml
services:
  db:
    image: postgres:16-alpine
    restart: unless-stopped
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
    expose:
      - "5432"

  py-renderer:
    image: ghcr.io/YOUR_GITHUB_USERNAME/myapp-py-renderer:latest
    restart: unless-stopped

  go-backend:
    image: ghcr.io/YOUR_GITHUB_USERNAME/myapp-go-backend:latest
    restart: unless-stopped
    ports:
      - "127.0.0.1:18080:8080"
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

Why:

* `127.0.0.1:18080:8080` makes the app reachable **only from the VPS itself**, so AzuraCast/nginx can proxy to it without exposing a new public port.
* `db` stays internal-only.
* `restart: unless-stopped` makes the stack survive reboots.
* Replacing `build:` with `image:` means the VPS only pulls images instead of building them locally, which is safer on a small host.

One app-side adjustment if the public URL will live under `/myapp/`:

* set your session cookie path to `Path: "/myapp"` in Go, or rely on nginx `proxy_cookie_path`. The nginx rewrite is enough for first deployment.

### 5) Step-by-step test and deploy sequence on the VPS

1. **Prepare GHCR auth on the VPS**

```bash
echo "$GITHUB_TOKEN" | docker login ghcr.io -u YOUR_GITHUB_USERNAME --password-stdin
```

2. **Clone repo and switch compose to `image:`**

```bash
git clone YOUR_REPO_URL myapp
cd myapp
```

3. **Use the revised `docker-compose.yml`**

* change `go-backend` to `127.0.0.1:18080:8080`
* switch `go-backend` and `py-renderer` from `build:` to `image:`
* keep Postgres internal-only

4. **Pull and start**

```bash
docker compose pull
docker compose up -d
docker compose ps
docker compose logs -f go-backend
```

5. **Private VPS-only smoke test**

```bash
curl -i http://127.0.0.1:18080/
curl -i http://127.0.0.1:18080/me
```

6. **Add proxy rule to AzuraCast**
   Edit `/var/azuracast/custom.conf` and append:

```nginx
location = /myapp {
    return 301 /myapp/;
}

location /myapp/ {
    proxy_pass http://127.0.0.1:18080/;
    include proxy_params;
    proxy_cookie_path / /myapp/;
}
```

7. **Validate nginx before reload**

```bash
docker exec azuracast nginx -t
```

8. **Reload nginx**

```bash
docker exec azuracast nginx -s reload
```

9. **Test through AzuraCast**

```bash
curl -i http://127.0.0.1/myapp/
curl -i http://127.0.0.1/myapp/me
```

10. **Browser test**
    Open:

```text
https://YOUR_DOMAIN/myapp/
```

11. **If login/session testing is needed from the browser console**
    Use `fetch("/myapp/login", ...)`, `fetch("/myapp/me", { credentials: "include" })`, and keep requests under the `/myapp` prefix.

### 6) Operational cautions

* Do not bind the app to public `8080`. The current compose file would do that if left unchanged. 
* Do not try to add a new nginx `server {}` in `custom.conf`; that file is included inside AzuraCast’s existing `server`.
* Do not expect changes to `db/schema.sql` to apply automatically on an existing Postgres volume. That init script is only for first initialization. 
* Prefer image pulls over local builds on this VPS because the host is small and already runs AzuraCast and another app.

If you want this turned into a ready-to-paste `README-deploy.md`, I can format it exactly that way.

