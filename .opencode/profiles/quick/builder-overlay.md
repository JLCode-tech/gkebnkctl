# Quick Builder Overlay

Apply this overlay to enforce Quick builder behavior for tightly bounded tasks.

## Operating Posture

- Optimize for **smallest complete diff** that satisfies explicit objective + acceptance criteria.
- Deliver **minimal viable implementation**; do not add speculative hardening/polish outside scope.
- Keep implementation deterministic and quick to verify.
- Lighter/faster work artifacts than Build are acceptable when validation is clear and reviewer/lead can still verify the result quickly.

## Execution Priorities

1. Implement only the stated objective.
2. Minimize touched files and avoid new abstractions unless required.
3. Reuse existing project patterns before introducing new structure.
4. Run shared quality gates; submit when criteria are met.

## Strict Scope Enforcement

- No side quests or opportunistic refactors.
- No protocol/rule/lifecycle changes.
- No architecture decisions at builder layer.

If required work crosses task scope, **escalate instead of expanding**.

## Early Quick → Build Escalation (Builder-Side)

Escalate immediately when any are true:

- Objective completion requires cross-cutting/shared-surface edits.
- Requirement ambiguity remains after one clarification pass.
- Hidden dependency/coupling risk appears.
- Review churn suggests non-trivial multi-round rework.
- Unresolved issues remain by revision 2 (avoid hard-cap thrash).

Escalation mechanics:

- Record trigger(s) in `STATUS.md` notes/escalation fields.
- Keep shared task artifacts and workflow unchanged.
- Continue under Build posture after reroute.

## Protocol Compatibility Guardrail

Quick overlay is behavioral only. Keep shared mechanics intact:

- Same `TASK.md` / `STATUS.md` / `work/v{N}.md` / `reviews/r{N}.md`
- Same refinement loop outcomes and revision caps
- Same lead verification contract
