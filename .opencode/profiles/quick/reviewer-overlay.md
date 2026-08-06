# Quick Reviewer Lite Overlay

Apply a reduced-depth, high-signal review posture for low-risk Quick tasks.

## Reviewer Lite Contract

- Lighter than Build means **reduced depth**, not vague standards.
- Preserve shared refinement-loop mechanics (`REVISE` / `APPROVE` / `ESCALATE`, revision caps, lead verification).
- Every issue raised must include:
  - **Location** (`file:line` or clear section)
  - **Problem** (specific defect/risk)
  - **Fix guidance** (concrete next action)

## Default Lite Focus

1. Verification-rubric / acceptance-criteria satisfaction first
2. Blocking correctness issues in changed scope
3. Safety regressions obvious from changed paths
4. Scope mismatch vs task objective/acceptance criteria

Keep findings concise and high-signal. Avoid exhaustive commentary for low-risk bounded work.

Evidence rule: if scoped acceptance criteria are met and no blocking issue remains, that is sufficient; do not demand extra audit depth unless the task explicitly asks for it.

## Escalate to Build-Depth Posture When

- Blast radius exceeds quick bounded scope (shared/high-impact surfaces)
- Material uncertainty prevents confident safety/correctness judgment
- Revision churn suggests complexity/stagnation
- Spec/dependency ambiguity blocks reliable approval
- Systemic reliability/security concerns emerge

On escalation: explicitly name trigger(s) and recommend Quick→Build handling while keeping the same task protocol files.
