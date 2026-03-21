# MacGuffin Launch Readiness Audit

**Date:** 2026-03-21
**Work Item:** mg-44bb

---

## 1. CLI End-to-End Command Audit

### `mg init` — PASS
- Creates `~/.macguffin` with correct directory tree (`work/available`, `work/claimed`, `work/done`, `mail`, `log`).
- `--git` flag initializes a git repo for snapshot support.

### `mg new` — PASS
- Creates work items with YAML frontmatter in `work/available/`.
- Supports `--type`, `--depends`, and other flags.
- ID generation uses configurable prefix (default `mg-`).

### `mg list` — PASS
- Groups items by status (available, claimed, pending).
- `--status` flag filters by specific status.
- `--all` / `-a` / `--archived` flags include done and archived items.
- `--repo` flag filters by repository substring.
- Default view correctly hides done/archived items.

### `mg show` — PASS
- Displays work item details by ID.
- Searches across all status directories.

### `mg claim` — PASS
- Atomically claims a work item using `rename(2)`.
- Concurrent claims (10 simultaneous) correctly result in exactly 1 winner.

### `mg done` — PASS
- Moves claimed item to `work/done/`.
- `--result` flag writes a `.result.json` sidecar file.

### `mg archive` — PASS
- Moves done items to `work/archived/` based on `--days` threshold (default 7).
- `--days=0` archives all done items immediately.

### `mg mail` — PASS
- `mg mail send` delivers messages to agent inboxes (Maildir-style `new/`).
- `mg mail list` shows messages with subject and sender.
- `mg mail read` moves messages from `new/` to `cur/`.

### `mg reap` — PASS
- Detects stale claims from dead PIDs.
- Returns reaped items to `work/available/`.

### `mg edit` — PASS (not tested end-to-end, but command is registered and has tests)

---

## 2. Build & Test

### `go build` / `build.sh` — PASS
- `build.sh` runs `go install ./cmd/mg` successfully.
- Binary builds cleanly with no warnings.

### `go test ./...` / `test.sh` — PASS
- All 5 packages pass: `cmd/mg`, `internal/event`, `internal/mail`, `internal/workitem`, `internal/workspace`.

### `gofmt` / `fmt.sh` — FAIL
- `internal/workitem/edit_test.go` has formatting issues.
- `fmt.sh` exits non-zero, which would block the pre-commit hook.

### E2E Milestones Test (`scripts/e2e_milestones_test.sh`) — FAIL
- The `extract_id()` helper hardcodes `gt-[a-f0-9]+` as the ID pattern, but the default prefix is now `mg-`.
- The test hangs/fails at M1 because it cannot extract any IDs from command output.
- **Blocker:** E2E tests are non-functional with the default configuration.

### Event Test (`scripts/event_test.sh`) — NOT TESTED (depends on e2e infrastructure)

---

## 3. README Accuracy

### Installation section — PASS
- Homebrew tap, shell installer, and `go install` instructions are all present and correct.
- macOS `/usr/bin/mg` name collision is documented with a clear workaround.

### Quick Start section — PASS
- All example commands work as documented.

### Commands table — NEEDS_REVIEW
- **Missing from table:** `claim`, `done`, `archive`, `reap`, `schedule`, `edit`. These are all registered commands but not listed in the README command table.
- The table lists 10 commands; the CLI actually has 16 (including `help` and `completion` from cobra).
- The `mg event` description in the table is accurate.

### Directory Layout section — NEEDS_REVIEW
- Missing `work/pending/` directory (used for dependency-gated items).
- Missing `work/archived/` directory (used by `mg archive`).

### Project Structure section — NEEDS_REVIEW
- Missing `internal/event/` from the tree listing.

---

## 4. Goreleaser Configuration

### `.goreleaser.yml` — PASS
- Builds for Linux, macOS, FreeBSD (amd64/arm64, excluding FreeBSD arm64).
- Binary-only archives with SHA256 checksums.
- Homebrew tap publishing configured for `drellem2/homebrew-tap`.
- ldflags correctly set version from git tag.

---

## 5. Edge Cases in Work Item Lifecycle

### Silent error messages — FAIL
- All error conditions (double claim, claim nonexistent ID, done on unclaimed item, show nonexistent ID) exit with code 1 but produce **no output**.
- Root cause: `SilenceErrors: true` in cobra config + `main()` discards the error: `if err := rootCmd.Execute(); err != nil { os.Exit(1) }`.
- Users and scripts get no diagnostic feedback on what went wrong.

### Double claim — PASS (behavioral)
- Attempting to claim an already-claimed item correctly fails (exit 1).

### Claiming nonexistent ID — PASS (behavioral)
- Correctly fails (exit 1), though no error message is shown.

### Done on unclaimed item — PASS (behavioral)
- Correctly fails (exit 1).

---

## Summary

### Blockers (must fix before launch)

1. **Silent error messages.** All CLI errors produce no output — just a silent exit 1. Users and automation have no way to diagnose failures. (main.go error handling)
2. **E2E test broken.** `scripts/e2e_milestones_test.sh` hardcodes `gt-` prefix but default is now `mg-`. The entire E2E suite is non-functional.
3. **Unformatted code.** `internal/workitem/edit_test.go` fails `gofmt` check, which blocks the pre-commit hook.

### Nice-to-haves (non-blocking)

1. **README command table incomplete.** Missing `claim`, `done`, `archive`, `reap`, `schedule`, `edit` from the commands table.
2. **README directory layout incomplete.** Missing `work/pending/` and `work/archived/` directories.
3. **README project structure incomplete.** Missing `internal/event/` from the tree.
4. **E2E test for events.** `scripts/event_test.sh` likely has the same `gt-` prefix issue (not verified due to e2e test failure).
