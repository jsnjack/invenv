# Universal Project Conventions

Run `make check` (or the project's equivalent validation gate) after every
change. It must pass before reporting the task as complete.

---

## Work patterns

- One concern per change. Don't mix refactoring with feature work.
- **Minimal footprint.** Change only what is needed to satisfy the requirement.
  Don't refactor nearby code, reorganize files, or improve unrelated things
  unless explicitly asked.
- **Clarify before assuming.** When requirements are ambiguous or conflicting,
  ask — don't guess and proceed.
- **Present options.** When a task has multiple valid solutions, list all of
  them with a short description and pros/cons for each. Ask the user to choose
  before doing any implementation.
- **Plan in concepts, not code vocabulary.** When describing a plan, use plain
  language — not variable names, function names, or pseudocode. Write "if
  output is suppressed" not "if `isSilent` is true"; write "the retry limit"
  not "`maxRetries`". Code names are implementation details that belong in the
  code, not in the reasoning.
- Never report work as done until all requirements are met and `make check`
  passes. If requirements cannot be met, say so explicitly.
- When something is unclear, read the existing code first — match its patterns.
- **Keep `AGENTS.md` current.** After any change that affects project
  structure, architecture, patterns, or design decisions, update `AGENTS.md`
  to reflect the new state. A stale `AGENTS.md` is worse than none.

---

## Build & validate

Every project exposes a single validation command (typically `make check`) that
runs in order: format → vet → build → test → lint. All steps must pass.

Missing tools print an install command and exit — never auto-install silently.

```bash
make check      # full validation gate — run after every change
make test       # tests only
make build      # compile only
make standards  # refresh AGENTS.universal.md from the standards repo (if present)
```

---

## Logging

Two debug flags on every command that produces observable behaviour:

- `--debug` / `-d` — verbose output to **stderr**. Human-readable. State
  changes, requests, responses. For interactive debugging.
- `--trace` — maximum detail written to `/tmp/<binary>.log`, **truncated on
  every start**. Wire data, every state transition. Designed for agent
  self-diagnosis — when something breaks, run with `--trace` and read the log.

Default (no flags): warnings and errors only.

The two flags are independent and composable.

**TUI applications:** the TUI owns the terminal. Never write logs to stderr —
it corrupts the UI. Route all diagnostic output to the trace file. Surface
errors through the UI itself.

---

## Code quality

- **Thin entry point.** CLI/main handles argument parsing, configuration
  loading, wiring, and startup only. No business logic.
- **Single responsibility.** Each module/package does one thing. Name it after
  what it does, not what uses it.
- **No hidden coupling.** Cross-module side effects are expressed as explicit
  callbacks or interfaces — never rely on shared global state or reach into
  another module's internals.
- **Errors carry context.** Every error returned or logged includes enough
  context to understand where it came from and why. Never propagate a bare
  error silently.
- **Fail fast on setup, recover gracefully at runtime.** Startup errors should
  exit immediately. Errors in long-running background workers (watchers,
  servers, queues) should log and continue — never crash a running service over
  a single bad event.
- **No new dependencies without justification.** Prefer the standard library
  or built-in tooling. Only introduce a third-party dependency when the
  justification is explicit and agreed.
- **Flag breaking changes.** If a change alters existing behaviour (API,
  config format, output format, CLI flags), say so before implementing it.

---

## Code style

- **Consistency over preference.** Match the style of the surrounding code.
  When adding to an existing pattern, extend it — don't introduce a second
  pattern.
- **Names describe what, not how.** Function and variable names describe their
  purpose, not their implementation.
- **Public symbols have documentation.** Every exported type, function, and
  constant has a doc comment.
- **No magic values.** Any literal (string, number, identifier) used in more
  than one place gets a named constant.
- **No dead code.** Remove unused code. If something is temporarily disabled,
  replace it with a TODO comment explaining why and what needs to happen.
- **Comments explain why, not what.** The code says what. Comments explain
  intent, gotchas, and non-obvious constraints.

---

## Testing

Tests exist to catch regressions — a test that doesn't fail when behaviour
breaks has no value.

- Tests live next to the code they test.
- A good test is readable, deterministic, and isolated — it should be
  understandable without reading the implementation, produce the same result on
  every run, and not depend on external state or other tests.
- Tests are structured as named cases run independently — one assertion failure
  should not block others.
- Every new exported function gets a test. Every bug fix gets a regression test.

---

## Never

- Never skip `make check` before reporting the task as complete.
- Never swallow errors silently — always log or return with context.
- Never log to stderr in TUI applications.
- Never auto-install missing tools — print the install command and exit.
- Never leave commented-out code in the codebase.
- Never introduce a second pattern when one already exists.
- Never change code outside the scope of the current task.
- Never add a dependency without explicit justification and agreement.
- Never change existing behaviour silently — always flag it first.
- Never commit on behalf of the user.
