SHELL := /bin/sh
PREFIX ?= /usr/local
GO ?= go
TEST_ROOT := $(CURDIR)/.tmp-test
TEST_ENV := TMPDIR="$(TEST_ROOT)/tmp" GOCACHE="$(TEST_ROOT)/go-build" GOTMPDIR="$(TEST_ROOT)/go-tmp" GOPATH="$(TEST_ROOT)/gopath" GOBIN="$(TEST_ROOT)/gobin"

.PHONY: build test review install install-codex install-claude install-pi install-agents init-server server server-dev server-logs

build:
	mkdir -p bin "$(TEST_ROOT)/tmp" "$(TEST_ROOT)/go-build" "$(TEST_ROOT)/go-tmp" "$(TEST_ROOT)/gopath" "$(TEST_ROOT)/gobin"
	$(TEST_ENV) $(GO) build -trimpath -o bin/zcode-agent ./cmd/zcode-agent
	$(TEST_ENV) $(GO) build -trimpath -o bin/zcode-server ./cmd/zcode-server

test:
	mkdir -p "$(TEST_ROOT)/tmp" "$(TEST_ROOT)/go-build" "$(TEST_ROOT)/go-tmp" "$(TEST_ROOT)/gopath" "$(TEST_ROOT)/gobin"
	$(TEST_ENV) $(GO) test ./...

review:
	gofmt -w cmd internal
	mkdir -p "$(TEST_ROOT)/tmp" "$(TEST_ROOT)/go-build" "$(TEST_ROOT)/go-tmp" "$(TEST_ROOT)/gopath" "$(TEST_ROOT)/gobin"
	$(TEST_ENV) $(GO) vet ./...
	$(TEST_ENV) $(GO) test -race ./...

install: build
	mkdir -p "$(PREFIX)/bin"
	cp bin/zcode-agent "$(PREFIX)/bin/zcode-agent"
	cp bin/zcode-server "$(PREFIX)/bin/zcode-server"

install-codex:
	./scripts/install-agent.sh codex

install-claude:
	./scripts/install-agent.sh claude

install-pi:
	./scripts/install-agent.sh pi

install-agents:
	./scripts/install-agent.sh all

init-server:
	./scripts/init-server.sh cloud "$(or $(CONFIG),configs/server.local.json)"

server: build
	@test -n "$(CONFIG)" || (echo "CONFIG is required, for example: make server CONFIG=configs/server.local.json" >&2; exit 2)
	./bin/zcode-server --config "$(CONFIG)"

server-dev: build
	./bin/zcode-server --config configs/server.dev.json

server-logs: build
	@test -n "$(CONFIG)" || (echo "CONFIG is required, for example: make server-logs CONFIG=configs/server.local.json" >&2; exit 2)
	./bin/zcode-server logs --config "$(CONFIG)" --limit "$(or $(LIMIT),100)"
