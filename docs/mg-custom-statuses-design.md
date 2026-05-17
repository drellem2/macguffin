# Custom Statuses in mg — Design & Recommendation

**Status:** design / recommendation. Not implemented.
**Origin:** mg-1389 (Daniel directive 2026-05-17 16:32 BST via reminders). Architect deliverable per [[feedback_dont_park_implementation_on_daniel]].
**Author:** architect.
**Sibling docs:** `docs/mg-flow-redesign.md` (`mg flow` consumes status data), `pogo/docs/product-manager-design.md` (PM ownership model — orthogonal). Forward dep: drellem2/macguffin#4 (`mg unarchive` / `mg edit --status`).

## TL;DR

Custom statuses are **refinements** of the existing five default statuses
(`available`, `claimed`, `done`, `shelved`, `archived`) — not replacements,
not peers, not a new top-level lifecycle. Each custom status is parented to
exactly one default; the item physically lives in the default's directory tree
and carries a `status: <custom>` frontmatter field as a refinement.

Daniel's invariant — *"defaults don't get screwed with"* — is preserved by
treating defaults as **reserved, hardcoded**, and giving custom statuses no
filesystem footprint of their own.

**Recommendations:**

1. **Schema:** custom statuses declared in `~/.macguffin/config.toml`, each
   `parent`-ed to one default. `parent` is mandatory; orphan customs not
   allowed.
2. **Default-protection:** reserved-name validation at config load (default
   names cannot appear in `[[status]]` blocks). All existing subcommands
   (`mg claim`, `mg done`, `mg shelve`, `mg archive`, `mg reopen`,
   `mg unshelve`) ignore custom statuses entirely.
3. **State machine:** no declared transitions in v1. Free movement among
   customs sharing a parent; defaults retain their existing transition
   semantics; cross-parent custom moves implicitly retitle the parent.
4. **CLI:** `mg edit --status=<name>` is the single setter (covers both
   defaults and customs). `mg list --status=<name>` filters by parent (with
   children included) or custom (exact). `mg statuses` lists; `mg statuses add`
   is the ergonomic config editor.
5. **Migration:** none in v1. Existing tag-as-status patterns keep working.
   Ship a `mg migrate-tag-to-status` helper only when demand appears.
6. **Events:** add a single generic `work.status_change{old, new, actor, pid}`
   event emitted for *every* status transition (including defaults).
   `work.claim`/`work.done`/etc. stay (backwards-compat) and emit alongside.

**Cost estimate:** ~150–250 LOC across `internal/status/` (new package — load
config, validate, list, parent-resolution) + ~50 LOC in
`internal/cmd/{edit,list,statuses}.go` + reserved-name table + one new event
type. No migration, no filesystem reshuffling, no breaking change to defaults.

**Why this shape:** the "parent-default" model is the load-bearing
simplification. It means a custom status is *an attribute*, not *a new
location*. Every existing query, filter, and tool that assumes the five
defaults continues to work — it sees the parent. New queries that want the
finer granularity opt in via the custom name. The whole feature is additive.

---

## 1 · Background

Today's mg lifecycle is the five defaults: `available` (Scheduled),
`claimed` (In Progress), `done` (Done), `shelved` (Cancelled),
`archived` (post-done). When users want richer states — *"Scheduled Q3"*,
*"Blocked on architecture"*, *"In code review"* — they encode them as **tags**
(`scheduled-q3`, `blocked-on-arch`, `in-review`). The pogo
project-status-tag-convention (2026-05-15) formalized this workaround.

The pattern is structurally wrong:

- Tags are unordered, multi-valued; status is single-valued, lifecycle-ordered.
- `mg list --tag=blocked-on-arch` and `mg list --status=available` are
  different operations; the second runs faster, has hooks, has events, has
  history. The first does not.
- "Show me everything Scheduled Q3" via `--tag=scheduled-q3` mixes orthogonal
  concerns (themes vs lifecycle position) into one filter axis.

Daniel's directive (2026-05-17 16:32 BST): *"Tags are being used as status
which is temporary. Better solution is maybe to allow custom statuses as long
as defaults don't get screwed with."* This design proposes the concrete shape.

