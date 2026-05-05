VERSION     ?= dev
COMMIT      ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE  ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
GOCACHE     ?= $(CURDIR)/.cache/go-build
GOMODCACHE  ?= $(CURDIR)/.cache/go-mod
export GOCACHE
export GOMODCACHE

BINARY      := sysproxy
MODULE      := github.com/mar0ls/go-sysproxy
LDFLAGS     := -ldflags="-s -w \
  -X '$(MODULE)/internal/buildinfo.Version=$(VERSION)' \
  -X '$(MODULE)/internal/buildinfo.Commit=$(COMMIT)' \
  -X '$(MODULE)/internal/buildinfo.BuildDate=$(BUILD_DATE)'"

.PHONY: all cli test lint clean tidy dist

all: cli

$(GOCACHE) $(GOMODCACHE):
	mkdir -p $@

tidy:
	go mod tidy

cli: | $(GOCACHE) $(GOMODCACHE)
	CGO_ENABLED=0 go build $(LDFLAGS) -o dist/$(BINARY) ./cmd/sysproxy

# Cross-platform release builds.
dist: | $(GOCACHE) $(GOMODCACHE)
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64  go build $(LDFLAGS) -o dist/$(BINARY)-darwin-amd64       ./cmd/sysproxy
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64  go build $(LDFLAGS) -o dist/$(BINARY)-darwin-arm64       ./cmd/sysproxy
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64  go build $(LDFLAGS) -o dist/$(BINARY)-linux-amd64        ./cmd/sysproxy
	CGO_ENABLED=0 GOOS=linux   GOARCH=arm64  go build $(LDFLAGS) -o dist/$(BINARY)-linux-arm64        ./cmd/sysproxy
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64  go build $(LDFLAGS) -o dist/$(BINARY)-windows-amd64.exe  ./cmd/sysproxy
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64  go build $(LDFLAGS) -o dist/$(BINARY)-windows-arm64.exe  ./cmd/sysproxy

test: | $(GOCACHE) $(GOMODCACHE)
	go test ./... -count=1

cover: | $(GOCACHE) $(GOMODCACHE)
	go test ./... -count=1 -coverprofile=coverage.out
	go tool cover -func=coverage.out | tail -3

lint:
	golangci-lint run ./...

clean:
	rm -rf dist/ .cache/
