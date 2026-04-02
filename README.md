<div align="center">
  <img src=".github/assets/logo.png" alt="Goku" width="200" />
  <h1>goku</h1>
  <p><em>Enlightenment (悟 → `go`) through the (homelab) void (空 → `ku`)</em></p>
  <p>A golinks solution written in Go.</p>

[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Docker](https://img.shields.io/badge/Docker-ready-2496ED?logo=docker&logoColor=white)](Dockerfile)

</div>

---

## Quick Start

```bash
# Run
just run

# Or without just:
go run ./cmd/goku
```

Visit `http://localhost:9001` for the management UI.

## Local Development

Install the tracked git hooks once per clone:

```bash
just install-hooks
```

That configures Git to use `.githooks/pre-commit`, which runs `go test -race -coverprofile=coverage.out ./...` before each commit so CI-style test failures are caught locally.

## Features

- **Golinks** — `go/gh` → GitHub, `go/g` → Google
- **Prefix rules** — `go/r/golang` expands to subreddit URL
- **Template rules** — `go/gh/{owner}/{name}` fills placeholders
- **Live reload** — edit `config/config.yaml`, changes apply instantly
- **Management UI** — dark-themed web UI to add/delete links and rules
- **Prometheus metrics** — `/metrics`
- **Health check** — `/healthz` (JSON with version, uptime, counts)
- **API key auth** — protect UI and API; redirects stay public

## Configuration

Edit `config/config.yaml`:

```yaml
links:
  gh: https://github.com
  g: https://google.com
rules:
  - name: reddit
    type: prefix
    pattern: r
    redirect: https://www.reddit.com/r
  - name: gh
    type: template
    pattern: gh/{owner}/{name}
    redirect: https://github.com/{owner}/{name}
```

## API

| Method | Path                       | Description                                      |
| ------ | -------------------------- | ------------------------------------------------ |
| `GET`  | `/`                        | Management UI                                    |
| `GET`  | `/{path}`                  | Redirect to target URL                           |
| `GET`  | `/healthz`                 | Health check (JSON)                              |
| `GET`  | `/metrics`                 | Prometheus metrics                               |
| `GET`  | `/api/links`               | List all links (JSON)                            |
| `POST` | `/api/links`               | Add a link (form: name, url)                     |
| `POST` | `/api/links/{name}/delete` | Delete a link                                    |
| `POST` | `/api/rules`               | Add a rule (form: name, type, pattern, redirect) |
| `POST` | `/api/rules/{name}/delete` | Delete a rule                                    |

## Environment Variables

| Variable              | Default              | Description                                     |
| --------------------- | -------------------- | ----------------------------------------------- |
| `GOKU_WEB_PORT`       | `9001`               | Port to listen on                               |
| `GOKU_CONFIG`         | `config/config.yaml` | Config file path                                |
| `GOKU_ADMIN_USERNAME` | `admin`              | Username for Basic Auth (browser login)         |
| `GOKU_ADMIN_PASSWORD` | _(empty — no auth)_  | Password for Basic Auth (enables UI protection) |
| `GOKU_API_KEY`        | _(from config)_      | Bearer token for API access (overrides config)  |

## Authentication

Set `GOKU_ADMIN_PASSWORD` to protect the UI and API:

```bash
GOKU_ADMIN_PASSWORD=my-secret just run
```

When set, the management UI, `/api/*`, and `/metrics` require credentials.
Redirects (`go/gh`, `go/r/golang`, etc.) and `/healthz` remain public.

**Browser access:** HTTP Basic Auth — your browser prompts for username and password.
Defaults to `admin` / your password. Override the username with `GOKU_ADMIN_USERNAME`.

**API access:** Use the Bearer token (printed in logs at startup). On first run, a key is
automatically generated and saved to `config/.api_key` (gitignored).
Subsequent restarts reuse the same key.

```bash
curl -H "Authorization: Bearer <key>" http://localhost:9001/api/links
```

You can override the saved key by setting `GOKU_API_KEY` in the environment.

If `GOKU_ADMIN_PASSWORD` is not set, everything is open (suitable for local development).

## Roadmap

- [ ] Auto-correction & suggestions for mistyped links (e.g. `go/ghub` → `go/gh`)
- [ ] Live update on file change without needing to restart the server
- [ ] Click analytics — track hit counts per link over time
- [ ] Link search & filtering in the UI
- [ ] Link expiration & time-limited redirects
- [ ] Edit existing links in-place from the UI
- [ ] Rate limiting on redirects and API endpoints
- [ ] Multi-user support with role-based permissions
- [ ] QR code generation for links

## Background

Goku arguably started as an itch I first scratched during an internship at Cisco, where I built an auto-correcting URL shortener and redirection tool for the intranet there. The first of its kind at the time, it was a massive hit among my peers. So much so that the idea stuck with me.

There have since been multiple solutions and iterations in my homelab including janky DNS scripts (rite of passage), some Traefik magic and a one-off Caddy config, but none satisfied the itch especially when faced with expanding scenarios like multi-level queries (e.g. `go/t/TWS-4291/comments`), so I embarked on rewriting the concept from scratch and in Go.

The name _goku_ (悟空) is eternally recognizable for anyone familiar with DBZ and also means _enlightenment through emptiness_. Match made in heaven for this project.

---

<p align="center">Made with &hearts; in NYC</p>