---

## 2 · Schema — `[[status]]` blocks in `~/.macguffin/config.toml`

Custom statuses are declared in the existing per-user macguffin config:

```toml
# ~/.macguffin/config.toml

[[status]]
name    = "blocked-on-arch"
parent  = "available"          # MANDATORY — must be one of the 5 defaults
display = "Blocked: awaiting architecture review"

[[status]]
name    = "scheduled-q3"
parent  = "available"
display = "Scheduled for Q3"

[[status]]
name    = "in-review"
parent  = "claimed"
display = "In code review"

[[status]]
name    = "wontfix-revisit"
parent  = "shelved"
display = "Wontfix — revisit next quarter"
```

**Field rules:**

- `name`: lowercase, kebab-case, ≤32 chars. Must not match a default. Must be
  unique across all `[[status]]` blocks.
- `parent`: must be exactly one of `available`, `claimed`, `done`, `shelved`,
  `archived`. Mandatory — orphan customs are not allowed because every item
  must have a "real" lifecycle position.
- `display`: optional human-readable form for `mg statuses` and pretty-printers.
  Defaults to `name`.

**Why per-user config** (vs per-project `.mg/statuses.toml`): a macguffin
workspace is per-user; multiple projects share the same workspace. Per-project
declarations would collide and confuse cross-project queries. If multi-tenancy
arrives (see mg-5d8a design), per-tenant statuses become a natural extension —
out of scope here.

**Why mandatory `parent`** (vs the "free-text-opaque" alternative): without a
parent, custom statuses become a parallel lifecycle that competing subsystems
(refinery, mg flow, schedule promote) would all need to learn. With a parent,
those subsystems keep operating on the five defaults and treat the custom name
as a passenger. This is the single biggest reason for the design to take this
shape.

---

## 3 · Default-protection mechanism

Three layers, in order of strictness:

1. **Reserved-name validation at config load.** A hardcoded
   `var ReservedStatuses = [...]string{"available","claimed","done","shelved","archived"}`
   in `internal/status/`. If any `[[status]] name = "<reserved>"` appears in
   config, load fails with `error: reserved status name "claimed" cannot be
   redeclared (file:line)`.
2. **No subcommand overlap.** `mg claim`, `mg done`, `mg shelve`, `mg archive`,
   `mg reopen`, `mg unshelve` operate exclusively on default statuses. Setting
   a custom status uses `mg edit --status=<name>` only (see §5). This means
   the existing CLI verbs and their event types are untouched.
3. **No filesystem footprint.** Custom statuses do **not** create new
   directories under `~/.macguffin/`. Items physically live in
   `available/`, `claimed/`, `done/`, `shelved/`, `archived/` based on their
   *parent* default. The custom-status string is just the `status:` frontmatter
   field. This is the simplification that makes the feature compatible with
   every existing tool that walks the workspace.

If a user manually edits a frontmatter `status:` field to an unknown value
(not a default and not in config), mg's behaviour: warn at read time, treat
the item as if its status were the parent of the closest declared custom, or
fall back to `available` if no parent can be inferred. Specifically: log
`warning: item mg-XXXX has unknown status "foo"; treating as parent
"available"`. Strict mode (`strict_statuses = true` in config) upgrades this
to an error.

---

## 4 · State machine semantics

**v1: no declared transitions among custom statuses.** Free movement.

Rationale: Daniel's directive is minimal — *"allow custom statuses."* Adding a
transition DSL on top is overbuild for v1. Users own the discipline of
moving items between custom states they declared. The cost of forbidden
transitions in practice is low; the cost of declaring transitions is real
config + a validator + error surfaces.

