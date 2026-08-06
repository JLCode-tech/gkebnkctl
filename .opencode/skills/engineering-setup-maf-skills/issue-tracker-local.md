# Issue tracker: MAF Task System

Issues and tasks for this repo live as markdown files in `.agent/tasks/active/`.

See `.agent/config/task-system.md` for the full layout and conventions.

## Conventions

- One task per directory: `.agent/tasks/active/<task-id>/`
- The task spec is `.agent/tasks/active/<task-id>/TASK.md`
- The task state is `.agent/tasks/active/<task-id>/STATUS.md`
- Triage state is recorded as a `State:` line in `STATUS.md` (see `triage-labels.md` for the role strings)
- Work artifacts are in `.agent/tasks/active/<task-id>/work/`

## When a skill says "publish to the issue tracker"

Create a new task using `bin/maf-task.sh create <task-id>` (creating the directory and scaffold automatically).

## When a skill says "fetch the relevant ticket"

Read the file at `.agent/tasks/active/<task-id>/TASK.md`. The user will normally pass the task-id directly.
