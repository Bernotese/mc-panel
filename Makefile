SHELL := /bin/bash
GO ?= go
export CGO_ENABLED ?= 0

.PHONY: generate check test test-go test-race test-web lint dev preview preview-logs preview-down web-build build release deploy-testvm clean

generate:
	sqlc generate
	cd web && npm run gen:api

# check is the only verification meant for the Mac: formatting, vet and compile (macOS and
# Linux, including the root-only integration test files) and TypeScript types. Unit, race,
# lint and end-to-end tests run in CI.
check:
	@files="$$(gofmt -l $$(git ls-files '*.go'))"; if [ -n "$$files" ]; then echo "gofmt needed:"; echo "$$files"; exit 1; fi
	$(GO) vet ./...
	GOOS=linux $(GO) vet -tags nftintegration,dockerintegration ./...
	GOOS=linux GOARCH=arm64 $(GO) build ./...
	cd web && npm run typecheck

test: test-go test-web

test-go:
	$(GO) test ./...

test-race:
	CGO_ENABLED=1 $(GO) test -race ./...

test-web:
	cd web && npm test

lint:
	golangci-lint run ./...
	cd web && npm run lint && npm run typecheck

dev:
	./scripts/dev.sh

# UI preview in Docker (mock API, no backend); only the user starts it.
PREVIEW := docker compose -f compose.preview.yaml

preview:
	$(PREVIEW) up --build --detach
	@echo "UI preview: http://localhost:5180  (admin / correct-horse-battery, TOTP 123456)"

preview-logs:
	$(PREVIEW) logs --follow

preview-down:
	$(PREVIEW) down

web-build:
	cd web && npm run build

build: web-build
	mkdir -p bin
	$(GO) build -trimpath -o bin/ ./cmd/...

release:
	goreleaser release --clean --snapshot

# Builds REF on the internal test VM from GitHub and (re)deploys it there (docs/dev/README.md, "Test-VM").
TESTVM ?= claude@192.168.1.117
REF ?= main

deploy-testvm:
	ssh $(TESTVM) 'bash -s -- $(REF)' < scripts/testvm-deploy.sh

clean:
	rm -rf bin dist .dev
	find internal/webui/dist -mindepth 1 ! -name .gitkeep -delete
