# gkebnkctl Remediation Tracker

This file tracks the open remediation items for `JLCode-tech/gkebnkctl`. It is
maintained by the maintainers and should be updated as items close so that any
agent or contributor opening the repo sees current, non-stale state.

## Legend

- ✅ Resolved
- 🔄 In progress / has open PR
- ⬜ Open

## Resolved

| ID | Title | Resolution |
|----|-------|------------|
| GKE-01..06 | Initial batch of hygiene items & network ownership labeling | Merged in PR #48, #49, #50 |
| GKE-07 | `down --dry-run` shared VPC resources reporting | Merged in PR #51 |
| GKE-08 | README disclosure of shared VPC resources | Documented in README & docs/ALIGNMENT.md |
| GKE-09 | Example configs sanitized and validated | Examples sanitized and tested |
| GKE-10 | CI header comments aligned with triggers | Updated in .github/workflows/ |
| GKE-11 | GitHub Actions upgraded to latest versions | Actions upgraded across all workflows |
| GKE-12 | govulncheck pinned with gate script | Added scripts/govulncheck-gate.sh |
| GKE-13 | Dependabot workflow & configs | Configured for dependencies and actions |
| GKE-14 | cspell.json with automated spellcheck CI | Added .github/workflows/spellcheck.yml |
| GKE-15 | CHANGELOG comprehensive release history | Expanded in CHANGELOG.md |
| GKE-16 | ADR policy alignment | Managed in docs/ |
| GKE-17 | Phase asymmetry documented | Fully detailed in docs/PHASES.md |
| GKE-18 | Internal tooling untracked and ignored | Cleaned up in .gitignore |
| GKE-19 | Worktree hygiene and artifact cleanup | .gitignore updated, binaries excluded |
| GKE-20 | Phase numbering gaps documented | Explained in docs/PHASES.md |
| GKE-21 | Staticcheck and lint configs aligned | Fully passing staticcheck in CI |

## Outstanding

None. All remediation items are 100% resolved and verified.

## Last updated

2026-09-02
