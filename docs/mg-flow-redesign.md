# `mg flow` Grouping — Rethink & Recommendation

**Status:** design / recommendation. Not implemented.
**Origin:** mg-69ba. **Author:** architect.
**Sibling docs:** `pogo/docs/product-manager-design.md` (settled what "product line" means), `pogo/docs/spend-tracking-design.md` (mg-d66b — flow consumes spend data later).

## TL;DR

Don't replace per-status flow — **extend it.** Add a `--group-by {status,repo,tag,tag:<value>,assignee,priority,age}` flag. Default stays `status` (it has narrow but real signal — workflow leaks, claim leaks, pending bloat). Real-product bottlenecks emerge under `--group-by repo` or `--group-by tag`. Bottleneck math (worst median-age vs throughput) is grouping-agnostic; runs in any mode unchanged. Add an `--age-distribution` cross-cut for the clog signal, which is the most actionable bottleneck readout regardless of grouping.

**No mg schema changes.** The PM design (mg-69cb) settled that "product = (repos, tags)" lives in PM config, not as a new mg field. mg flow respects that: PMs run `mg flow --group-by tag:<theme> --repo=<repo>` to get product-scoped views. mg core stays generic.

**Cost:** ~80 LOC extension in `internal/flow/` + ~20 LOC flag wiring in `cmd/mg/flow.go`. No migration. Existing `mg flow` (mg-23b0) keeps its current default behavior.

---

## 1 · Is per-status flow useless, or just incomplete?

**Incomplete, not useless.** Per-status has narrow but real signal:

| Signal | What per-status surfaces |
|---|---|
| **Claim leak** | Items pile up in `claimed` → polecat or crew got stuck / crashed / forgot to mark `done`. Indistinguishable from "real work in progress" without the age dimension, which the existing flow already includes. |
| **Pending bloat** | `pending` count growing → dependency graph isn't unblocking; either upstream items are stuck, or auto-promote (mg-c24f) is broken. |
| **Done → archive lag** | `done` aging → human (i.e., me/Daniel) hasn't archived; not a workflow problem, but a tidiness one. |
| **Available pile-up** | `available` aging without claims → no agent is picking work; spawn pressure too low or queue too cold. |

What per-status **doesn't** surface: which **product or theme** is jammed. Every workspace funnels through the same statuses, so "claimed has 5 stuck items" has no product-line meaning. The bottleneck synthesis ("worst median-age vs throughput status") is the weakest part — `claimed` almost always wins because claimed is where work *happens*. That's not a bottleneck, it's just where time goes.

So: keep per-status, downgrade the bottleneck-synthesis claim slightly, and add real product-level grouping for the macro view.

---

## 2 · Tag-based flow — pros & cons

### Pros

- **Maps to product lines / themes.** "`lean` tag has 3 stuck items" is actionable; "claimed has 3 stuck" is not.
- **Cheap.** Tags already exist on mg items. No schema change.
- **Plays well with the PM design.** PMs configure `tags_any=[...]`; `mg flow --group-by tag:<my-tag>` is exactly the view a PM wants for its product.
- **Cross-repo themes.** `ux` or `infra` cuts across repos; tag-grouping captures that.

### Cons

- **Tag discipline varies.** Daniel's tags drift between coarse (`pogo, macguffin, ux` on one item) and specific (`lean, one-third, case3, path-a, gap`). A naive per-tag breakdown produces a long, noisy list.
- **Multi-tag double-count.** An item with `[pogo, ux, refinery]` shows up in three rows. Acceptable for `--group-by tag` if we label the row clearly ("counts items with this tag — items with multiple tags appear in multiple rows"); not acceptable as a default mode.
- **Taxonomy drift.** `lean` becomes `lean,case3,path-a` over time; flow comparisons across time get noisy.
- **No canonical "primary" tag.** `[pogo, ux, refinery]` doesn't have a primary; picking the first alphabetically or by insertion is arbitrary.

### Recommendation: support both ways

1. **`--group-by tag`** — one row per distinct tag, item-multi-counts allowed but labeled. Useful for "what theme is jammed."
2. **`--group-by tag:<value>`** — filter to items containing `<value>` and group by status (or by another axis). Useful for "what's the flow state of my one product."
3. **Don't pick a "primary tag" automatically.** Picking arbitrarily makes lies. Force the operator (or the PM config) to choose `--group-by tag:<value>` if they want a single-product view.

---

## 3 · Project-specific axes

The PM design (mg-69cb) **already settled** this question: product = (repos, tags) in PM config; no `product` field on mg items.

For `mg flow`, that translates to:

- **`--group-by repo`** — most stable axis. `repo` is structurally pinned at item creation; no drift. Best default for cross-product overview.
- **`--group-by tag` / `--group-by tag:<value>`** — themes within or across repos. Subject to tagging discipline.
- **PMs use `mg flow --repo=<their-repo> --group-by tag:<theme>`** to get the exact view they need. Composed from primitives, not a new specialized mode.

**Not recommended:** a `product` field. (1) mg-69cb explicitly rejected it. (2) It would push product-line semantics into a generic tool that other workspaces use differently. (3) `repo` + `tags` already give 95% of the signal; the extra cost isn't worth a new schema field.

---

## 4 · Alternative axes worth shipping

Single design choice: ship `--group-by` with these values, all backed by the same metric pipeline:

| Group-by value | Surfaces | Notes |
|---|---|---|
| `status` (default) | workflow leaks, claim/pending/done bloat | current mg flow behavior, unchanged |
| `repo` | cross-product overview | most stable; recommended for product macro view |
| `tag` | theme-level aggregation | items with N tags counted N times; label clearly |
| `tag:<value>` | one product / one theme | filter then sub-group by status |
| `assignee` | who's stuck (or who's a bottleneck) | mostly useful when >1 human OR human-vs-polecat split matters |
| `priority` | "are high-priority items actually moving faster?" | sanity check on prioritization |
| `age` | clog detection by bucket | recommend the standard `<24h, 24h–7d, 7d–30d, >30d` buckets |

