# make check runs every gate; it must pass before every commit.
# Tests never see the caller's tmux: TMUX, TMUX_PANE and HERDR_* are unset.

GO     ?= go
# gofmt from the toolchain go.mod selects (go1.27.3), not the one on PATH.
GOFMT  := $(shell $(GO) env GOROOT)/bin/gofmt
NODE   ?= node
# Plugin tests: *.test.js / *.test.mjs under internal/integrations.
# The pi extension is a .ts file in plain JS syntax; its test copies it to a
# temp .mjs and imports that, so the system node 20 runs it (no type stripping).
NODE_TESTS = $(shell find internal/integrations -name '*.test.js' -o -name '*.test.mjs' 2>/dev/null)
UNSET  = env -u TMUX -u TMUX_PANE $(foreach v,$(filter HERDR_%,$(.VARIABLES)),-u $(v))

.PHONY: check fmt vet staticcheck govulncheck test node-test build dev

check: fmt vet staticcheck govulncheck test node-test
	@echo "make check: all gates passed"

# .claude/ may hold agent worktrees (gitignored); skip it.
fmt:
	@out=$$(find . -path ./.claude -prune -o -name '*.go' -print | xargs $(GOFMT) -l); if [ -n "$$out" ]; then echo "gofmt -l: not formatted:"; echo "$$out"; exit 1; fi; echo "gofmt: ok"

vet:
	$(GO) vet ./...

staticcheck:
	$(GO) tool staticcheck ./...

govulncheck:
	$(GO) tool govulncheck ./...

test:
	$(UNSET) $(GO) test ./...

node-test:
ifeq ($(strip $(NODE_TESTS)),)
	@echo "node --test: no plugin tests yet"
else
	$(UNSET) $(NODE) --test $(NODE_TESTS)
endif

build:
	$(GO) build -o herdmux ./cmd/herdmux

# The herdmux-dev harness: a fresh `tmux -L herdmux-dev` server with the real
# tmux.conf plus the layer, opened in a Windows Terminal tab (NO_WT=1: headless).
dev:
	bash dev/run.sh
