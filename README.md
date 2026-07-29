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
```

Supports Linux (amd64, arm64), macOS (amd64, arm64), and FreeBSD (amd64).

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
| `mg show <id> [--json] [--body-hash]` | Display a work item by ID. `--json` emits the full item as one JSON object (adds `creator`, `body`, `body_hash`, `budget`, `spent` to the `list --json` field set). `--body-hash` prints only the body's SHA-256 — the version token `mg edit --if-unchanged` checks against — so a guarded write is one command rather than a pipe through `jq`. When two archived twins share a short ID across partitions, disambiguate with `mg show <id>@<partition>` (e.g. `mg show mg-4fa7@2026-04`). |
| `mg list [--json]` | List work items. `--json` emits NDJSON (one JSON object per line) for scripts and dashboards. |
| `mg claim ID` | Atomically claim a work item by ID. |
| `mg done ID` | Mark a claimed work item as done. **Refused** if the item declares a remainder and nothing is tracking what it recommends — see the two rows below. |
| `mg new --no-declares-remainder` | An item whose **output is a recommendation** — a triage verdict, a design, a proposal — carries a `declares-remainder` tag. Its build is undone by construction the moment it completes, so `mg done` refuses the item until a successor names what carries it forward. **mg emits the tag by default**, from the type (`--type=design`, `scoping`, `audit`, `idea`) and from a body whose leading carrier block says `stage: triage` — a triage being a workflow position rather than a type. `--no-declares-remainder` is the escape for the genuine exception; `--declares-remainder` forces it on for a type mg does not default (a task that ends in a proposal). Add it to an item that already exists with `mg edit <id> --add-tags=declares-remainder`; retract a declaration that turned out to be wrong with `--rm-tags=declares-remainder`. The default reads the type, but **the guard never does** — `mg done` fires on the tag the item carries, so a wrong default is visible in the item's own text instead of silent at completion time. |
| `mg done <id> --successor <id>` | Discharges the declaration: it records a `successor:<id>` tag on the item **before** completing it, so the completed record names its own tracker for any later reader. The id must resolve to a real item and cannot be the item itself — a pointer at nothing and a pointer at oneself both track nothing. The guard fires **only on the declaration**, never on `type` or on a body `stage:` value: those are proxies, and both were tried. `type: design` misses a triage (mg-ee98 was a `type: task` triage whose verdict was IMPLEMENT on a reproduced data-loss bug, and nothing carried the fix). "Non-terminal `stage:`" over-fires, because a stage-shaped **pause** owes nothing while a stage-shaped **gate** does, and no stage value distinguishes them — across the whole store on 2026-07-29 that predicate would have fired on 41 items, 34 already archived. An item that never declares completes exactly as it always has, so the guard fails in the safe direction. There is no `--force`: the declaration is opt-in, and retracting a wrong one is the documented correction. |
| `mg edit ID [flags]` | Update fields on an existing work item. **Adding to a body? Use `--append-body-file`, not `--body-file`** — see [Concurrent edits](#concurrent-edits-lost-updates). |
| `mg restore-body ID [--list] [--from=STAMP]` | Put back a body that a replace-mode edit overwrote. Every `mg edit --body`/`--body-file` saves the body it is about to destroy first; `--list` shows what is saved (newest first) and a bare invocation restores the most recent. An item with **nothing saved is an error** (exit 3, `no_body_backup`), never a quiet success and never an empty body. See [Recovering an overwritten body](#recovering-an-overwritten-body). |
| `mg archive [ID]` | With an `ID`, archive exactly that one done item and nothing else — the form to use for items the refinery never merged (investigations, evaluations). With no `ID`, sweep `done/` and archive every item older than `--days` (default 7; `--days=0` takes **all** done items). The two forms are exclusive: passing both an `ID` and `--days` is an error, never a silent choice between archiving one item and archiving every item. `--dry-run` previews without moving anything. A targeted archive that cannot act (unknown ID, item not done) exits non-zero and says why. **Three guards refuse an archive**: done `type: design` items with no successor, items of any type tagged `blocked-on-*`, and items tagged `declares-remainder` whose successor is missing or has been deleted — see below. The last is a *backstop*; `mg done` refuses first, at the moment, to the agent standing there. |
| `mg archive <id> --successor <id>` | A design's output *is* a recommendation, so at the moment it is done the thing it recommends is undone by construction — and an archived item cannot be the tracker for undone work. Archiving a done `type: design` item is therefore **refused** unless it names a successor. `--successor <id>` records a `successor:<id>` tag on the design before moving it, so the archived record names its own tracker; the id must resolve to a real item and cannot be the design itself. The check is on the item **type**, not on any wording in its body, and is satisfied **only** by that structured tag — a body that merely mentions an id proves nothing about who is tracking what. The sweep form never forces: it skips guarded items, leaves them in `done/`, and names them on stderr. A design that was *abandoned* rather than implemented is a legitimate archive — `--force` takes it and records a `work.archive_forced` event naming the guard that was bypassed. `--force` is documented here and in `mg archive --help`, and is deliberately **not** named by the refusal itself, so a guard hit mid-cleanup does not hand out its own bypass. |
| Archiving an item tagged `blocked-on-*` | A `blocked-on-*` tag says **a person still owes something here**, and an archived item cannot be the tracker for outstanding work — so archiving one is **refused**, whatever its type. The refusal **names the tag it found** (`blocked-on-daniel`, `blocked-on-daniel-confirm`, …), because "blocked" and "no successor" have different remedies and an operator has to be able to tell which fired. The remedy is to settle what the tag names and then `mg edit <id> --rm-tags=blocked-on-<who>`. The check is on the **tag**, not on any wording in the body: the tag was already a live convention and already queryable across every status (`mg list --tag=blocked-on-daniel` with no `--status` spans statuses and finds `done` items), so this only teaches the *destructive* operation to read an index that already existed. `--successor` does **not** satisfy it — naming a tracker for a recommendation says nothing about whether a person still owes something. `--force` **does** apply, as for the successor guard: an obligation can be discharged out of band and the tag left behind, and without a recorded escape hatch the operator strips the tag by hand — the same bypass with none of the audit trail. A forced archive records a `work.archive_forced` event with `reason: blocked_on_tag`, and the refusal itself does not mention `--force`. The sweep skips blocked items, leaves them in `done/`, and names each one on stderr **with the reason it was refused**. |
| `mg unarchive ID [--status=S]` | Restore an archived work item to the status it held when archived. Refuses if that status is unknown — pass `--status` to say where it belongs. |
| `mg unclaim ID` | Release a claim, returning the work item to `available/`. |
| `mg schedule` | Promote pending items **whose gates have all opened** — every dependency met, and any `snooze:` wake time now past — **and report the pending items no completion can ever promote** — those waiting on a `shelved` or nonexistent parent, each named with the parent responsible. A stranded item is invisible from every other angle: it is not `available/`, so stall-watch and priority-wake cannot see it, and `pending` is exactly what a correctly-waiting item looks like. This is the only view that tells the two apart. |
| Dependency satisfaction | A dependency is met once the parent **has passed through `done`** — `done/` and `archive/` both count. Archiving is a filing decision about completed work, not a repudiation of the completion, so archiving a parent never strands its dependents. Placement is decided **at filing time** against the resolved state of every dependency, not deferred to the next sweep: an item whose dependencies are already satisfied is filed straight to `available/`, never parked in `pending/` to wait on an unrelated completion. |
| `mg snooze ID (--until TIME \| --for DURATION)` | Set an item aside until a time. It moves to `pending/` carrying a `snooze:` attribute and returns to `available/` on the first `mg schedule` sweep after that time. **Snooze is an attribute, not a sixth status** — see [Snooze](#snooze-not-now-come-back-at). `--for` takes Go durations plus `d`/`w` (`90m`, `6h`, `3d`, `2w`); `--until` takes RFC3339 or a local `2026-08-03 14:30`, and a bare date means **09:00 local**, never midnight. The resolved absolute instant is echoed back and stored as RFC3339 UTC. Snoozing a claimed item releases the claim, as shelving does. |
| `mg unsnooze ID` | Lift a snooze early. A pending item returns to `available/` if its dependencies are also met and stays `pending` if they are not — lifting one gate does not lift the others. Refuses an item that is not snoozed. |
| Depending on a `shelved` item | Shelving means **"not now"**, not "cancelled". A new item filed onto a shelved parent is filed as **`shelved`**, alongside its parent — not released, and not left in `pending/` masquerading as an item that is waiting correctly. This is the same treatment `mg shelve` already gives dependents that exist when the parent is shelved, so `shelved` means the same thing whichever side of the parent's shelving the dependent was created on. `mg new` **says so**, naming the gate and the `mg unshelve <id>` that lifts it. `mg unshelve` on the parent brings dependents back as `pending`, and completing the parent then releases them normally — the chain is parked, never destroyed. |
| `mg mail send\|reply\|list\|read\|archive\|migrate` | Maildir-style messaging between agents. `archive AGENT/MSG-ID` moves a message out of the active mailbox; `list AGENT --archived` inspects archived messages. Each operation logs a `mail.sent`/`mail.read`/`mail.archived` event to `events.jsonl` and a caller-attributed line (pid + `POGO_AGENT_NAME`) to `log/mail-audit.log`; malformed message files are skipped loudly (`mail.malformed` event, stderr warning, count in `list` output). `read` and `reply` refuse to touch another agent's mailbox when `POGO_AGENT_NAME` is set (both mark the message read for its owner) unless `--force` is given; denials and forced reads are audited too. |
| Canonical mailbox addressing | Every recipient/mailbox argument is canonicalized before it resolves to a directory: the harness prefixes `mg-` and `cat-` are stripped, so the work-item alias `mg-<id>`, the process name `cat-mg-<id>`, and the bare live id `<id>` all address the **same** mailbox. This closes the silent-drop class where mailing `mg-<id>` (the spelling many templates use) minted a stray mailbox nobody read while a watcher polling `mg mail list mg-<id>` waited forever. Crew mailboxes (`mayor`, `architect`, …) carry no prefix and pass through unchanged. `mg mail migrate` (`--dry-run` to preview) is a one-shot, idempotent cleanup that merges any pre-existing stray `mg-<id>` mailbox into its canonical `<id>` box — unread, read and archived mail alike — preserving read state and then removing the emptied stray directory. |
| `--append-body-file PATH` / `--append-body TEXT` (`mg edit`) | Append to the existing body instead of replacing it. Reads verbatim, same as `--body-file` (`-` for stdin). An append composes against the body **on disk at write time**, so it cannot destroy a section the caller never saw — see [Concurrent edits](#concurrent-edits-lost-updates). The appended text is stored byte-for-byte; only the join is normalized, to exactly one blank line, so a leading `## heading` renders as a heading. Mutually exclusive with `--body`/`--body-file` (exit 2). |
| `--if-unchanged HASH` (`mg edit`) | Refuse the edit (exit 4, `body_changed`) unless the stored body still hashes to `HASH`, from `mg show ID --body-hash`. **Opt-in**: without it, `--body-file` behaves exactly as it always has. The hash covers the stored body *including* its `# Title` heading, so it also catches a competing `--title`. A prefix of 8+ characters is accepted. |
| `--body-file PATH` (`mg new`, `mg edit`, `mg mail send`) | Read the body **verbatim** from a file (`-` for stdin) instead of from `--body`. Mutually exclusive with `--body` (exit 2); an unreadable path is an error, **never** a silently empty body. Reach for it whenever the body's exact text matters. The shell expands `` `backticks` ``, `$VAR` and `$(cmd)` inside `--body="..."` **before mg runs**, so those terms are silently gone from the item or message while mg still reports success — and mg cannot detect this, because the mangled string is byte-identical to one you typed that way. `--body-file` puts no shell in the body's path at all. `--body` is unchanged and **not** deprecated: it stays correct for bodies carrying no metacharacters. |
| Derived mail subject (`mg mail send`) | `--subject` is **optional**. Omitted, the subject is the body's **first line** (RFC822 / git-commit convention), so it rides inside the same quoted heredoc as the body — the safe spelling is now the short one. This matters because a subject can only be given inline, and no quoting survives ordinary prose: double quotes lose `` `backticks` ``/`$VAR`/`$(cmd)`, and single quotes lose apostrophes **silently**, since English carries them in pairs (`--subject='the rock'n'roll release'` is balanced, exits 0, and delivers `the rocknroll release`). The derived subject is **echoed back** (`Subject: …`, plus `subject`/`subject_derived` under `--json`) — a derivation nobody can see is the defect, not the cure. The first line is **copied, not consumed**: the body arrives whole. A blank or control-character-bearing first line is **refused** (exit 2) naming `--subject`, rather than writing a malformed header. Passing `--subject` is unchanged, empty-value refusal included; `mg mail reply` is untouched. |
| Mail threading | Every message carries a `Message-Id` equal to its MSG-ID — which is its maildir file name, not a second id space. `mg mail send --in-reply-to MSG-ID` is the explicit, stateless primitive: it stamps `In-Reply-To` and seeds `References`. `mg mail reply AGENT/MSG-ID` wraps it, resolving the recipient, the `Re:` subject and the ancestry chain from the original, marking it read but **not** archiving it. `References` keeps the 20 most recent ids so message files stay cat-able. Nothing is inferred from read history — read and send are separate processes, so threading is always explicit. Message ids are minted **per delivery**: they round-trip for threading within one mailbox and are not globally unique. Header values may not contain CR, LF, or other control characters (exit 2, `invalid_header_value`) — an unsanitized newline would inject arbitrary headers. |
| `mg event append <type> [--key=value ...]` | Append a structured event to `events.jsonl`. |
| `mg event list [--type=T] [--since=TS] [--tail=N] [--json]` | List events with optional filtering. Output is already NDJSON; `--json` is accepted for consistency (no behavior change). |
| `mg flow [--live] [--repo=P] [--blocked-after=D] [--group-by AXIS] [--json]` | Per-status flow view: throughput, median age, bottleneck, blocked chains. `--json` emits the computed snapshot as one JSON object under a single stable schema — the same top-level keys for the status and `--group-by` paths; the grouped path fills `groups` and leaves the status-only fields empty. |
| `mg spend [--by AXIS] [--since D] [--window W] [--total] [--json]` | Aggregate token consumption per item, tag, repo, agent, etc. `--since` is a rolling duration (`24h`, `7d`); `--window today\|week` is calendar-anchored; `--total` prints the today/this-week/all-time headline. See [Token spend accounting](#token-spend-accounting). |
| `mg snapshot` | Commit a git snapshot of current state. |
| `mg log [args]` | Show snapshot history (passes args to `git log`). |
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

**`mg schedule` is the driver, and it must be run on a clock.** A gate is worth
only as much as the thing that opens it, and a snoozed item is invisible from
every other angle — it is not `available/`, so stall-watch and priority-wake
cannot see it, and `pending` is exactly what a correctly-waiting item looks like.
Three properties make it impossible to set a gate nothing will open:

- **Level-triggered, never edge-triggered.** The sweep asks whether the wake
  time has *passed*, not whether it just *arrived*. A driver down through the
  wake instant therefore **delays** an item and can never lose one; one late
  sweep recovers it.
- **Loud at snooze time.** A wake time that has already passed, or that mg
  cannot parse, is refused when you set it — not written and forgotten. A
  `snooze:` value that reaches disk unparseable anyway (a hand-edit) **holds**
  the item and is **named** by `mg schedule`'s stranded report and by `mg show`.
- **No driver, no gate.** `mg schedule` stamps `work/.last-sweep` on every run.
  If nothing has driven the sweep in the last two hours, `mg snooze` **refuses**
  and prints the command that wires the driver. `--force` overrides with a
  warning.

Register the driver once — pogod, because it persists schedules to disk and
replays them through host sleep, NTP steps and its own restarts:

```sh
scripts/install-snooze-driver.sh     # AGENT=… CRON=… to override
```

`mg schedule` also lists what is still snoozed and when each item wakes, because
a population nobody can enumerate is a population nobody audits.

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
| 3 | the OS user | a human at a terminal |
| 4 | `unknown` | nothing else resolved |

Steps 3 and 4 are weak, deliberately. On a single-user box every agent is
`daniel`, so the OS user is vague — but vague is recoverable and a confident
wrong answer is not. The same identity now appears in the mail audit log
(`log/mail-audit.log`), so both logs name a caller the same way.

**Events written before `mg-3122` still carry the old meaning.** The log is
append-only; historical lines are not rewritten. Treat `actor` on a
pre-`mg-3122` line as "the assignee at the time", not as the caller.

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
 "lines_before":"42","lines_after":"42","guarded":"false"}
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
`.md`**, and it moves with the `.md` on every status transition. So the
directory a sidecar sits in is part of its identity, and **globbing across
statuses is unsafe**:

```sh
ls ~/.macguffin/work/*/mg-560d.result.json | head -1   # WRONG
```

Shell globs expand in alphabetical order, so `available/` and `claimed/` are
returned **ahead of** `done/`. A stale copy left by a pre-`mg-ab67` transition
wins, and it reads as current — there is nothing in the file saying otherwise.
Ask where the item is, then use that explicit path:

```sh
status=$(mg show mg-560d --json | jq -r .status)       # RIGHT
cat ~/.macguffin/work/"$status"/mg-560d.result.json
```

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

Run `mg sidecars` to find strays left behind by older versions. Note that
claimed items carry a PID suffix (`<id>.md.<pid>`), so "sidecar with no
matching `<id>.md`" is not a usable test for orphanhood in `claimed/` — use
`mg sidecars`, which resolves the item rather than pattern-matching filenames.

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
