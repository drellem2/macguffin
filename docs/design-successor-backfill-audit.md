# Archived `type=design` successor backfill audit

Tracking: mg-ae35 (filed by pm-pogo 2026-07-29 off mg-0418, at the mayor's
referral). Audit performed 2026-07-29.

A one-time backfill of the 8 archived `type=design` items that predate the
`mg archive` successor guard (mg-12a0). **Not** an ongoing leak: new design
items cannot silently archive bare any more. See "The guard is live" below.

## Why

An adopted design ruling with no successor is a silent liability — it reads
as finished work. `mg-0418` carried a complete design ruling delivered
2026-07-17 13:38Z and an architect ruling adopting it in full at 13:43Z. No
successor was ever filed. Twelve days later, on 2026-07-29 between 02:50 and
04:20, three agents destroyed each other's work through exactly the defect
that ruling had already solved on paper.

The shape: **the ruling completes, the ticket archives, and nothing carries
the build forward.**

## Step 1 — the guard is live (verified, not assumed)

The ticket required confirming the guard fires before treating the
population as closed. Verified on a scratch store (`MG_ROOT` pointed at a
temp dir — never against live data, since exercising a destructive guard on
the real store is itself the harm):

```
$ mg archive mg-c1a4                       # a done type=design item
Error: mg-c1a4 is a done design with no successor: archiving it would hide
the work it recommends, since an archived item cannot be the tracker for
undone work.
  → File the item that tracks the recommendation, then run
    'mg archive mg-c1a4 --successor <id>'.
$ echo $?
4
```

And the accepting path records the structured tag:

```
$ mg archive mg-c1a4 --successor mg-7326
Archived mg-c1a4: scratch design for guard check
$ mg show mg-c1a4 --json | jq .tags
["successor:mg-7326"]
```

The population is therefore **bounded and finite**. This audit closes it.

## The population

Measured, not estimated:

```
$ mg list --status=archived --json | jq -r 'select(.type=="design") | .id'
mg-0418  mg-2da4  mg-4798  mg-75f9  mg-936a  mg-b399  mg-c910  mg-e0ca
```

Name what the count is OF: it is `type == design` parsed per line from
`mg list --status=archived --json` (1679 archived items at audit time, 8 of
them design). It is **not** a text match — grepping the word "design" over
the same listing returns 71 and means nothing.

## Method

Three passes, in decreasing order of trust:

1. **Structured tag.** `successor:<id>` is what the guard records and is the
   only self-proving signal. All 8 predate the guard, so all 8 had none —
   this pass passed nobody and could not, by construction.
2. **Result sidecars.** All 8 carry a `*.result.json` with
   `"kind": "design-memo"` and an explicit `verdict` field. This settled the
   has-ruling column mechanically, without reading prose for tone. Worth
   knowing for any future audit: **the ruling is frequently in the sidecar,
   not the body** — mg-c910's body says so explicitly, noting its own
   sidecar was empty at the time and the ruling was pasted into the body to
   compensate.
3. **Text references, treated as candidates to READ.** A search cannot
   distinguish a thing from talk about the thing. Every text hit was opened.

Pass 3 earned its keep exactly once, and it was decisive. **mg-4bd4** names
`mg-4798` in its own title, so it satisfies any text search for a successor
— and it tracks nothing of it. Its body says so in as many words:
"this ticket **stands alone**" and "mg-4798's recommendation **not built**".
Had this audit accepted title matches, mg-4798 would have been scored as
covered and the ruling would have stayed buried.

## Results

| id | has ruling | has successor | action taken |
|---|---|---|---|
| mg-0418 | yes | yes — mg-f326 (done) | recorded `successor:mg-f326` |
| mg-2da4 | yes | yes — mg-158e (available) | recorded `successor:mg-158e` |
| mg-4798 | yes | **no** | **filed mg-ebb0**; recorded |
| mg-75f9 | yes | yes — mg-b8f1 + mg-345b (both archived) | recorded both |
| mg-936a | yes | yes — mg-f86c + mg-8c75 (both archived) | recorded both |
| mg-b399 | yes | yes — mg-3f1b (archived) | recorded `successor:mg-3f1b` |
| mg-c910 | yes | **no** | **filed mg-ed3f**; recorded |
| mg-e0ca | yes | yes — mg-d91f (archived) + mg-fdbc (available) | recorded both |

