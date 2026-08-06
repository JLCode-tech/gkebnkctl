---
name: handoff
description: Write a session-handoff brief that a fresh Lead can pick up cold. Use when user says "handoff", "hand it off", "I'm logging off", "context is full", or "compaction is about to hit". Do not use for task-level context bundles (see bin/maf-context.sh for those) or for normal end-of-turn summaries.
---

# Handoff

## Purpose

Produce a brief that a fresh Lead — opening this repo cold tomorrow, after compaction, or from a different machine — can read in under two minutes and pick up where the current session left off. Reference existing files by path rather than duplicating their content; the brief is a pointer, not a copy.

Task-level handoffs (build → review → verify within one task) already use `bin/maf-context.sh`. This skill handles the **session-level** gap: what the Lead was doing across multiple tasks or conversations.

The checkpoint cadence in `.agent/rules/context-budget.md` (~8h wall clock or ~300 tool calls — surfaced by `bin/maf-session-close.sh`) is one of the moments to invoke this skill, alongside the user-driven triggers above.

## Output location

Write to `.agent/handoff/{YYYY-MM-DD-HHMM}-{slug}.md` where `slug` is a kebab-case noun phrase summarising what was in flight. Create the `.agent/handoff/` directory if it doesn't exist. Files are append-only history — never overwrite an older handoff.

## Step 1 — Confirm the trigger

Precondition: user message indicates a session-end signal (handoff, logging off, context full, compaction imminent), OR your own context is >80% full.

Action: in one sentence, confirm what's being handed off ("Handing off the X work — let me write the brief"). Do not start writing yet.

## Step 2 — Collect the references

Precondition: you confirmed a handoff is wanted.

Action: gather these pointers without re-reading their content. Use shell tools for paths/timestamps only.

- Active task IDs from `.agent/tasks/active/` (just the directory names — do not read the TASK.md bodies)
- Current branch and any uncommitted work: `git status --short`, `git branch --show-current`
- Open PRs touched this session (if any): just the branch names you remember creating
- Any new files you wrote that aren't yet committed
- `.agent/ACTIVE_CONTEXT.md` last-updated timestamp

If a piece of information requires reading a file you haven't already read this session, **skip it**. The handoff brief points at files, not at their contents.

## Step 3 — Draft the brief

Precondition: references collected.

Action: write the file using this exact shape. Keep the entire brief under 60 lines.

```markdown
# Handoff — {one-line title}

**Written**: {YYYY-MM-DD HH:MM} by Lead session
**Branch at write time**: {branch}
**Trigger**: {user-handoff | compaction-imminent | context-full | end-of-day}

## What was in flight

- {one-line goal of the session}
- {one-line: where it got to (state, not narrative)}

## Next concrete action

{One sentence. What the fresh Lead should do *first* on resume. Always a single named command, file edit, or read.}

## Pointers

- Active tasks: {list of task-ids, or "none"}
- Branches with uncommitted intent: {list, or "none"}
- Open PRs (not yet pushed by user): {list, or "none"}
- Relevant ADRs / docs: {paths, no copying}
- Decisions made this session worth remembering: {1-3 bullets, link to MEMORY.md if added}

## What is NOT done

{One bullet per known gap. Be specific about acceptance criteria still failing.}

## Open questions for the human

{Anything the Lead needs the human to decide before continuing. Empty list is fine.}
```

Each section is a fixed shape. If a section has nothing, write "none" — do not omit the heading.

## Step 4 — Update the active pointer

Precondition: brief written and saved.

Action: append one line to `.agent/MEMORY.md` under a `## Handoffs` section (create the section if absent):

```
- [handoff] {YYYY-MM-DD HH:MM} — {one-line title} — `.agent/handoff/{filename}`
```

Keep `.agent/MEMORY.md` under 100 lines; if needed, prune older handoff entries by moving them into a `.agent/handoff/INDEX.md` and leaving only the most recent 3 pointers in MEMORY.md.

## Step 5 — Report to the human

Precondition: brief written, pointer updated.

Action: in chat, give the user the path to the brief and the single next concrete action. One short paragraph. Do not paste the brief's content.

## Keep handoffs lean

A useful handoff stays a pointer, not a copy. The patterns that work:

- **Reference files by path.** Point at TASK.md, ADRs, or work files; the fresh Lead reads them directly.
- **State the next concrete action.** Replace narrative ("we tried X, then Y…") with the single next step.
- **Keep it factual.** Record what the fresh Lead needs to act; speculative hypotheses belong in TASK.md.
- **Stay between-tasks.** Task-level context is already structured at `.agent/tasks/{id}/`; the handoff is the layer above that.
