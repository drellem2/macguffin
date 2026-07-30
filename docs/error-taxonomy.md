# mg error taxonomy — the public contract

`mg` errors carry a **stable, machine-readable taxonomy** so scripts and agents
can branch on failures reliably. This document is the **public contract**. It is
governed exactly like the `mg ... --json` data-field contract: **frozen and
additive-only.** Exit codes, category names, and error-code slugs already
defined here never change meaning; new categories and slugs may be added.

Implemented under issue #57 (design: `~/.pogo/shared/mg-57-error-taxonomy-design.md`).
The typed error lives in `internal/mgerr`; the single rendering seam is
`cmd/mg/main.go`.

## 1. Exit codes (coarse category — FROZEN)

Every `mg` invocation exits with one of these codes. Categories map many
fine-grained error codes onto one exit code for cheap shell branching.

| Exit | Category    | Meaning |
|------|-------------|---------|
| 0    | (success)   | — |
| 1    | `internal`  | unexpected / IO / bug; the uncategorized catch-all (fs failure, marshal error, git exec failure, undeterminable home dir) |
| 2    | `usage`     | the caller misused the CLI (bad arg count, unknown flag/subcommand, mutually-exclusive flags, invalid value) |
| 3    | `not_found` | a named entity does not exist (unknown work-item ID, unknown mailbox/message, a message that exists but is unparseable) |
| 4    | `conflict`  | the entity exists but is in the wrong state for the operation (already claimed/done, unmet deps, not shelved, race-lost claim) |

New categories get **5, 6, …** — existing numbers never change.

**Backward compatibility.** Before this taxonomy every failure exited `1`.
Keeping `1 = internal` makes `2`/`3`/`4` a refinement that carves specific
categories out of the old catch-all. Any consumer testing `exit == 0` vs
`!= 0` is unaffected; only a (never-contractual) "`exit == 1` means any error"
check needs updating.

**Retryability is a field, not a code.** A race-lost claim (retryable) and a
hard conflict (not) are *both* exit `4`. Retryability is carried in the JSON
error object's `retryable` field (§3), keeping the exit table small and stable.

## 2. Error-code slugs (fine identifier — FROZEN, additive)

Stable `snake_case` machine ids, one per distinct failure meaning. Add new slugs
freely; never rename or repurpose an existing one.

