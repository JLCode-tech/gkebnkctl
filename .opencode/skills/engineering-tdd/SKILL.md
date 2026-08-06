---
name: tdd
description: Test-driven development with red-green-refactor loop. ALWAYS invoke when user wants to build a new feature, fix a bug test-first, mentions "red-green-refactor", asks for integration tests, says "test-first", or asks to add tests alongside new code. Do not use for diagnosing failing tests on existing code (use diagnose) or for designing without writing tests (use improve-codebase-architecture).
---

# Test-Driven Development

## Philosophy

**Core principle**: Tests should verify behavior through public interfaces, not implementation details. Code can change entirely; tests shouldn't.

**Good tests** are integration-style: they exercise real code paths through public APIs. They describe _what_ the system does, not _how_ it does it. A good test reads like a specification - "user can checkout with valid cart" tells you exactly what capability exists. These tests survive refactors because they don't care about internal structure.

**Bad tests** are coupled to implementation. They mock internal collaborators, test private methods, or verify through external means (like querying a database directly instead of using the interface). The warning sign: your test breaks when you refactor, but behavior hasn't changed. If you rename an internal function and tests fail, those tests were testing implementation, not behavior.

See [tests.md](tests.md) for examples and [mocking.md](mocking.md) for mocking guidelines.

## Vertical slices, not horizontal

**Write one test, write the code that passes it, then move on.** This is "vertical slicing" — each behavior gets a full RED→GREEN trip before the next one starts.

Writing all tests first ("horizontal slicing") produces **crap tests**:

- Tests written in bulk test _imagined_ behavior, not _actual_ behavior
- You end up testing the _shape_ of things (data structures, function signatures) rather than user-facing behavior
- Tests become insensitive to real changes — they pass when behavior breaks, fail when behavior is fine
- You outrun your headlights, committing to test structure before understanding the implementation

**Correct approach**: vertical slices via tracer bullets. One test → one implementation → repeat. Each test responds to what you learned from the previous cycle. Because you just wrote the code, you know exactly what behavior matters and how to verify it.

```
WRONG (horizontal):
  RED:   test1, test2, test3, test4, test5
  GREEN: impl1, impl2, impl3, impl4, impl5

RIGHT (vertical):
  RED→GREEN: test1→impl1
  RED→GREEN: test2→impl2
  RED→GREEN: test3→impl3
  ...
```

## Workflow

### 1. Planning

**Precondition**: user has described a feature or bug to address. No code has been written yet for this slice.

**Directive**: confirm the planning checklist with the user before any code is written.

When exploring the codebase, use the project's domain glossary so that test names and interface vocabulary match the project's language, and respect ADRs in the area you're touching.

Before writing any code:

- [ ] Confirm with user what interface changes are needed
- [ ] Confirm with user which behaviors to test (prioritize)
- [ ] Identify opportunities for [deep modules](deep-modules.md) (small interface, deep implementation)
- [ ] Design interfaces for [testability](interface-design.md)
- [ ] List the behaviors to test (not implementation steps)
- [ ] Get user approval on the plan

Ask: "What should the public interface look like? Which behaviors are most important to test?"

**You can't test everything.** Confirm with the user exactly which behaviors matter most. Focus testing effort on critical paths and complex logic, not every possible edge case.

### 2. Tracer Bullet

**Precondition**: planning checklist complete and the user has approved the prioritized behavior list.

**Directive**: write ONE test that confirms ONE thing about the system, then write the minimal code that passes it.

```
RED:   Write test for first behavior → test fails
GREEN: Write minimal code to pass → test passes
```

This is your tracer bullet — proves the path works end-to-end.

### 3. Incremental Loop

**Precondition**: tracer bullet passes; you have at least one verified end-to-end path.

**Directive**: for each remaining behavior, run one full RED→GREEN cycle before starting the next.

```
RED:   Write next test → fails
GREEN: Minimal code to pass → passes
```

Rules per cycle:

- Write one test at a time.
- Write only enough code to pass the current test.
- Keep the test focused on observable behavior.
- Defer ideas about future tests to a note — write them down, then return to the current test.

### 4. Refactor

**Precondition**: every test in the current slice is passing (GREEN).

**Directive**: look for [refactor candidates](refactoring.md) and apply them one at a time, re-running the tests after each.

- [ ] Extract duplication
- [ ] Deepen modules (move complexity behind simple interfaces)
- [ ] Apply SOLID principles where natural
- [ ] Consider what new code reveals about existing code
- [ ] Run tests after each refactor step

**Refactor only while GREEN.** Get to GREEN first; revert any refactor attempt that turns the suite RED.

## Checklist Per Cycle

```
[ ] Test describes behavior, not implementation
[ ] Test uses public interface only
[ ] Test would survive internal refactor
[ ] Code is minimal for this test
[ ] No speculative features added
```
