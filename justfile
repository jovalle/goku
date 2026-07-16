set shell := ["bash", "-eu", "-o", "pipefail", "-c"]

version := `git describe --tags --always --dirty 2>/dev/null || echo dev`
commit  := `git rev-parse --short HEAD 2>/dev/null || echo unknown`
date    := `date -u +%Y-%m-%dT%H:%M:%SZ`

default:
  @just --list

list:
  @just --list

fmt:
  @files="$(rg --files -g '*.go' -g '!vendor/**')"; \
  if [ -n "$files" ]; then \
    gofmt -w $files; \
  fi

lint:
  @files="$(rg --files -g '*.go' -g '!vendor/**')"; \
  if [ -n "$files" ]; then \
    unformatted="$(gofmt -l $files)"; \
    if [ -n "$unformatted" ]; then \
    echo "These files need formatting:"; \
    echo "$unformatted"; \
    echo "Run: just fmt"; \
    exit 1; \
    fi; \
  fi
  go vet ./...

test:
  go test -race -cover ./...

cover:
  go test -race -coverprofile=coverage.out ./...
  go tool cover -func=coverage.out
  @echo ""
  @echo "To open in browser: go tool cover -html=coverage.out"

hooks:
  git config core.hooksPath .githooks

build:
  go build -ldflags="-X main.version={{version}} -X main.commit={{commit}} -X main.date={{date}}" \
    -o bin/goku ./cmd/goku

vulncheck:
  GOWORK=off go run golang.org/x/vuln/cmd/govulncheck@latest ./...

# Propagate the go.mod Go version to the CI/release workflows and Dockerfile.
sync:
  @gv="$(awk '/^go [0-9]/ {print $2; exit}' go.mod)"; \
  GV="$gv" perl -i -pe 's/(go-version:\s*")[\d.]+(")/$1$ENV{GV}$2/' \
    .github/workflows/ci.yml .github/workflows/release.yml; \
  GV="$gv" perl -i -pe 's/(golang:)[\d.]+(-alpine)/$1$ENV{GV}$2/' Dockerfile; \
  echo "Synced Go version to $gv across workflows and Dockerfile"

# Best-effort, non-blocking notice when a newer Go release is available (used by hooks).
outdated:
  @current="$(awk '/^go [0-9]/ {print $2; exit}' go.mod)"; \
  latest="$(curl -fsS --max-time 3 'https://go.dev/VERSION?m=text' 2>/dev/null | head -n1 | sed 's/^go//')" || true; \
  if [ -n "${latest:-}" ] && [ "$current" != "$latest" ]; then \
    printf '\033[33m[goku] Go %s is available (current: %s). Run: just upgrade\033[0m\n' "$latest" "$current"; \
  fi

# Detect the latest Go release and, after confirmation, upgrade go.mod and sync everything.
upgrade:
  @current="$(awk '/^go [0-9]/ {print $2; exit}' go.mod)"; \
  latest="$(curl -fsS --max-time 10 'https://go.dev/VERSION?m=text' | head -n1 | sed 's/^go//')"; \
  if [ -z "$latest" ]; then echo "Could not determine the latest Go version"; exit 1; fi; \
  if [ "$current" = "$latest" ]; then echo "Go is already up to date ($current)"; exit 0; fi; \
  printf 'Upgrade Go %s -> %s across go.mod, workflows, and Dockerfile? [y/N] ' "$current" "$latest"; \
  read -r reply; \
  case "$reply" in [yY]|[yY][eE][sS]) ;; *) echo "Aborted."; exit 0;; esac; \
  go mod edit -go="$latest"; \
  if [ -f go.work ]; then go work edit -go="$latest"; fi; \
  just sync; \
  echo "Verifying with govulncheck..."; \
  just vulncheck; \
  echo "Upgraded to Go $latest. Review the diff, then commit."

run:
  go run ./cmd/goku

docker:
  docker build -t goku .

clean:
  rm -rf bin/ coverage.out

ci: sync lint test build vulncheck