| Category    | Slugs |
|-------------|-------|
| `usage`     | `usage` (generic cobra flag/arg/unknown-command), `mutually_exclusive_flags`, `invalid_value`, `missing_required`, `invalid_header_value` (a mail header value carrying CR/LF or other control characters), `empty_partition` (an `<id>@` qualifier with nothing after the `@`), `ambiguous_body_backup` (`mg restore-body --from=<prefix>` matched more than one saved body; the message lists every stamp it matched. Refused rather than resolved to a best guess — picking for the caller is how you restore the wrong version onto a body you have just destroyed, and there is no third copy), `ephemeral_repo` (`mg new` resolved a `repo` inside a tree pogo owns as ephemeral — `~/.pogo/polecats/` or `~/.pogo/refinery/worktrees/` — where the directory is deleted when the agent owning it is reaped. A work item is durable and its repo path is a promise, so an item pointing there breaks silently and only at dispatch time, long after the filer is gone. Fires on the RESOLVED path, so an explicit `--repo="$(pwd)"` is refused exactly as an omitted flag is; the hint names the repo the worktree was created from rather than substituting it. `--allow-ephemeral-repo` is the documented override), `empty_override` (`mg shelve --override=` was given with a blank or whitespace-only reason. The override is a STRING and not a boolean precisely so it carries what the operator knew that the guard did not; one satisfiable by the space bar is a boolean wearing a string's clothes, so a blank one is named as a mistake rather than silently falling through to the guard's own refusal one line later) |
| `not_found` | `no_such_item`, `no_such_message`, `no_such_mailbox`, `malformed_message` (exists but unparseable; `retryable=false`), `no_such_partition` (an `<id>@<partition>` qualifier named a partition holding no twin of the id; the hint lists the partitions where the id IS archived), `no_body_backup` (`mg restore-body` found no saved prior body for the item. It is an ERROR and not a quiet success: a recovery command that reports success when it recovered nothing is a second way to lose a body. Reachable for an ordinary reason — bodies are saved only from the first replace-mode edit after this shipped, and appends/`--title` overwrite nothing — so the hint names the directory it looked in and points at `work.edited`, which records that the loss happened but not the bytes), `body_backup_not_found` (`mg restore-body --from=<prefix>` matched none of the saved bodies; the hint names `--list`) |
| `conflict`  | `already_claimed`, `already_done`, `unmet_dependencies`, `not_claimed`, `not_done`, `not_shelved`, `item_shelved`, `item_archived`, `unknown_prior_status` (`mg unarchive` cannot establish the status the item held when it was archived — the event log has no `work.archive` record for it — and refuses rather than pick one; the hint names `--status`), `body_changed` (`mg edit --if-unchanged=<hash>` found the stored body no longer hashes to the value the caller read, so a full-body write would have destroyed a change nobody saw. `retryable=false` **on purpose**: re-running the identical command with the same now-stale hash can never succeed, and a caller that retried on it would spin. The remedy is a re-read, which the hint names — along with `--append-body-file`, which composes against what is on disk and cannot clobber at all), `claim_race` (`retryable=true`), `shelve_blocked_on_tag` (`mg shelve` refused an item tagged `blocked-on-*`; the refusal names the tag and the `mg edit --rm-tags` that discharges it), `shelve_without_successor` (`mg shelve` refused an item that DECLARES A REMAINDER — it carries `declares-remainder`, or its type is `design`/`scoping`/`audit`/`idea`, or its body's leading carrier block says `stage: triage` — and names no successor; the refusal names which of those arms fired, since they are found differently even though they are answered the same way), `shelve_dangling_successor` (the `successor:` tag names an item that no longer exists, so nothing is tracking the recommendation. Kept distinct from the bare refusal because "you pointed at a deleted item" and "you pointed at nothing" need different fixes, and collapsing them hides the fact that a link the operator believed was in place has rotted), `ambiguous_id` (the short ID names more than one item *of the same terminality*; the message lists every candidate path. A live item shadowing an archived one resolves to the live item and notes the shadowed path on stderr. Two archived twins in different partitions cannot be tie-broken by liveness — disambiguate with an `<id>@<partition>` qualifier, e.g. `mg show mg-4fa7@2026-04`, which the hint names) |
| `internal`  | `internal` (catch-all), `io_error`, `encode_error`, `id_exhausted` (`mg new` could not mint an unused short ID) |

## 3. JSON error object (FROZEN, additive)

When a command is run with `--json` and fails, it prints **one JSON object to
STDERR**, namespaced under a top-level `"error"` key:

```json
{"error":{"code":"already_claimed","category":"conflict","exit":4,"message":"mg-1234: already claimed (by PID 991).","hint":"Run 'mg unclaim mg-1234' to release it, or 'mg show mg-1234' to inspect.","retryable":false}}
```

| Field       | Notes |
|-------------|-------|
| `code`      | fine slug (§2), frozen/additive |
| `category`  | frozen category name (`internal` \| `usage` \| `not_found` \| `conflict`) |
| `exit`      | the int the process exits with (matches §1) — convenient for stderr-only consumers |
| `message`   | human problem statement (no hint). **Human-facing, NON-CONTRACTUAL** — may be reworded in any release; never parse it. |
| `hint`      | remediation; **omitted** when absent (`omitempty`). **Human-facing, NON-CONTRACTUAL** — may be reworded in any release; never parse it. |
| `retryable` | bool; **always present** for predictable parsing |

**What is frozen vs. not.** The frozen contract is the set of **field names**
(`code`, `category`, `exit`, `message`, `hint`, `retryable`), the **`code` slug
values** (§2), the **`category` names**, and the **`exit` ints** (§1). The
`message` and `hint` **text** is human-facing and **not** frozen — it may be
reworded between releases without any version bump. Programs must branch only on
`code`, `category`, and `exit`, never on `message`/`hint` wording.

**Stream separation.** A `--json` *data* command streams its data object to
**stdout** (unchanged — see the `--json` data-field contract). The error object
goes to **stderr**, under the `"error"` namespace, so the two streams never
collide: on a nonzero exit, parse stderr. Data `--json` output on stdout is
completely unchanged by this feature.

**Note.** For a pure arg-count/unknown-flag error, cobra fails before the
`--json` flag is parsed, so such errors render as human text even under
`--json`. A malformed invocation getting human text is acceptable.

## 4. Human (non-JSON) rendering

Without `--json`, errors keep the traditional feel — `Error: <message>` plus an
indented `→ <hint>` line when a hint exists:

```
Error: mg-1234: already claimed (by PID 991).
  → Run 'mg unclaim mg-1234' to release it, or 'mg show mg-1234' to inspect.
```

## 5. Consuming errors — recipes

Shell, branch on the coarse exit code:

```sh
mg claim "$id"
case $? in
  0) echo "claimed" ;;
  3) echo "no such item" ;;
  4) echo "conflict — someone else has it" ;;
  *) echo "other failure" ;;
esac
```

Agent / script, branch on the fine slug and honor retryability:

```sh
if ! err=$(mg claim "$id" --json 2>&1 >/dev/null); then
  code=$(printf '%s' "$err" | jq -r '.error.code')
  retry=$(printf '%s' "$err" | jq -r '.error.retryable')
  [ "$code" = "claim_race" ] && [ "$retry" = "true" ] && exec mg claim "$id"
fi
```

*(`mg claim` itself has no `--json` flag; the snippet illustrates the pattern
for the `--json`-capable commands such as `show`, `list`, `mail`, `spend`,
`flow`.)*

## 6. Governance

Same as the `--json` data contract: **frozen and additive-only.** To add a new
failure mode, add a new slug (and, if a genuinely new coarse class is needed, a
new exit code `≥ 5`). Never change the exit code, category name, or meaning of
anything already listed here.

**What is frozen:** exit codes (§1), category names, error-code slugs (§2), and
the JSON error-object **field names** (§3).

**What is NOT frozen:** the human-facing `message` and `hint` **text**. These may
be reworded in any release without a version bump — they are for humans, not
machines. Agents and scripts must branch **only** on `code`, `category`, and
`exit`, and must never parse `message`/`hint` wording. Accordingly the contract
tests (`cmd/mg/errtaxonomy_test.go`) assert machine fields and the structural
*framing* of the human render, but never pin `message`/`hint` sentences; the
pre-existing message-quality/sanitization checks in `cmd/mg/errmsg_test.go` and
`internal/workitem/errors_test.go` are quality tests, not contract tests. The
contract itself is enforced by `cmd/mg/errtaxonomy_test.go` and
`internal/mgerr/mgerr_test.go`.
