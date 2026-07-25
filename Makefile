.PHONY: all init dev build package test live lint vet fmt typecheck bindings check clean

# The Go binary embeds frontend/dist, so nothing that compiles Go works until
# the frontend has been built at least once. Targets that touch the Go build
# depend on the dist marker below rather than assuming it is there.
DIST := frontend/dist/index.html

all: check

init:
	go mod download
	cd frontend && npm install

dev:
	wails dev

$(DIST):
	cd frontend && npm install && npm run build

build: $(DIST)
	wails build

# Signed/notarized macOS bundle and platform installers.
package: $(DIST)
	wails build -clean -platform darwin/universal

test: $(DIST)
	go test ./...

# Exercises problem generation, the coach, and the reviewer against a real
# model. Needs ollama running. Override with MODEL=...
MODEL ?= qwen3.5:9b
live: $(DIST)
	WHITEBOARDY_LIVE=1 WHITEBOARDY_MODEL=$(MODEL) go test ./internal/harness -run Live -v

lint: $(DIST)
	golangci-lint run

vet: $(DIST)
	go vet ./...

fmt:
	gofmt -w .

typecheck:
	cd frontend && npx tsc --noEmit

# Regenerates frontend/wailsjs from the Go binding surface in app.go. Run this
# after changing any exported method on App or any type it returns.
bindings:
	wails generate module

check: fmt vet lint test typecheck

# Leaves frontend/dist itself in place: the directory is tracked so //go:embed
# resolves on a fresh clone.
clean:
	rm -rf build/bin
	find frontend/dist -mindepth 1 ! -name .gitkeep -delete
