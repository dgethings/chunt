.PHONY: all lsp clean grammar-clean release snapshot release-dry-run generate grammar grammar-bump grammar-root test test-lsp test-grammar cover cover-html workspace

TS ?= tree-sitter
# Sibling grammar repo (tree-sitter-cisco-ios-jinja2). OPTIONAL: chunt depends
# on the grammar as a published Go module (see the go.mod require line), so a
# normal build/test fetches it from the module proxy and needs NO local
# checkout. The sibling is only required by the opt-in contributor targets
# below (grammar, generate, test-grammar, workspace, grammar-clean). It is
# auto-detected so both a normal clone (go.mod at the repo root) and a
# git-worktree checkout (go.mod under a worktree dir such as main/) work;
# override with make TS_DIR=/abs/path/to/grammar.
TS_CANDIDATES := ../tree-sitter-cisco-ios-jinja2 ../tree-sitter-cisco-ios-jinja2/main
# First existing candidate's go.mod, else the conventional ../tree-sitter-cisco-ios-jinja2
# default. Uses wildcard/firstword/patsubst (not the $(if) function) so the line
# carries no shell keyword a Makefile-unaware linter trips on.
TS_DIR ?= $(patsubst %/go.mod,%,$(firstword $(wildcard $(TS_CANDIDATES:%=%/go.mod)) ../tree-sitter-cisco-ios-jinja2/go.mod))
GRAMMAR_MODULE := github.com/dgethings/tree-sitter-cisco-ios-jinja2
TS_PARSER := $(TS_DIR)/src/parser.c
# Go's cgo cache does not track the parser.c that binding.go #includes, so
# after `tree-sitter generate` Go keeps compiling the old grammar. Writing a
# Go file whose contents derive from parser.c's hash into the binding package
# makes the cache key change whenever the grammar changes.
TS_HASH_GO := $(TS_DIR)/bindings/go/parser_hash.go
BIN := bin/chunt
WORKSPACE := go.work

