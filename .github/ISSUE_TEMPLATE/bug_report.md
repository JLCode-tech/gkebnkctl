---
name: Bug report
about: Something gkebnkctl does that it shouldn't, or doesn't do that it should
title: 'bug: <one-line symptom>'
labels: []
assignees: ''
---

<!--
  Thanks for filing a bug. The structure below mirrors the shape that
  has worked well for in-tree work: a precise symptom, a reliable
  reproduction, a clear what-it-should-do, and enough environment
  context to triage.

  Delete the comment blocks before submitting.
-->

## Symptom

<!--
  One short paragraph. Be precise about *what is broken* — exact error
  text, the exact verb/flag combination that triggers it, the specific
  output that's wrong. Quote the failure verbatim if it surfaces one.

  Examples that work:
    - "`gkebnkctl down -w my-cluster` exits 1 with `context deadline
       exceeded`; the same command with `--timeout 10m` succeeds."
    - "PDF book page 120 shows the mermaid diagram's shapes and
       arrows but the label text inside each node is missing."

  Examples that don't help much:
    - "gkebnkctl is broken" — too vague to act on.
    - "doesn't work as expected" — what was expected?
-->

## Reproduction

<!--
  The minimum sequence that produces the symptom on a clean machine.
  Use a fenced shell block. Include enough setup that someone else can
  run it cold — workspace name, what state the workspace is in, env
  vars required, etc.
-->

```
# 1. workspace state to start from
gkebnkctl ws ...

# 2. the command that fails
gkebnkctl <verb> ...

# 3. what you see
<paste the exact output here>
```

## Expected behavior

<!--
  One short paragraph. What should the symptom step have produced
  instead? Be concrete — exact output, exit code, side-effects.
-->

## Actual behavior

<!--
  Optional if "Symptom" already covered this; otherwise spell out
  what actually happened in the reproduction. Logs / stack traces /
  screenshots welcome — paste in fenced code blocks or drag-drop
  images.
-->

## Environment

<!--
  - gkebnkctl version: `gkebnkctl version` output (must be present)
  - OS / arch: e.g. Linux x86_64, macOS arm64, Windows x86_64
  - GCP region/zone: e.g. ca-tor, us-south
  - Backend used: local | docker | k8s | ssh:<target>
  - Anything else load-bearing — terraform version if relevant,
    docker/podman version if backend=docker, kube version if
    backend=k8s.
-->

- `gkebnkctl version`:
- OS / arch:
- GCP region/zone:
- Backend:

## Suspect pipeline / hypotheses (optional)

<!--
  If you've narrowed it down — which file, which function, which
  pipeline stage — say so. Ranked-likelihood hypotheses are great:
    1. Most likely cause + why
    2. Second cause + why
    3. ...
  This is optional; if you just have the symptom, that's fine too.
-->

## Acceptance criteria

<!--
  Numbered, testable — what "fixed" means. "It works" is not a
  criterion. Each item should be something an integrator can run and
  observe pass/fail. Include a regression check where it makes sense
  (something that would have caught this if it had existed).
-->

1.
2.
3.

## Out of scope (deliberately)

<!--
  The adjacent fixes you can imagine being asked for and are NOT
  asking for in this issue. Keeps the fix small and surfaces
  follow-up issues.
-->

- ...
- ...

## Notes

<!--
  Anything else: links to related issues, prior workarounds, full
  logs (paste in <details><summary> blocks if long), upstream
  reports, etc.
-->
