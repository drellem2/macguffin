# MacGuffin Architecture

An OS-level substrate for agentic workflows, built on UNIX primitives.

## Design Principles

1. **Filesystem is the database.** Live operational state lives in files and directories, observable with `ls`, `cat`, `grep`, `watch`. No query language required to see what's happening.

2. **Atomic operations via the kernel.** Claim, signal, and notify use filesystem atomics (`rename(2)`, `mkdir(2)`, `flock`) — not application-layer locking, not commit-and-push.

3. **Git for the cold path only.** Durability, audit, and cross-machine sync use git — but git is never on the hot path of claiming work or signaling completion. Think of git the way Maildir thinks of IMAP: a sync layer, not the live store.

4. **Convention over machinery.** Structure comes from directory layout and naming conventions, not from a database schema. Tools are ergonomic wrappers, not gatekeepers — you can always `ls` your way to the truth.

5. **The bitter lesson.** Don't build elaborate coordination protocols to compensate for today's agent limitations. Agents will get better at ad-hoc coordination. Build the thinnest possible substrate and let the agents do the thinking.

## Why Not Git (for the hot path)

Git was the natural first choice but fails under concurrent agent workloads:

| Requirement | Git's problem |
|-------------|--------------|
| **Atomic claim** | No `SELECT ... FOR UPDATE`. Read→modify→commit→push is multi-step. Two agents can claim the same work item. |
| **Concurrent writes** | `git push` rejects on conflict. N agents finishing simultaneously → N-1 retry. Under load this becomes a retry storm. |
| **Notification** | No subscription. Detecting "something changed" requires `git pull` + diff. Polling, not reacting. |
| **Speed** | Every state change requires staging, committing, and (for visibility) pushing. Heavyweight for "move task from queue A to queue B." |

These aren't problems with files-as-database. They're problems with git-as-synchronization-layer. The local filesystem has all the primitives git lacks.

## Why Not SQL

Dolt/SQL solves the concurrency problems git has, but creates new ones:

- **Opaque state.** You can't `ls` a SQL table. Every observation requires a query tool.
- **Heavyweight dependency.** A database server is a process to manage, a port to allocate, a crash to recover from.
- **Impedance mismatch with UNIX.** Agents are processes. Processes communicate via files, pipes, signals, exit codes. Routing everything through SQL means wrapping every native operation.
- **IDE, not OS.** A SQL-backed system is an application you interact with through its interface. A filesystem-backed system is infrastructure you interact with through any tool.

## Filesystem Layout

```
~/.macguffin/                       # Or XDG_STATE_HOME/macguffin
├── work/
│   ├── available/                  # Unclaimed work items
│   │   ├── gt-a3f.md               # One file per work item
│   │   └── gt-k9z.md
│   ├── claimed/                    # Atomically moved here on claim
│   │   └── gt-a3f.md.82407         # Suffixed with claimant PID
│   └── done/                       # Moved here on completion
│       └── gt-a3f.md.82407         # Result appended or in sidecar
│
├── agents/
│   ├── arch.pid                    # PID file = alive. Absent = dead.
│   ├── arch.sock                   # Optional: UNIX domain socket for IPC
│   └── polecat-7f2.pid
│
├── mail/                           # Maildir-style per-agent inboxes
│   ├── arch/
│   │   ├── new/                    # Unread messages (files)
│   │   └── cur/                    # Read messages
│   └── mayor/
│       ├── new/
│       └── cur/
│
├── log/                            # Append-only event log
│   └── events.jsonl                # Machine-readable event stream
│
└── .git/                           # Cold path: audit + sync
```

## Core Primitive: Atomic Claim

The foundation of the entire system. Everything else is convention layered on top.

### The operation

To claim work item `gt-a3f`:

```bash
mv available/gt-a3f.md claimed/gt-a3f.md.$$
```

`rename(2)` is atomic on all local filesystems. If two processes race:
- One succeeds (gets the file)
- The other gets `ENOENT` (file already moved)
- The kernel serialized it — no locks, no retries, no database

### Properties

- **Exactly-once delivery.** The file is in one directory at a time. No double-claim.
- **Crash-safe.** If the claimant dies, `claimed/gt-a3f.md.82407` remains. A reaper can detect stale claims by checking whether PID 82407 is alive.
- **Observable.** `ls claimed/` tells you what's in progress and who has it. No query tool needed.
- **Fast.** Single syscall. No network round-trip, no commit, no push.

### Completion

