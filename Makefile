SHELL := /bin/bash
GO ?= go
export CGO_ENABLED ?= 0

.PHONY: generate check test test-go test-race test-web lint dev preview preview-logs preview-down web-build build release installer sign-release verify-release-build deploy-testvm test-vm clean

generate:
	sqlc generate
	cd web && npm run gen:api

# check is the only verification meant for the Mac: formatting, vet and compile (macOS and
# Linux, including the root-only integration test files) and TypeScript types. Unit, race,
# lint and end-to-end tests run on the test VM (make test-vm) and in CI.
check:
	@files="$$(gofmt -l $$(git ls-files '*.go'))"; if [ -n "$$files" ]; then echo "gofmt needed:"; echo "$$files"; exit 1; fi
	$(GO) vet ./...
	GOOS=linux $(GO) vet -tags nftintegration,dockerintegration,acceptance ./...
	GOOS=linux GOARCH=arm64 $(GO) build ./...
	cd web && npm run typecheck
	@for f in scripts/install/*.sh scripts/install/tests/*.sh scripts/sign-release.sh scripts/verify-release-build.sh scripts/release-lib.sh; do bash -n "$$f" || exit 1; done
	scripts/install/assemble.sh 0.0.0-check build/install.sh && bash -n build/install.sh

test: test-go test-web

test-go:
	$(GO) test ./...
	$(GO) test -tags acceptance ./internal/timescale

test-race:
	CGO_ENABLED=1 $(GO) test -race ./...
	CGO_ENABLED=1 $(GO) test -race -tags acceptance ./internal/timescale

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

# Version written into build/install.sh (release: the tag without v; goreleaser passes it).
INSTALLER_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//')

# Assembles the installer (installer spec §3) into build/install.sh.
installer:
	scripts/install/assemble.sh "$(INSTALLER_VERSION)" build/install.sh

# Signs and publishes the draft release TAG with the owner's release key (docs/dev/README.md,
# "Release process"); the owner runs it on their own machine. KEY defaults to
# ~/.ssh/mc-panel-release.
sign-release:
	@test -n "$(TAG)" || { echo "usage: make sign-release TAG=vX.Y.Z [KEY=path] [SKIP_BUILD_CHECK=1]"; exit 2; }
	scripts/sign-release.sh $(if $(SKIP_BUILD_CHECK),--skip-build-check) "$(TAG)" $(if $(KEY),"$(KEY)")

# Rebuilds the release TAG from its tagged commit (temp dir, Node of .node-version via fnm or
# PATH) and compares it with the draft's tarballs; sign-release runs it, too.
verify-release-build:
	@test -n "$(TAG)" || { echo "usage: make verify-release-build TAG=vX.Y.Z"; exit 2; }
	scripts/verify-release-build.sh "$(TAG)"

# Builds REF on the internal test VM from GitHub and (re)deploys it there (docs/dev/README.md, "Test VM").
TESTVM ?= claude@192.168.1.117
REF ?= main

deploy-testvm:
	ssh $(TESTVM) 'bash -s -- $(REF)' < scripts/testvm-deploy.sh

# Copies the working tree to the test VM and runs the test suites there (docs/dev/README.md,
# "Tests on the test VM"): SUITE=all|go|lint|web|e2e|shots.
SUITE ?= all

test-vm:
	TESTVM=$(TESTVM) ./scripts/test-vm.sh $(SUITE)

clean:
	rm -rf bin build dist .dev
	find internal/webui/dist -mindepth 1 ! -name .gitkeep -delete
