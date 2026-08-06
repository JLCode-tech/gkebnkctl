---
name: explore-build-refute
description: Three-phase workflow for multi-component, correctness-sensitive changes — parallel read-only Explore agents with file:line-quoted question contracts, parallel Ownership Builders in one worktree under disjoint file-ownership contracts, then a single frontier reviewer told to adversarially REFUTE correctness against an enumerated risk list. ALWAYS invoke when user wants to build several owned components/areas at once and cares about correctness, mentions "explore build refute", asks for an adversarial review pass, or describes a feature that spans multiple files/modules where a wrong merge would be expensive to catch late. Do not use for a single self-contained change (use tdd) or for pure investigation with no build phase (use diagnose).
---

# Explore → Build → Refute

Named variant of the `orchestrator-subagent` runtime pattern (ADR-033). Reproduces the highest-performing observed session shape: parallel exploration, parallel ownership-scoped building in one shared worktree, then one adversarial reviewer over the merged result.

Runtime bookkeeping: `bin/maf-suggest-runtime.sh`'s `Suggested-Pattern` stays `orchestrator-subagent` for this shape — the variant name only shows up in the `Rationale:` line. Do not introduce a `Pattern: explore-build-refute` value anywhere; it is not a registered enum token.

## When this pattern applies

Use it when **both** are true:
- The work partitions into several genuinely separate owned areas (files, modules, subsystems) that can be built in parallel.
- Correctness matters enough to warrant a dedicated adversarial pass — the change is easy to get subtly wrong, or a wrong merge across the owned areas would be expensive to catch after the fact.

Reach for a plain `orchestrator-subagent` build (sequential Builder → Reviewer) instead when the change is small, single-owner, or low-risk — the extra explore and refute phases are overhead the task does not need. Reach for `agent-team` instead when the partitions are long-running and mostly independent, with no need for a single merged-range adversarial pass.

## Sizing guidance

- **Explorers**: one per distinct area of unknown behavior, capped around 3–5. More than that usually means the focus-file list is too broad — split the objective instead.
- **Ownership builders**: one per owned area from the ownership contract, same cap (3–5). Each builder's owned-files list must be disjoint from every other builder's — this is what makes same-worktree parallelism safe (ADR-033 precondition a).
- **Adversarial reviewer**: always exactly one, reading the full merged commit range. Never one reviewer per builder — a per-builder reviewer configuration is precisely what ADR-031 mandates worktree isolation to prevent, and this pattern only sidesteps that mandate because there is a single reviewer over the union of diffs (ADR-033 precondition b).

If either precondition (disjoint ownership contracts, single merged-range reviewer) cannot be met, this pattern does not apply — fall back to ADR-031's default one-worktree-per-parallel-builder isolation.

## Phase 1 — Explore

**Precondition**: the objective names (or can be narrowed to) a small set of focus files/areas whose current behavior is not yet understood well enough to spec builders against.

**Directive**: dispatch one read-only explorer subagent per focus area, in parallel. Each explorer answers a fixed set of numbered questions and returns findings — it changes nothing.

Paste-ready template (fill the bracketed fields):

```
You are an Explore agent. Repo/worktree: [path — confirm you are in the correct
checkout before reading anything].

Focus files:
- [path/to/file_a]
- [path/to/file_b]

Answer these questions. For each answer, quote the exact file:line and a short
code excerpt (2-5 lines) that supports it. If you cannot answer from the named
focus files, say so explicitly rather than guessing.

1. [Question about current behavior/contract of area A]
2. [Question about current behavior/contract of area B]
3. [Question about an edge case or invariant that matters for the upcoming change]
4. [Question about how area A and area B currently interact, if at all]
5. [Question about existing test/verification coverage for this area]
6. [Optional: question about a specific risk the requester is worried about]

Output structure:
## Findings
1. [file:line] — [answer + quoted excerpt]
2. [file:line] — [answer + quoted excerpt]
...

## Open questions
[Anything you could not resolve from the named files]

This is a read-only pass. Make no edits, and do not run mutating commands —
only inspect and report.
```

**Phase 1 exit criterion**: every explorer has returned file:line-grounded findings for its focus area, and there is enough shared understanding to write disjoint ownership contracts for Phase 2.

## Phase 2 — Ownership builders

**Precondition**: Phase 1 findings exist, and the objective can be split into owned areas with no file overlap between them.

**Directive**: dispatch one builder subagent per owned area, all working in the **same** worktree, each spec including an explicit disjoint file-ownership contract. This is the ADR-031 exception (ADR-033) — it only holds because ownership is enforced by contract, not assumed from convention.

Paste-ready template per builder:

```
You are Builder [N] of [total]. Role: [what this builder owns and delivers].

Hard limits: no git commit, no git push, no force operations. Stay inside your
owned files.

File ownership contract: you own [list exact paths/globs, e.g. handler_orders.py,
handler_orders_test.py]. Every other file in this worktree — including anything
matching [illustrative pattern, e.g. handler_*] outside your list — belongs to
a sibling builder. Treat those as read-only reference material only.

Environment: [exact venv/interpreter path], test invocation: [exact command,
e.g. `.venv/bin/pytest tests/test_handler_orders.py -x`].

Defect evidence (if fixing a bug): [live-reproduced failure — command run,
actual output, expected output].

Patterns to mirror: [pointer to an existing in-repo file/function that shows
the house style for this kind of change].

Deliverable: working code in your owned files + passing tests, reported back
via [work file / return value — do not commit].
```

**Phase 2 exit criterion**: every builder has delivered against its owned files, and the union of all builder diffs covers the intended change with zero overlapping files.

## Phase 3 — Adversarial refute

**Precondition**: Phase 2 exit criterion met. All builder diffs exist in the shared worktree, uncommitted or committed as the workflow requires.

**Directive**: dispatch exactly one frontier-tier reviewer over the full merged commit range. Give it an enumerated, specific risk list and require it to either confirm each risk is handled or produce a concrete failing scenario with file:line — no vague "looks fine."

Paste-ready template:

```
You are the adversarial reviewer for this merged change. Commit range: [exact
range, e.g. `git diff <base-ref>..HEAD` or `git diff <base-ref> -- <paths>`].
Read the full range — this covers every owned area from every builder.

Your job is to REFUTE correctness, not confirm it. For each risk below, either
(a) confirm with file:line evidence why it cannot occur, or (b) produce a
concrete failing scenario — file:line + the exact input/sequence that breaks it.
A verdict without file:line evidence is not acceptable for either outcome.

Risks to refute:
1. [Specific correctness risk #1 — e.g. "area A and area B disagree about X's
   error contract at the seam between them"]
2. [Specific correctness risk #2]
3. [Specific correctness risk #3]
4. [Add one risk per meaningful interaction/edge case surfaced in Phase 1]

Report per risk: CONFIRMED-SAFE (file:line evidence) or FAILS (file:line +
concrete failing scenario). End with an overall verdict: APPROVE / REVISE /
ESCALATE.
```

**Phase 3 exit criterion**: every risk has a CONFIRMED-SAFE or FAILS verdict with file:line evidence. FAILS verdicts route back to the owning builder for a fix; do not re-run the whole three-phase cycle for a single-risk fix.

## After the loop

Curate any FAILS finding whose root cause generalizes (e.g. a class of seam bug this pattern is good at catching) into `.agent/LESSONS.md`, same as any other review finding — this pattern does not get a separate curation path.
