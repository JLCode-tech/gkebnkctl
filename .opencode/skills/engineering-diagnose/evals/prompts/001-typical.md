# Eval prompt 001 — typical bug report

User wants help debugging a flaky CI test that fails ~10% of runs with a
timeout in the authentication middleware.

---

I'm seeing my CI fail about 10% of the time on the `auth-middleware.test.ts` suite.
It times out after 30s. Local runs always pass. Can you help me figure out what's
going on?
