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

run:
  go run ./cmd/goku

docker:
  docker build -t goku .

clean:
  rm -rf bin/ coverage.out

ci: lint test build
