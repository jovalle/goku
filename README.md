<!-- markdownlint-disable MD033 MD041 -->
<div align="center">
  <img src=".github/assets/logo.png" alt="Goku" width="200" />
  <h1>goku</h1>
  <p><em>Enlightenment (悟 → `go`) through the (homelab) void (空 → `ku`)</em></p>
  <p>Self-hosted golinks written in Go.</p>

[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![CI](https://github.com/jovalle/goku/actions/workflows/ci.yml/badge.svg)](https://github.com/jovalle/goku/actions/workflows/ci.yml)
[![Docker Workflow](https://github.com/jovalle/goku/actions/workflows/docker.yml/badge.svg)](https://github.com/jovalle/goku/actions/workflows/docker.yml)
[![Release Workflow](https://github.com/jovalle/goku/actions/workflows/release.yml/badge.svg)](https://github.com/jovalle/goku/actions/workflows/release.yml)
[![Latest Release](https://img.shields.io/github/v/release/jovalle/goku?display_name=tag)](https://github.com/jovalle/goku/releases)
[![Coverage](https://img.shields.io/badge/Coverage-go%20test%20%28CI%29-31c653)](https://github.com/jovalle/goku/actions/workflows/ci.yml)

</div>
<!-- markdownlint-enable MD033 MD041 -->

---

## What is a golink?

A golink is a short, memorable keyword/URL that redirects to a longer destination. Think a URL shortener but without the random short code (e.g. `2bjs09`). In goku, each golink pairs an alias such as `gh` with a destination such as `https://github.com`. Once your network resolves the `go` hostname to goku (see [Set up golinks on a LAN](#set-up-golinks-on-a-lan)), opening `http://go/gh` takes you to that destination.

Golinks are increasingly commonplace in corporate environments (so much so one might develop muscle memory 😅) and goku brings that same experience to the homelab.

## Quick Start

1. Run goku:

   ```bash
   just run
   ```

   or:

   ```bash
   go run ./cmd/goku
   ```

2. Open the apps:
   - Public endpoint: `http://localhost:9000`
   - Admin panel: `http://localhost:9001`

3. To require an admin login, set a password:

```bash
GOKU_ADMIN_PASSWORD=my-secret just run
```

Without `GOKU_ADMIN_PASSWORD`, the admin UI is open and does not show a logout button.

## What it does

- Resolves exact aliases and placeholder patterns, including `{}` and named placeholders.
- Runs public redirects on `:9000` and the admin UI and API on `:9001`.
- Reports health as JSON on both ports and streams updates from `/ws/health` on the public port.
- Lets you search, sort, add, edit, delete, enable, disable, and preview golinks from the admin UI.
- Accepts session, basic, and bearer authentication for admin and API requests.
- Reloads config changes from disk and writes admin and API changes back to `config/config.yaml`.
- Exposes Prometheus metrics at `/metrics` on the admin port.

## Terminology

- A **golink** is a saved redirect.
- An **alias** is the short path or pattern you type, such as `gh` or `r/{subreddit}`.
- A **destination** is the target URL or URL template.

## Configuration

Edit `config/config.yaml`:

```yaml
aliases:
  - alias: 'gh'
    destination: 'https://github.com'
  - alias: 'gh/{owner}/{repo}'
    destination: 'https://github.com/{owner}/{repo}'
  - alias: 'r/{subreddit}'
    destination: 'https://www.reddit.com/r/{subreddit}'
  - alias: 'yt/{}'
    destination: 'https://www.youtube.com/results?search_query={}'
  - alias: 't/{:=BarackObama}'
    destination: 'https://twitter.com/@{}'
```

### Placeholder rules

- Single placeholder aliases can use `{}`.
- Multiple placeholders must be uniquely named.
- Destination placeholders must be defined by the alias pattern.
- Add a default with `{name:=value}` or `{:=value}`. For the `t` alias above, `http://go/t` uses `BarackObama`, while `http://go/t/jovalle` uses `jovalle` instead.

### Using a `go/` hostname

Links such as `http://go/gh` work when DNS or a local hosts file resolves `go` to the goku server. A local domain such as `go.home.arpa` works too. Without local name resolution, use the server URL, for example `http://localhost:9000/gh`.

## Endpoints

### Public (`:9000`)

| Method | Path         | Description             |
| ------ | ------------ | ----------------------- |
| `GET`  | `/`          | Public status page      |
| `GET`  | `/{path...}` | Golink redirect         |
| `GET`  | `/preview`   | Golink redirect preview |
| `GET`  | `/healthz`   | Health JSON             |
| `GET`  | `/ws/health` | Health WebSocket stream |

### Admin (`:9001`)

| Method | Path                  | Description                                |
| ------ | --------------------- | ------------------------------------------ |
| `GET`  | `/`                   | Admin panel                                |
| `GET`  | `/login`              | Login page (when password auth is enabled) |
| `POST` | `/login`              | Create admin session                       |
| `POST` | `/logout`             | Clear admin session                        |
| `GET`  | `/metrics`            | Prometheus metrics                         |
| `GET`  | `/api/aliases`        | List aliases                               |
| `POST` | `/api/aliases`        | Create/update alias                        |
| `POST` | `/api/aliases/edit`   | Edit alias                                 |
| `POST` | `/api/aliases/toggle` | Enable/disable alias                       |
| `POST` | `/api/aliases/delete` | Delete alias                               |
| `POST` | `/api/import`         | Batch import aliases                       |

## Environment variables

| Variable               | Default              | Description                                           |
| ---------------------- | -------------------- | ----------------------------------------------------- |
| `GOKU_API_PORT`        | `9000`               | Public endpoint port                                  |
| `GOKU_ADMIN_PORT`      | `9001`               | Admin endpoint port                                   |
| `GOKU_WEB_PORT`        | `9001`               | Admin port fallback when `GOKU_ADMIN_PORT` is unset   |
| `GOKU_CONFIG`          | `config/config.yaml` | Config file path                                      |
| `GOKU_PUBLIC_BASE_URL` | _(empty)_            | Absolute public base URL used for admin preview links |
| `GOKU_ADMIN_USERNAME`  | `admin`              | Username for basic auth                               |
| `GOKU_ADMIN_PASSWORD`  | _(empty)_            | Enables login page + session auth for admin UI        |
| `GOKU_API_KEY`         | _(generated/file)_   | Admin API bearer token                                |

## Set up golinks on a LAN

If you want links like `http://go/gh` on your LAN, set up local name resolution and route that hostname to goku.

1. Pick a local hostname.
   - Recommended: `go.home.arpa` or a domain you own.
   - You can use single-label `go` if your LAN resolver supports it.

2. Configure DNS, or use a hosts file as a fallback.
   - DNS: create an `A`/`AAAA` record for your chosen host pointing to the machine running goku or your reverse proxy.
   - Hosts fallback (per client machine):

     ```text
     # /etc/hosts (macOS/Linux)
     192.168.1.50 go go.home.arpa
     ```

3. Route traffic to goku's public port, `:9000`.
   - If you run goku directly on the host, send HTTP traffic for `go` to `:9000`.
   - If you use a reverse proxy, point that hostname to `http://127.0.0.1:9000`.
   - If you use Traefik with Docker/service autodiscovery, make the golink redirect router target the public port (`9000`). The admin/API port (`9001`) shows the directory and CRUD API, but it intentionally returns 404 for alias paths.

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

4. Check the redirect.
   - Open `http://go.home.arpa/gh` (or `http://go/gh` if using single-label hostnames).
   - Run `curl -I http://go.home.arpa/gh` and confirm a redirect response.

## Authentication

Set `GOKU_ADMIN_PASSWORD` to require a login for the admin UI. The API accepts the resulting session, basic authentication, or a bearer key.

Without `GOKU_ADMIN_PASSWORD`, the admin UI is open. You can still protect API endpoints with a bearer key.

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

<!-- markdownlint-disable-next-line MD033 -->
<p align="center">Made with ❤️ and 🇩🇴☕ in NYC</p>