SRCS := $(wildcard main.go cmd/*.go) $(shell find internal -name '*.go')

VERSION := $(shell svu current 2>/dev/null || echo "0.0.0")
NEXT    := $(shell svu next)
# RELEASE_TAG is the version we build and publish. For a normal release it
# equals NEXT. When NEXT == VERSION and the VERSION tag already exists, a
# previous `make release` pushed the tag but died before goreleaser ran; in
# that case we resume by releasing VERSION. When NEXT == VERSION and the
# tag does not exist, RELEASE_TAG stays empty and the release target errors
# out below.
ifeq ($(NEXT),$(VERSION))
  RESUMING    := $(if $(shell git tag -l "$(VERSION)"),1,)
  RELEASE_TAG := $(if $(RESUMING),$(VERSION),)
else
  RESUMING    :=
  RELEASE_TAG := $(NEXT)
endif
ROOT_DIR := $(shell pwd)
CC_WRAPPER := $(ROOT_DIR)/scripts/cc
# Fall back to the `gh` CLI's keyring token when GITHUB_TOKEN isn't in the
# environment. Recursive expansion (=) means `gh auth token` only runs when
# the variable is actually referenced (e.g. by `make release`).
GITHUB_TOKEN ?= $(shell gh auth token)

export CC_WRAPPER
export GITHUB_TOKEN

# ---------------------------------------------------------------------------
# Build & test — self-contained: the grammar is a published module (go.mod)
# ---------------------------------------------------------------------------
all: $(BIN)

$(BIN): $(SRCS)
	CGO_ENABLED=1 go build -o $(BIN) .

lsp:
	CGO_ENABLED=1 go build -o $(BIN) .

test: test-lsp

test-lsp:
	CGO_ENABLED=1 go test -race ./...

# ---------------------------------------------------------------------------
# Coverage — CGO-enabled suite with a total-coverage floor (COVER_MIN).
# The floor is a TOTAL percentage only: per-package floors are intentionally
# not gated yet to avoid churn (keyword is the high-water mark at ~92%).
# Measured total today is ~75.7%, so COVER_MIN defaults to 75 (green now);
# ratchet it up toward 80 as coverage improves. cover.out is gitignored.
# ---------------------------------------------------------------------------
COVER_MIN ?= 75

cover:
	CGO_ENABLED=1 go test -race -cover -coverprofile=cover.out ./...
	@echo
	@go tool cover -func=cover.out | awk '/^total:/ { gsub(/%/,"",$$NF); tot=$$NF+0 } { print } END { printf "\n[cover] total %.1f%% (floor %g%%)\n", tot, $(COVER_MIN); if (tot < $(COVER_MIN)) { printf "[cover] FAIL: total %.1f%% is below the %g%% floor -- set COVER_MIN or add tests\n", tot, $(COVER_MIN); exit 1 } printf "[cover] OK: total %.1f%% >= %g%% floor\n", tot, $(COVER_MIN) }'

# Local convenience: regenerate cover.out and open an HTML report in a browser.
cover-html:
	CGO_ENABLED=1 go test -race -cover -coverprofile=cover.out ./...
	go tool cover -html=cover.out

# ---------------------------------------------------------------------------
# Grammar dependency management
# ---------------------------------------------------------------------------

# Bump the grammar to a published version. Defaults to @latest; pass
# GRAMMAR_VERSION=vX.Y.Z for a specific tag. Operates on go.mod directly
# (GOWORK=off so a contributor's local go.work, if present, does not interfere).
grammar-bump:
	@before=$$(GOWORK=off go list -m -f '{{.Version}}' $(GRAMMAR_MODULE) 2>/dev/null || echo none); \
	target=$(if $(GRAMMAR_VERSION),$(GRAMMAR_VERSION),latest); \
	GOWORK=off CGO_ENABLED=1 go get $(GRAMMAR_MODULE)@$$target && GOWORK=off go mod tidy; \
	after=$$(GOWORK=off go list -m -f '{{.Version}}' $(GRAMMAR_MODULE)); \
	echo "[chunt] grammar $(GRAMMAR_MODULE): $$before -> $$after"

# ---------------------------------------------------------------------------
# Contributor-only targets — need a local sibling grammar checkout
# ---------------------------------------------------------------------------

# Fail fast with a helpful message if the sibling grammar is absent. Runs first
# (as a normal prerequisite) so the opt-in targets below never get past it.
grammar-root:
	@if [ ! -f "$(TS_DIR)/go.mod" ]; then \
		echo "[chunt] sibling grammar not found at $(TS_DIR)" >&2; \
		echo "[chunt] clone it: git clone https://github.com/dgethings/tree-sitter-cisco-ios-jinja2 ../tree-sitter-cisco-ios-jinja2" >&2; \
		exit 1; \
	fi

# Build/regenerate the sibling grammar's Go binding (cd && make + parser.c hash
# stamp). Phony: re-runs the grammar's own incremental make each time. The
# published module already ships a built binding, so this is only needed when
# developing the grammar locally.
grammar: grammar-root
	cd $(TS_DIR) && make
	@hash=$$(shasum -a 256 $(TS_PARSER) | cut -c1-16); \
	tmp=$(TS_HASH_GO).tmp; \
	printf 'package tree_sitter_cisco_ios_jinja2\n\n// Code generated by make. DO NOT EDIT.\nconst parserSHA256 = "%s"\n' "$$hash" > $$tmp; \
	cmp -s $$tmp $(TS_HASH_GO) && rm $$tmp || mv $$tmp $(TS_HASH_GO)

generate: grammar

# Run the grammar's tree-sitter corpus tests (needs the sibling checkout).
test-grammar: grammar
	cd $(TS_DIR) && $(TS) test

# Opt-in local-dev override: write a gitignored go.work pointing chunt at the
# sibling grammar instead of the published module, so local grammar edits are
# picked up immediately. Remove go.work (or run `make clean-workspace`) to
# revert to the published module.
workspace: grammar-root
	@tmp=$$(mktemp); \
	printf 'go 1.26.3\n\nuse .\n\nreplace %s => %s\n' "$(GRAMMAR_MODULE)" "$(TS_DIR)" > $$tmp; \
	if ! cmp -s $$tmp $(WORKSPACE) 2>/dev/null; then \
		mv $$tmp $(WORKSPACE); echo "[chunt] wrote $(WORKSPACE) (grammar -> $(TS_DIR))"; \
	else rm -f $$tmp; fi

# ---------------------------------------------------------------------------
# Cleanup
# ---------------------------------------------------------------------------
clean: clean-workspace
	go clean -cache
	rm -f $(BIN)

# Remove a locally-generated go.work so chunt reverts to the published module.
clean-workspace:
	rm -f $(WORKSPACE) $(WORKSPACE).sum

# Clean the sibling grammar's build artifacts (needs the checkout).
grammar-clean: grammar-root
	$(MAKE) -C $(TS_DIR) clean

# ---------------------------------------------------------------------------
# Release
# ---------------------------------------------------------------------------
release:
ifeq ($(RELEASE_TAG),)
	$(error No new conventional commits since $(VERSION) (svu next=$(NEXT)). Add a feat:/fix: commit, or tag manually to force a release.)
endif
	@echo "Releasing $(RELEASE_TAG) (current: $(VERSION), next: $(NEXT), resuming: $(if $(RESUMING),yes,no))"
	@if [ -z "$(RESUMING)" ]; then \
		if git tag -l "$(RELEASE_TAG)" | grep -q "$(RELEASE_TAG)"; then \
			echo "Tag $(RELEASE_TAG) already exists; aborting to avoid re-publishing." >&2; \
			exit 1; \
		fi; \
		git tag -a "$(RELEASE_TAG)" -m "Release $(RELEASE_TAG)"; \
		git push origin "$(RELEASE_TAG)"; \
	else \
		echo "  (resume: tag $(RELEASE_TAG) already pushed, skipping tag step)"; \
	fi
	goreleaser release --clean
	gh release upload $(RELEASE_TAG) dist/config.yaml dist/metadata.json dist/artifacts.json

snapshot:
	goreleaser release --snapshot --clean

release-dry-run:
	@echo "Current: $(VERSION) -> Next: $(NEXT)"
	goreleaser release --snapshot --clean
