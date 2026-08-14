# gkebnkctl Remediation Tracker

This file tracks the open remediation items for `JLCode-tech/gkebnkctl`. It is
maintained by the maintainers and should be updated as items close so that any
agent or contributor opening the repo sees current, non-stale state.

## Legend

- ✅ Resolved
- 🔄 In progress / has open PR
- ⬜ Open

## Resolved

| ID | Title |
|----|-------|
| GKE-01..06 | Initial batch of hygiene items |
| GKE-18 | Internal-tooling untrack |
| GKE-07 | `down --dry-run` under-reports shared VPC resources — merged via PR #51; local `main` fast-forwarded to include the fix |

## Outstanding

| ID | Sev | Title | Notes |
|----|-----|-------|-------|
| GKE-08 | P1 | README doesn't disclose shared VPC per-cluster resources | Depends on GKE-05 |
| GKE-09 | P1 | Example configs partially ignored | Needs sanitisation review |
| GKE-10 | P2 | CI header comment contradicts triggers | Touch `ci.yml` |
| GKE-11 | P2 | CI actions two majors behind | Touch `ci.yml` |
| GKE-12 | P2 | `govulncheck@latest` unpinned | Touch `ci.yml` |
| GKE-13 | P2 | No `dependabot.yml` | Add config |
| GKE-14 | P2 | `cspell.json` present but no CI job | Touch `ci.yml` |
| GKE-15 | P2 | CHANGELOG is a 12-line stub | Docs |
| GKE-16 | P2 | One ADR tracked while `docs/adr/.gitignore` ignores rest | Decide ADR policy |
| GKE-17 | P2 | 20 up vs 3 down phases undocumented | Document intentional asymmetry |
| GKE-19 | P3 | Worktree junk (`bin/`, `artifacts/`, 95 MB binary) | Cleanup |
| GKE-20 | P3 | Phase numbering gaps undocumented | Docs |
| GKE-21 | P3 | `staticcheck.conf` referenced but missing | Add or remove reference |

## Branch hygiene note

Local `main` has been fast-forwarded to `origin/staging` (`bc29e3e`) so it
contains the GKE-07 fix. `origin/main` is still on the pre-merge commit because
the branch requires the "spell" status check; pushing directly is blocked. The
normal `staging → main` flow will bring `origin/main` forward.

## Last updated

2026-08-14
