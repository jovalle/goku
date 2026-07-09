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

install-hooks:
  git config core.hooksPath .githooks

build:
  go build -ldflags="-X main.version={{version}} -X main.commit={{commit}} -X main.date={{date}}" \
    -o bin/goku ./cmd/goku

vulncheck:
  go run golang.org/x/vuln/cmd/govulncheck@latest ./...

# Propagate the go.mod Go version to the CI/release workflows and Dockerfile.
sync-versions:
  @gv="$(awk '/^go [0-9]/ {print $2; exit}' go.mod)"; \
  GV="$gv" perl -i -pe 's/(go-version:\s*")[\d.]+(")/$1$ENV{GV}$2/' \
    .github/workflows/ci.yml .github/workflows/release.yml; \
  GV="$gv" perl -i -pe 's/(golang:)[\d.]+(-alpine)/$1$ENV{GV}$2/' Dockerfile; \
  echo "Synced Go version to $gv across workflows and Dockerfile"

run:
  go run ./cmd/goku

docker:
  docker build -t goku .

clean:
  rm -rf bin/ coverage.out

ci: sync-versions lint test build vulncheck
