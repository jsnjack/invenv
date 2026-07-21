# AGENTS.md

> See [AGENTS.universal.md](./AGENTS.universal.md) and [AGENTS.go.md](./AGENTS.go.md) for universal conventions.
> Refresh: `make standards`

---

## Overview

`invenv` is a CLI that runs Python scripts inside an automatically managed
virtual environment. It detects the right interpreter (via shebang or `-p`),
locates a matching requirements file, creates/caches a venv keyed by
`(requirements hash, python version)` under the user cache dir
(`os.UserCacheDir()/invenv` — `~/.cache/invenv/` on Linux), installs
dependencies, and `exec`s the script. Aimed at users who want
`./script.py`-style convenience without the bookkeeping.

---

## Architecture

```
main.go                   Thin entry point — calls cmd.Execute()
cmd/
  cmd_root.go             Root cobra command. Runs a Python script in a managed venv.
  cmd_init.go             `init` subcommand. Creates a .venv in the current directory.
  script.go               Script type. Resolves interpreter/requirements, builds & manages venvs.
  process.go              /proc scan to detect processes currently using a venv (Linux only).
  utils.go                Helpers: hashing, locking, shebang parsing, exec wrappers, stale cleanup.
  logger.go               slog setup: --debug (stderr) and --trace (file) levels.
e2e/                      Black-box tests: build the real binary, run it as a
                          subprocess against real scripts/requirements.
                          Gated behind the "e2e" build tag — see `make e2e`.
invenv.spec.tpl           RPM spec template (Version substituted at rpm build time).
rpmbuild/                 Local rpmbuild workspace (SOURCES/, SRPMS/, RPMS/, BUILD/).
```

---

## Build & Run

```bash
make check          # full validation gate (fmt, vet, build, test, lint)
make build          # cross-compile linux+darwin (amd64/arm64) to bin/
make test           # tests in cmd/
make e2e            # black-box CLI tests in e2e/ — builds the real binary,
                     # needs network (real pip installs); not part of `make check`
make rpm            # build a source RPM under rpmbuild/SRPMS/
make copr           # submit the source RPM to Fedora COPR (surfly/invenv)
make release        # github release + rpm + copr
make standards      # refresh AGENTS.universal.md and AGENTS.go.md
```

Smoke test:
```bash
./invenv --version
./invenv -- /path/to/script.py
```

---

## Design Decisions

- **Env ID = hash(requirements) + python version**, base62-encoded. Two scripts
  with identical dependencies and interpreter share a single venv.
- **`syscall.Exec` not `os/exec`** when launching the user's script — the
  Python process replaces invenv entirely so signals and exit codes pass
  through cleanly.
- **Filesystem locks** serialize venv creation across concurrent invenv
  runs. A lockfile is created atomically via `os.Link` from a temp file and
  contains the owner's PID + timestamp; a heartbeat goroutine refreshes its
  mtime while held. A lock is stale (and recovered automatically) when it is
  empty/malformed, its PID is dead, or its mtime is older than 15 minutes
  despite a live PID (PID reuse).
- **Built marker (`.invenv-built`)** is written after a successful build; a
  directory without it (and without `bin/python`) is treated as a partial
  build and rebuilt.
- **`init` subcommand stores env ID in `.venv/.venv.version`** because the
  venv lives at a fixed path (not keyed by hash), so the ID file is the only
  way to detect that requirements changed and the venv must be rebuilt.
- **Stale environments (older than 14 days) are cleaned on every run**, but
  only if no process is currently using them.
- **RPM + COPR distribution** is first-class alongside GitHub releases —
  Fedora-family users install via `dnf copr enable surfly/invenv`.

---

## Gotchas

- `process.go` reads `/proc` and is Linux-only. It is used only by the
  stale-environment cleanup (`cleanupStaleEnv`); on darwin the /proc read
  fails, so stale environments are never removed there — the cache grows
  until cleaned manually. Lock staleness itself is cross-platform (PID
  liveness via signal 0 + mtime heartbeat).
- `removeDir` falls back to `sudo rm -rf` specifically on `EACCES` (not the
  wider `EPERM`, e.g. immutable files/FUSE mounts) — a venv created by
  another user can still trigger an interactive sudo prompt.
- The RPM spec is generated from `invenv.spec.tpl` via `envsubst`. Editing
  `invenv.spec` directly is pointless; it gets overwritten.
- `e2e/` tests build the binary in `TestMain` and run it as a real
  subprocess; each test gets its own `HOME`/`XDG_CACHE_HOME` (see
  `newIsolatedHome`) but `--trace` always writes to the fixed
  `icmd.TraceLogPath` (`/tmp/invenv.log`), so the one test exercising it
  can't run concurrently with another instance of itself. The suite relies
  on running sequentially (no `t.Parallel()` anywhere in that package).
