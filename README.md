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
brew tap drellem2/macguffin
brew install mg
```

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

# Create a work item
mg new --type=bug "Auth tokens not refreshing"

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
| `mg show <id>` | Display a work item by ID. |
| `mg list [--json]` | List work items. `--json` emits NDJSON (one JSON object per line) for scripts and dashboards. |
| `mg claim ID` | Atomically claim a work item by ID. |
| `mg done ID` | Mark a claimed work item as done. |
| `mg edit ID [flags]` | Update fields on an existing work item. |
| `mg archive` | Archive done items older than N days. |
| `mg unclaim ID` | Release a claim, returning the work item to `available/`. |
| `mg schedule` | Promote pending items whose dependencies are met. |
| `mg mail send\|list\|read` | Maildir-style messaging between agents. |
| `mg event append <type> [--key=value ...]` | Append a structured event to `events.jsonl`. |
| `mg event list [--type=T] [--since=TS] [--tail=N]` | List events with optional filtering. |
| `mg flow [--live] [--repo=P] [--blocked-after=D]` | Per-status flow view: throughput, median age, bottleneck, blocked chains. |
| `mg snapshot` | Commit a git snapshot of current state. |
| `mg log [args]` | Show snapshot history (passes args to `git log`). |
| `mg version` | Print version. |

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
├── log/                  # Append-only event log
└── .git/                 # Optional: cold-path audit trail
```

Work items are Markdown files with YAML frontmatter — human-readable,
machine-parseable, and diffable. Claiming is a single `rename(2)` syscall:
if two processes race, exactly one wins. The loser gets `ENOENT`. No locks,
no retries, no database.

## Project Structure

```
cmd/mg/          # CLI entry point and subcommands
internal/
  workitem/      # Work item creation, parsing, ID generation
  workspace/     # Directory layout, init, git operations
  mail/          # Maildir-style message delivery
  event/         # Structured event logging
```

## License

See repository for license details.
