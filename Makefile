# liseur-sync — common tasks.
#
# `make` on its own lists what is here. Everything below is a thin
# wrapper over the go tool: nothing in this file is required to build,
# test or run the server, and CI does not use it. That is deliberate —
# a Makefile that becomes load-bearing is a second build system.

SHELL := bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

BIN     := bin/liseur-sync
PKG     := ./cmd/liseur-sync
TEMPL   := ./internal/webui/
DEVDIR  ?= tmp/
CONFIG  ?= $(DEVDIR)liseur-sync.dev.toml

# The full suite needs a throwaway PostgreSQL to exercise the second
# backend; without LISEUR_PG_TEST_DSN the store tests run on SQLite
# alone and say so. .env is where that DSN lives and is gitignored.
ENVFILE ?= .env
loadenv = set -a; [ -f $(ENVFILE) ] && . ./$(ENVFILE); set +a;

.PHONY: help
help: ## List these targets
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[1m%-16s\033[0m %s\n", $$1, $$2}'

# ---------------------------------------------------------------- build

.PHONY: build
build: generate ## Build the binary into bin/
	go build -trimpath -o $(BIN) $(PKG)

.PHONY: generate
generate: ## Regenerate templ templates (*_templ.go)
	go tool templ generate $(TEMPL)

.PHONY: install
install: generate ## go install the binary into GOBIN
	go install -trimpath $(PKG)

.PHONY: clean
clean: ## Remove build output and the dev data directory
	rm -rf bin dist $(DEVDIR)

# ----------------------------------------------------------------- test

.PHONY: test
test: generate ## Full suite with the race detector (and PostgreSQL if .env has a DSN)
	@$(loadenv) go test -race -timeout 300s ./...

.PHONY: test-fast
test-fast: generate ## Same suite without the race detector, for a quick loop
	@$(loadenv) go test ./...

# T is a -run pattern: make test-one T=TestReaderOpens
.PHONY: test-one
test-one: generate ## Run one test or package: make test-one T=TestName [P=./internal/api]
	@$(loadenv) go test -race -count=1 -v -run '$(T)' $(or $(P),./...)

# The browser checks skip themselves without Chromium, including in CI,
# so they are the one part of the suite that has to be asked for.
.PHONY: test-browser
test-browser: generate ## Drive the reader in real Chromium (needs chromium + node)
	go test ./internal/webui/ -count=1 -v \
		-run 'TestReaderOpensInARealBrowser|TestDetachedReaderOpensInARealBrowser'

.PHONY: cover
cover: generate ## Write and open a coverage profile
	@$(loadenv) go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

# ----------------------------------------------------------------- lint

.PHONY: fmt
fmt: ## Format the tree
	golangci-lint fmt ./cmd ./internal

.PHONY: vet
vet: generate ## go vet
	go vet ./...

.PHONY: lint-openapi
lint-openapi: ## Lint docs/openapi.yaml the way CI does (needs npx)
	npx -y @redocly/cli@latest lint docs/openapi.yaml --format stylish

.PHONY: lint-go
lint-go: generate ## Run golangci-lint
	golangci-lint run ./...

.PHONY: lint
lint: generate lint-go lint-openapi ## Run all linters

# check is what CI runs, in the order CI runs it, so a red pipeline can
# be reproduced without pushing.
.PHONY: check
check: generate ## Everything CI checks: fresh templ, gofmt, vet, race suite
	@if [ -n "$$(git status --porcelain internal/webui)" ]; then \
		echo "templ output is stale; commit the generated files"; \
		git diff --stat internal/webui; exit 1; \
	fi
	@unformatted="$$(gofmt -l ./cmd ./internal)"; \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt would change these files:"; echo "$$unformatted"; exit 1; \
	fi
	go vet ./...
	@$(loadenv) go test -race -timeout 300s ./...

# ------------------------------------------------------------------ run

# A dev config is generated rather than committed: it points at $(DEVDIR)
# so a local run leaves nothing in the working tree, and it allows plain
# HTTP, which the server otherwise refuses for anything carrying a
# credential. Both are wrong for a real deployment, which is why this
# file is not liseur-sync.toml and is gitignored.
$(CONFIG): liseur-sync.example.toml
	@sed -e 's|^insecure_http = false|insecure_http = true|' \
	     -e 's|^url = "liseur-sync.db"|url = "$(DEVDIR)/liseur-sync.db"|' \
	     -e 's|^root = "content"|root = "$(DEVDIR)/content"|' \
	     $< > $@
	@echo "wrote $@ (plain HTTP, data under $(DEVDIR)/) — edit it freely, it is gitignored"

.PHONY: config
config: $(CONFIG) ## Write a local dev config if there is not one

.PHONY: run
run: generate $(CONFIG) ## Run the server once
	go run $(PKG) serve -config $(CONFIG)

.PHONY: dev
dev: generate $(CONFIG) ## Run the server and restart it on every change (needs reflex)
	@command -v reflex >/dev/null || { \
		echo "reflex is not installed: go install github.com/cespare/reflex@latest"; exit 1; }
	CONFIG=$(CONFIG) reflex --decoration=fancy --config=.reflex.conf

# ARGS is the admin subcommand: make admin ARGS="create-user alice"
.PHONY: admin
admin: $(CONFIG) ## Run an admin subcommand: make admin ARGS="create-user alice"
	go run $(PKG) admin -config $(CONFIG) $(ARGS)

# The README's images. This one goes to the network — it fetches real
# books from Standard Ebooks — and needs a browser, so it is a target
# you ask for rather than one anything depends on.
.PHONY: screenshots
screenshots: generate ## Retake the README screenshots (needs chromium, node, jq)
	scripts/screenshots.sh
