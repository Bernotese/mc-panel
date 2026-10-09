SHELL := /bin/bash
GO ?= go
export CGO_ENABLED ?= 0

.PHONY: generate test test-go test-race test-web lint dev web-build build release clean

generate:
	sqlc generate
	cd web && npm run gen:api

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

web-build:
	cd web && npm run build
	touch internal/webui/dist/.gitkeep

build: web-build
	mkdir -p bin
	$(GO) build -trimpath -o bin/ ./cmd/...

release:
	goreleaser release --clean --snapshot

clean:
	rm -rf bin dist .dev
	find internal/webui/dist -mindepth 1 ! -name .gitkeep -delete
