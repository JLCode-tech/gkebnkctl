/**
 * Memory Compaction Plugin — v4 (plain JS, no Bun/TS required)
 *
 * Three layers of defense against post-compaction "helpful assistant" mode:
 *
 * 1. `experimental.chat.system.transform` — PRIMARY. PREPENDS recovery instructions
 *    to the FRONT of the system prompt on EVERY message. After compaction, the agent
 *    sees these FIRST — before any bloated instruction files can drown them out.
 *
 * 2. `experimental.session.compacting` — SECONDARY. Injects recovery instructions
 *    into the compaction prompt so the summarizer LLM is told to preserve them.
 *    (Known to be unreliable — GitHub #17412 — but costs nothing to keep.)
 *
 * 3. `event` — DIAGNOSTIC. Logs compaction events so we can verify the plugin loads.
 *
 * v4 changes (2026-03-19):
 *   - PREPEND (unshift) instead of APPEND (push) to system prompt
 *   - Recovery protocol is now the FIRST thing the model sees
 *   - Root cause: large .agent/ files (50KB+) buried the recovery instructions
 */

const RECOVERY_PROTOCOL = `
## CRITICAL: Post-Compaction Recovery Protocol

If context was just compacted (you notice limited conversation history):

1. **Check TodoWrite** — find the \`in_progress\` item. This is your current task.
2. **Read \`.agent/CURRENT_WORK.md\`** — your last checkpoint of progress and context.
3. **Continue the in_progress task immediately.** Your FIRST action must be a tool call (edit, write, bash, grep) that advances the work.

### What NOT to do after compaction
- DO NOT summarize what happened in previous sessions
- DO NOT output a status report or recap to the user
- DO NOT ask "What would you like me to do?" or "Should I continue?"
- DO NOT re-read every memory file before acting
- DO NOT call tools that are not in your tool definitions (no "invalid", no "dummy_tool")
- Your first response MUST be a tool call, not a text message

### If no TodoWrite state exists
Check \`.agent/backlog/BACKLOG.md\` for the \`current:\` item or pick the highest priority \`ready\` item.
`.trim()

export const MemoryCompaction = async (ctx) => {
  // Log that the plugin loaded successfully
  try {
    await ctx.client.app.log({
      body: {
        service: "memory-compaction",
        level: "info",
        message: "Memory Compaction Plugin loaded (v4 — prepend, plain JS)",
      },
    })
  } catch (_) {
    // client.app.log may not be available — fail silently
  }

  return {
    // Layer 1 (PRIMARY): PREPEND recovery instructions to the system prompt
    // This runs on EVERY LLM call — the agent sees this FIRST
    "experimental.chat.system.transform": async (_input, output) => {
      const alreadyPresent = output.system.some(
        (s) => s.includes("Post-Compaction Recovery Protocol")
      )
      if (!alreadyPresent) {
        output.system.unshift(RECOVERY_PROTOCOL)
      }
    },

    // Layer 2 (SECONDARY): Inject into the compaction prompt
    // The compaction LLM sees this and should include it in the summary
    "experimental.session.compacting": async (_input, output) => {
      output.context.push(RECOVERY_PROTOCOL)

      try {
        await ctx.client.app.log({
          body: {
            service: "memory-compaction",
            level: "info",
            message: `Compaction hook fired for session ${_input.sessionID} — recovery protocol injected`,
          },
        })
      } catch (_) {
        // fail silently
      }
    },

    // Layer 3 (DIAGNOSTIC): Log compaction events
    event: async ({ event }) => {
      if (event.type === "session.compacted") {
        try {
          await ctx.client.app.log({
            body: {
              service: "memory-compaction",
              level: "warn",
              message: "Session compacted — recovery protocol active via system.transform hook",
            },
          })
        } catch (_) {
          // fail silently
        }
      }
    },
  }
}
