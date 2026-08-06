---
name: Feature request
about: Propose a new command, flag, or capability for gkebnkctl
title: 'feat: <one-line summary>'
labels: []
assignees: ''
---

<!--
  Thanks for proposing a feature. The structure below mirrors the shape
  that has worked well for in-tree work: a clear motivation, a concrete
  surface proposal, testable acceptance criteria, and an explicit list
  of things you are NOT asking for so scope stays small.

  Delete the comment blocks before submitting.
-->

## Motivation

<!--
  One short paragraph: what does the current tool not do, and what
  would users do with this if it existed? Linking the gap to a real
  workflow (e.g. "today recovering a bucket needs a shell loop of
  `cos object get`") helps far more than abstract framing.
-->

## Proposed surface

<!--
  Concrete command shape, flag names, positional args. Mirror existing
  patterns in the codebase where possible — symmetry is cheaper than
  novelty.
-->

```
gkebnkctl <verb> <args> [flags]
```

- `<arg1>` — what it is, whether required.
- `--flag` — what it does, default, required-or-not.

## Behavior

<!--
  Spell out enough that someone implementing this doesn't have to ask
  "what should happen when X". Cover at minimum:
    - the happy path
    - empty / no-op / missing-input cases
    - error cases (what exits non-zero, what the message names)
    - interaction with the existing global flags
      (--workspace, --output, --quiet, --verbose, --on, --backend)
    - any side-effects on the filesystem / GCP project
-->

## Acceptance criteria

<!--
  Numbered, testable. "Works" is not a criterion. Each item should
  be something an integrator can run and observe pass/fail.
  Aim for 4-10 items.
-->

1.
2.
3.

## Out of scope (deliberately)

<!--
  List the adjacent things you can imagine being asked for and are
  NOT asking for in this issue. Prevents scope creep during
  implementation and makes follow-up issues easy to spot.
-->

- ...
- ...

## Files likely touched

<!--
  Optional, but very helpful — gives the implementer a starting point
  and surfaces accidental cross-package reach early.
-->

- `internal/...` —
- `internal/cli/...` —

## Notes

<!--
  Anything else that doesn't fit above: prior discussion, related
  issues, links to upstream docs, screenshots, etc.
-->
