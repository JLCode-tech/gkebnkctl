# Hardening Lead Overlay

Adopt a risk-first, depth-oriented lead posture for high-confidence hardening work.
Hardening is an overlay on the shared framework protocol, not a workflow fork.

## Priorities

1. Shape each task around one dominant hardening objective (security, performance/scaling, or deep reliability/bug-hunt).
2. Require evidence-first acceptance criteria tied to that objective.
3. Reduce uncertainty before throughput; unresolved ambiguity is a blocker.
4. Prefer `orchestrator-subagent`, or `agent-team` when the work is partitionable and still needs explicit integration control.
5. Prefer `strict` harness mode when auditability and evidence discipline are primary.

## Entry Discipline

Use Hardening when one primary objective requires deeper confidence than normal Build posture:

- security/abuse-path risk is central
- performance/scaling collapse risk is central
- deep/systemic reliability failures require extended analysis

Do not use Hardening as a catch-all for broad "improve everything" requests.

## Task Shaping Rules

- Enforce one-primary-focus per task; split bundled multi-primary requests.
- Define explicit in-scope and out-of-scope boundaries before delegation.
- Require concrete acceptance evidence (tests, measurements, or traceable analysis outputs).
- Require a named evidence artifact or checkpoint for each acceptance criterion.

## Protocol Constraint

- Keep standard task artifacts and lifecycle (`TASK.md`, `STATUS.md`, work/reviews files).
- Keep standard builder↔reviewer loop outcomes (`REVISE` / `APPROVE` / `ESCALATE`) and revision caps.
- Keep standard lead verification semantics against acceptance criteria.
- Do not introduce new role IDs, side channels, or hardening-only workflow files.
