<div align="center">
  <img src=".github/assets/logo.png" alt="Goku" width="200" />
  <h1>goku</h1>
  <p><em>Enlightenment (悟 → `go`) through the (homelab) void (空 → `ku`)</em></p>
  <p>A golinks solution written in Go.</p>

[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![CI](https://github.com/jovalle/goku/actions/workflows/ci.yml/badge.svg)](https://github.com/jovalle/goku/actions/workflows/ci.yml)
[![Docker Workflow](https://github.com/jovalle/goku/actions/workflows/docker.yml/badge.svg)](https://github.com/jovalle/goku/actions/workflows/docker.yml)
[![Release Workflow](https://github.com/jovalle/goku/actions/workflows/release.yml/badge.svg)](https://github.com/jovalle/goku/actions/workflows/release.yml)
[![Latest Release](https://img.shields.io/github/v/release/jovalle/goku?display_name=tag)](https://github.com/jovalle/goku/releases)
[![Coverage](https://img.shields.io/badge/Coverage-go%20test%20%28CI%29-31c653)](https://github.com/jovalle/goku/actions/workflows/ci.yml)

</div>

---

## Quick Start

1. Run goku:

```bash
just run
```

or:

```bash
go run ./cmd/goku
```

1. Open the apps:

- Public endpoint: `http://localhost:9000`
- Admin panel: `http://localhost:9001`

1. (Optional) enable admin login:

```bash
GOKU_ADMIN_PASSWORD=my-secret just run
```

Without `GOKU_ADMIN_PASSWORD`, the admin UI is open (no logout button shown).

## Current Capabilities

- Alias-based redirects with placeholder support (`{}` and named placeholders)
- Public and admin endpoints split by port (`:9000` and `:9001`)
- Live health JSON (`/healthz`) on both ports and WebSocket stream (`/ws/health`) on public
- Admin alias directory with:
  - search
  - sortable columns
  - add / edit / delete
  - enable / disable toggle
  - clickable destination links
  - clickable alias preview page with countdown redirect via public endpoint
- API key + optional password authentication for admin/API operations
- Config live-reload from disk
- Dashboard/API alias changes are written back to `config/config.yaml` for persistence
- Prometheus metrics (`/metrics`) on admin

## Configuration

Edit `config/config.yaml`:

```yaml
aliases:
  - alias: gh
    destination: https://github.com
  - alias: gh/{owner}/{repo}
    destination: https://github.com/{owner}/{repo}
  - alias: r/{subreddit}
    destination: https://www.reddit.com/r/{subreddit}
  - alias: yt/{}
    destination: https://www.youtube.com/results?search_query={}
```

Placeholder rules:

- Single placeholder aliases can use `{}`.
- Multiple placeholders must be uniquely named.
- Destination placeholders must be defined by the alias pattern.

`go/` prefix note:

- Keep using `go/` style links if that is your workflow (for example `http://go/gh` or `http://go/r/golang`).
- This assumes your DNS (or local host mapping) resolves `go` (or a hostname like `go.home.arpa`) to your goku endpoint.
- Without DNS/host routing, use the explicit server URL instead (for example `http://localhost:9000/gh`).

## Endpoints

Public (`:9000`):

| Method | Path         | Description             |
| ------ | ------------ | ----------------------- |
| `GET`  | `/`          | Public status page      |
| `GET`  | `/{path...}` | Alias redirect          |
| `GET`  | `/preview`   | Alias redirect preview  |
| `GET`  | `/healthz`   | Health JSON             |
| `GET`  | `/ws/health` | Health WebSocket stream |

Admin (`:9001`):

| Method | Path                       | Description                                |
| ------ | -------------------------- | ------------------------------------------ |
| `GET`  | `/`                        | Admin panel                                |
| `GET`  | `/login`                   | Login page (when password auth is enabled) |
| `POST` | `/login`                   | Create admin session                       |
| `POST` | `/logout`                  | Clear admin session                        |
| `GET`  | `/metrics`                 | Prometheus metrics                         |
| `GET`  | `/api/aliases`             | List aliases                               |
| `POST` | `/api/aliases`             | Create/update alias                        |
| `POST` | `/api/aliases/edit`        | Edit alias                                 |
| `POST` | `/api/aliases/toggle`      | Enable/disable alias                       |
| `POST` | `/api/aliases/delete`      | Delete alias                               |
| `POST` | `/api/import`              | Batch import aliases                       |
| `GET`  | `/api/broken-links`        | Read unresolved paths seen by server       |

## Environment Variables

| Variable              | Default              | Description                                    |
| --------------------- | -------------------- | ---------------------------------------------- |
| `GOKU_API_PORT`       | `9000`               | Public endpoint port                           |
| `GOKU_ADMIN_PORT`     | `9001`               | Admin endpoint port                            |
| `GOKU_WEB_PORT`       | `9001`               | Backward-compatible admin port fallback        |
| `GOKU_CONFIG`         | `config/config.yaml` | Config file path                               |
| `GOKU_PUBLIC_BASE_URL`| _(empty)_            | Absolute public base URL used for admin preview links |
| `GOKU_ADMIN_USERNAME` | `admin`              | Username for basic auth compatibility          |
| `GOKU_ADMIN_PASSWORD` | _(empty)_            | Enables login page + session auth for admin UI |
| `GOKU_API_KEY`        | _(generated/file)_   | Admin API bearer token                         |

## Homelab/LAN Setup for `go/`

If you want links like `http://go/gh` on your LAN, set up local name resolution and route that hostname to goku.

1. Pick a local hostname

- Recommended: `go.home.arpa` or another local domain you control.
- You can use single-label `go` if your LAN resolver supports it.

1. Configure DNS (or hosts as a fallback)

- DNS: create an `A`/`AAAA` record for your chosen host pointing to the machine running goku or your reverse proxy.
- Hosts fallback (per client machine):

```text
# /etc/hosts (macOS/Linux)
192.168.1.50 go go.home.arpa
```

1. Route traffic to goku public port (`:9000`)

- If you run goku directly on the host, send HTTP traffic for `go` to `:9000`.
- If you use a reverse proxy, point that hostname to `http://127.0.0.1:9000`.

Minimal examples:

```caddyfile
go.home.arpa {
  reverse_proxy 127.0.0.1:9000
}
```

```nginx
server {
  listen 80;
  server_name go.home.arpa;
  location / {
    proxy_pass http://127.0.0.1:9000;
  }
}
```

```yaml
# Traefik labels (example)
traefik.http.routers.goku.rule=Host(`go.home.arpa`)
traefik.http.services.goku.loadbalancer.server.port=9000
```

1. Validate

- Open `http://go.home.arpa/gh` (or `http://go/gh` if using single-label hostnames).
- Run `curl -I http://go.home.arpa/gh` and confirm a redirect response.

## Authentication Notes

- When `GOKU_ADMIN_PASSWORD` is set:
  - Admin UI requires login.
  - API can use session, basic auth, or bearer key.
- When `GOKU_ADMIN_PASSWORD` is empty:
  - Admin UI is open.
  - Bearer key can still protect API endpoints when configured.

The API key is generated on first run and stored at `config/.api_key` if not supplied via `GOKU_API_KEY`.

## Development

```bash
just fmt
just test
just build
```

## Background

Goku arguably started as an itch I first scratched during an internship at Cisco, where I built an auto-correcting URL shortener and redirection tool for the intranet there. The first of its kind at the time, it was a massive hit among my peers. So much so that the idea stuck with me.

There have since been multiple solutions and iterations in my homelab including janky DNS scripts (rite of passage), some Traefik magic and a one-off Caddy config, but none satisfied the itch especially when faced with expanding scenarios like multi-level queries (e.g. `t/TWS-4291/comments`), so I embarked on rewriting the concept from scratch and in Go.

The name _goku_ (悟空) is eternally recognizable for anyone familiar with DBZ and also means _enlightenment through emptiness_. Match made in heaven for this project.

---

<p align="center">Made with &hearts; in NYC</p>