```bash
# Write result (if any)
echo '{"status": "fixed", "commit": "abc123"}' > done/gt-a3f.result.json

# Signal done
mv claimed/gt-a3f.md.$$ done/gt-a3f.md
```

### Reaping stale claims

A background reaper (or any agent, or a cron job) can detect abandoned work:

```bash
for f in claimed/*.md.*; do
    pid="${f##*.}"
    if ! kill -0 "$pid" 2>/dev/null; then
        # Process is dead. Move back to available.
        base="${f%.*}"
        mv "$f" "available/$(basename "$base")"
    fi
done
```

This is the UNIX equivalent of a database's deadlock detector — but it uses `kill -0`, not connection timeouts.

## Notification: How Agents React

Three mechanisms, from simplest to most responsive:

### 1. Filesystem watch (primary)

```bash
# macOS
fswatch -1 available/    # Blocks until a file appears

# Linux
inotifywait -e moved_to available/
```

Agents watch their relevant directories. When a file appears (new work, new mail), they react. No polling, no pull, no diff.

### 2. Named pipe (FIFO) for point-to-point

```bash
mkfifo agents/arch.fifo

# Sender
echo "new-work gt-a3f" > agents/arch.fifo

# Receiver (blocks until message arrives)
read msg < agents/arch.fifo
```

Useful for nudge-style wake signals without filesystem events.

### 3. UNIX signals for emergency interrupt

```bash
kill -USR1 $(cat agents/arch.pid)    # "Check your queue"
```

The agent traps `USR1` and checks for work. Coarse but instant.

## Mail: Maildir Convention

Messages are files. Delivery is atomic rename. Reading is `cat`.

### Send a message

```bash
# Write to temp, then atomic move into recipient's new/
msg_id="$(date +%s).$$.$RANDOM"
cat > mail/arch/tmp/$msg_id <<EOF
From: mayor
Subject: Review needed
Date: 2026-03-20T16:30:00Z

Please review the auth refactor before we merge.
EOF
mv mail/arch/tmp/$msg_id mail/arch/new/$msg_id
```

### Read messages

```bash
ls mail/arch/new/             # List unread
cat mail/arch/new/$msg_id     # Read one
mv mail/arch/new/$msg_id mail/arch/cur/$msg_id   # Mark read
```

This is literally Maildir — a proven, battle-tested pattern from 1strstrstr1995 that solved the same concurrent-delivery problem for email that we're solving for agent coordination.

## Work Item Format

A work item is a Markdown file with YAML frontmatter:

```markdown
---
id: gt-a3f
type: bug
priority: 2
created: 2026-03-20T16:00:00Z
creator: arch
depends: []
---

# Auth tokens not refreshing after expiry

The refresh logic in `auth/token.go` doesn't handle the case where
the refresh token itself has expired. Users see a 401 loop.

## Acceptance

- Token refresh works when refresh token is expired
- Integration test covers this path
```

- **Human-readable.** It's a Markdown file. Open it in anything.
- **Machine-parseable.** YAML frontmatter for structured queries.
- **Self-contained.** Everything needed to work on it is in the file.
- **Diffable.** Git tracks changes naturally.

### Querying work items

Simple shell pipeline — no query language:

```bash
# All open bugs, sorted by priority
grep -l 'type: bug' available/*.md | xargs grep -l 'priority: 1'

# What is arch working on?
ls claimed/*.md.$(cat agents/arch.pid) 2>/dev/null

# How many items done today?
find done/ -name '*.md' -newer /tmp/today-marker | wc -l
```

For richer queries, a thin CLI wrapper can parse frontmatter. But the raw files are always there.

## Agent Lifecycle

An agent is a process. Its state is its process state.

| State | How you know |
|-------|-------------|
| **Running** | PID file exists, `kill -0 $pid` succeeds |
| **Idle** | Running, but no files in `claimed/` with its PID suffix |
| **Working** | Running, has files in `claimed/` |
| **Dead** | PID file exists, `kill -0 $pid` fails (or PID file absent) |

No state machine. No database rows. The kernel already tracks this.

### Starting an agent

```bash
# The agent writes its own PID file on startup
echo $$ > agents/myname.pid
trap 'rm -f agents/myname.pid' EXIT

# Then enters its work loop
while true; do
    file=$(ls available/ | head -1)
    if [ -n "$file" ]; then
        mv "available/$file" "claimed/$file.$$" 2>/dev/null && work_on "$file"
    else
        fswatch -1 available/    # Sleep until work appears
    fi
done
```

### Observing the system