`age` is the hidden gem — see §5.

### Why one flag, not seven separate flags

- One mental model: "group by what?" — answer is one string.
- Composable with `--repo` filter as today.
- Easy to extend with `tag:<value>` syntax.
- `mg flow --group-by repo` reads naturally from the CLI.

---

## 5 · Bottleneck synthesis — re-examined

The current synthesis ("worst median-age vs throughput in any single status") is grouping-agnostic — feed it any partition and it picks a worst row. Under tag-based grouping, the worst row is "the tag whose median age is high relative to its throughput." Under repo, same. Math doesn't change.

**But** the synthesis is most actionable under **age**, not under any of the others. The real clog signal is:

```
Items >30d old: 3
Items 7d–30d:  8
Items 24h–7d:  12
Items <24h:    21
```

That's a histogram a human reads in 1 second and points to the right bin. Status-grouping or tag-grouping require the operator to translate "claimed has high median age" into "something is stuck"; age-grouping just shows you what's stuck.

### Recommendation

1. **Keep the existing bottleneck-synthesis logic** — it's grouping-agnostic and harmless. Run it under whatever `--group-by` the operator picks.
2. **Add `--age-distribution` as a cross-cut**, always available, computed from the item set under whatever `--repo`/`--group-by` filter is active. Print it as a small histogram below the main table.
3. **Soften the bottleneck claim in the output.** Instead of "🔥 BOTTLENECK: claimed", say "highest median-age-to-throughput ratio: claimed". Less alarmist, more honest about what the metric measures.

---

## 6 · Migration story

(b) **Keep per-status as one mode, add `--group-by` for the others. Default unchanged.**

- Current `mg flow` invocations (no flag) behave identically.
- `--group-by` is purely additive.
- Internal refactor: factor out the "compute metrics for an item partition" logic so it doesn't care whether the partition is by status, repo, tag, etc. Single grouping interface; many group-by implementations.
- Tests: existing tests stay green. Add new tests per group-by value.

Cost is small enough that "rebuild" is a worse choice. The flow code (~440 LOC across `cmd/mg/flow.go` + `internal/flow/`) is well-organized; adding a grouping layer is cleaner than a parallel command.

### What we DON'T do

- Don't add an `mg flow-tag` or `mg flow-repo` subcommand. One command, one flag.
- Don't deprecate per-status. It's still useful for the workflow-leak signals.
- Don't add a `product` field to mg items.
- Don't try to be smart about "primary tag." Arbitrary picks are lies.

---

## 7 · Coordination with mg-69cb (PM design)

Already aligned:

| Question | mg-69cb said | mg-69ba says |
|---|---|---|
| What is a "product line"? | (repos, tags) in PM config; no new mg field | Same. `--group-by repo` and `--group-by tag:<value>` are the primitives PMs compose. |
| Who decides product membership? | PM config on Daniel's box | Same. mg flow respects whatever the PM passes via flags. |
| Should mg core change? | No | No. |

PM consumption pattern (post-implementation):

```bash
# pm-pogo's morning sweep, simplified:
mg flow --repo=pogo --group-by tag:ux --age-distribution
mg flow --repo=macguffin --group-by tag:flow --age-distribution
```

Composition over specialization.

---

## 8 · Recommendation summary

1. **Add `--group-by`** to `mg flow` with values `status` (default), `repo`, `tag`, `tag:<value>`, `assignee`, `priority`, `age`.
2. **Add `--age-distribution`** as a cross-cut histogram, always computed under the active filters.
3. **Soften the bottleneck claim** in the output — name the metric, don't dramatize it.
4. **Keep per-status as default.** It's incomplete, not useless.
5. **No mg schema changes.** No `product` field. PMs compose via flags.
6. **Pure extension** of the existing flow code. ~80 LOC in `internal/flow/`, ~20 LOC in `cmd/mg/flow.go`. No migration.
7. **Don't auto-pick a "primary tag."** Force the operator to be explicit via `tag:<value>`.

---

## What's NOT in scope (v0)

- Per-day / per-hour / per-week trend lines (would need event aggregation over time, separate ticket if wanted).
- Cost dimension on flow (waits for mg-d66b spend tracking — when that lands, `mg flow --group-by tag --show=cost` becomes interesting).
- Custom user-defined groupings via expression / lambda. YAGNI.
- Renaming `mg flow`. Name's fine.
- Persistence of flow snapshots for "compare today vs last week." Defer; events.jsonl already enables this if needed.

---

## Implementation sketch (small)

```
internal/flow/
├── flow.go         (existing — refactor: extract grouping abstraction)
├── grouping.go     (NEW — implements GroupBy interface for each value)
├── render.go       (existing — minor: render arbitrary groups)
├── age.go          (NEW — age-distribution histogram)
└── flow_test.go    (existing + new cases per group-by value)
```

```go
// internal/flow/grouping.go
type Grouping interface {
    Name() string
    Key(item workitem.Item) []string  // multiple keys for multi-tag
    Order() []string                  // stable display order
}

func ParseGroupBy(s string) (Grouping, error) {
    // "status" → StatusGrouping{}
    // "repo"   → RepoGrouping{}
    // "tag"    → TagGrouping{}
    // "tag:ux" → TagFilterGrouping{value: "ux"} (single key, sub-group by status)
    // "assignee", "priority", "age"
}
```

`Compute()` takes a `Grouping` parameter; existing `Compute()` calls `Compute(StatusGrouping{})` for backward compat. No call site changes outside `flow.go`.

That's the whole design.
