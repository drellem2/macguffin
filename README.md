# MacGuffin

An OS-level substrate for agentic workflows, built on UNIX primitives.

MacGuffin provides work-item tracking, atomic task claiming, and inter-agent
messaging — all backed by the local filesystem. No database, no server, no
query language. Just files, directories, and `rename(2)`.

## Design Philosophy

1. **Filesystem is the database.** State lives in files. Observe it with `ls`,
   `cat`, `grep`, `watch`.
2. **Atomic operations via the kernel.** Claim and signal use `rename(2)`,
   `mkdir(2)`, and `flock` — not application-layer locking.
3. **Git for the cold path only.** Durability and audit use git, but git is
   never on the hot path. Think Maildir + IMAP: git syncs, the filesystem runs.
4. **Convention over machinery.** Structure comes from directory layout and
   naming, not a schema. The CLI is convenience, not gatekeeper — you can
   always `ls` your way to the truth.
5. **The bitter lesson.** Build the thinnest substrate. Let agents do the
   thinking.

## Installation

### Homebrew (macOS and Linux)

```bash
brew install --cask drellem2/tap/mg
```

mg is distributed as a Homebrew **cask** (a pre-compiled binary). On macOS the
cask install strips the Gatekeeper quarantine bit so the unsigned binary runs
without a security prompt.

### Shell installer

```bash
curl -sSfL https://raw.githubusercontent.com/drellem2/macguffin/main/install.sh | sh
```

Or manually:

```bash
sh install.sh                          # installs to ~/.local/bin
INSTALL_DIR=/usr/local/bin sh install.sh  # custom location
SHADOW_MG=0 sh install.sh              # skip the /usr/local/bin/mg symlink
sh install.sh --force                  # install even over a newer mg
```

Supports Linux (amd64, arm64), macOS (amd64, arm64), and FreeBSD (amd64).

The installer asks the GitHub API which release is latest, and does so
anonymously — no token needed, and none is wanted, for an install on your own
machine. The API meters anonymous callers per source IP (60/hour), which only
becomes a problem where an IP is shared with strangers: on CI runners that quota
is often already spent, and the call comes back `403` with nothing wrong on your
end. Set `GITHUB_TOKEN` (or `GH_TOKEN`) there and the request is metered against
the token instead. This repo's own `install` job does that with the default
workflow token.

The installer only ever installs **releases**, so running it on a host that
already has a newer `mg` would be a downgrade. It refuses to do that. Both
candidate binaries are checked, because they are not always the same file:
`$INSTALL_DIR/mg`, which is what gets overwritten, and whatever `command -v mg`
resolves to, which is what your shells actually run. The refusal names both
paths and both versions, so you can tell which one triggered it.

To downgrade deliberately, pass `--force` (or set `MG_FORCE=1`):

```bash
curl -sSfL https://raw.githubusercontent.com/drellem2/macguffin/main/install.sh | sh -s -- --force
```

A binary whose version cannot be read at all — an unstamped source build reports
`dev` — is *warned* about rather than refused. That is the absence of a
comparison, not evidence of a downgrade, and `build.sh` falls back to `dev` by
design when there is no tagged checkout to derive a version from.

On macOS and Linux, the installer also drops a symlink at `/usr/local/bin/mg`
pointing to the installed binary. `/usr/local/bin` precedes `/usr/bin` in the
default PATH on both systems, so this shadows `/usr/bin/mg` (the microemacs
editor on macOS) and lets `mg` resolve to MacGuffin from any shell or
subprocess. Writing to `/usr/local/bin` may require `sudo`. Set `SHADOW_MG=0`
to skip, or `SHADOW_DIR=...` to point the symlink elsewhere.

To remove:

```bash
sh uninstall.sh                        # removes binary + shadow symlink
```

Requires Go 1.24+ to build from source:

```bash
go install ./cmd/mg
```

## Quick Start

```bash
# Initialize the workspace
mg init

# Create a work item — coding example
mg new --type=bug "Auth tokens not refreshing"

# Or a non-coding work item — the type field is free-form
mg new --type=research "Audit Q3 incidents for recurring root causes"
mg new --type=draft "Outline weekly digest for stakeholders"

# List available work
mg list

# Show a specific item
mg show <id>

# Send mail to an agent. The QUOTED heredoc is the canonical form: <<'EOF'
# passes the bytes through untouched. An unquoted <<EOF expands backticks,
# $VAR and $(cmd) exactly as --body="..." does, reintroducing the bug.
# Omit --subject and the body's FIRST LINE becomes the subject, so the subject
# rides inside the heredoc too — it is echoed back on send so you can see what
# was taken.
mg mail send <agent> --from=me --body-file - <<'EOF'
Review needed: Daniel's call on Monday's park

Check the auth refactor — `go vet ./...` is clean but $BUILD_TAG is unset.
EOF

# A file works the same way
mg mail send <agent> --from=me --body-file ./msg.md

# --subject is still there when you want an explicit one. It can only be given
# inline, so it carries the shell's expansion hazard: --subject="Daniel's call
# on Monday's park" has an EVEN number of apostrophes, so the shell hands mg
# "Daniels park" and the send succeeds on it.
mg mail send <agent> --from=me --subject="Review needed" --body-file ./msg.md

# --body is the inline-only shortcut: fine when the body carries no shell
# metacharacters, silently lossy when it does
mg mail send <agent> --from=me --body="Check the auth refactor."

# Reply to a message, threading it to the original
mg mail reply <agent>/<msg-id> --body="Looks good."

# A recipient mg has never seen is REFUSED (exit 3) and the near neighbours are
# named — mail has bad addresses now. A name counts as known when a mailbox of
# that name exists, or a work item is called that.
mg mail send definitely-nobody-9ecf --from=me --body=hi
#   Error: no mailbox named "definitely-nobody-9ecf", and no work item is called
#   that either: mg has never seen this recipient

# A KNOWN recipient can still be one nobody reads: a maildir outlives its agent.
# The delivery happens and the exit is 0, but stderr says so.
mg mail send cf1e --from=me --body="are you there?"
#   Delivered: me → cf1e/new/1786516434490533000.1720.3000
#   warning: delivered, but work item mg-cf1e has been completed, so the agent
#   that read cf1e's mailbox is probably gone.

# --create is the explicit "this recipient really is new"; mg mail register does
# the same registration without sending. Both leave a durable record of who
# registered the name and when — existence alone never could, because a box is
# created by delivering to it.
mg mail send <new-agent> --create --from=me --body="First contact."
mg mail register <new-agent>

# So an unregistered box is distinguishable AFTER the fact, not just at send
# time. The enumeration marks the boxes nobody established and counts them.
mg mail list
#   daniel            26 unread  UNREGISTERED
#   666 of 1365 mailboxes are UNREGISTERED: the box exists only because mail was
#   delivered to it, and no work item is named for it either.
#   567 of those are holding mail: in use right now, with nothing recording who
#   the name belongs to.

# Registering a box already in use ADOPTS it, and says it was in use unregistered
# rather than reporting a registration that never happened.
mg mail register daniel
#   Registered existing mailbox: daniel — it was in use UNREGISTERED, with 26
#   messages already delivered; this registration vouches for the name from now
#   on, not for that mail

# A mailbox that never existed reads as missing, not as quiet
mg mail list <agent>
#   No such mailbox: bf3ad — it has never existed, so no mail has ever been delivered to it
#     Did you mean bf3ae?  (run 'mg mail list' to see every mailbox)

# Under --json, stdout is message objects and nothing else, in every state of
# the mailbox — so counting rows counts messages, and an empty box counts 0.
mg mail list <agent> --json | jq -s 'length'   # 0 for an empty box, N for N messages

# What mg has to say ABOUT the listing goes to stderr: the box's own state when
# there is nothing to list, and the sender-filter summary when a predicate is on.
mg mail list <agent> --json 2>&1 >/dev/null | jq .
#   {"mailbox":"bf3ae","unread":0,"exists":true,"registration":"registered"}
#   {"mailbox":"bf3ad","unread":0,"exists":false,"registration":"unregistered"}

# Filter by SENDER — exact match on the From field, never a substring, so it
# cannot hide a real message whose SUBJECT merely mentions the name. Both flags
# are repeatable and comma-separated.
mg mail list <agent> --exclude-from=scheduler,stall-watch
mg mail list <agent> --from=pm-pogo

# What the filter hid is always reported, so a filtered listing can never be
# mistaken for a quiet mailbox
mg mail list architect --exclude-from=scheduler
#   sender filter: --exclude-from=scheduler — 1 of 265 shown, 264 hidden
#   ● architect/1754…  pm-pogo       ratio measurement

# The filter hides the pile from ONE listing; reclaim drains it. Superseded
# copies of a recurring generator's fallback mail move to archive/, keeping the
# newest of each schedule. Selection is by the From FIELD, so a real message is
# never in range. Run --dry-run first; nothing is ever deleted.
mg mail reclaim --dry-run                      # fleet-wide, count only
mg mail reclaim architect                      # one mailbox
mg mail reclaim --from=scheduler,stall-watch --older-than=7d
#   Reclaimed mail from scheduler older than 24h0m0s, keeping the newest 1 per schedule.
#     architect                    180 reclaimed, 86 retained
#   10780 of 11974 matching message(s) reclaimed across 549 of 1381 mailbox(es); 1194 retained.
#   Nothing was deleted — reclaimed mail is in archive/ ('mg mail list AGENT --archived').

# Git snapshots (optional)
mg init --git          # enable git tracking
mg snapshot            # take a snapshot
mg log                 # view snapshot history
```

## Commands

