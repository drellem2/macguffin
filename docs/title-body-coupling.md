# `mg edit`: the title/body coupling, settled

Origin: mg-bac6, 2026-07-30. Two agents each produced a careful, correctly-measured,
**mutually contradictory** rule for this coupling nine days apart. This document is the full
matrix, the mechanism, and the one-line rule.

## The one-line rule

> **A work item's title IS the body's first `# ` heading — there is no other copy — so when both
> are given `--title` wins and the heading is rewritten to match; when only a body is given the
> heading would silently rename the item, and `mg edit` now refuses that instead of doing it.**

## Why two correct measurements disagreed

**They did not measure the same thing, and the behaviour never changed.** The likely explanation
going in was that a both-directions guard had landed in the nine-day window. It had not:

```
$ git log -S 'strings.Contains(body, "# "+item.Title)' -- internal/workitem/
78e8038 2026-03-21 feat: add mg edit command for updating work items (mg-551d)

$ git log -S 'Extract title from first markdown heading' -- internal/workitem/
7eedf32 2026-03-20 feat: M1 work item create + read — mg new, show, list (ma-zn1)
```

The derivation and the write guard were both untouched from **2026-03-21 to 2026-07-30**. Nothing
landed in the window. The two reports ran **one arm each, on different shapes**, and each
generalised their shape's answer into a direction.

## The mechanism

Two rules, in two files, that answered the same question differently:

| | rule | where |
|---|---|---|
| **read** | the title is the **first line** with prefix `"# "` — positional | `Parse`, `internal/workitem/workitem.go` |
| **write** | prepend `# <title>` unless `strings.Contains(body, "# "+title)` — **anywhere**, as a substring, at any depth | `composeBody`, same file |

**A position-insensitive write guard in front of a position-sensitive read is the entire defect.**
It failed in *both* directions depending only on the incoming body's shape:

- The body carried `# <title>` *below* a different first heading → `Contains` was satisfied,
  nothing was prepended, and the read then took the **first** heading. The item was silently
  **retitled**. This ate the titles of mg-2ce4 and mg-0418.
- The body's heading was **reworded** → `"# "+title` no longer appeared as a substring, so the
  stored title was prepended **above** the caller's heading. Two near-identical H1s, title
  unchanged, and the body grew by two lines on an input that was smaller.

Both exited 0 and printed a success line. Worse, the success line printed the *in-memory* title —
in the retitle case, the value the write had just destroyed. **The CLI asserted a title that was
already false at the moment it printed it.**

### The nastiest cell: the defensive move disarmed the defence

2026-07-21 reported that `--title` "does not protect". That is true, and the reason is specific:
the only enforcement `--title` had was the `composeBody` prepend, and passing **the title you are
trying to preserve** guaranteed `Contains` matched the old heading still sitting below the
prepended one. So the prepend was suppressed, the read took the first heading, and the flag that
looked protective was the one ensuring the clobber. Measured, on the old binary:

```
$ mg edit mg-6ba3 --title "STORED TITLE" --body-file a.md    # a.md prepends "# NEW HEADING"
Updated mg-6ba3: STORED TITLE (body 3 → 7 lines)             # <- the title it just destroyed
$ mg show mg-6ba3 --json | jq -r .title
NEW HEADING
```

`--title` protected only when its value did *not* already appear as `# <value>` anywhere in the new
body. That is why 07-30 saw `--title` work (the value was new) and 07-21 saw it fail (the value was
the one being defended).

## The matrix, measured

