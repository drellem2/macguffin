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
mg mail send <agent> --from=me --subject="Review needed" --body-file - <<'EOF'
Check the auth refactor — `go vet ./...` is clean but $BUILD_TAG is unset.
EOF

# A file works the same way
mg mail send <agent> --from=me --subject="Review needed" --body-file ./msg.md

# --body is the inline-only shortcut: fine when the body carries no shell
# metacharacters, silently lossy when it does
mg mail send <agent> --from=me --subject="Review needed" --body="Check the auth refactor."

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
| `mg done ID` | Mark a claimed work item as done. |
| `mg edit ID [flags]` | Update fields on an existing work item. **Adding to a body? Use `--append-body-file`, not `--body-file`** — see [Concurrent edits](#concurrent-edits-lost-updates). |
| `mg archive [ID]` | With an `ID`, archive exactly that one done item and nothing else — the form to use for items the refinery never merged (investigations, evaluations). With no `ID`, sweep `done/` and archive every item older than `--days` (default 7; `--days=0` takes **all** done items). The two forms are exclusive: passing both an `ID` and `--days` is an error, never a silent choice between archiving one item and archiving every item. `--dry-run` previews without moving anything. A targeted archive that cannot act (unknown ID, item not done) exits non-zero and says why. **Two guards refuse an archive**: done `type: design` items with no successor, and items of any type tagged `blocked-on-*` — see below. |
| `mg archive <id> --successor <id>` | A design's output *is* a recommendation, so at the moment it is done the thing it recommends is undone by construction — and an archived item cannot be the tracker for undone work. Archiving a done `type: design` item is therefore **refused** unless it names a successor. `--successor <id>` records a `successor:<id>` tag on the design before moving it, so the archived record names its own tracker; the id must resolve to a real item and cannot be the design itself. The check is on the item **type**, not on any wording in its body, and is satisfied **only** by that structured tag — a body that merely mentions an id proves nothing about who is tracking what. The sweep form never forces: it skips guarded items, leaves them in `done/`, and names them on stderr. A design that was *abandoned* rather than implemented is a legitimate archive — `--force` takes it and records a `work.archive_forced` event naming the guard that was bypassed. `--force` is documented here and in `mg archive --help`, and is deliberately **not** named by the refusal itself, so a guard hit mid-cleanup does not hand out its own bypass. |
| Archiving an item tagged `blocked-on-*` | A `blocked-on-*` tag says **a person still owes something here**, and an archived item cannot be the tracker for outstanding work — so archiving one is **refused**, whatever its type. The refusal **names the tag it found** (`blocked-on-daniel`, `blocked-on-daniel-confirm`, …), because "blocked" and "no successor" have different remedies and an operator has to be able to tell which fired. The remedy is to settle what the tag names and then `mg edit <id> --rm-tags=blocked-on-<who>`. The check is on the **tag**, not on any wording in the body: the tag was already a live convention and already queryable across every status (`mg list --tag=blocked-on-daniel` with no `--status` spans statuses and finds `done` items), so this only teaches the *destructive* operation to read an index that already existed. `--successor` does **not** satisfy it — naming a tracker for a recommendation says nothing about whether a person still owes something. `--force` **does** apply, as for the successor guard: an obligation can be discharged out of band and the tag left behind, and without a recorded escape hatch the operator strips the tag by hand — the same bypass with none of the audit trail. A forced archive records a `work.archive_forced` event with `reason: blocked_on_tag`, and the refusal itself does not mention `--force`. The sweep skips blocked items, leaves them in `done/`, and names each one on stderr **with the reason it was refused**. |
| `mg unarchive ID [--status=S]` | Restore an archived work item to the status it held when archived. Refuses if that status is unknown — pass `--status` to say where it belongs. |
| `mg unclaim ID` | Release a claim, returning the work item to `available/`. |
| `mg schedule` | Promote pending items whose dependencies are met. |
| `mg mail send\|reply\|list\|read\|archive\|migrate` | Maildir-style messaging between agents. `archive AGENT/MSG-ID` moves a message out of the active mailbox; `list AGENT --archived` inspects archived messages. Each operation logs a `mail.sent`/`mail.read`/`mail.archived` event to `events.jsonl` and a caller-attributed line (pid + `POGO_AGENT_NAME`) to `log/mail-audit.log`; malformed message files are skipped loudly (`mail.malformed` event, stderr warning, count in `list` output). `read` and `reply` refuse to touch another agent's mailbox when `POGO_AGENT_NAME` is set (both mark the message read for its owner) unless `--force` is given; denials and forced reads are audited too. |
| Canonical mailbox addressing | Every recipient/mailbox argument is canonicalized before it resolves to a directory: the harness prefixes `mg-` and `cat-` are stripped, so the work-item alias `mg-<id>`, the process name `cat-mg-<id>`, and the bare live id `<id>` all address the **same** mailbox. This closes the silent-drop class where mailing `mg-<id>` (the spelling many templates use) minted a stray mailbox nobody read while a watcher polling `mg mail list mg-<id>` waited forever. Crew mailboxes (`mayor`, `architect`, …) carry no prefix and pass through unchanged. `mg mail migrate` (`--dry-run` to preview) is a one-shot, idempotent cleanup that merges any pre-existing stray `mg-<id>` mailbox into its canonical `<id>` box — unread, read and archived mail alike — preserving read state and then removing the emptied stray directory. |
| `--append-body-file PATH` / `--append-body TEXT` (`mg edit`) | Append to the existing body instead of replacing it. Reads verbatim, same as `--body-file` (`-` for stdin). An append composes against the body **on disk at write time**, so it cannot destroy a section the caller never saw — see [Concurrent edits](#concurrent-edits-lost-updates). The appended text is stored byte-for-byte; only the join is normalized, to exactly one blank line, so a leading `## heading` renders as a heading. Mutually exclusive with `--body`/`--body-file` (exit 2). |
| `--if-unchanged HASH` (`mg edit`) | Refuse the edit (exit 4, `body_changed`) unless the stored body still hashes to `HASH`, from `mg show ID --body-hash`. **Opt-in**: without it, `--body-file` behaves exactly as it always has. The hash covers the stored body *including* its `# Title` heading, so it also catches a competing `--title`. A prefix of 8+ characters is accepted. |
| `--body-file PATH` (`mg new`, `mg edit`, `mg mail send`) | Read the body **verbatim** from a file (`-` for stdin) instead of from `--body`. Mutually exclusive with `--body` (exit 2); an unreadable path is an error, **never** a silently empty body. Reach for it whenever the body's exact text matters. The shell expands `` `backticks` ``, `$VAR` and `$(cmd)` inside `--body="..."` **before mg runs**, so those terms are silently gone from the item or message while mg still reports success — and mg cannot detect this, because the mangled string is byte-identical to one you typed that way. `--body-file` puts no shell in the body's path at all. `--body` is unchanged and **not** deprecated: it stays correct for bodies carrying no metacharacters. |
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

**Not locking.** Agents are long-lived and can die mid-edit, so a lock needs a
timeout, and a timeout reintroduces the same race with more moving parts. The
defect being fixed is *silence*, not concurrency.

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
│   ├── pending/          # Items waiting on dependencies
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
