.PHONY: build test vet tidy run clean

VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BIN     := bin/gkebnkctl
PKG     := github.com/JLCode-tech/gkebnkctl

LDFLAGS := -s -w \
	-X $(PKG)/internal/cli.Version=$(VERSION) \
	-X $(PKG)/internal/cli.Commit=$(COMMIT) \
	-X $(PKG)/internal/cli.BuildDate=$(DATE)

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/gkebnkctl

install: build
	$(BIN) install $(INSTALL_ARGS)

test:
	go test ./...

vet:
	go vet ./...

tidy:
	go mod tidy

run: build
	$(BIN) --help

clean:
	rm -rf bin/ dist/

.PHONY: release stamp-changelog goreleaser-check goreleaser-snapshot \
        staticcheck build-integration-tags

# Release date stamped into CHANGELOG.md's `## v1.0.0 — 2026-MM-DD`
# placeholder. Defaults to today; override with RELEASE_DATE=YYYY-MM-DD
# for testing or back-dated releases.
RELEASE_DATE ?= $(shell date +%Y-%m-%d)

# Pinned goreleaser image (matches `goreleaser/goreleaser:latest` on
# Docker Hub). Override via GORELEASER_IMAGE=... if the integrator wants
# to pin a specific release.
GORELEASER_IMAGE ?= goreleaser/goreleaser:latest

# stamp-changelog: replace the `2026-MM-DD` placeholder in CHANGELOG.md
# with $(RELEASE_DATE) (defaults to today). Idempotent — no-op if the
# placeholder is already gone (i.e., the CHANGELOG has been stamped or
# manually dated).
stamp-changelog:
	@if grep -q '2026-MM-DD' CHANGELOG.md; then \
	    sed "s/2026-MM-DD/$(RELEASE_DATE)/" CHANGELOG.md > CHANGELOG.md.tmp && mv CHANGELOG.md.tmp CHANGELOG.md; \
	    echo "    CHANGELOG.md v1.0.0 date stamped: $(RELEASE_DATE)"; \
	else \
	    echo "    CHANGELOG.md v1.0.0 date already stamped (skip)"; \
	fi

# goreleaser-check: lint .goreleaser.yml via the official goreleaser
# docker image. Fast — pure YAML + schema validation.
goreleaser-check:
	docker run --rm -v $(CURDIR):/work -w /work $(GORELEASER_IMAGE) check

# goreleaser-snapshot: end-to-end dry-run of the release pipeline.
# Cross-compiles for all goos/goarch combinations defined in
# .goreleaser.yml, produces archives + checksums in dist/. Does NOT
# tag, push, or publish — that's the integrator's tag-cut step.
goreleaser-snapshot:
	docker run --rm \
	    -v $(CURDIR):/work \
	    -w /work \
	    $(GORELEASER_IMAGE) release --snapshot --clean

# staticcheck: run honnef.co/go/tools/cmd/staticcheck against the whole
# module. Pinned via the `tool` directive in go.mod (Go 1.24+), so the
# version travels with the source tree — no global install, no PATH
# lookup, no "skipping" branch.
staticcheck:
	go tool staticcheck ./...

# build-integration-tags: compile-check the whole tree under the
# `integration` build tag without executing any tests. Closes the
# gap where `internal/exec/*_integration_test.go` files compiled fine
# on `go test ./...` (which skips integration-tagged files) but broke
# under `go test -tags integration ./...`. Running the build alone is
# faster than the full integration test sweep and catches the same
# shape of compile-time gap (unused imports, undefined symbols behind
# the tag, drift between the production code and the tag-gated test
# code).
build-integration-tags:
	go build -tags integration ./...

