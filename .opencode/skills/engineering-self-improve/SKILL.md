---
name: self-improve
description: Self-improvement loop — feeds recent audit findings and low eval scores to a haiku-tier judge that proposes unified diffs to role prompts and CLI help text for human/Lead approval. ALWAYS invoke when the user says "self-improve", "propose prompt edits from audits", "improve prompts from eval scores", or asks to run the self-improvement loop. Do not use for promoting recurring findings to LESSONS.md (use `maf-audit.sh curate` instead) or for individual bug fixes (use diagnose).
---

# Self-Improve

Runs `bin/maf-improve.sh` to generate LLM-proposed unified diff patches to
role prompts and CLI help text, based on audit findings and low eval scores.
All proposals require human/Lead approval before being applied.

## Relationship to `maf-audit.sh curate`

| Tool | Input | Output | When to use |
|------|-------|--------|-------------|
| `maf-audit.sh curate` | Recurring audit findings (by frequency) | Entries in LESSONS.md + reviewer checklist | Finding crosses recurrence threshold |
| `maf-improve.sh` | Audit files, low-score task refs | Unified diff patch file for review | Quality-gap driven; LLM proposes targeted prompt edits |

## Steps

1. **Identify inputs** — choose one or more:
   - Recent audit files (e.g., `.agent/tasks/done/<task-id>/audits/a1.md`)
   - Task IDs with low eval scores from `maf-eval.sh`
   - The shared audit ledger at `.agent/audits/`

2. **Run the improvement loop**:

   ```bash
   # From explicit audit files
   bin/maf-improve.sh --audit-file .agent/tasks/done/some-task/audits/a1.md

   # From task IDs (scans audits/ inside the task dir)
   bin/maf-improve.sh some-task another-task

   # Dry run (no API call — for testing)
   JUDGE_DRY_RUN=1 bin/maf-improve.sh --audit-file .agent/tasks/done/some-task/audits/a1.md

   # Override output location
   bin/maf-improve.sh --output /tmp/my-proposals.patch some-task
   ```

3. **Composable with `maf-eval.sh`** — feed low-score tasks automatically:

   ```bash
   # Score a task and pipe into improve if score < 0.7
   bin/maf-eval.sh some-task --json \
       | python3 -c '
   import json, sys
   d = json.load(sys.stdin)
   score = d["aggregate"]["score"] or 1.0
   if score < 0.7:
       print("some-task")
   ' | xargs -r bin/maf-improve.sh
   ```

4. **Review the proposals** at `.agent/improve-proposals.patch`:

   ```bash
   cat .agent/improve-proposals.patch
   # or view as a diff
   git apply --check .agent/improve-proposals.patch
   ```

5. **Selectively apply** with Lead approval:

   ```bash
   # Apply specific hunks interactively
   git apply -3 .agent/improve-proposals.patch
   # or patch individual files manually after reviewing
   ```

## Important

- Proposals are **never auto-applied**. Every patch requires Lead review.
- The judge uses the `haiku` tier (`JUDGE_MODEL` override available).
- `JUDGE_DRY_RUN=1` produces a deterministic stub patch with no API calls.
