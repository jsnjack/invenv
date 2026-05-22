# AGENTS.md

> See [AGENTS.universal.md](./AGENTS.universal.md) and [AGENTS.go.md](./AGENTS.go.md) for universal conventions.
> Refresh: `make standards`

---

## Overview

`invenv` is a CLI that runs Python scripts inside an automatically managed
virtual environment. It detects the right interpreter (via shebang or `-p`),
locates a matching requirements file, creates/caches a venv keyed by
`(requirements hash, python version)` under `~/.local/invenv/`, installs
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
invenv.spec.tpl           RPM spec template (Version substituted at rpm build time).
rpmbuild/                 Local rpmbuild workspace (SOURCES/, SRPMS/, RPMS/, BUILD/).
```

---

## Build & Run

```bash
make check          # full validation gate (fmt, vet, build, test, lint)
make build          # cross-compile linux+darwin (amd64/arm64) to bin/
make test           # tests in cmd/
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
- **Filesystem locks (O_CREATE|O_EXCL)** serialize venv creation across
  concurrent invenv runs. Stale locks (older than 15 minutes, or no process
  found via `/proc`) are recovered automatically.
- **`init` subcommand stores env ID in `.venv/.venv.version`** because the
  venv lives at a fixed path (not keyed by hash), so the ID file is the only
  way to detect that requirements changed and the venv must be rebuilt.
- **Stale environments (older than 14 days) are cleaned on every run**, but
  only if no process is currently using them.
- **RPM + COPR distribution** is first-class alongside GitHub releases —
  Fedora-family users install via `dnf copr enable surfly/invenv`.

---

## Gotchas

- `process.go` reads `/proc` and is Linux-only. On darwin the lock-stale
  detection skips the process check (`runtime.GOOS == "linux"` guard in
  `waitUntilEnvIsUnlocked`).
- `removeDir` falls back to `sudo rm -rf` on permission errors — a venv
  created by another user can trigger an interactive sudo prompt.
- The RPM spec is generated from `invenv.spec.tpl` via `envsubst`. Editing
  `invenv.spec` directly is pointless; it gets overwritten.