# release: full release-prep driver. Run before `git tag vX.Y.Z` to verify
# every release artifact builds cleanly. Steps:
#
#   1. Stamp today's date into CHANGELOG.md's vX.Y.Z placeholder
#   2. Run staticcheck ./... (pre-tag gate)
#   3. Compile-check under -tags integration (pre-tag gate)
#   4. Lint .goreleaser.yml via docker
#   5. Cross-compile snapshot build via goreleaser docker (writes dist/)
#
# Steps 2 + 3 catch the shape of gap where staticcheck-clean or
# -tags integration compile failures slip between tags. Running them
# locally before the tag commit means the integrator finds the breakage
# before goreleaser publishes the binaries, not after.
#
# After this completes successfully, the integrator's tag-cut sequence is:
#
#   git add -A && git commit -m "chore: prep vX.Y.Z release"
#   git tag vX.Y.Z && git push origin main --tags
#
# Pushing the tag triggers .github/workflows/release.yml (goreleaser
# builds the multi-platform binaries and publishes the GitHub Release).
release:
	@echo "==> [1/5] Stamping CHANGELOG.md release-date placeholder"
	@$(MAKE) stamp-changelog
	@echo ""
	@echo "==> [2/5] Running staticcheck ./... (pre-tag gate)"
	@$(MAKE) staticcheck
	@echo ""
	@echo "==> [3/5] Compile-checking under -tags integration (pre-tag gate)"
	@$(MAKE) build-integration-tags
	@echo ""
	@echo "==> [4/5] Linting .goreleaser.yml via $(GORELEASER_IMAGE)"
	@$(MAKE) goreleaser-check
	@echo ""
	@echo "==> [5/5] Snapshot build (multi-platform binaries → dist/)"
	@$(MAKE) goreleaser-snapshot
	@echo ""
	@echo "==> Release artifacts ready:"
	@echo ""
	@echo "    dist/:"
	@ls -la dist/checksums.txt dist/*.tar.gz dist/*.zip 2>/dev/null | head -20 || true
	@echo ""
	@echo "==> Next: review the diff, commit, tag, push:"
	@if [ "$(VERSION)" = "dev" ]; then \
	    echo "    (re-run with VERSION=vX.Y.Z to get tag-cut commands tailored to a real release)"; \
	    echo "    git add -A && git commit -m 'chore: prep vX.Y.Z release'"; \
	    echo "    git tag vX.Y.Z && git push origin main --tags"; \
	else \
	    echo "    git add -A && git commit -m 'chore: prep $(VERSION) release'"; \
	    echo "    git tag $(VERSION) && git push origin main --tags"; \
	fi

# --- additional targets ---
# Note: `build` and `test` already exist above and are kept verbatim
# (their existing recipes wire ldflags for version stamping).

.PHONY: test-short test-integration test-live test-cred-audit lint ci-local pre-commit-install

test-short:
	go test -short ./...

# test-cred-audit runs the security-spine regression suite from
# `internal/exec/audit_test.go`. Quick: < 5s on a clean tree.
# Run before tagging a release — a leaked credential in any backend
# is a stop-ship.
#
# Run -v to see exactly which audit cases fired:
#   make test-cred-audit ARGS="-v"
test-cred-audit:
	go test -run CredAudit $(ARGS) ./...

# test-integration runs the testcontainers-go-backed suites (currently
# only internal/remote/integration_test.go — adds an sshd container to
# exercise the SSH client). Requires Docker on the host. Not invoked by
# the default CI matrix on every PR — see .github/workflows/ci.yml's
# `integration` job, which gates this on Linux + same-repo PRs only.
# Run locally before pushing SSH-related changes.
test-integration:
	go test -tags integration -timeout 5m ./...

# test-live runs golden-file byte-equivalence tests for
# `gkebnkctl k get -o yaml` against `kubectl get -o yaml`. Requires:
#
#   - $KUBECONFIG (or ~/.kube/config) pointing at a real cluster
#   - kubectl on PATH for the comparison side
#   - gkebnkctl built and on PATH (or $GKEBNKCTL set to its path)
#
# Tests skip cleanly (rather than fail) when prerequisites are missing,
# so it's safe to invoke from CI as a manual-trigger job. Recommended:
# run before tagging a release to verify byte-equivalence.
test-live:
	go test -tags live -timeout 5m ./internal/k8s/...

# lint: the canonical local pre-push gate. Must run the SAME tools CI
# runs, in the SAME order, with the SAME failure semantics — otherwise
# every push turns into a snowflake-fix cycle (push → CI red → patch →
# repeat). staticcheck is a hard requirement (vendored via go.mod `tool`
# directive) instead of a silent skip.
#
# Mirror this with .github/workflows/ci.yml's `test` job:
#   1. gofmt -d -l . (fail on any diff)
#   2. go vet ./...
#   3. go tool staticcheck ./...
#   4. go test -race ./...     (test target — runs separately)
lint:
	@out=$$(gofmt -d -l .); if [ -n "$$out" ]; then echo "$$out"; echo "gofmt: files need formatting (run 'gofmt -w .')"; exit 1; fi
	go vet ./...
	go tool staticcheck ./...

# ci-local: full pre-push gate. Matches the `test` job in
# .github/workflows/ci.yml step-for-step. Run this before every push;
# if it's green, CI's `test` job will be too (modulo the integration
# tags, which are gated separately in CI and surface only on PRs to main).
ci-local: lint
	go test -race ./...
	go build ./...

