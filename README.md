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

# Send mail to an agent
mg mail send <agent> --from=me --subject="Review needed" --body="Check the auth refactor."

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
| `mg show <id> [--json]` | Display a work item by ID. `--json` emits the full item as one JSON object (adds `creator`, `body`, `budget`, `spent` to the `list --json` field set). |
| `mg list [--json]` | List work items. `--json` emits NDJSON (one JSON object per line) for scripts and dashboards. |
| `mg claim ID` | Atomically claim a work item by ID. |
| `mg done ID` | Mark a claimed work item as done. |
| `mg edit ID [flags]` | Update fields on an existing work item. |
| `mg archive` | Archive done items older than N days. |
| `mg unarchive ID` | Restore an archived work item back to `available/`. |
| `mg unclaim ID` | Release a claim, returning the work item to `available/`. |
| `mg schedule` | Promote pending items whose dependencies are met. |
| `mg mail send\|list\|read\|archive` | Maildir-style messaging between agents. `archive AGENT/MSG-ID` moves a message out of the active mailbox; `list AGENT --archived` inspects archived messages. Each operation logs a `mail.sent`/`mail.read`/`mail.archived` event to `events.jsonl` and a caller-attributed line (pid + `POGO_AGENT_NAME`) to `log/mail-audit.log`; malformed message files are skipped loudly (`mail.malformed` event, stderr warning, count in `list` output). `read` refuses to touch another agent's mailbox when `POGO_AGENT_NAME` is set (reading marks the message read for its owner) unless `--force` is given; denials and forced reads are audited too. |
| `mg event append <type> [--key=value ...]` | Append a structured event to `events.jsonl`. |
| `mg event list [--type=T] [--since=TS] [--tail=N] [--json]` | List events with optional filtering. Output is already NDJSON; `--json` is accepted for consistency (no behavior change). |
| `mg flow [--live] [--repo=P] [--blocked-after=D] [--group-by AXIS] [--json]` | Per-status flow view: throughput, median age, bottleneck, blocked chains. `--json` emits the computed snapshot as one JSON object under a single stable schema — the same top-level keys for the status and `--group-by` paths; the grouped path fills `groups` and leaves the status-only fields empty. |
| `mg spend [--by AXIS] [--since D] [--window W] [--total] [--json]` | Aggregate token consumption per item, tag, repo, agent, etc. `--since` is a rolling duration (`24h`, `7d`); `--window today\|week` is calendar-anchored; `--total` prints the today/this-week/all-time headline. See [Token spend accounting](#token-spend-accounting). |
| `mg snapshot` | Commit a git snapshot of current state. |
| `mg log [args]` | Show snapshot history (passes args to `git log`). |
| `mg schema` | Dump the full command tree as one JSON document (command names, use, flags, and a `mutates`/`idempotent` hint per command) for agent/tooling consumers. Frozen, additive-only shape versioned by `schema_version` (see [schema contract](#mg-schema-contract)). |
| `mg version` / `mg --version` / `mg -v` | Print version. Release builds include commit + date build metadata (e.g. `mg v0.1.3 (abc1234, 2026-07-08)`). |

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

Work items are Markdown files with YAML frontmatter — human-readable,
machine-parseable, and diffable. Claiming is a single `rename(2)` syscall:
if two processes race, exactly one wins. The loser gets `ENOENT`. No locks,
no retries, no database.

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

## License

Licensed under the GNU General Public License v3.0 — see [LICENSE](LICENSE).
