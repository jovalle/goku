# goku

A golinks solution written in Go.

## Quick Start

```bash
go run ./cmd/goku
```

Visit `http://localhost:9001` for the management UI.

## Features

- Static golinks like `go/gh`
- Prefix rules like `go/r/golang`
- Template rules like `go/gh/{owner}/{name}`
- Live config reload from `config/config.yaml`
- Management UI for links and rules
- Prometheus metrics at `/metrics`
- Health check at `/healthz`
- API key and basic auth protection for the admin surface

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
GOKU_ADMIN_PASSWORD=my-secret go run ./cmd/goku
```

When set, the management UI, `/api/*`, and `/metrics` require credentials.
Redirects (`go/gh`, `go/r/golang`, etc.) and `/healthz` remain public.

The server also persists a reusable API key to `config/.api_key` and accepts it as a Bearer token.