**8 of 8 carried a ruling. 6 already had a successor; 2 did not.**

The 6 pre-existing successors were all genuine, each naming its parent
ruling in its own body ("## Why (mg-75f9 ruling)", "Implements the
non-red-line half of the mg-e0ca design verdict", "Supersedes the framing of
mg-b399"). They were simply never *recorded* structurally, because they
predate the tag. That is now fixed: every archived design carries its
successor tag, so the archived record names its own tracker.

Bodies were not modified — verified byte-identical by hash before and after
tagging. Only tags were added, per the ticket's constraint.

Note on mg-e0ca: **mg-63c2** is a genuine descendant but a grandchild (it is
"the mg-d91f half that could not land cross-repo"), so it is recorded under
mg-d91f's lineage rather than as a direct successor.

## The two that were bare

Both rulings were re-verified as still unbuilt **today**, not inherited from
the ticket's word — a design doc with shipped code is archeology, not a plan.

### mg-4798 → filed as **mg-ebb0** (high, pogo)

Ruling: reject filtering `mg list`; adopt the assignee rule but enforce it
**in Go at the dispatch point** (`pogo agent spawn-polecat`), reusing the
predicate pogo already has; correct mayor.md's two false statements
regardless.

Verified 2026-07-29 — the ground moved, but the ruling is unbuilt:

- The predicate moved and improved. `stallwatch.go:401` is now
  `isDispatchGated` at `internal/stallwatch/stallwatch.go:475`; mg-a3a2
  generalized it into a config-driven list
  (`config.DefaultNonDispatchableAssignees = ["human", "parked"]`).
  **The successor ticket carries this correction**, so the builder does not
  chase the stale line number the verdict quotes.
- It is applied in two places, both inside stall-watch (`:235`, `:300`), and
  is **unexported** — reuse from `cmd/pogo` needs it exported or lifted.
- **No hit for the predicate anywhere in `cmd/pogo/`.** The dispatch point
  has no guard.
- Both false statements are still present verbatim: `mayor.md:19`
  ("`# Unassigned work ready to claim`") and `mayor.md:423` ("This item
  won't be dispatched to a {{.Worker}}").

So mg-a3a2 delivered the vocabulary half; the gate is enforced where work is
*watched*, not where it is *dispatched*.

### mg-c910 → filed as **mg-ed3f** (medium, pogo, PENDING DANIEL)

Ruling + architect sign-off: finish mechanism B with a loud three-way
`pogo agent prompt merge`; do not make dropins subtractive; reject
anchor/named-region/patch-dropin candidates.

**mg-c910 was the only one of the 8 with zero inbound references** — nothing
in the store mentioned it at all, let alone tracked it. It is the purest
instance of the class this audit exists for.

Verified 2026-07-29: `pogo agent prompt merge` does not exist; `pogo agent
prompt` exposes `install` only (`cmd/pogo/main.go:1596` → `:1649`). The
ruling's naming note still holds — `pogo reconcile` (`main.go:671`) is the
git-mirror subsystem, not this.

The build is **gated on Daniel** because it reverses an approved design
(mg-7488 line 115), so mg-ed3f is a decision tracker, not a dispatch. One
useful movement to record: the architect's **Condition A** (gate on mg-f86c
landing loud, or the reconcile verb inherits the silence) is **now
satisfied** — mg-f86c is merged and `cmd/pogod/main.go:1966` includes
`len(res.Conflicts)` in the log condition. Only Daniel's ruling and
Condition B (answer Q1 first, plus an upstream path) remain.

## Loose end, noted and deliberately not fixed

**mg-4671** is `available` and re-files the mechanism mg-12a0 already
shipped (commit ccef061). Same rule, filed 2026-07-29 03:51, eight days
after mg-12a0 landed it. It is live and dispatchable, so a polecat could be
sent to build a guard that has been in the tree for a week — and the mg-ae35
ticket explicitly says "do not re-file the mechanism".

Out of scope for this audit, which files successors rather than closing
duplicates. Flagged to the mayor.

## What this audit does not do

It files tickets; it does not build. None of the recommendations found were
implemented here — each successor is a separate dispatch. The 8 items were
not archived, unarchived, or otherwise edited beyond recording the successor
tag.
