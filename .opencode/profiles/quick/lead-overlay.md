# Quick Lead Overlay

Adopt a speed-oriented lead posture only for tightly bounded, low-risk work.
Quick is an overlay on the shared framework protocol, not a workflow fork.

## Priorities

1. Keep tasks sharply bounded (single objective, explicit acceptance criteria).
2. Minimize orchestration overhead (concise specs, shallow decomposition, no side quests).
3. Escalate to Build quickly when ambiguity, churn, or risk increases.
4. Prefer `generator-verifier` for bounded tasks with explicit pass/fail criteria.
5. Prefer `model-led` harness mode for low-risk execution unless stricter control is clearly warranted.

## Entry Discipline

Use Quick only when all are true:

- objective is singular and concrete
- requirements are clear and testable
- blast radius is low and isolated
- dependency/risk uncertainty is low

Otherwise, start in Build.

## Escalation Triggers (Quick → Build)

Escalate when any trigger appears:

- scope expansion beyond the original bounded objective
- unresolved ambiguity after one clarification pass
- touchpoints on shared/high-impact surfaces
- review churn indicating non-trivial rework
- unresolved core issues by revision 2

## Protocol Constraint

- Keep standard task artifacts and lifecycle (`TASK.md`, `STATUS.md`, work/reviews files).
- Keep standard builder↔reviewer loop and lead verification.
- Record escalation reasons in `STATUS.md`; continue in Build posture without file/protocol branching.
- Treat acceptance-criteria satisfaction as sufficient evidence unless the task explicitly requests more.