```bash
# Who's alive?
for f in agents/*.pid; do
    name="${f%.pid}"; name="${name##*/}"
    pid=$(cat "$f")
    if kill -0 "$pid" 2>/dev/null; then
        echo "$name ($pid): alive"
    else
        echo "$name ($pid): DEAD"
    fi
done

# What's the system doing right now?
echo "Available: $(ls available/ | wc -l)"
echo "In progress: $(ls claimed/ | wc -l)"
echo "Completed: $(ls done/ | wc -l)"
```

This is `ps` + `ls`. No dashboards, no query tools, no database connections.

## Git: The Cold Path

Git provides three things, none of them on the hot path:

### 1. Audit trail

A cron job or background process periodically commits:

```bash
cd ~/.macguffin
git add -A
git commit -m "state snapshot $(date -Iseconds)" --allow-empty
```

This captures the full history of work items — who claimed what, when things completed, what the queue looked like over time. But it's asynchronous. No agent waits for a commit to proceed.

### 2. Cross-machine sync

If agents span multiple machines:

```bash
git pull --rebase && git push
```

On a schedule or trigger. The filesystem is the source of truth locally; git reconciles across machines. Conflicts in `available/` are impossible (files are moved, not edited in place). Conflicts in `done/` are harmless (append-only).

### 3. Durability

If the machine crashes, `git log` shows the last committed state. The window of data loss equals the snapshot interval. For most workflows, a 1-minute snapshot interval is fine — and the `done/` directory is durable regardless since completed files aren't going anywhere.

## Pogo Integration

Pogo discovers repositories. MacGuffin discovers work. They share principles:

- **Background indexing.** Pogo watches for repos; MacGuffin watches for work items.
- **Convention-based discovery.** Pogo finds `.git/` directories; MacGuffin finds `.macguffin/` directories.
- **No registration.** You don't register a repo with pogo; you don't register a project with MacGuffin. They're found.
- **Cross-repo awareness.** Pogo indexes across repos; MacGuffin can aggregate work across project boundaries.

A natural integration: `pose` searches code, MacGuffin surfaces work items about that code. The link is the filesystem — both tools look at the same tree.

## Open Questions

### Scope of work items
Are work items per-project (living in each repo) or global (living in `~/.macguffin/`)? Per-project is more UNIX (data lives with the code) but makes cross-project queries harder. Global is simpler but detaches work from code.

**Leaning:** Per-project by default, with pogo-style aggregation for cross-project views.

### Agent identity
PID is ephemeral. What identifies an agent across restarts? Options:
- A name (string, like hostname) — simple, but requires a registry
- The command that started it — discoverable via /proc, but fragile
- A UUID assigned at first start, persisted in a file

**Leaning:** A name, written by the agent on startup. Convention, not registry.

### Dependency ordering
Work items can have `depends:` in frontmatter. But who enforces it? Options:
- **Nothing enforces it.** An agent can claim anything. Dependencies are advisory.
- **The claim operation checks.** Before `mv`, verify deps are in `done/`.
- **A scheduler moves items to `available/` only when deps clear.**

**Leaning:** Scheduler moves to `available/`. The claim primitive stays simple (just `mv`). Scheduling logic is separate.

### Multi-machine
The design above is single-machine. Cross-machine coordination is harder because `rename(2)` isn't atomic across NFS/network filesystems. Options:
- Git as the sync layer (works but has the hot-path problems described above)
- A lightweight coordination service (defeats the purpose)
- Accept that each machine has its own work queue, with explicit dispatch between them

**Leaning:** Single-machine first. Cross-machine is a later problem, and the answer is probably "explicit dispatch" (one machine sends work to another's queue) rather than shared state.

## Milestones

### M0: The claim primitive
Build and test the atomic claim loop. Five concurrent processes racing to claim items. Exactly one wins each time. Stale claims are reaped. This is the proof that the foundation works.

### M1: Agent lifecycle
PID files, startup/shutdown, liveness detection. `mg ps` shows running agents. `mg spawn` starts one. An agent that dies has its claims reaped.

### M2: Work item format + CLI
The Markdown+frontmatter format. `mg new "title"` creates a work item. `mg list` shows available work. `mg show <id>` reads one. All backed by files — the CLI is convenience, not gatekeeper.

### M3: Mail
Maildir-style messaging. `mg send <agent> "message"`. Agents watch their inbox with fswatch. Delivery is atomic.

### M4: Pogo integration
Work items in repos are discoverable by pogo. Cross-repo work aggregation. `pose` can search work items alongside code.

### M5: Git cold path
Background snapshots. Audit trail. Optional cross-machine sync.
