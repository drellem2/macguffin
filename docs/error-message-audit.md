# mg CLI error-message UX audit

Tracking: mg-c835 (Daniel feedback 2026-06-18 — "when you try to claim an
already claimed mg the error message is silly. Maybe we should investigate
other error cases' ux as well").

## Problem

mg's state-transition commands (`claim`, `done`, `unclaim`, `reopen`,
`shelve`, `unshelve`) are implemented as maildir-style `rename(2)` moves
between `work/<status>/` directories. When a move fails, the raw
`*os.LinkError` is wrapped with `%w` and bubbles all the way to the user.
The result leaks internal layout, the rename op, PID-suffix locking, and a
raw `ENOENT`, while *not* naming the actual semantic problem:

```
$ mg claim mg-4dbe          # second time
Error: claiming mg-4dbe: rename /Users/daniel/.macguffin/work/available/mg-4dbe.md /Users/daniel/.macguffin/work/claimed/mg-4dbe.md.83968: no such file or directory
```

Four things wrong: (1) exposes `.macguffin/work/available|claimed/`,
(2) exposes the rename + PID-suffix lock, (3) surfaces `ENOENT` which is
true but not actionable, (4) never says "already claimed by PID X".

## Style standard (adopted by this change)

```
<id>: <human-readable problem>. <optional next-step hint>.
```

- The CLI's top-level handler (`cmd/mg/main.go`) already prepends `Error: `,
  so messages start with the bare `<id>:`.
- `<id>` is the work-item ID the user typed — the most useful anchor.
- State the *semantic* problem ("already claimed", "not claimed yet",
  "already done"), never the filesystem mechanic.
- Add a next-step hint pointing at the command that fixes it, when one
  exists (`mg unclaim …`, `mg reopen …`, `mg unshelve …`, `mg show …`).
- For genuine OS failures (permission denied, disk full) surface the
  underlying cause text **only** — never the operation name or paths. A
  helper (`fsErrText`) strips the `*os.LinkError`/`*fs.PathError` wrapper
  down to its `errno` string.

Two small helpers (`internal/workitem/errors.go`) back this:

- `statusWithPID(root, id)` — reports an item's actual lifecycle status (and
  the claim-holder PID, when claimed) so each command can diagnose *where the
  item really is* before composing a message.
- `remediation(status, id)` — the canonical next-step hint for an item now
  sitting in `status`, so hints stay consistent across commands.

## Audit: every user-triggerable error path

Bar for inclusion: an error a normal user (Daniel, polecats, crew agents)
can trip during routine workflows. Leak severity: **HIGH** = exposes
paths/rename/ENOENT, **MED** = exposes an internal dir name (e.g.
`claimed/`), **OK** = already user-readable.

### `mg claim <id>`

| Case | Current (verbatim) | Leak | Proposed |
|---|---|---|---|
| already claimed | `claiming <id>: rename …/available/<id>.md …/claimed/<id>.md.<pid>: no such file or directory` | HIGH | `<id>: already claimed (by PID <pid>). Run 'mg unclaim <id>' to release it, or 'mg show <id>' to inspect.` |
| no such id | (same rename leak) | HIGH | `<id>: no such work item.` |
| already done | (same rename leak) | HIGH | `<id>: already done. Run 'mg reopen <id>' to move it back to claimed.` |
| pending (unmet deps) | (same rename leak) | HIGH | `<id>: not available yet — it is waiting on unmet dependencies. Run 'mg show <id>' to see its unmet dependencies.` |
| shelved | (same rename leak) | HIGH | `<id>: is shelved. Run 'mg unshelve <id>' to restore it.` |
| archived | (same rename leak) | HIGH | `<id>: is archived. Run 'mg unarchive <id>' to restore it.` |
| OS error (perm/disk) | (rename leak w/ paths) | HIGH | `<id>: could not be claimed: <errno text>.` |

### `mg done <id>`

| Case | Current (verbatim) | Leak | Proposed |
|---|---|---|---|
| not claimed (available) | `work item <id> not found in claimed/` | MED | `<id>: not claimed, so it cannot be completed. Run 'mg claim <id>' to claim it.` |
| not claimed (pending) | `work item <id> not found in claimed/` | MED | `<id>: not claimed — it is still pending on dependencies. Run 'mg show <id>' to see its unmet dependencies.` |
| already done | `work item <id> not found in claimed/` | MED | `<id>: already done. Run 'mg reopen <id>' to move it back to claimed.` |
| no such id | `work item <id> not found in claimed/` | MED | `<id>: no such work item.` |
| shelved / archived | `work item <id> not found in claimed/` | MED | `<id>: is shelved/archived, not claimed. Run 'mg unshelve/unarchive <id>' …` |
| OS error on move | `completing <id>: rename …: <errno>` | HIGH | `<id>: could not be completed: <errno text>.` |

### `mg unclaim <id>`

| Case | Current (verbatim) | Leak | Proposed |
|---|---|---|---|
| not claimed (available) | `work item <id> not found in claimed/` | MED | `<id>: not claimed, so there is nothing to release.` |
| no such id | `work item <id> not found in claimed/` | MED | `<id>: no such work item.` |
| done / shelved / archived | `work item <id> not found in claimed/` | MED | `<id>: already done / is shelved / is archived, not claimed. <hint>` |
| OS error on move | `releasing claim on <id>: rename …: <errno>` | HIGH | `<id>: could not release claim: <errno text>.` |

### `mg reopen <id>`

