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
| `usage`     | `usage` (generic cobra flag/arg/unknown-command), `mutually_exclusive_flags`, `invalid_value`, `missing_required` |
| `not_found` | `no_such_item`, `no_such_message`, `no_such_mailbox`, `malformed_message` (exists but unparseable; `retryable=false`) |
| `conflict`  | `already_claimed`, `already_done`, `unmet_dependencies`, `not_claimed`, `not_done`, `not_shelved`, `item_shelved`, `item_archived`, `claim_race` (`retryable=true`) |
| `internal`  | `internal` (catch-all), `io_error`, `encode_error` |

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
| `message`   | human problem statement (no hint) |
| `hint`      | remediation; **omitted** when absent (`omitempty`) |
| `retryable` | bool; **always present** for predictable parsing |

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
anything already listed here. The contract is enforced by
`cmd/mg/errtaxonomy_test.go` and `internal/mgerr/mgerr_test.go`.
