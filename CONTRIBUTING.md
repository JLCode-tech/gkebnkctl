# Contributing to gkebnkctl

Thank you for your interest in contributing to **gkebnkctl**! This document provides everything you need to get set up locally, run tests, and successfully ship your changes.

---

## Prerequisites

This project is tested on Linux and macOS hosts.

**Required:**
- **Go 1.25+** (Check `go.mod` for the exact source of truth)
- **git**, **make**, **docker**
- Standard dev utilities: `jq`, `unzip`, `gnupg`, `openssh-client`, `python3`, and `helm 3` (for chart operations)

**NOT Required:**
- `terraform` (gkebnkctl uses the GCP SDK directly)
- `kubectl` (Internalized via `client-go`)
- `aws` CLI (Internalized via the GCP SDK, unless you need `aws sso login`)
- `goreleaser` (Pulled at release time automatically)

---

## Building Locally

You can easily build the binary from the root directory:

```bash
go build -o gkebnkctl ./cmd/gkebnkctl
./gkebnkctl --help
```

*Tip: Check the `Makefile` for common workflow targets.*

---

## Testing

### Running Tests
The unit suite runs entirely without external dependencies. Always run these locally before pushing your code.

```bash
gofmt -l .     # Must be empty
go vet ./...   # Must be clean
staticcheck ./... # Must be clean
go test ./...   # Must pass
```

### Integration Tiers

| Tier | What it exercises | When it runs |
|---|---|---|
| **Unit** | Pure Go packages, fakes for external IO | Every PR (CI) |
| **`kind`-based** | Apply manifests against a local kind cluster | PR (CI) |
| **GCP SDK mocked** | GCP SDK middleware fakes (tests phase orchestration) | PR (CI) |
| **`testcontainers`** | SSH backend integration via containerised sshd | PR (CI) |
| **Live e2e** | Real GCP project + real GKE cluster | On demand only |

*Note: Integration tests are gated by build tags so they don't run by default. To run them:*
```bash
go test -tags integration ./...
```

### Live End-to-End (e2e) Tests
The full e2e tier spins up a real GKE cluster and tears it down. **It costs real money** and takes ~25 minutes per cycle. Use it sparingly against a sandbox account.

```bash
gcloud auth application-default login
./scripts/e2e-test-full.sh
```

---

## Code Style & Guidelines

- **Surgical changes:** Touch only what the change requires; match the surrounding style.
- **Clarity over cleverness:** Prefer maintainable code over impressive one-liners.
- **Comment WHY, not WHAT:** Avoid comments explaining what the code does, document *why* when non-obvious.
- **Complete implementations:** If you can't finish a code path in a PR, document the limitation and gate the surface.

For deeper architectural context, read the [Architecture Guide](docs/ARCHITECTURE.md).

---

## Adding a New Phase

If you're extending the provisioning graph:
1. Read the existing phase you're closest to in shape (e.g. `phase17_secondary_enis.go`).
2. Add a new file `phaseNN_<name>.go` and its corresponding test.
3. Wire it into `internal/cli/lifecycle.go:runPhasedUp` and the inverse in `runPhasedDown` at the correct ordering.
4. Make sure the phase is **idempotent** on healthy re-runs.
5. Update `docs/ARCHITECTURE.md` if the phase changes the model.

---

## Adding a Scenario or Demo

- **Scenario:** Create `internal/scenarios/<name>/` implementing the `scenarios.Scenario` interface. Self-register via `init()`.
- **Demo:** Create `internal/demo/<name>/` implementing the same interface. Self-register via `init()`. Each demo owns a dedicated VIP.

Ensure you include a `VerifyDeps` struct with a `TestVerifyCallOrder` test, an idempotent `Cleanup`, and templated embedded manifests via `//go:embed`.

---

## Releasing

Releases are published automatically via `.github/workflows/release.yml` using `goreleaser` when a `vX.Y.Z` tag is pushed:

```bash
git tag -a vX.Y.Z -m "release vX.Y.Z"
git push origin vX.Y.Z
```

---

## Reporting Issues

Open an issue using the templates in `.github/ISSUE_TEMPLATE/`. For bugs, please include:
- `gkebnkctl --version`
- The minimal `cluster.yaml` (redacted)
- The full stderr output
- Whether the issue reproduces on a fresh `up` or specific state

Thank you for helping us improve **gkebnkctl**!