| Case | Current (verbatim) | Leak | Proposed |
|---|---|---|---|
| not done (available) | `work item <id> not found in done/` | MED | `<id>: not done — it is available, so there is nothing to reopen.` |
| not done (claimed) | `work item <id> not found in done/` | MED | `<id>: not done — it is already claimed (in progress).` |
| no such id | `work item <id> not found in done/` | MED | `<id>: no such work item.` |
| OS error on move | `reopening <id>: rename …: <errno>` | HIGH | `<id>: could not be reopened: <errno text>.` |

### `mg shelve <id>`

| Case | Current (verbatim) | Leak | Proposed |
|---|---|---|---|
| no such id | `work item <id> not found` | OK-ish | `<id>: no such work item.` (standardized) |
| already shelved | `work item <id> is already shelved` | OK | unchanged |
| cannot shelve (done) | `cannot shelve <id>: item is done` | OK | unchanged |
| OS error on move | `shelving <id>: rename …: <errno>` | HIGH | `<id>: could not be shelved: <errno text>.` |
| no item for `--tag` | `no items found with tag "x"` | OK | unchanged |

### `mg unshelve <id>`

| Case | Current (verbatim) | Leak | Proposed |
|---|---|---|---|
| not shelved | `work item <id> not found in shelved/` | MED | `<id>: is not shelved (status: <status>), so there is nothing to unshelve.` |
| no such id | `work item <id> not found in shelved/` | MED | `<id>: no such work item.` |
| OS error on move | `unshelving <id>: rename …: <errno>` | HIGH | `<id>: could not be unshelved: <errno text>.` |

### `mg show <id>` / `mg edit <id>` / `mg assign <id>`

| Case | Current (verbatim) | Leak | Proposed |
|---|---|---|---|
| no such id | `work item <id> not found` | OK-ish | `<id>: no such work item.` (standardized via `Read`/`FindPath`) |
| edit, OS error writing | `writing work item: <path>: <errno>` | HIGH | `<id>: could not be saved: <errno text>.` |
| edit, no fields | `no fields specified; use --title …` | OK | unchanged (validation) |
| edit, bad priority/budget | `invalid priority "x" …` | OK | unchanged (validation) |

### `mg new …`

| Case | Current (verbatim) | Leak | Proposed |
|---|---|---|---|
| OS error writing file | `writing work item: <path>: <errno>` | HIGH | `could not create work item: <errno text>.` |
| missing title | `title is required (use --title …)` | OK | unchanged |
| bad priority/budget/prefix | `invalid priority "x" …` etc. | OK | unchanged (validation) |
| `--title` + positional | `cannot use both --title flag and …` | OK | unchanged |

### `mg unarchive <id>`

| Case | Current (verbatim) | Leak | Proposed |
|---|---|---|---|
| no such id | `work item <id> not found` | OK-ish | `<id>: no such work item.` (standardized) |
| not archived | `cannot unarchive <id>: item is <s>, not archived` | OK | unchanged |
| OS error on move | `unarchiving <id>: rename …: <errno>` | HIGH | `<id>: could not be unarchived: <errno text>.` |

### `mg mail send <agent>`

| Case | Current (verbatim) | Leak | Proposed |
|---|---|---|---|
| missing flag | `--from, --subject, and --body are required` | OK | unchanged (validation) |
| OS error writing tmp | `writing to tmp: <path>: <errno>` | HIGH | `could not deliver message to <agent>: <errno text>.` |
| OS error tmp→new | `atomic move tmp→new: rename …: <errno>` | HIGH | `could not deliver message to <agent>: <errno text>.` |

### `mg mail read|archive <agent>/<id>`

| Case | Current (verbatim) | Leak | Proposed |
|---|---|---|---|
| bad format | `expected AGENT/MSG-ID format, got "x"` | OK | unchanged |
| not found | `message "x" not found …` | OK | unchanged |

## Out of scope (documented, intentionally not changed)

These are internal-invariant failures, not reachable through a normal user
workflow, so they are listed for completeness but left as-is (changing them
would not improve any routine path):

- `workitem.Schedule` — `promoting <id>: rename …` (auto-promotion during
  `done`; only fails on a concurrent fs fault mid-promote).
- `workitem.Archive` — `archiving <id>: rename …` (the `mg` CLI has no
  `archive` subcommand that drives `workitem.Archive`; it is invoked by
  tooling/cron sweeps, not interactively).
- `reading <dir>/: …` scan errors across the package — these only fire when
  the workspace tree is missing/corrupt (run `mg init`), an operator-level
  condition, not a routine per-item error.

## Phase 2 implementation summary

- New `internal/workitem/errors.go`: `statusWithPID`, `remediation`,
  `withHint`, `fsErrText`, and per-command diagnostics
  (`explainClaimFailure`, `explainDoneFailure`, `explainUnclaimFailure`,
  `explainReopenFailure`, `explainUnshelveFailure`).
- `claim/done/unclaim/reopen/shelve/unshelve.go`: on a failed move, run the
  diagnosis (for `ENOENT`) or `fsErrText`-sanitize (for real OS errors).
- `FindPath`/`Read`/`Status` not-found message standardized to
  `<id>: no such work item`.
- `Create` and `mail.Send` write/rename failures sanitized via `fsErrText`.
- Table-driven tests covering the top-5 hit commands (claim, done, show,
  new, mail) plus unclaim/reopen/unshelve, asserting no path/rename/ENOENT
  leaks remain.
</content>
</invoke>