**Default transitions are unchanged.** The existing semantics — you cannot
`mg claim` an `archived` item, you cannot `mg done` an `available` item
without claiming it first, etc. — apply exactly as today, on the *parent*
default. Setting `--status=blocked-on-arch` (parent: `available`) on a
`claimed` item moves the item to `available/` and clears the claim file
(matching today's `mg unclaim` behaviour) — because the parent transition
itself is a default-state move.

**Cross-parent custom transitions:** allowed. Setting `--status=in-review`
(parent: `claimed`) on an `available`-parented item physically moves the item
to `claimed/` (matching today's `mg claim` behaviour, *without* a claim sidecar
write — claim semantics stay tied to `mg claim` only). This is the one subtle
case; it gets a §6 test case.

If declared transitions become valuable later (v1.1 or v2), the natural
extension is `[[status]] allowed_from = ["scheduled-q3"]` — additive and
opt-in. Defer until there's evidence of need.

---

## 5 · CLI surface

### Setting status

- **`mg edit --status=<name>`** is the canonical setter.
  - Accepts any default (`available`, `claimed`, `done`, `shelved`, `archived`)
    or any custom from config. Unknown names are rejected.
  - Default → default transitions go through the same code path as `mg claim`,
    `mg done`, etc. (sharing the move + sidecar logic), so they emit the same
    events as today plus a new `work.status_change` (§7).
  - Default → custom (matching parent): no physical move; updates
    frontmatter + emits `work.status_change`.
  - Cross-parent custom: physical move per §4 + frontmatter update + event.
- **`mg claim`, `mg done`, `mg shelve`, `mg archive`, `mg reopen`,
  `mg unshelve`** continue to do exactly what they do today. They are
  shortcuts; the underlying op is `mg edit --status=<corresponding-default>`
  plus their specific side effects (claim sidecar, archive backfill, etc.).
- **No per-custom-status subcommands** (`mg block-on-arch <id>` etc.). Avoids
  command-name explosion; one general setter is enough.

### Querying

- **`mg list --status=<name>`**
  - `--status=available`: returns items in `available/` (which by §3 includes
    every item whose custom status has `parent="available"`). This is the
    backwards-compatible behaviour; existing callers get a superset.
  - `--status=blocked-on-arch`: returns only items whose `status:` frontmatter
    is exactly `blocked-on-arch`.
  - `--status=available --exact`: returns *only* items whose `status:` is
    literally `available` (i.e. no custom refinement). New flag, opt-in;
    addresses the "I want the raw default, not the parent rollup" query.
- **`mg list --status=<default>/<custom>`** (e.g. `available/blocked-on-arch`):
  alternative explicit form; equivalent to `--status=blocked-on-arch` but
  self-documenting in scripts.

### Managing the catalog

- **`mg statuses`** — list all statuses grouped by parent:
  ```
  available (default)
    blocked-on-arch    Blocked: awaiting architecture review
    scheduled-q3       Scheduled for Q3
  claimed (default)
    in-review          In code review
  done (default)       — no custom statuses
  shelved (default)
    wontfix-revisit    Wontfix — revisit next quarter
  archived (default)   — no custom statuses
  ```
- **`mg statuses add <name> --parent=<default> [--display="..."]`** —
  ergonomic config editor. Validates, appends a `[[status]]` block to
  `~/.macguffin/config.toml`, reloads.
- **`mg statuses rm <name>`** — removes from config. Refuses if any item
  currently has that status (count surfaced); `--force` reassigns affected
  items to the parent default before removal.
- **`mg statuses rename <old> <new>`** — atomic rename; rewrites all affected
  item frontmatter under a single git snapshot.

These three management verbs are sugar for editing `~/.macguffin/config.toml`
directly + bulk frontmatter updates. They could ship in v1.1 if v1 needs to
fit in a small change.

---

## 6 · Migration story

**v1: nothing automatic.** Existing tag-as-status patterns (`scheduled-q3`
as a tag) keep working — tags are still tags. The feature is additive: users
opt in by declaring `[[status]]` blocks and setting `--status=<name>` on the
items they want refined. The old tag-based filter `mg list --tag=scheduled-q3`
keeps returning the historical items; new items get the status field.

**v1.1: ship `mg migrate-tag-to-status <tag> --to-status=<name>` when demand
appears.** Bulk operation: scans every item carrying `<tag>`, runs
`mg edit --status=<name>` on each, removes the tag. Single git snapshot for
the whole batch.

Why defer: Daniel's directive didn't ask for migration. The pogo
project-status-tag-convention is recent (2026-05-15) and there's not yet a
large pile of tag-encoded statuses to migrate. Shipping the migrator
prematurely commits to a tag↔status mapping that may not match what users
actually want. Ship the feature, let usage patterns settle, then offer the
helper.

Special case worth flagging: items currently using tag-as-status are in
multiple PMs' queues. A PM-initiated migration (pm-pogo, pm-onethird) should
run via the helper *and* file follow-up tickets to update the relevant PM
sweep procedures. Mention this in the v1.1 ticket body when filed.

---

## 7 · Event taxonomy

Today's status events are subcommand-named: `work.claim`, `work.done`,
`work.shelve`, `work.archive`, `work.unshelve`, `work.reopen`. This taxonomy
doesn't scale — adding `work.status.blocked-on-arch` for every custom is a
non-starter.

**Add a single generic event: `work.status_change`** with payload:

```json
{
  "id": "mg-XXXX",
  "old": "available",
  "new": "blocked-on-arch",
  "actor": "architect",
  "pid": 85226,
  "ts": "2026-05-17T17:45:23Z"
}
```

Emitted for **every** status transition, including default↔default ones (so
`mg claim` now emits *both* `work.claim` and `work.status_change`).

**Why both:** consumers of `work.claim` (refinery, mg flow, anything
downstream) keep working unchanged. Consumers that want generic status
visibility (a new "status timeline" report, the proposed `mg flow
--group-by status-history`) subscribe to `work.status_change`. Over time, if
the per-status events prove redundant, deprecation can happen — but not in v1.

The reverse mapping (`work.status_change → work.claim`-style sugar) is
trivial to implement in `internal/event/emit.go` — emit both from the same
call site.

---

## 8 · Open choices for Daniel

These are the only unresolved points; everything else above is recommendation
+ rationale:

1. **Default protection: error vs warn on unknown frontmatter status.**
   §3 recommends warn-and-fallback by default, error in `strict_statuses =
   true` mode. Daniel — confirm warn-as-default? (vs error-as-default with
   `strict_statuses = false` to opt out.)
2. **Management subcommands `mg statuses {add,rm,rename}` — v1 or v1.1?**
   The feature is functional without them (users edit config.toml by hand).
   §5 leaves room for either. Recommend v1 because they're tiny and the
   editing workflow is otherwise rough — but I'll defer.
3. **Migration helper `mg migrate-tag-to-status` — v1 or v1.1?**
   §6 recommends v1.1 (wait for demand). Daniel may have stronger view if
   he plans to drive a migration himself early.

## 9 · Routing & rollout

Per `feedback_design_vs_exec_routing`:

- **Architect (this doc):** design complete; pending Daniel's §8 picks.
- **Polecat-executed once §8 lands** — mayor dispatches against the
  macguffin repo:
  1. `internal/status/` package — config parsing, validation, reserved-name
     check, parent resolution. With table-driven tests.
  2. `cmd/mg/edit.go` extension — `--status` flag handling routed through
     the new package; default cross-parent moves.
  3. `cmd/mg/list.go` extension — `--status=<custom>` filter; `--exact` flag.
  4. `cmd/mg/statuses.go` — new subcommand (`list` default + `add` / `rm` /
     `rename` if §8.2 picks v1).
  5. `internal/event/` extension — emit `work.status_change` alongside the
     existing per-verb events.
  6. Optional: `cmd/mg/migrate.go` if §8.3 picks v1.
- **PM-side roll-in:** pogo project-status-tag-convention (2026-05-15) gets
  superseded notes once landed. PM agents may start declaring + using
  custom statuses. Separate follow-up ticket on pm-pogo, not blocking impl.

**References:** mg-1389 (this directive). drellem2/macguffin#4 (sibling
`mg unarchive` / `mg edit --status` impl — explicit forward dep; that ticket
should land *after* this design picks, so its `mg edit --status` work uses
this design's CLI surface). `mg-7e5e` (Claniel — concrete tag-as-status use
case driving demand). pogo project-status-tag-convention 2026-05-15 (the
workaround this supersedes when landed).