| Command | Description |
|---------|-------------|
| `mg init [--git]` | Create the `~/.macguffin` directory tree. `--git` enables snapshot tracking. |
| `mg new` | Create a new work item (Markdown + YAML frontmatter). |
| `mg show <id> [--json] [--body-hash] [--sections]` | Display a work item by ID. `--json` emits the full item as one JSON object (adds `creator`, `body`, `body_hash`, `budget`, `spent`, `result_path`, `result` to the `list --json` field set) — `result_path`/`result` are the item's **verdict**, resolved rather than globbed for; both are `null` when it recorded none, and a non-null `result_path` with a `null` `result` means the file is there and is not valid JSON. Resolved `Successor:` / `Predecessor:` lines name a linked item's **title and current status**, or `⚠ UNRESOLVED` when the id no longer names anything — see [`mg done` naming the successor](#work-items) below. `--body-hash` prints only the body's SHA-256 — the version token `mg edit --if-unchanged` checks against — so a guarded write is one command rather than a pipe through `jq`. `--sections` prints an index of the body's **dated headings** instead of the item — see [Reading a body that has become a log](#reading-a-body-that-has-become-a-log). When two archived twins share a short ID across partitions, disambiguate with `mg show <id>@<partition>` (e.g. `mg show mg-4fa7@2026-04`). |
| `mg list [--json] [--wide]` | List work items. On a terminal each line is fitted to the terminal width: the **id, type, assignee and snooze marker are always shown**, and the **title and tags** are shortened (with a `…` marker) to make room — the fields worth keeping sit last on the line, so cutting at a column would delete exactly them. Piped or redirected output is **never** truncated, so anything parsing `mg list` sees the full line; `--wide` (alias `--no-truncate`) opts a terminal out too. `--json` emits NDJSON (one JSON object per line) for scripts and dashboards, and is unaffected by any of this. Alongside `tags` it carries the structured carriers that live *inside* them as their own fields — `successor`, `predecessor` (both always arrays, never null) and `declares_remainder` — so a check does not have to know the tag spelling to be written. |
| `mg claim ID` | Atomically claim a work item by ID. |
| `mg done ID` | Mark a claimed work item as done. **Refused** if the item declares a remainder and nothing is tracking what it recommends — see the two rows below. |
| `mg new --no-declares-remainder` | An item whose **output is a recommendation** — a triage verdict, a design, a proposal — carries a `declares-remainder` tag. Its build is undone by construction the moment it completes, so `mg done` refuses the item until a successor names what carries it forward. **mg emits the tag by default**, from the type (`--type=design`, `scoping`, `audit`, `idea`) and from a body whose leading carrier block says `stage: triage` — a triage being a workflow position rather than a type. `--no-declares-remainder` is the escape for the genuine exception; `--declares-remainder` forces it on for a type mg does not default (a task that ends in a proposal). Add it to an item that already exists with `mg edit <id> --add-tags=declares-remainder`; retract a declaration that turned out to be wrong with `--rm-tags=declares-remainder`. The default reads the type, but **the guard never does** — `mg done` fires on the tag the item carries, so a wrong default is visible in the item's own text instead of silent at completion time. |
| `mg done <id> --successor <id>` | Discharges the declaration: it records a `successor:<id>` tag on the item **before** completing it, so the completed record names its own tracker for any later reader. The id must resolve to a real item and cannot be the item itself — a pointer at nothing and a pointer at oneself both track nothing. The guard fires **only on the declaration**, never on `type` or on a body `stage:` value: those are proxies, and both were tried. `type: design` misses a triage (mg-ee98 was a `type: task` triage whose verdict was IMPLEMENT on a reproduced data-loss bug, and nothing carried the fix). "Non-terminal `stage:`" over-fires, because a stage-shaped **pause** owes nothing while a stage-shaped **gate** does, and no stage value distinguishes them — across the whole store on 2026-07-29 that predicate would have fired on 41 items, 34 already archived. An item that never declares completes exactly as it always has, so the guard fails in the safe direction. There is no `--force`: the declaration is opt-in, and retracting a wrong one is the documented correction. **A refusal never costs you the `--result`** — the sidecar is written before the guards run, beside the item where it currently sits, and travels with the item wherever it goes next, so a refusal costs a retry and the refusal says so. That ordering is load-bearing rather than tidy: on the gh-issue track the successor build ticket is not filed until after the human gate, so when a triage reports there is no id that can legally satisfy the guard, and "supply a successor or lose your work" is precisely the pressure that produces a fabricated one. **The refusal also names the move that is not a bypass.** At that same moment the agent has finished, cannot complete, and cannot name anything, so offering it only `--successor` leaves it to improvise a hold — and what it improvises is holding the *claim*, which is how five completed triages became indistinguishable from stranded work (see `mg unclaim` below). So the hint names `mg unclaim <id> --assignee=human` as well. Handing the item to whoever it waits on **discharges nothing**: it keeps its declaration and trips this guard again at the next `mg done`, which is exactly what separates it from the retraction the refusal still deliberately refuses to teach. |
| `mg done` naming the successor | Existence is the **only** thing `--successor` can check, so a real id naming the *wrong* item is a legal argument — and it used to complete with exit 0 and no output, silently gating a live item on a ticket that could never carry the work. `mg done` now prints what it linked (`Successor mg-4b01 (available): build the thing the triage recommended`) whenever the completed item carries a `successor:` tag, whether this run supplied it or an earlier `mg edit --add-tags` did; an item with no successor prints no line, because noise is how the line that matters stops being read. It is **printed, not enforced**, because both structural alternatives were measured over-firing against the live store on 2026-08-07 (40 successor links across 36 items): requiring the successor to name the item back via `depends:` would refuse **40 of 40** — that back-reference has never once been written — and refusing an already-terminal successor would refuse **29 of 40**, most of them designs whose build has legitimately since landed. That is an over-fire count, not an escape count, and a guard firing at that volume is one whoever it inconveniences removes. The title is not a guard and does not pretend to be one; it is the difference between a mistake anyone reads and a mistake nobody can see. |
| The successor trace is durable, reciprocal, and readable as a field | The link a completion records is only worth what a later reader can get out of it, and for one night it was worth nothing — not because it was missing but because it was **unreachable from where anyone stood**. `mg done --successor` had written a `successor:<id>` tag onto the completed item since mg-9259, and both items in the report that produced this row were carrying theirs. The reporter ran `mg show <id> --json \| jq 'keys'`, grepped for `succ\|next\|child`, checked the result sidecar, found the link in neither, and filed the `declares-remainder` gate as **bypassed**. It had fired and been satisfied. That inference was correct reasoning from the only evidence available, so **the absence of a readable trace produced a false report about a working mechanism** — the same failure as a missing trace, arriving by a different route. Three things changed and none of them moved where the link is stored. **(1)** `successor`, `predecessor` and `declares_remainder` are first-class fields on `mg show --json` and `mg list --json`, projected from the tags, which stay authoritative — both link fields are always arrays, never `null`, so `(.successor\|length)==0` holds on every item rather than only the ones that have links. That makes the gate's own audit question answerable from fields alone: `mg list --all --json \| jq -r 'select(.declares_remainder and (.status=="done" or .status=="archived") and (.successor\|length)==0) \| .id'` — *every item that declared a remainder and completed naming nothing to carry it forward*, with an empty result meaning the gate has not been bypassed. That question is **was one named**, which is not **does one still exist**: a link whose target was deleted afterwards passes it, so `mg list --help` documents the companion query (`.successor - $ids \| length > 0` over the full id set) for links that rotted after the guards checked them. Reading either empty result alone as clean is the same mistake this row exists to stop. **(2)** The link is written on **both ends**: the successor gets `predecessor:<id>` back, so a chain is walkable from the tracker to what it inherited and not only the other way. Completion and archival reconcile **every** `successor:` tag on the item, not just an id the current run supplied, because the tag has three routes onto an item (`--successor`, `mg edit --add-tags`, `mg new --tags`) and a back-link that exists for one of them is a chain that breaks depending on how it was filed. Writing the reverse link is deliberately **not** the same as gating on it — the row above records that requiring the back-reference would have refused **40 of 40** links, because it had never once been written; `requireRemainderDischarged` still reads the forward tag and nothing else. **(3)** `mg show` prints resolved `Successor:` / `Predecessor:` lines carrying the linked item's title and *current* status. `mg done` already printed that at the moment of the link and it scrolled away with the terminal; a rotted link (target deleted after the guards checked it) prints `⚠ UNRESOLVED` rather than being skipped, that being the one state a bare tag list cannot tell from a healthy one. The reverse write touches a **second** item, so it is best-effort: a completion that already satisfied its gate is never turned into a refusal because another file could not be updated. It is **not silent** about it — a failure prints a note on stderr naming both ends **and** emits a `work.backlink_failed` event, so `mg event list --type=work.backlink_failed` separates a one-directional chain nothing tried to close from one that was attempted and failed. A best-effort link whose only failure record was a printed line would have been this defect one level down. The `mg archive` **sweep** reconciles as well — the last moment an item is ever written, and so the only route by which a link filed before reciprocity existed becomes walkable from both ends; it is a side effect on the *linked* item and never on which items the sweep moves, so `--dry-run` stays an honest preview. **Known gap:** items already in `archive/` acquire no reverse link, nothing moving them again; links on items still in `done/` close on their next sweep. |
| `mg edit ID [flags]` | Update fields on an existing work item. **Adding to a body? Use `--append-body-file`, not `--body-file`** — see [Concurrent edits](#concurrent-edits-lost-updates). |
| `mg restore-body ID [--list] [--from=STAMP]` | Put back a body that a replace-mode edit overwrote. Every `mg edit --body`/`--body-file` saves the body it is about to destroy first; `--list` shows what is saved (newest first) and a bare invocation restores the most recent. An item with **nothing saved is an error** (exit 3, `no_body_backup`), never a quiet success and never an empty body. See [Recovering an overwritten body](#recovering-an-overwritten-body). |
| `mg archive [ID]` | With an `ID`, archive exactly that one done item and nothing else — the form to use for items the refinery never merged (investigations, evaluations). With no `ID`, sweep `done/` and archive every item older than `--days` (default 7; `--days=0` takes **all** done items). The two forms are exclusive: passing both an `ID` and `--days` is an error, never a silent choice between archiving one item and archiving every item. `--dry-run` previews without moving anything. A targeted archive that cannot act (unknown ID, item not done) exits non-zero and says why. **Three guards refuse an archive**: done `type: design` items with no successor, items of any type tagged `blocked-on-*`, and items tagged `declares-remainder` whose successor is missing or has been deleted — see below. The last is a *backstop*; `mg done` refuses first, at the moment, to the agent standing there. |
| `mg archive <id> --successor <id>` | A design's output *is* a recommendation, so at the moment it is done the thing it recommends is undone by construction — and an archived item cannot be the tracker for undone work. Archiving a done `type: design` item is therefore **refused** unless it names a successor. `--successor <id>` records a `successor:<id>` tag on the design before moving it, so the archived record names its own tracker; the id must resolve to a real item and cannot be the design itself. The check is on the item **type**, not on any wording in its body, and is satisfied **only** by that structured tag — a body that merely mentions an id proves nothing about who is tracking what. The sweep form never forces: it skips guarded items, leaves them in `done/`, and names them on stderr. A design that was *abandoned* rather than implemented is a legitimate archive — `--force` takes it and records a `work.archive_forced` event naming the guard that was bypassed. `--force` is documented here and in `mg archive --help`, and is deliberately **not** named by the refusal itself, so a guard hit mid-cleanup does not hand out its own bypass. |
| Archiving an item tagged `blocked-on-*` | A `blocked-on-*` tag says **a person still owes something here**, and an archived item cannot be the tracker for outstanding work — so archiving one is **refused**, whatever its type. The refusal **names the tag it found** (`blocked-on-daniel`, `blocked-on-daniel-confirm`, …), because "blocked" and "no successor" have different remedies and an operator has to be able to tell which fired. The remedy is to settle what the tag names and then `mg edit <id> --rm-tags=blocked-on-<who>`. The check is on the **tag**, not on any wording in the body: the tag was already a live convention and already queryable across every status (`mg list --tag=blocked-on-daniel` with no `--status` spans statuses and finds `done` items), so this only teaches the *destructive* operation to read an index that already existed. `--successor` does **not** satisfy it — naming a tracker for a recommendation says nothing about whether a person still owes something. `--force` **does** apply, as for the successor guard: an obligation can be discharged out of band and the tag left behind, and without a recorded escape hatch the operator strips the tag by hand — the same bypass with none of the audit trail. A forced archive records a `work.archive_forced` event with `reason: blocked_on_tag`, and the refusal itself does not mention `--force`. The sweep skips blocked items, leaves them in `done/`, and names each one on stderr **with the reason it was refused**. |
| `mg unarchive ID [--status=S]` | Restore an archived work item to the status it held when archived. Refuses if that status is unknown — pass `--status` to say where it belongs. |
| `mg unclaim ID [--assignee=WHO]` | Release a claim, returning the work item to `available/`. **A claim is not a hold.** A claimed item says someone took it and says nothing about *why*, so to any sweeper collecting claims left by dead agents, a deliberate hold and an abandoned claim are the same object — and the usual discriminator, "was anything produced?" (a pushed branch, a merged commit), is blind by construction to work whose only artifact is the ticket body. On 2026-08-07 that released **five completed gh-issue triages** back into the dispatchable pool, where the next dispatch would have re-run each triage over the body carrying its only copy. `--assignee` is the fix at the callsite: it records who the item is waiting on **and then** releases it, in that order, so the item is never in `available/` without the reason it is held. The order is the whole flag — the two-command form (`mg edit --assignee` after `mg unclaim`) is what produced the defect: mg-24d2 was released at 18:24:18Z, assigned at 18:27:15Z, and a priority-wake named it as *"ready and unclaimed — claim or dispatch now"* in the 2m57s between. `--assignee=""` clears the field; `human`, `parked` and `blocked:<agent>` are what pogo's dispatcher gates on by default (`config.IsDispatchGated`, as of 2026-08-07; the sentinel list is configurable, the `blocked:` shape is not), and any value at all says more than a bare claim does. The recorded assignee is echoed back (`Waiting on human`) and rides on the `work.unclaim` event, which now also records its **actor** — every other transition said who; the release did not, and five releases were later described as "attributed" when the log lines carried no actor at all. |
| `mg unclaim` says what it is releasing | If the released item **declares a remainder that nothing tracks** — the same condition `mg done` refuses to complete on — and it lands with no assignee, `mg unclaim` says so and names the correction. It is a **report, not a refusal**: a sweep of genuinely stranded claims has to stay one command that works, and a guard here would fire on the abandoned-triage case the sweep exists for. It is also the discriminator a sweeper otherwise lacks, and it is the *item's own declaration about its output* — not a stage, a type, or a body grep — so it sees a triage whose deliverable is prose exactly as well as it sees a build ticket. Every one of the five swept items carried `declares-remainder`; four named no successor; mg held that at the moment of each release and said nothing. An item that declares nothing releases silently, exactly as before — a note on the routine case is a note nobody reads on the case that matters. |
| `mg reclaim ID [--pid N]` | Re-stamp the owner PID on a claim, **without the item leaving `claimed/`**. This is the handover half of a claim made on someone else's behalf: pogod claims a work item at spawn time under its own PID — before the worker process exists, so an item being worked is never invisible to an ownership check — and the worker's first act is `mg reclaim <id>`, moving the recorded PID to its own. That the PID changed is a **positive signal that the worker itself acted**, which nothing else in the store provides. The item must already be claimed; an available item is **refused** (exit 4, `not_claimed`, remedy `mg claim`), a done/shelved/archived one likewise, and an unknown id is exit 3. Keeping this a separate verb rather than a flag on `mg claim` is deliberate: `mg claim` refusing a non-available item is what makes two concurrent dispatches onto one item impossible, and a `--steal` flag would put that guard one typo away from being off. **The item never leaves `claimed/`** — the implementation is a single `rename(2)` *within* `claimed/`, `<id>.md.<old>` → `<id>.md.<new>`, not an unclaim followed by a claim, which would park the item in `available/` for the duration and reopen exactly the window pogod's spawn-time claim exists to close. Re-stamping to the PID already recorded exits 0 and changes nothing, so a worker repeating the step after a context compaction gets a no-op, not an error that reads as a failure. `--pid` defaults to `$POGO_PID` and then to the calling process's PID, matching `mg claim`; the transition is printed (`Reclaimed mg-7d6d: pid 32194 -> 40881`) so an operator reading a transcript can tell which side of the handover they are looking at. A re-stamp records the same `work.claim` event a claim does, with `prev_pid` added and both statuses `claimed` — `mg spend` pairs a claim with the next release to attribute spend, so a silent handover would bill the worker's whole run to whoever claimed on its behalf. |
| `mg schedule` | The full sweep and the only report on the pending population. **It is no longer the only thing that opens a snooze** — every `mg` command does that (see [Snooze](#snooze-not-now-come-back-at)) — but it is the only thing that sweeps the dependency gate, tidies spent gates, and enumerates what is waiting. Promote pending items **whose gates have all opened** — every dependency met, and any `snooze:` wake time now past — **and report every pending item it could not promote**, each with the gate that held it and that gate's state (`depends: mg-2c34 (claimed)`, `snoozed: wakes … (in 1h 29m)`, or both). Both gates are reported, because "no items promoted" over a non-empty pending set otherwise reads as "nothing is waiting". It **also** calls out separately the pending items no completion can *ever* promote — those waiting on a `shelved` or nonexistent parent, each named with the parent responsible. A held item is invisible from every other angle: it is not `available/`, so stall-watch and priority-wake cannot see it, and `pending` is exactly what a correctly-waiting item looks like. This is the only view of that population. |
| Dependency satisfaction | A dependency is met once the parent **has passed through `done`** — `done/` and `archive/` both count. Archiving is a filing decision about completed work, not a repudiation of the completion, so archiving a parent never strands its dependents. Placement is decided **at filing time** against the resolved state of every dependency, not deferred to the next sweep: an item whose dependencies are already satisfied is filed straight to `available/`, never parked in `pending/` to wait on an unrelated completion. |
| `mg snooze ID (--until TIME \| --for DURATION)` | Set an item aside until a time. It moves to `pending/` carrying a `snooze:` attribute and returns to `available/` on the first **`mg` command of any kind** after that time — every mg invocation promotes elapsed gates, so nothing needs to be scheduled for this to work. **Snooze is an attribute, not a sixth status** — see [Snooze](#snooze-not-now-come-back-at). `--for` takes Go durations plus `d`/`w` (`90m`, `6h`, `3d`, `2w`); `--until` takes RFC3339 or a local `2026-08-03 14:30`, and a bare date means **09:00 local**, never midnight. The resolved absolute instant is echoed back and stored as RFC3339 UTC. Snoozing a claimed item releases the claim, as shelving does. |
| `mg unsnooze ID` | Lift a snooze early. A pending item returns to `available/` if its dependencies are also met and stays `pending` if they are not — lifting one gate does not lift the others. Refuses an item that is not snoozed. |
| `mg shelve (ID \| --tag T)` | Hide a work item and everything that depends on it. Restored with `mg unshelve`. **Two guards refuse a shelve**, ported from `mg archive` because a shelved item is no more a tracker for outstanding work than an archived one, and shelve is the cheapest of the three exits to reach — one command, no claim, no status precondition. It is refused if the item is tagged `blocked-on-*`, or if it **declares a remainder**: it carries `declares-remainder`, *or* its type is one whose output IS a recommendation (`design`, `scoping`, `audit`, `idea`), *or* its body's leading carrier block says `stage: triage`. The last two arms read the same defaults `mg new` writes, and they are here rather than at `mg done` because they reach the items filed before that default existed — measured on the live store on 2026-07-30, the whole 181-item shelf, of which exactly one carries the tag. Satisfied by an existing `successor:<id>` tag naming an item that still exists (`mg edit <id> --add-tags=successor:<id>`); a successor does **not** answer the `blocked-on-*` arm, for the reason it does not at archive time. `--tag` applies the same guards item by item, skips the ones it refuses, and names each on stderr **with the reason** — a bulk shelve that skipped the guards would be the targeted form's refusal one flag away. |
| `mg shelve <id> --override "<why>"` | Shelve an item a guard refused, and record why. **It takes a string, not a flag.** A bare `--force` records that somebody overrode the gate and loses the only thing a later reader needs, which is *what they knew that the gate did not* — so the reason is required, and whitespace is not a reason. Using it emits a `work.shelve_forced` event carrying **both** halves: `guard` (the code of the refusal bypassed — `shelve_blocked_on_tag`, `shelve_without_successor`, `shelve_dangling_successor`) and `reason` (the operator's string). Legitimate uses are real: a design genuinely abandoned rather than deferred, an obligation discharged out of band with the tag left behind. It applies to **one named item** and is refused with `--tag`: an override is a claim about an item the operator looked at, and a bulk one is a claim about items they did not. Like `--force` on `mg archive`, it is documented here and in `mg shelve --help` and is deliberately **not** named by the refusal itself, so a guard hit mid-cleanup does not hand out its own bypass. |
| Shelving **cascades**, and now says so | Shelving a target recursively shelves every open item that depends on it — so it hides every audit, follow-up and remainder filed *at* that target. Measured on the live shelf on 2026-07-30: **32 of 175** items got there as a dependent and nothing ever told anyone; **0** have an open dependent, which is not health but a consequence — the cascade cannot leave one by construction. The cascade is **not** gated: refusing the operator's shelve on the strength of an item they never named, and stranding a dependent in `available/` with its dependency gone, are both worse than the move. It is **reported** instead. `mg shelve` prints `Also shelved N dependent item(s)`, names each one, and says how to get them back; the `work.shelve` event carries a `dependents` field listing the ids that shelve hid (transitively, and always present — empty when it hid nothing, so absence means "written before this shipped" and nothing else), and each cascaded item's own event carries `cascaded_from` naming the item that pulled it in. |
| Depending on a `shelved` item | Shelving means **"not now"**, not "cancelled". A new item filed onto a shelved parent is filed as **`shelved`**, alongside its parent — not released, and not left in `pending/` masquerading as an item that is waiting correctly. This is the same treatment `mg shelve` already gives dependents that exist when the parent is shelved, so `shelved` means the same thing whichever side of the parent's shelving the dependent was created on. `mg new` **says so**, naming the gate and the `mg unshelve <id>` that lifts it. `mg unshelve` on the parent brings dependents back as `pending`, and completing the parent then releases them normally — the chain is parked, never destroyed. |
| `mg mail send\|reply\|list\|read\|archive\|reclaim\|register\|migrate` | Maildir-style messaging between agents. `archive AGENT/MSG-ID` moves a message out of the active mailbox; `list AGENT --archived` inspects archived messages, and `list AGENT --from`/`--exclude-from` filters it by sender (see the two rows below). Each operation logs a `mail.sent`/`mail.read`/`mail.archived` event to `events.jsonl` and a caller-attributed line (pid + `POGO_AGENT_NAME`) to `log/mail-audit.log`; malformed message files are skipped loudly (`mail.malformed` event, stderr warning, count in `list` output). `read` and `reply` refuse to touch **another agent's** mailbox when `POGO_AGENT_NAME` is set (both mark the message read for its owner) unless `--force` is given; denials and forced reads are audited too. |
| Bad addresses (`mg mail send`, `mg mail register`) | A recipient mg has **never seen** is refused (exit 3, `no_such_mailbox`), and the refusal names the near neighbours it might have meant (*"did you mean `v9ecf`?"*). Before this, mail had **no bad addresses**: a mailbox was created by the act of delivering to it, so every send exited 0 and reported `Delivered` — a typo minted a dead drop, and the `(new mailbox created)` note could not distinguish that from the first legitimate mail to a new agent, because it is equally true of both. mg has no agent registry, so a recipient counts as known when either of two things on disk says so: **a mailbox of that name exists**, or **a work item of that name exists** (polecat mailboxes are named for the work item their agent runs, so a new agent is addressable before its first mail arrives — without which every legitimate dispatch would carry the override flag, which is where a flag stops being read). `--create` is the explicit "this recipient really is new": it registers and delivers. `mg mail register NAME` performs the registration alone — idempotent, creates an empty maildir, touches no mail — for provisioning an agent ahead of any message. `mg mail reply` gets the same treatment: its recipient comes from a `From` header the sender wrote, which is free text mg never validated. |
| Registration is a durable record (`mg mail register`, `mg mail list`) | The refusal above fires **once per name**: existence is what it consults, so a name talked past it once with `--create` is a good address forever after, and nothing on disk afterwards separates it from one somebody deliberately established. The live proof is the `daniel` mailbox — in daily use, receiving real mail from several agents, never registered. It *works*, and "it works" is exactly the evidence that was missing. Registering now writes `.registration.json` inside the mailbox naming **who registered it, when, and via which spelling** (`register`, `send --create`, `reply --create`), so the question survives the send. A box's **standing** is one of three answers: `registered` (a record exists), `work-item` (no record, but a work item is named that, so the name is derivably legitimate — this is most of the store), or `unregistered` (neither, so the box exists only because mail was delivered to it). `mg mail list` marks the last kind and counts them, including how many are **holding mail right now**; `--json` carries `registration` on every mailbox object. Registering a box that already exists **adopts** it — the record is written, marked `adopted`, and stamped with how much mail it inherited, which it explicitly does not vouch for. Re-registering never rewrites an existing record: its value is naming the *first* deliberate act. **Nothing is refused on the basis of standing** — 1361 boxes predate the record, and a store-wide refusal would break every one of them to punish a bookkeeping gap they had no way to close. One thing *is* refused: registering a **stray prefixed box** (`cat-mg-01ce`, whose canonical name is `01ce`). Names are canonicalized, so doing that minted a new empty box under the canonical name, reported success, and left the stray holding its mail and still marked — a phantom mailbox produced by following the listing's own advice. It is now exit 4 naming `mg mail migrate`, and the footer separates strays (463 of the 667) from the names actually worth adopting. |
| Missing vs. empty mailbox (`mg mail list AGENT`) | A mailbox that never existed reports `No such mailbox: X`, with near neighbours suggested; an existing one that is merely quiet says `(mailbox exists)`. Under `--json` the two used to emit **nothing at all** — byte-identical empty output — so no scripted consumer could tell a quiet inbox from a misdelivery. The box's own state `{mailbox,unread,exists,registration}` now answers that, on **stderr**. It was briefly on stdout, and putting it there answered the question by breaking a more basic one: `mg mail list AGENT --json` then emitted **two schemas selected by state** — message objects when there was mail, a mailbox object when there was not — so `jq -s 'length'` returned **1 for an empty mailbox** and every "do I have mail?" written as a row count got a false positive, forever, on exactly the case where the answer was no. Selecting a field was no better: `.from` on the status object is `null`, which reads as a *corrupt message* rather than an empty box, and cost the reporter two misdiagnoses in one shift. stdout is now message objects and nothing else in every state, so a row count is a message count; the same split moved the sender-filter summary off the stream, since a `--exclude-from` away is not far enough for the defect to be fixed. Exit stays **0** in both cases: a mailbox nobody has mailed yet is the normal state of a new agent, and a poller asking after one is asking a fair question. |
| Sender predicate (`mg mail list AGENT --from` / `--exclude-from`) | `--from=NAME` lists only mail from those senders; `--exclude-from=NAME` hides them. Both are repeatable and comma-separated, both match the **From field exactly** — case-insensitively, with the same `mg-`/`cat-` stripping every mailbox argument gets — and **neither is ever a substring match**: `--exclude-from=scheduler` hides neither `scheduler-v2` nor a real message whose *subject* says "scheduler". A name given to both flags is refused, since nothing could match and the empty listing would say nothing about the mailbox. **Why a flag rather than a documented pipeline.** A `*/10` mail-check appends a row forever, so on 2026-08-09 `architect` measured **264 of 265** unread from `scheduler` and `pm-pogo` **284 of 287**; the mailbox is timestamp-ordered and the noise arrives at **both ends**, so no `head -N`/`tail -N` can see a real message buried in the middle — `tail` bounds volume, not time. Two agents ran the natural command that morning and one came within a hunch of reporting "no mail" over a 32-hour-old message at row 108. The escape both reached for, `grep -v scheduler`, is **retracted**: it matches the rendered *line*, so it discards exactly the correspondence *about* the noise — and it self-validates until the first message that mentions the term, which is correlated with the topic and so arrives when the traffic matters most. That is a category error, not a bad pattern: a text filter over a field-structured listing cannot see which column it landed in, and no better pattern fixes it. |
| Reclaiming the pile (`mg mail reclaim`) | The sender predicate hides the noise from **one listing**; this drains it. A scheduler fire that cannot reach an agent's PTY falls back to writing the fire into that agent's mailbox, and **nothing ever removed those copies** — one 33-hour crew outage rendered as 264 consecutive rows in a single box and **12,295 fleet-wide**, which every mail check then pages through. Coalescing bounds what a *future* outage writes; it reclaims nothing already there. `mg mail reclaim [AGENT]` moves superseded copies to `archive/`: with no AGENT it sweeps every mailbox. A message moves only when **all three** hold — its **From field** exactly matches a `--from` sender (default `scheduler`; never a substring, never the subject text, never row volume); it is **not among the newest `--keep`** copies of its recurring notification, grouped by **exact Subject**, so a live pointer survives per schedule; and its **Date parses and is older than `--older-than`** (default `24h`). An unparseable Date is retained *and counted*, never guessed at. Measured against a copy of the live store: **10,780 of 11,974** scheduler copies reclaimed, **11,451 non-scheduler active messages unchanged**, and every one of the 10,780 moved files carried `From: scheduler`. **Why sender and not volume.** The alternative already happened — a 1,594-message bulk sweep run under pressure very nearly took a triage packet and a fleet notify report with it, caught only by re-listing the archive afterwards. That sweep was not wrong for archiving too much; it was wrong for selecting by volume. **A sender that has a mailbox of its own is refused** without `--force`: that name is a *correspondent*, and "an older copy sharing a subject is obsolete" is true of recurring machine notifications and false of a thread. Generators (`scheduler`, `stall-watch`) have no mailbox — nobody replies to them — which is what makes them safe to sweep. Nothing is deleted: reclaimed mail stays readable under `list AGENT --archived`, every move is audited (`op=reclaim`), and one `mail.reclaimed` **summary** event is emitted per mailbox rather than one per message, so draining an unbounded pile does not write an unbounded pile into `events.jsonl`. Pruning `archive/` is deliberately out of scope — deletion is the one operation whose failure is silent and permanent. |
| A filter always says what it hid | A filter is another bounded read, and a bounded read that reports nothing manufactures absence — the defect the predicate above exists to remove, which its own remedy would otherwise reproduce. So whenever a predicate is active the rows are preceded by `sender filter: --exclude-from=scheduler — 1 of 265 shown, 264 hidden`, and a predicate that removes **everything** says outright that *the mailbox is NOT empty*, rather than borrowing the wording of a quiet inbox. The report is a **header**, following the same reasoning as the body-length counts on `mg mail read`: a footer is cut by the same bounded read it would warn about. Under `--json` the same figures arrive as one trailing object `{mailbox,unread,exists,listed,suppressed,from,exclude_from}` — emitted whether or not any message matched, because the scripted reader is who this must be safest for (a coordinator inbox at 1,582 unread bulk-archived 1,451 noise rows and came within a re-listing of losing two real messages). Like the empty-mailbox sentinel it carries **no `id`**, so the documented `jq 'select(.id and …)'` guard skips it; `unread` is always the mailbox's true count, never the filtered one. Without a predicate nothing extra is printed or emitted, so every existing invocation is unchanged. |
| Reading your own mailbox under another name | The cross-box guard compares the caller against the mailbox name — but a mailbox has **no registration**, so an agent's inbox is whichever name its *senders* used, routinely the work item it is running rather than its agent name. Agent `pd639` reading box `d639` was refused, in wording that reads like a permissions error; an agent meeting it concludes it may not read its own mail and leaves the mail unread — the exact outcome the guard exists to prevent. Ownership is now asserted on **two pieces of evidence together**: the mailbox name appears inside the caller's own name, *and* it is a real work item in this store. Either alone is far too loose. When the guard does still fire on a work-item-named box, the refusal says the box belongs to whoever is running that item and that `--force` is the right answer, rather than framing it purely as an intrusion. |
| Canonical mailbox addressing | Every recipient/mailbox argument is canonicalized before it resolves to a directory: the harness prefixes `mg-` and `cat-` are stripped, so the work-item alias `mg-<id>`, the process name `cat-mg-<id>`, and the bare live id `<id>` all address the **same** mailbox. This closes the silent-drop class where mailing `mg-<id>` (the spelling many templates use) minted a stray mailbox nobody read while a watcher polling `mg mail list mg-<id>` waited forever. Crew mailboxes (`mayor`, `architect`, …) carry no prefix and pass through unchanged. `mg mail migrate` (`--dry-run` to preview) is a one-shot, idempotent cleanup that merges any pre-existing stray `mg-<id>` mailbox into its canonical `<id>` box — unread, read and archived mail alike — preserving read state and then removing the emptied stray directory. |
| `--append-body-file PATH` / `--append-body TEXT` (`mg edit`) | Append to the existing body instead of replacing it. Reads verbatim, same as `--body-file` (`-` for stdin). An append composes against the body **on disk at write time**, so it cannot destroy a section the caller never saw — see [Concurrent edits](#concurrent-edits-lost-updates). The appended text is stored byte-for-byte; only the join is normalized, to exactly one blank line, so a leading `## heading` renders as a heading. Mutually exclusive with `--body`/`--body-file` (exit 2). |
| `--if-unchanged HASH` (`mg edit`) | Refuse the edit (exit 4, `body_changed`) unless the stored body still hashes to `HASH`, from `mg show ID --body-hash`. **Opt-in**: without it, `--body-file` behaves exactly as it always has. The hash covers the stored body *including* its `# Title` heading, so it also catches a competing `--title`. A prefix of 8+ characters is accepted. |
| `--if-assignee VALUE` (`mg edit`) | The same guard one field over, on the **dispatch gate**. Refuse the edit (exit 4, `assignee_changed`) unless the stored `assignee` is **exactly** `VALUE`; `--if-assignee=""` requires it to be unset, which makes the flag a compare-and-swap (`--if-assignee="" --assignee=blocked:me` takes a gate only if nobody already holds one). `--if-unchanged` covers the body; nothing covered the one field that decides whether an item is dispatched at all. On 2026-08-12 the mayor gated mg-27d4 with `blocked:pm-pogo`, pm-pogo set it back to `mayor` a minute later, and the mayor's next four writes each printed `Updated mg-27d4` while the hold was gone. **Nothing there was a bug** — both agents wrote what they meant and `events.jsonl` names both, with before/after values. What was missing was any way for the holder to *say* the hold was still in place, so a hold survived only as long as nobody disagreed with it, silently and in the holder's direction. It is a precondition rather than a warning mg emits on its own because mg has **no baseline**: inside one invocation there is a read and a write microseconds apart and no record of what the *caller* last saw. The refusal names both values and the file's mtime; `mg event list --type=work.edited` names the actor who moved it. |
| Gate-vocabulary validation (`--assignee`) | **A misspelled gate is an open gate, and it looks exactly like a closed one.** `human`, `parked` and `blocked:<agent>` are what pogo's dispatcher gates on (`config.IsDispatchGated`). A near-miss of those spellings is now **refused** (exit 2, `invalid_value`) instead of stored: `blocekd:pm-pogo`, `blocked-pm-pogo`, `blocked:` (no agent), `Blocked:pm-pogo` and `Human` each used to exit 0 and store a value that gates nothing — which reads to a human exactly like a hold and to the dispatcher like an ordinary name, with no later signal, because a value nobody recognises is indistinguishable from an agent nobody has heard of. The check applies to `mg edit`, `mg assign` and `mg unclaim --assignee` (where it refuses **before** the release, leaving the item claimed rather than dropping it into `available/` under a hold that holds nothing). **Deliberately narrow**: mg has no register of legitimate assignees, so it cannot refuse "unrecognised" — any agent name is valid and passes, and so does an unrelated colon-bearing value like `team:infra`. Only attempts at the gate that missed are refused. The residual is stated rather than papered over: a typo far enough from the three spellings (`parkd`) is indistinguishable from a name and still gets through, because widening the net means refusing by edit distance from a five-letter word, and `parker` lives in that neighbourhood. There is no `--force` — the way past the guard is to spell the gate correctly. |
| Title/body coupling (`--title` vs `--body-file`) | **A work item's title IS the body's first `# ` heading** — there is no `title:` in the frontmatter and no other copy, so editing one edits the other. When **both** are given, `--title` wins and the body's leading heading is rewritten in place to match it. When only a body is given, its first heading would rename the item, and `mg edit` **refuses** that (exit 4, `title_side_effect`) rather than performing a silent write to a field the caller did not name — the hint offers both remedies, since either may be what you meant. The direction-agnostic safe procedure is `--title="…" --body-file F` where **F has no leading `# ` heading**: mg writes the heading from the title, and writes exactly one. Headings *below* the first are ordinary content, left alone and counted on stderr; a blockquoted `> # heading` is a heading to neither rule, so it can neither become nor displace the title. See [docs/title-body-coupling.md](docs/title-body-coupling.md) for the measured shape × `--title` matrix (mg-bac6). |
| `--body-file PATH` (`mg new`, `mg edit`, `mg mail send`) | Read the body **verbatim** from a file (`-` for stdin) instead of from `--body`. Mutually exclusive with `--body` (exit 2); an unreadable path is an error, **never** a silently empty body. Reach for it whenever the body's exact text matters. The shell expands `` `backticks` ``, `$VAR` and `$(cmd)` inside `--body="..."` **before mg runs**, so those terms are silently gone from the item or message while mg still reports success — and mg cannot detect this, because the mangled string is byte-identical to one you typed that way. `--body-file` puts no shell in the body's path at all. `--body` is unchanged and **not** deprecated: it stays correct for bodies carrying no metacharacters. |
| Derived mail subject (`mg mail send`) | `--subject` is **optional**. Omitted, the subject is the body's **first line** (RFC822 / git-commit convention), so it rides inside the same quoted heredoc as the body — the safe spelling is now the short one. This matters because a subject can only be given inline, and no quoting survives ordinary prose: double quotes lose `` `backticks` ``/`$VAR`/`$(cmd)`, and single quotes lose apostrophes **silently**, since English carries them in pairs (`--subject='the rock'n'roll release'` is balanced, exits 0, and delivers `the rocknroll release`). The derived subject is **echoed back** (`Subject: …`, plus `subject`/`subject_derived` under `--json`) — a derivation nobody can see is the defect, not the cure. The first line is **copied, not consumed**: the body arrives whole. A blank or control-character-bearing first line is **refused** (exit 2) naming `--subject`, rather than writing a malformed header. Passing `--subject` is unchanged, empty-value refusal included; `mg mail reply` is untouched. |
| Mail threading | Every message carries a `Message-Id` equal to its MSG-ID — which is its maildir file name, not a second id space. `mg mail send --in-reply-to MSG-ID` is the explicit, stateless primitive: it stamps `In-Reply-To` and seeds `References`. `mg mail reply AGENT/MSG-ID` wraps it, resolving the recipient, the `Re:` subject and the ancestry chain from the original, marking it read but **not** archiving it. `References` keeps the 20 most recent ids so message files stay cat-able. Nothing is inferred from read history — read and send are separate processes, so threading is always explicit. Message ids are minted **per delivery**: they round-trip for threading within one mailbox and are not globally unique. Header values may not contain CR, LF, or other control characters (exit 2, `invalid_header_value`) — an unsanitized newline would inject arbitrary headers. |
| `mg event append <type> [--key=value ...]` | Append a structured event to `events.jsonl`. |
| `mg event list [--type=T] [--since=TS] [--tail=N] [--json]` | List events with optional filtering. Output is already NDJSON; `--json` is accepted for consistency (no behavior change). |
| `mg flow [--live] [--repo=P] [--blocked-after=D] [--group-by AXIS] [--json]` | Per-status flow view: throughput, median age, bottleneck, blocked chains. `--json` emits the computed snapshot as one JSON object under a single stable schema — the same top-level keys for the status and `--group-by` paths; the grouped path fills `groups` and leaves the status-only fields empty. |
| `mg spend [--by AXIS] [--since D] [--window W] [--total] [--json]` | Aggregate token consumption per item, tag, repo, agent, etc. `--since` is a rolling duration (`24h`, `7d`); `--window today\|week` is calendar-anchored; `--total` prints the today/this-week/all-time headline. See [Token spend accounting](#token-spend-accounting). |
| `mg snapshot` | Commit a git snapshot of current state. |
| `mg log [args]` | Show snapshot history (passes args to `git log`). |
| `mg sidecar <id> [--path] [--json]` | Print the result sidecar **resolved** from the item's own location — never scanned for, never a candidate list, so it follows an item into the month-partitioned archive. `--path` prints the absolute path instead of the contents. Exit **0** with the result on stdout, **3** (`no_sidecar`) with *nothing* on stdout when there is none, **1** (`io_error`) when the store could not be read. Use this instead of `ls ~/.macguffin/work/*/<id>.result.json`, which is one level deep and cannot see an archived sidecar at all. See [Reading a result sidecar](#reading-a-result-sidecar). |
| `mg sidecars [--json]` | Report every `<id>.result.json` that is not sitting beside its item's `.md`, **classified by content**: `identical`, `equivalent` (same JSON re-serialised), `subset` (names the superset and the keys only it holds), `conflict` (names every key in disagreement), `opaque`, or `unknown` (a file could not be *read* — a failed probe, never reported as a difference). Reports and never deletes; a `subset` verdict is advice, not an instruction to keep the superset. See [Reading a result sidecar](#reading-a-result-sidecar). |
| `mg schema` | Dump the full command tree as one JSON document (command names, use, flags, and a `mutates`/`idempotent` hint per command) for agent/tooling consumers. Frozen, additive-only shape versioned by `schema_version` (see [schema contract](#mg-schema-contract)). |
| `mg version` / `mg --version` / `mg -v` | Print version. Release builds include commit + date build metadata (e.g. `mg v0.1.3 (abc1234, 2026-07-08)`). |

### Snooze: "not now, come back at …"

`mg snooze` expresses the one thing the model had no word for. Before it,
"revisit this on Monday" was written into `assignee` — the only field that
silenced stall-watch — which made a routing field carry a scheduling signal and
left tickets sitting in someone's queue looking like decisions they owed.

```sh
mg snooze mg-1234 --for 3d
mg snooze mg-1234 --until 2026-08-03            # 09:00 local on that day
mg snooze mg-1234 --until "2026-08-03 14:30"    # local wall clock
mg snooze mg-1234 --until 2026-08-03T14:30:00Z  # explicit zone
mg unsnooze mg-1234                             # lift it early
```

**It is an attribute, not a sixth status.** Status in mg is the directory an
item is in — there is no `status` field and there never has been. A snoozed item
is a **`pending/` item carrying a `snooze:` timestamp**: it waits on a CLOCK
exactly as `depends:` waits on an ITEM, both gates are ANDed, and both are
evaluated in the one place. `ls work/pending` and `grep snooze:` still answer
truthfully, which a status computed at read time would not.

**Every `mg` command opens elapsed gates.** A gate is worth only as much as the
thing that opens it, and a snoozed item is invisible from every other angle — it
is not `available/`, so stall-watch and priority-wake cannot see it, and
`pending` is exactly what a correctly-waiting item looks like. So the opener is
mg itself: on **any** invocation — `mg list`, `mg show`, `mg claim`, anything —
a goroutine started beside your command promotes every pending item whose wake
time has passed. Three properties make it impossible to set a gate nothing will
open:

- **Level-triggered, never edge-triggered.** The check asks whether the wake
  time has *passed*, not whether it just *arrived*. Nothing running at the wake
  instant therefore **delays** an item and can never lose one; the next `mg` of
  any kind recovers it.
- **Not scheduled.** Promotion is a property of the store and the binary, not of
  a cron on one particular agent. Readiness used to depend on mayor's
  `mg-schedule-sweep`, and when that schedule was lost the sweep of 2026-08-04
  reported `the previous sweep ran 4d 9h ago` — four days of gates that had
  opened and stayed shut.
- **Loud at snooze time.** A wake time that has already passed, or that mg
  cannot parse, is refused when you set it — not written and forgotten. A
  `snooze:` value that reaches disk unparseable anyway (a hand-edit) **holds**
  the item and is **named** by `mg schedule`'s stranded report and by `mg show`.

This is a **behaviour change on read paths**: `mg list` can move a file. The
promotion is announced on stderr (`Snooze elapsed: promoted mg-1234 …`), so
`--json` stdout carries nothing but the NDJSON stream you asked for, and it can
never fail your command — a store it cannot write produces a warning and exit 0.
Set `MG_NO_AUTO_PROMOTE=1` for a provably read-only mg.

A command that promotes also **reports the promotion it just made**: the
promoter finishes before the command reads the store, so `mg list` shows the
item under `available`, not under the `pending` it occupied a moment earlier.
The one exception is a store that has stopped responding, where mg abandons the
promotion after a few seconds and says so rather than making you wait.

**`mg schedule` is still worth running on a clock, for its reports.** It is no
longer what opens a snooze, but it is the only view of the pending population:
the **held** report, and the **stranded** report — items no completion can ever
release, which automatic promotion by construction can never fix. It also sweeps
the dependency gate and tidies spent gates. Register it once — pogod, because it
persists schedules to disk and replays them through host sleep, NTP steps and its
own restarts:

```sh
scripts/install-snooze-driver.sh     # AGENT=… CRON=… to override
```

Its old staleness warning claimed *"Snoozes open only when this sweep runs"*.
That is no longer true, so the warning now names what it actually detects — a
stranded population going unread — and a **new and stricter** check replaces it
as the safety net: a pending item with **every gate open** immediately after a
sweep can only mean promotion is *failing* (a read-only store, a full disk), and
`mg schedule` says so by name. That is positive evidence about the outcome
rather than a proxy measurement of a cron's absence.

`mg schedule` also lists **every pending item it could not promote**, with each
gate that held it and that gate's state — `depends: mg-2c34 (claimed)`,
`snoozed: wakes 2026-07-29T17:16:05Z (in 1h 29m)`, or both on one line — because
a population nobody can enumerate is a population nobody audits. Both gates are
reported, not just the snooze: an item blocked on a dependency that never
completes is the case that most needs surfacing, and it was the one case the
sweep used to be silent about.

**What snooze is not.** Not a priority, not an assignment, not a workflow state
machine, and not a recurrence: one item, one absolute wake time. It declares a
**pause**, not a remainder — a snoozed item owes nothing and is not waiting on
anybody.

### Concurrent edits (lost updates)

`mg edit --body` / `--body-file` sends a **complete replacement body**, composed
by the caller from a read that happened seconds, minutes, or a whole agent turn
earlier. Anything another writer stored inside that window is destroyed. Before
`mg-f326` this happened silently — exit 0, no warning, no diff — and three
agents did it to each other three times in two hours.

**The fix is to stop sending whole bodies.**

```sh
mg edit mg-1234 --append-body-file - <<'EOF'
## 2026-07-29 04:20 — reconciliation

...
EOF
```

An append composes against the body **on disk at write time**, so it cannot
destroy a section it never saw. It also matches how these bodies are actually
written — dated sections that accumulate — and needs no coordination with
anyone. All three of the night's collisions were appends of separate sections
that had no reason to conflict.

**When a full rewrite really is the shape, name the version you read:**

```sh
HASH=$(mg show mg-1234 --body-hash)
# ... compose the new body ...
mg edit mg-1234 --if-unchanged="$HASH" --body-file ./new-body.md
```

If someone wrote in between, the edit is **refused** (exit 4, `body_changed`)
and names what mg can observe — the hash you passed, the hash on disk now, the
current size, and when the file was last written. Nothing is partially applied.

`--if-unchanged` is **opt-in**. A bare `--body-file` behaves exactly as it
always has, because `mg` self-installs across the whole fleet on merge and this
is the most-used write path in that fleet's own tooling: a write path that
starts refusing by default is a decision to make on purpose, not a side effect
of adding a flag.

Two further properties worth knowing:

- **`--title` alone is body-safe.** It rewrites the `# heading` line in place and
  leaves every other byte of the body untouched. It is the one edit two agents
  can make to a live item without racing each other's prose. (The body hash
  still moves, because the heading is part of the body — so `--if-unchanged`
  catches a competing retitle too.)
- **Every body change is recorded.** A body-changing edit prints its size delta
  on the success line (`Updated mg-1234: title (body 227 → 113 lines)`) and
  writes a `work.edited` event carrying the before/after hashes and line counts.
  Neither recovers lost bytes. Both exist because `grep -c` returns the same
  zero for a deliberate deletion and a destroyed one: once a clobber is known to
  have happened, every genuine absence nearby reads as damage, and there was
  previously no instrument anywhere in the system that could tell the two apart.
- **Every *metadata* change is recorded too.** See
  [Who did this](#who-did-this-audit-attribution) below.

**Not locking.** Agents are long-lived and can die mid-edit, so a lock needs a
timeout, and a timeout reintroduces the same race with more moving parts. The
defect being fixed is *silence*, not concurrency.

### Reading a body that has become a log

The append convention above works, and it must not be made heavier — the ticket
is the only artifact the next actor reads. What fails is **reading** it: a
long-lived contested item accumulates dated sections, and its live spec then
sits in the same undifferentiated body as the history that superseded it. A
reader who starts at the top has no signal that line 232 overturns line 105.

```sh
mg show mg-01f7 --sections
```

```
mg-01f7: 6 dated sections.
Lines are lines of the BODY: mg show mg-01f7 --json | jq -r .body
Order is document order. Which section is CURRENT is a judgement, not a field — this does not say.

    27  2026-08-12  ## [SUPERSEDED ORIGINAL TITLE — demoted 2026-08-12 06:36Z, kept for the record]
   105  2026-07-21  ## ⚠ CORRECTION + UPGRADE 2026-07-21 02:55Z: this should be WAKE-TRIGGERED, not parked
   160  2026-08-05  ## EVIDENCE (doctor, 2026-08-05 22:00Z): the silence is NOT launchd-only …
   232  2026-08-06  ## ⚠ RETRACTION (doctor, 2026-08-06 06:05Z) — MY 22:00Z EVIDENCE SECTION ABOVE IS WRONG.
   292  2026-08-12  ## 2026-08-12 06:35Z — a SECOND, independent blocker on idle sleep …
   338  2026-08-12  ## 2026-08-12 06:33Z — mayor: the passive option does not exist …
```

A plain `mg show` says so in one line, above the body, once there are **three or
more** — because the failure is not knowing the body is a log until you are
already deep in it:

```
Title:     DANIEL DECISION, not a wait: settling 'is the launchd wedge sleep-induced' …
Sections:  this body carries 6 dated sections — see `mg show mg-01f7 --sections`
```

- **A dated heading is any `#`–`######` heading whose text contains a `20xx-xx-xx`
  date**, outside fenced code blocks, other than the body's first `# ` line
  (that one is the item's *title*, already printed above). The date does not
  have to lead the heading, and that is deliberate: the sections a reader most
  needs are written `## STRUCK 2026-07-30: …` and `## ⚠ RETRACTION (doctor,
  2026-08-06) — …`, with the verdict first because the verdict is the point.
  Indexing only `## 2026-08-12`-style headings would list the tidy appends and
  drop every retraction.
- **Line numbers are lines of the body** — the bytes `mg show <id> --json | jq -r
  .body` returns — not offsets into `mg show`'s own output, whose header block
  is a different height for each item.
- **Order is document order, never date order.** A section dated before the one
  above it is an anomaly a reader must be able to see.
- `--sections --json` emits `{"id", "count", "sections":[{"line","date","level","heading"}]}`;
  `sections` is `[]` and never `null`.
- On a terminal, a heading longer than the window is fitted with a `…` marker —
  the line and date columns are never cut. Piped or redirected output is **never**
  truncated, the same rule `mg list` follows, because a heading cut at the
  terminal's width would hide the half of a retraction that says what was retracted.

**Neither flag says which section is CURRENT**, and nothing mechanical can —
that is a judgement someone made. The convention that records it is to **strike
a superseded section inline where it sits**, not to add a new one at the bottom:
a reader who hits line 105 has no way to know that line 232 overturns it. `mg`
does not enforce that, and an earlier design that tried to — a `--spec` flag
printing everything above the first dated heading — was tested and rejected: on
`mg-49b1` the live spec was in a *later* section, so `--spec` would have printed
a retracted position with machine authority.

### Who touched this last

Two agents editing one item could not see each other, and what that cost was not
data — it was a **false bug report**. On `2026-08-11` the mayor mailed the human
that `mg edit --append-body-file` was silently reverting an assignee field, filed
it high, and an investigation was dispatched. It then retracted: pm-pogo had been
deliberately handing the item back, twice, **both writes had landed**, and the
value that read as corruption was a colleague answering. A colleague's edit and a
corrupted field were the same observation.

So an edit now names the last *recorded* writer, on **stderr**:

```
$ mg edit mg-1234 --append-body-file - <<'EOF'
## a reply
EOF
Updated mg-1234: the title (body 84 → 96 lines)
note: mg-1234 was last edited 4m ago by mayor (append). A field you did not write
is a colleague, not corruption — 'mg event list --type=work.edited | grep
mg-1234' names every writer and both body hashes.
```

- **It refuses nothing and it is not a lock**, for the reason above: 919 of 1,287
  edits measured over 15 days already use the append path that cannot clobber,
  and making the safe path heavier is how callers get routed onto the unsafe one.
- **Silence has one meaning:** no other party has a recorded edit of this item
  since you last wrote it. An agent iterating on its own item sees nothing, so
  the note does not become wallpaper — which is the failure mode of a warning
  that fires on every write.
- **No recency threshold**, deliberately. A cutoff would be a number nothing in
  the incident record justifies, and a wrong cutoff turns silence into a lie. The
  age is printed instead, so a three-week-old touch can be discounted by the
  reader — a judgement they can make and `mg` cannot.
- **It reports what was *recorded*.** `events.jsonl` is best-effort by design (a
  state change must not fail because the log is unwritable), so an absent record
  means "not recorded", not "did not happen". The note also names only the
  **last** writer; the `mg event list` line in it is the full roll.

### Recovering an overwritten body

`--if-unchanged` proves **nobody else** wrote between your read and your write.
It says nothing about whether **your own read succeeded** — and that is the end
the damage came in from on `2026-07-29`, when a 149-line body became two lines
of a shell usage error:

```sh
mg show mg-8970 --body > b8970.md && python3 ...   # `mg show` has no --body flag
mg edit mg-8970 --body-file=b8970.md --if-unchanged=<hash>
```

`mg show` wrote its own usage error into the file and exited non-zero. The `&&`
bound only the `python3` step, so `mg edit` on the next line ran unconditionally
and faithfully wrote what it was given. The guard was passed **and satisfied**:
it was watching the concurrency end of a read-modify-write while the corruption
entered at the read end. `work.edited` recorded `lines_before=149
lines_after=4 guarded=true` and both hashes — enough to *prove* the loss, and
useless for *repairing* it. The store is not a git repo, so there was no VCS
fallback either.

**So mg keeps the body it is about to destroy.** Before any `mg edit --body` /
`--body-file` overwrites a body, the prior body is written to
`~/.macguffin/work/.bodybak/<id>/<timestamp>-<hash8>.md`. The ten most recent
are kept per item.

```sh
mg restore-body mg-1234 --list        # what is saved, newest first
mg restore-body mg-1234               # put back the most recent
mg restore-body mg-1234 --from=20260729T161400
```

- **The restore can fail, and says so.** An item with nothing saved exits 3
  (`no_body_backup`) and leaves the body alone. A recovery command that reports
  success when it recovered nothing is a second way to lose a body.
- **`--from` matches a timestamp prefix**, and a prefix naming *more than one*
  saved body is refused (exit 2, `ambiguous_body_backup`) rather than resolved
  to a best guess. Picking for you is how you restore the wrong version onto a
  body you just destroyed.
- **Restoring is itself a replace, so it is undoable.** The body a restore
  overwrites is saved first; restoring the wrong version is not terminal, which
  is what makes trying one safe.
- **`work.edited` now carries `body_backup`**, the path to the saved bytes — so
  the audit line that could previously only *prove* a body was destroyed now
  points at the copy.
- **A backup that cannot be written refuses the edit**, leaving the stored item
  byte-identical. A recovery guarantee that quietly stops holding is worse than
  none, because it is relied upon.

**What this does NOT cover.** Only the wholesale overwrite path.
`--append-body-file` is *not* a replace — it composes against the body on disk
at write time and cannot destroy a section it never saw — so it is already safe
and is deliberately not backed up; nor is `--title`, which rewrites the heading
line in place. Bodies are saved from the first replace-mode edit **after this
shipped**, so an item damaged before then has nothing here.

**Where backups go on a transition.** They are keyed by ID and do **not** move
on claim / unclaim / done / reopen / shelve / unshelve — a shelved item's saved
bodies stay in `work/.bodybak/<id>/` and restore normally. `mg archive` moves
them into `work/archive/<partition>/.bodybak/<id>/` with the record, so the
archive stays self-contained and nothing is orphaned in the live tree; `mg
unarchive` brings them back.

**Deliberately not a heuristic block.** The tempting version is "refuse a
replace that shrinks the body by >90%, or whose content looks like an error
message". A shrink ratio hard-codes a fact about normal edits and decays — a
legitimate rewrite that condenses a bloated body gets refused, which trains
people to reach for `--force`. Content-sniffing for `Error:` fails on any item
that legitimately quotes one; the ticket that asked for this feature would trip
it. A blocking control has to be right about the **future**; a backup only has
to be **cheap**, and it is correct for every failure mode including the ones
nobody predicted — a wrong path, a truncated pipe, an editor crash, a bad `sed`.
A false block costs a real edit and erodes trust in the guard; a useless backup
costs a few KB.

### Who did this: audit attribution

Every state change writes a line to `~/.macguffin/events.jsonl` carrying an
`actor` field. **`actor` is the identity that ran the command** — never a
property of the item it acted on.

That sentence is load-bearing because the field used to mean something else.
Until `mg-3122` it resolved to the item's **assignee**, then its creator, then
the OS user. Measured on the live log, the same command shape on the same item
produced three different answers depending only on who the item was *assigned*
to:

```
assignee unset          work.edited          actor = "daniel"            ← unix user
assignee = parked       work.snooze_elapsed  actor = "parked"
assignee = zzz-probe    work.edited          actor = "zzz-probe"
```

All three read as real answers and two of them were false. In a fleet where the
mayor edits PM-filed tickets, PMs edit each other's, and polecats edit their
own, the log confidently named the wrong actor for every assigned item — which
is strictly worse than an empty field, because nothing in the line tells a
reader a substitution happened.

`actor` now resolves in this order, and consults the item at no step:

| # | Source | When |
|---|--------|------|
| 1 | `MG_ACTOR` | explicit override — a wrapper script or test that knows its identity |
| 2 | `POGO_AGENT_NAME` | set by pogod on every agent it spawns; the one string separating the agents that share this box's unix user |
| 3 | the OS user | nothing more specific is set — **in practice this is pogod**, not a human |
| 4 | `unknown` | nothing else resolved |

Steps 3 and 4 are weak, deliberately. On a single-user box every agent is
`daniel`, so the OS user is vague — but vague is recoverable and a confident
wrong answer is not. The same identity now appears in the mail audit log
(`log/mail-audit.log`), so both logs name a caller the same way.

**Read `actor: daniel` as "pogod, or unknown" — not as Daniel.** pogod spawns
agents with `POGO_AGENT_NAME` set but runs itself with neither that nor
`MG_ACTOR`, so the daemon's own `mg` invocations land on step 3. Measured
2026-07-30 across the live log: **every** `daniel` line was a `work.claim` or
`work.done` written by the daemon's pid — including a `work.claim` on `mg-cb71`
taken while that item had **no assignee at all**, twenty seconds before the
polecat's own re-claim recorded `actor: cb71` correctly.

Those two event types are the dispatch and the completion path, which is exactly
where a reader wants to know who acted. The misreading is not hypothetical in
its `creator` form: on 2026-07-30 the mayor read `creator: daniel` off `mg-75f0`,
concluded a RED finding had already reached Daniel through his own ticket, and
stopped considering escalation until another agent caught it. `actor: daniel` on
a `work.claim` supports the same inference, and pogod having no actor identity of
its own is why the fallback is reachable on that path at all — giving the daemon
one would close it at the source.

**This is not the pre-`mg-3122` assignee behaviour.** A characterization that
circulated in the fleet after `mg-3122` — "the audit log's `actor` records the
item's ASSIGNEE, not whoever acted" — describes lines written *before* that fix
and is wrong about the field now. The `mg-cb71` claim above is the discriminating
case: an assignee-sourced field would have been empty there, and it read
`daniel`. The defect that remains is narrower than that claim and differently
shaped — the field records who acted, and degrades to the OS user when the actor
is pogod.

**Events written before `mg-3122` still carry the old meaning.** The log is
append-only; historical lines are not rewritten. Treat `actor` on a
pre-`mg-3122` line as "the assignee at the time", not as the caller.

#### `creator` is the filer, resolved the same way

An item's `creator` is **whoever ran `mg new`**, resolved by the identical
table above — `MG_ACTOR`, else `POGO_AGENT_NAME`, else the OS user, else
`unknown`. One resolution, two fields, so the item and the event log never
disagree about who a caller is.

Until `mg-ddf4` it was the **unix user alone**. Measured across the live store
on 2026-07-30: **2040 of 2041 items read `creator: daniel`.** Every agent on
this box runs as the same unix user, so the field named that user for a ticket
filed thirty seconds ago by a polecat exactly as it did for one filed last week
by a human. It was constant across the whole store and carried zero
information — while wearing a field name that promises authorship, which is the
same defect as `mg-3122` one field over and from the same cause.

The evidence that this is a property of the artifact rather than of careless
readers: **two agents independently read the field and reached the same wrong
conclusion, an hour apart, with no contact.** One derived a general mechanism
from it ("every ticket Daniel files bypasses my filing path") and wrote it into
durable memory as measurement; the other asserted a filing time and author off
the same field on a different item. A field that produces the same error in two
independent readers is not fixed by asking readers to be more careful.

**Items filed before `mg-ddf4` still read `daniel`,** and nothing distinguishes
those from an item a human really did file. Frontmatter is not rewritten. Treat
`creator: daniel` on a pre-`mg-ddf4` item as **"unknown"**, not as Daniel.

#### `creator` is attribution, not authentication

`POGO_AGENT_NAME` is an ordinary environment variable and every agent on this
box authenticates as the **same unix user**. Any agent can therefore file as any
name, and process ancestry is the only unforgeable signal — one mg does not
consult. So:

- **`creator` is self-asserted and forgeable. Never gate access on it.** It
  answers "who says they filed this", which is enough for routing, triage and
  reading a history, and is not enough for authorization.
- A mechanism that routes on "who filed this" is now *implementable*, where
  before `mg-ddf4` it was not — but only on that self-asserted basis, and only
  for items filed after the fix.

The same caveat already applies to `mg mail send --from`, which is likewise a
caller-supplied string.

#### Every field that names an actor

Audited in the `mg-ddf4` pass. The rule the table encodes: a field may be
populated from the **caller's** identity or from an **explicit argument**, never
from the item it acts on and never from a process property standing in for a
name it cannot know.

| Field | Where | Populated from | Status |
|-------|-------|----------------|--------|
| `actor` | `events.jsonl` | `MG_ACTOR` → `POGO_AGENT_NAME` → OS user | fixed in `mg-3122`; still degrades to `daniel` when the caller is **pogod**, on `work.claim` / `work.done` |
| `creator` | item frontmatter, `mg show` | same resolution | fixed in `mg-ddf4` |
| `caller` | `log/mail-audit.log` | `POGO_AGENT_NAME`, else `-` | correct — degrades to `-`, not to a wrong name |
| `from` | mail messages | the required `--from` flag | explicit; self-asserted, never inferred |
| `assignee` | item frontmatter | the `--assignee` flag | a routing target, not a claim about who acted |

Two near-misses that are **not** defects, recorded so the next audit does not
re-open them: `mg list --assignee=human` and the blue `human` label resolve the
literal token `human` against the OS user (`cmd/mg/list.go`) — that is
deliberately a question about the unix user, not about who acted. And `mg spend
--by=...` is a grouping axis whose `by` is a column name, not an identity.

**The half with no field at all** is the social one — who wrote a memory note,
who proposed a rule, who made a call in a mail thread. There is nothing there to
correct, and the same inference-filling happens. The cheap remedy, no code
required: **when attributing something, cite where you read it.** "You wrote X
in your 04:11 mail" is checkable by the person being credited; "your framing" is
not, and the second form is what decays into a wrong attribution a third agent
then inherits.

#### Metadata edits are logged

`work.edited` used to fire only when the **body** changed. `mg edit <id>
--assignee=X` and `mg edit <id> --priority=high` both printed `Updated <id>`
and wrote nothing at all — the log recorded exit 0 as if nothing had happened.
That mattered most for `assignee`, which is the **dispatch gate**: `human` and
`parked` suppress both stall-watch and dispatch, so the single field deciding
whether an item is ever worked on could be flipped by any agent with no audit
record whatsoever.

A metadata-only edit now emits `work.edited` with `mode=metadata`, a `fields`
list naming what moved, and a `<field>_before` / `<field>_after` pair for each:

```json
{"ts":"2026-07-29T16:04:00Z","type":"work.edited","item_id":"mg-ad6b",
 "actor":"cat-3122","mode":"metadata","fields":"assignee",
 "assignee_before":"mayor","assignee_after":"parked",
 "body_hash_before":"a1b2c3d4","body_hash_after":"a1b2c3d4",
 "lines_before":"42","lines_after":"42","guarded":"false",
 "body_read_state":"not_at_risk"}
```

Tracked fields: `title`, `type`, `repo`, `assignee`, `priority`, `budget`,
`depends`, `tags`. Notes on reading these lines:

- **`fields` is in a fixed order**, not map order, so two identical edits
  produce identical lines and a diff of the log is readable.
- **The body hashes are still emitted, and are equal.** That is the positive
  statement "the body was not at risk on this write" — an absent field could
  not make it.
- **A cleared field is a change like any other.** `assignee_before=parked`,
  `assignee_after=` is how you find an item being un-parked.
- **An unset budget is `""`, not `"0"`** — `--budget=0` is the flag that
  *unsets* one, so the two must stay distinguishable.
- **A no-op emits nothing.** Setting a field to the value it already holds
  changes nothing on disk and manufactures no audit line; a log that records
  non-events is a slower way to be untrustworthy.

#### Lost updates are *unmeasurable*, not measured-as-zero

`body_hash_before` looks like it answers "did this write clobber someone?" It
cannot. It is read from the stored body **inside** the edit — the state at *write*
time, never what the caller read — so it always equals the previous line's
`body_hash_after`, and any "zero clobbers" figure computed from the pair is true
**by construction**. That figure was computed over the live log, and retracted.

The record captures what a write overwrote. Nothing captured what the writer
*believed* it was overwriting, so these two were identical in the log:

```
a deliberate rewrite of the body as it currently stands
a rewrite by someone whose read was ten minutes and three edits stale
```

Every `work.edited` line therefore carries **`body_read_state`**:

| value | meaning |
|---|---|
| `asserted` | the caller named the body version it believed it was overwriting (`--if-unchanged`). The value is alongside as `body_hash_asserted`. |
| `unmeasurable` | a full-body replacement landed with no such record. Whether it destroyed an unseen write is **not derivable from this log, in either direction** — it is neither evidence of a clobber nor evidence of none. |
| `not_at_risk` | an append (composes against the body on disk), a metadata edit, or a title rewrite: no body section could be lost. |

93 of 138 measured replacements supplied no `--if-unchanged`, and those are the
`unmeasurable` ones. Recording the absence *explicitly* is the whole point: a
silent gap reads as clean, and read as clean it was. No behaviour changed, and
the question is answerable for writes made from here on — lines written before
`2026-08-12` carry no such field, which is itself unmeasurable.

The field is about the **body**, as its name says. A metadata edit is
`not_at_risk` because no prose was at stake; the assignee it moved is guarded by
`--if-assignee` and recorded by `fields` / `assignee_before` / `assignee_after`
on the same line.

### `--json` output contract

Commands that support `--json` follow one convention so scripts and dashboards
can parse them uniformly: **collections emit NDJSON** (one JSON object per line —
`list`, `spend`, `event list`) and **single items emit one JSON object**
(`show`, and `flow`, which marshals one snapshot object). Field names are a
**frozen, additive-only** contract: new fields may be added over time, but
existing ones are never renamed or removed.

### `mg schema` contract

`mg schema` emits **one JSON document** describing the whole command tree, so
agent/tooling consumers can discover mg's surface without scraping `--help`. Its
shape follows the same discipline as `--json`: field names are **frozen and
additive-only**, and `schema_version` is bumped only on a breaking shape change.

`schema_version` gates the **shape only**. Two things it deliberately does *not*
gate — a consumer diffing the command surface for stability must **ignore** both:

- **`version`** (top-level) is **build metadata** — the mg binary's version
  string. It changes every release and describes nothing about the command
  surface, so exclude it from surface diffs.
- Each command's **`mutates` / `idempotent`** booleans are **advisory** planner
  hints ("is this safe to retry?"). Their *values* may be refined between
  releases **without** a `schema_version` bump; only their presence and type are
  frozen. Where idempotency is flag-dependent (e.g. `edit --title` converges but
  `edit --add-tags` accumulates) the hint takes the conservative value (`false`).

## Directory Layout

```
~/.macguffin/
├── work/
│   ├── available/        # Unclaimed work items
│   ├── pending/          # Items waiting on dependencies that can still be met
│   ├── claimed/          # Atomically moved here on claim (PID-suffixed)
│   ├── done/             # Completed items + result sidecars
│   └── archive/          # Archived done items (date-partitioned)
├── mail/                 # Maildir-style per-agent inboxes
│   └── <agent>/
│       ├── new/          # Unread messages
│       └── cur/          # Read messages
├── spend/                # Harvested token-spend store (by-item/, by-agent/)
├── log/                  # Append-only event log
└── .git/                 # Optional: cold-path audit trail
```

### Reading a result sidecar

A completed item's result lives in `<id>.result.json` **beside that item's
`.md`**, and it moves with the `.md` on every status transition. **Ask mg where
it is. Never build the path yourself:**

```sh
mg sidecar mg-560d            # the result, on stdout            # RIGHT
mg sidecar mg-560d --path     # the absolute path, nothing else
mg show mg-560d --json        # result_path and result, with the item
```

`mg sidecar` resolves the item and reads the file beside it. It never scans and
never returns a candidate list, so it follows an item into
`work/archive/2026-08/` without knowing that the archive is partitioned at all.
Its three outcomes are deliberately distinct — **exit 0** with the result on
stdout, **exit 3** (`no_sidecar`) with *nothing* on stdout when the item
recorded none, **exit 1** (`io_error`) when the store could not be read — so a
caller cannot render "there is none" and "I could not look" as the same empty
string.

The hand-built path is wrong twice over, and the second one is the one that
bites:

```sh
ls ~/.macguffin/work/*/mg-560d.result.json | head -1   # WRONG
```

* **The archive is nested by month.** That pattern is *one level* deep, and an
  archived sidecar is at `work/archive/2026-08/mg-560d.result.json`. It cannot
  match one, ever — and the archive holds the overwhelming majority of the
  store's sidecars. The glob does not fail *into* your result; it fails *beside*
  it, and what lands in your result is the empty set. On 2026-08-13 two agents
  ninety minutes apart published "no sidecar" for items that had one, both
  recording `verdict=pass`.
* **`archived` is a status, not a directory.** The directory is `archive`.
  `work/archived/` matches nothing, silently — that was the second of those two
  false negatives.
* **Alphabetical order.** Among what a one-level glob *can* see, `available/`
  and `claimed/` sort ahead of `done/`, so a stale copy left by a
  pre-`mg-ab67` transition is returned first and reads as current.

The old recipe here — `status=$(mg show ID --json | jq -r .status)` then
`work/$status/ID.result.json` — is broken for exactly the same reason: it yields
`work/archived/` for an archived item. Use `mg sidecar`.

`mg done --result` **merges into** any result already recorded for the item
rather than replacing it, so completing an item cannot silently discard what the
work found:

```sh
# the agent records its findings while the item is claimed
echo '{"kind":"investigation","summary":"..."}' > ~/.macguffin/work/claimed/mg-560d.result.json
mg done mg-560d --result='{"branch": "polecat-560d"}'
# done/mg-560d.result.json now holds kind, summary AND branch
```

The merge is a shallow, key-by-key union in which `--result` wins: keys it sets
are overwritten, keys it says nothing about survive. If the two cannot be merged
— either side is valid JSON but not an object — `mg done` refuses and changes
nothing, leaving both copies on disk to reconcile by hand.

`mg sidecars` (plural) is an **integrity scan over the whole store**, not a
lookup: it finds strays left behind by older versions. To read ONE item's
result, use `mg sidecar` (singular). Note that claimed items carry a PID suffix
(`<id>.md.<pid>`), so "sidecar with no matching `<id>.md`" is not a usable test
for orphanhood in `claimed/` — use `mg sidecars`, which resolves the item rather
than pattern-matching filenames.

Each stray is compared by **content**, not by bytes, because the safe action
differs by case and a single `differs` verdict forces the operator to open both
files — which is where reconciliation errors come from:

```
  ~/.macguffin/work/claimed/mg-a6c9.result.json
      item is archived; vs ~/.macguffin/work/archive/2026-07/mg-a6c9.result.json
      SUBSET — the authoritative copy is the superset; it agrees on every shared key
      only in the authoritative copy: branch, completed_by, mr
      mechanically mergeable: keeping the authoritative copy loses nothing the stray holds
      but confirm the subset is not a TRUNCATION before discarding it
```

Naming the keys is the point: `differs` tells you to look, `conflict: branch,
completed_by, mr` tells you what you are choosing between. Two verdicts are not
differences at all — `equivalent` is the same object re-serialised (common,
since anything through `encoding/json` normalises key order) and needs no human,
and `unknown` means a file could not be **read**, which is a failed probe rather
than a finding. The tool classifies and never merges or deletes.

Work items are Markdown files with YAML frontmatter — human-readable,
machine-parseable, and diffable. Claiming is a single `rename(2)` syscall:
if two processes race, exactly one wins. The loser gets `ENOENT`. No locks,
no retries, no database.

### Workspace root

Every `mg` command reads and writes exactly one store. It is resolved in this
order, highest precedence first:

| Precedence | Source | Example |
|---|---|---|
| 1 | `--root` flag | `mg --root=/tmp/scratch new "item"` |
| 2 | `MG_ROOT` environment variable | `MG_ROOT=/tmp/scratch mg new "item"` |
| 3 | default | `$HOME/.macguffin` |

`MG_ROOT` is the only environment variable `mg` consults for this. An empty or
unset `MG_ROOT` falls through to the default. Relative paths are resolved to
absolute once, when the command starts.

**`cd` does not isolate `mg`.** The working directory is never consulted; `mg`
does not walk up from `$PWD` looking for a workspace the way `git` finds `.git`.
Anything that must run `mg`'s mutating verbs against a throwaway store — a test,
a smoke script, an agent — has to say so explicitly:

```sh
export MG_ROOT=$(mktemp -d)
mg init
mg new "safe to clobber"   # never touches ~/.macguffin
```

Without this, `mg claim` and `mg done` operate on the shared store, and an `mg
list --json | head -1` picks a live work item belonging to someone else.

Two commands pass their arguments through verbatim (`mg event append`, `mg log`)
and so do not bind `--root`; use `MG_ROOT` to redirect those. `mg event append`
rejects `--root` outright rather than record it as event data.

## Concepts

### Assignee

A work item's `assignee` names the agent that **owns triage and routing**
for the item — not the agent that runs the work. Substantive work is
performed by an ephemeral polecat (a one-shot agent named after the
work-item ID at spawn time) regardless of who the assignee is. Polecats
are never named in advance, so they cannot be assigned ahead of time.
The assignee is the durable owner who decides whether to dispatch the
work to a polecat, hold it, or close it without execution.

In practice this means a ticket assigned to `mayor`, a PM agent, or
`human` will still typically be executed by a polecat once the assignee
decides to dispatch it.

### Repo metadata

`mg new` records a `repo` breadcrumb on the work item — the code repository
the item is about. By default it auto-detects this from the current git
toplevel (`git rev-parse --show-toplevel`), so a developer filing from inside
a project repo gets it filled in automatically.

Auto-detection is **skipped under pogo automation** (when `POGO_PID` is set in
the environment). A crew agent or polecat files from its own prompt/work
directory, whose git toplevel is the agent's scratch dir — not the code repo
the item concerns — so auto-detecting there would record a misleading path.
Automated filers should pass `--repo=PATH` explicitly when the item targets a
specific code repo.

Override the default with `--repo=PATH`, or opt out entirely with `--no-repo`
(or `--repo=""`).

#### Ephemeral repo paths are refused

The breadcrumb has to outlive the filing, and two trees under the pogo home do
not: `~/.pogo/polecats/` and `~/.pogo/refinery/worktrees/` hold one disposable
git worktree per agent, deleted when that agent is reaped. A `repo` that resolves
inside either is **refused** (exit 2, code `ephemeral_repo`):

```
$ mg new --title="filed from a polecat worktree"
Error: refusing to record repo /Users/you/.pogo/polecats/0b57: it is inside
~/.pogo/polecats, which pogo deletes when the agent owning it is reaped — the
item would outlive the path and fail only when someone dispatched it
  → --repo was omitted, so mg resolved it from the current directory. Pass the
    durable repo the item is actually about — this worktree was created from
    /Users/you/dev/macguffin, so --repo=/Users/you/dev/macguffin; or --no-repo
    if the item is not about a code repo; or --allow-ephemeral-repo to record
    this path anyway.
```

This is a delayed, silent failure otherwise: the path is real at filing time and
stays real while the worktree lives, and breaks only when the item is dispatched
*after* the reap — handing a polecat a repo that no longer exists, for an item
filed weeks earlier by an agent nobody can ask.

The guard keys on the **resolved path**, so it catches an explicit
`--repo="$(pwd)"` as well as an omitted flag: both file the same doomed item, and
"remember to pass the right `--repo`" is the kind of convention this refusal
exists to replace. It deliberately does *not* rewrite the path to the worktree's
origin repo — it only **names** that origin in the hint, so the substitution is a
copy-paste the filer confirms rather than a guess mg makes on their behalf.

Note this is a separate rule from the `POGO_PID` one above, and a strictly
broader one: `POGO_PID` is not set in a polecat's environment at all, so that
check never fired for the fleet's most common filer. A path is an observation
about where the repo is; an environment variable is a hope that whoever spawned
the process exported the right name.

Pass `--allow-ephemeral-repo` for the genuine exception — an item that really is
about a particular worktree. Existing items are untouched: a stale path on an
already-filed item is a true record of where it was filed.

### Token spend accounting

`mg spend` aggregates how many tokens agents have consumed, grouped by work
item, tag, repo, agent, priority, or assignee. It reads Claude Code transcripts
(`~/.claude/projects/`) plus the macguffin event log, joins each assistant
message to the work item that was claimed at the time, and writes per-item
NDJSON to `~/.macguffin/spend/`.

```bash
mg spend                    # per-item totals + a grand-TOTAL row (default)
mg spend --by tag           # roll up by tag
mg spend --since 7d         # rolling window: the last 7×24 hours
mg spend --window today     # calendar window: since local midnight
mg spend --window week      # calendar window: since this week's Monday
mg spend --total            # headline: today / this week / all time
mg spend --json             # machine-readable output for dashboards
```

**Total from a single command.** Every grouped view ends with a `TOTAL` row
that column-sums the rows shown — so bare `mg spend` answers "how many tokens
in total?" without a flag. For the default per-item (and per-agent) view each
record is counted once, so the `TOTAL` row is the true grand total. `--json`
mirrors it as a trailing object with the reserved key `TOTAL`; the payload
stays a JSON array of the same shape, and the key is uppercase so it never
collides with a (lowercase) item id, tag, repo, agent, priority, or assignee —
consumers that select groups by key are unaffected. For a time-bucketed
headline of today / this week / all time in one shot, use `--total`.

**Windows.** `--since` and `--window` bound the same data two different ways
and are mutually exclusive. `--since D` is a *rolling* window ending now
(`--since 24h` = the last 24 hours, moment to moment). `--window` is
*calendar-anchored* in your machine's local time: `today` starts at local
midnight, `week` starts at the most recent Monday (ISO-8601 week start). Use
rolling windows for "recent activity" and calendar windows for "this
day/week's bill." Note that `--window week`'s Monday anchor is a fixed
calendar convention — it *approximates* a weekly-usage view but does **not**
track Anthropic's actual weekly usage-limit reset (which is account-specific
and not exposed here); see the limit-meter caveat below.

**What "historical" means here.** Spend is tracked **only once harvested**.
Harvesting runs automatically at the start of every `mg spend` invocation, so
running the command is what advances the record. The harvest is incremental
and idempotent — it keys on `(session, message-uuid)`, so re-runs skip
already-counted messages. For continuous capture without having to run the
command by hand, schedule it (e.g. a cron entry or `pogo schedule` running
`mg spend` periodically); otherwise a window is only as complete as the last
time the command ran.

Once a message has been harvested, its record lives in `~/.macguffin/spend/`
and **survives Claude Code restarts, `mg` upgrades, and transcript rotation** —
the store is the source of truth, not the transcripts. The one thing that
loses data is **deleting a transcript before it has been harvested**: unharvested
tokens in a deleted transcript are gone, because the store never saw them. If
you rotate or prune `~/.claude/projects/` aggressively, harvest first.

**Attribution degrades gracefully; tokens are never dropped.** A message is
attributed to a work item by joining it to the claim interval that was open
when it was written (with pogod's live agent registry as a fallback). If a
polecat has already been reaped by harvest time and left no closing claim
event — so neither the interval join nor the registry can name its item — its
tokens are **still counted**, they just land in the per-agent overhead bucket
(`by-agent/<name>.jsonl`, visible under `mg spend --by agent`) instead of
against the item. Harvesting promptly (or on a schedule) keeps attribution
fine-grained; the grand total is unaffected either way.

**This is a single-machine tally.** The store lives under `~/.macguffin/` on
one host and only sees that host's transcripts. There is no cross-machine
aggregation.

**What it measures — and does not.** `mg spend` measures **token consumption
recorded in transcripts** (input, cache-read, cache-create, output). It is
**not** a read of Anthropic's usage-limit meter — the two can diverge (see
[pogo #45](https://github.com/drellem2/pogo/issues/45) for the limit-meter
discussion). Precise cross-project or per-account cost reconciliation is
explicitly **out of scope**: an API proxy that meters requests at the wire is
the right tool for that job. Treat `mg spend` as a faithful attribution of
*where transcript tokens went*, not as a billing ledger.

## Project Structure

```
cmd/mg/          # CLI entry point and subcommands
internal/
  workitem/      # Work item creation, parsing, ID generation
  workspace/     # Directory layout, init, git operations
  mail/          # Maildir-style message delivery
  event/         # Structured event logging
  spend/         # Token-spend harvester, store, and aggregation
```

## Utilities

Standalone tools built on `mg`. Open a PR to add yours — one bullet per utility.

- [mg-roadmap](https://github.com/drellem2/mg-roadmap) — aggregates `mg` work items into a product-line → initiative → item roadmap, with token budget vs. actual spend per line.

## License

Licensed under the GNU General Public License v3.0 — see [LICENSE](LICENSE).