Throwaway items in an isolated `$MG_ROOT`; no live ticket touched. Base item in the normal stored
shape (body's first H1 exactly equals the title). `STORED` = title on disk, `NEW` = heading in the
input file, `REAL` = a fresh `--title` value.

### Before

| shape | `--title` | title after | H1s | substance | exit |
|---|---|---|---|---|---|
| (a) prepend `# NEW` above existing H1 | — | **NEW** ← clobbered | 2 | intact | 0 |
| (a) prepend | `REAL` | REAL | **3** | intact | 0 |
| (a) prepend | `STORED` (defensive) | **NEW** ← clobbered | 2 | intact | 0 |
| (b) replace H1 in place | — | STORED (stale) | **2** | intact | 0 |
| (b) replace H1 in place | `REAL` | REAL | **2** | intact | 0 |
| (c) no `# ` heading at all | — | STORED | 1 | intact | 0 |
| (c) no `# ` heading at all | `REAL` | REAL | 1 | intact | 0 |
| (d) blockquoted `> # NEW` prepend | — | STORED | 1 | intact | 0 |
| (d) blockquoted `> # NEW` prepend | `REAL` | REAL | 2 | intact | 0 |

Shape (c) with `--title` is the only fully clean cell, and it is exactly the procedure both
observers converged on independently. Substance survived byte-for-byte in **every** cell, before
and after — only the heading region was ever affected, which is why this was so hard to see.

### After

| shape | `--title` | title after | H1s | exit |
|---|---|---|---|---|
| (a) prepend | — | STORED (unchanged) | 1 | **4 — refused** |
| (a) prepend | `REAL` | REAL | 2 (caller's own, noted on stderr) | 0 |
| (a) prepend | `STORED` (defensive) | STORED (unchanged) | 1 | **4 — refused** |
| (b) replace H1 | — | STORED (unchanged) | 1 | **4 — refused** |
| (b) replace H1 | `REAL` | REAL | 1 | 0 |
| (c) no heading | — | STORED | 1 | 0 |
| (c) no heading | `REAL` | REAL | 1 | 0 |
| (d) blockquoted prepend | — | STORED | 1 | 0 |
| (d) blockquoted prepend | `REAL` | REAL | 1 | 0 |

Every cell is now either clean or a loud refusal. No cell silently changes a field the caller did
not name.

## Shape (d): blockquoting, settled — the memory note was right

Agent memory carried "blockquoting protects a prepended heading" as UNVERIFIED-BUT-LOAD-BEARING,
inferred from five tickets nobody chose for the purpose. **It is confirmed, and for a reason rather
than by luck:** `Parse` matches `strings.HasPrefix(line, "# ")` on the *raw* line, so `> # heading`
is not a heading to it and can never become the title. `firstHeadingLine` now applies exactly the
same rule, which is what keeps the read side and the write side from drifting apart again. Keep the
note; it is load-bearing and true.

## The population: this reached real tickets

Over the 2009 stored bodies in the work store:

- **403** bodies have more than one `# ` heading. That number over-counts — many bodies
  legitimately use several H1 sections.
- **196** bodies (9.8%) carry the actual corruption signature: the first H1 is ≥70% similar to a
  later H1, i.e. a stacked near-duplicate of the title.

The deltas in that population are overwhelmingly **stripped backticks, changed quote style, and
capitalization** — a title authored through a shell (`--title="… \`pogo schedule\` …"`, where the
shell ate the backticks) against a body authored verbatim through `--body-file`. Two paths for one
sentence, differing by a character or two, and exact-substring matching turned that into a stacked
heading. That is the same one-comma delta seen in **mg-c8d5**, which makes the specimen a
representative of a 196-item population rather than an anecdote.

mg-c8d5 is **left exactly as found**, as the ticket requires: it is the evidence and the
before/after control. Nothing here repairs stored bodies.

Four *existing tests* were also unwittingly manufacturing this state: `seedWithBody` in
`cmd/mg/restorebody_test.go` ran `mg new task <title>`, and since `mg new` joins its positionals
into the title, the item was titled `task Real body` while its body led with `# Real body`. Every
run produced a stacked H1 and every assertion passed — the same failure mode as 07-21's own
post-mortem, that a probe "passes" on a shape where nothing can go wrong.

## The fix

Three layers, strongest first.

**1. The bad state is unrepresentable, not warned about.** `reconcileTitleHeading`
(`internal/workitem/titleheading.go`) is now the single place the coupling is decided, and it keys
off the body's **first heading, positionally** — the read side's own rule. Three total cases: no
heading → synthesise one; heading already says the title → leave it; heading says something else →
rewrite it **in place**. There is no input for which mg authors a second heading of its own. The
duplicate has nowhere to live.

**2. The silent side effect is refused (exit 4, `title_side_effect`).** A body edit whose first
heading differs from the current title renames the item; `Update` refuses that when the caller did
**not** pass `--title`. The refusal's remedy *is* the safe procedure, which is why it is a field and
not a `--force`: name `--title`, or hand `--body-file` a body with no leading heading. The
mg-0418 lineage asked for a guard covering **both** directions, and this covers both — the hint
offers each, because each direction was reported as "the" bug by someone who had measured only
their own.

A second door was found while testing: rewriting a differing first heading in place can land on a
title the supplied body *already* carries lower down, and then the rewrite is what authors the
duplicate. That is refused too (`duplicate_title_heading`) — but only when the write **increases**
the count. The 196 already-stacked bodies stay editable; mg does not get to refuse work because of
damage it did earlier.

**3. The instruments stopped lying.** The success line reports the title as read back from the body
just written, plus the transition when it moved (`Updated mg-1234: new (title was "old")`). A
`work.edited` event carries `title_before`/`title_after` — the one field agents search by had no
record of ever moving, which is why reconstructing this took two probes and nine days. Extra
headings below the title are counted on stderr, not refused: multi-section bodies are legitimate and
mg does not rewrite prose to satisfy a number.

`mg edit --help` states which field wins and that the other is rewritten.

## Guarding the invariant, not a literal

The tests assert a **round trip**: `Parse(Render(item)).Title == item.Title`, and the title's
heading is the first one in the stored body. That holds for any title and any body, so it does not
decay on the next legitimate change to either side. No test pins a heading count or a title string
as a constant; the CLI matrix derives its expected heading count from the body it handed over.

**Positive control (required, and it runs first).** Two CLI tests establish that the guard *can*
fail before any test shows that ordinary edits pass:
`TestCLI_TitleCoupling_PositiveControl_ReplacedHeadingIsRefused` (the 07-30 shape) and
`TestCLI_TitleCoupling_PositiveControl_PrependedHeadingIsRefused` (the 07-21 shape, including the
defensive-`--title` arm). Both assert exit 4 *and* that the refused item is left byte-identical. A
green run on well-formed input is not evidence; that is how this survived four months.

## Reproducing

`mg edit`'s coupling is exercised by `internal/workitem/titleheading_test.go` and
`cmd/mg/titlecoupling_cli_test.go`. To re-measure by hand, against a throwaway store:

```sh
export MG_ROOT=$(mktemp -d) && mg init
printf '# t\n\nsubstance\n' > base.md
ID=$(mg new --type=task --title=t --no-repo --body-file base.md | grep -o 'mg-[0-9a-f]*')
printf '# reworded\n\nsubstance\n' > new.md
mg edit "$ID" --body-file new.md            # refused: exit 4, title_side_effect
mg edit "$ID" --title=reworded --body-file new.md   # clean: one H1, title reworded
mg show "$ID" --json | jq -r .title         # read the title BACK — the only thing that reveals it
```

Reading the title back and counting H1s is the only check that ever revealed this. Do it after any
full-body rewrite.
