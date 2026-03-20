# MacGuffin Milestones

Each milestone is the smallest complete product that can be tested end-to-end.
"Complete" means: a human or script can exercise the full behavior and verify
correctness without any future milestone existing.

---

## M0 — Init + Layout

**Ship:** `macguffin init` creates the canonical directory tree.

```
~/.macguffin/
├── work/{available,claimed,done}/
├── agents/
├── mail/
└── log/
```

**End-to-end test:**
```bash
macguffin init
test -d ~/.macguffin/work/available
test -d ~/.macguffin/work/claimed
test -d ~/.macguffin/work/done
test -d ~/.macguffin/agents
test -d ~/.macguffin/mail
test -d ~/.macguffin/log
```

**Why this is a milestone:** Establishes the convention. Every subsequent
milestone assumes this layout exists. If the layout is wrong, everything
downstream is wrong. Ship it separately so it can be argued about in isolation.

**Delivers:** `macguffin init`, idempotent.

---

## M1 — Work Item Create + Read

**Ship:** Create a work item as a Markdown file with YAML frontmatter.
Read it back.

**End-to-end test:**
```bash
macguffin init
macguffin new --type=bug "Auth tokens not refreshing"
# Verify: exactly one .md file appeared in available/
ls ~/.macguffin/work/available/*.md
# Verify: frontmatter has id, type, created, creator
cat ~/.macguffin/work/available/gt-*.md | head -8
# Read it back via CLI
macguffin show gt-a3f
```

**Why this is a milestone:** You can now create and inspect work items. The
format is locked in and can be validated. No claim, no agents — just files
that represent work.

**Delivers:** `macguffin new`, `macguffin show`, `macguffin list`, ID generation.

---

## M2 — Atomic Claim

**Ship:** A single operation — `macguffin claim <id>` — that atomically moves
a work item from `available/` to `claimed/` using `rename(2)`. Racing processes
get exactly-once semantics.

**End-to-end test:**
```bash
macguffin new "Race target"
id=$(ls ~/.macguffin/work/available/ | head -1 | sed 's/.md//')
# 10 concurrent claimants
for i in $(seq 10); do
    macguffin claim "$id" &
done
wait
# Exactly 1 file in claimed/, 0 in available/
test "$(ls ~/.macguffin/work/claimed/ | wc -l)" -eq 1
test "$(ls ~/.macguffin/work/available/ | wc -l)" -eq 0
```

**Why this is a milestone:** This is the proof that the foundation works.
Every other coordination primitive (reaping, scheduling, agent loops) depends
on atomic claim being correct. It must be tested in isolation under contention.

**Delivers:** `macguffin claim <id>`. Returns success/failure. Appends PID suffix.

---

## M3 — Complete + Lifecycle Query

**Ship:** `macguffin done <id>` moves a claimed item to `done/`. The full
create→claim→done lifecycle works. `macguffin list` shows items grouped by state.

**End-to-end test:**
```bash
macguffin new "Full lifecycle"
id=<created-id>
macguffin claim "$id"
macguffin done "$id" --result '{"status":"fixed","commit":"abc123"}'
# Verify: item in done/, result sidecar exists
test -f ~/.macguffin/work/done/${id}.md
test -f ~/.macguffin/work/done/${id}.result.json
# Verify: list shows it as done
macguffin list --status=done | grep "$id"
# Verify: list shows nothing available or claimed
test "$(macguffin list --status=available | wc -l)" -eq 0
```

**Why this is a milestone:** The complete work item lifecycle is now
exercisable. You can create work, claim it, complete it, and query state.
This is the first milestone a team could actually use (manually) to track work.

**Delivers:** `macguffin done <id>`, `macguffin list [--status=STATE]`,
result sidecar write.

---

## M4 — Stale Claim Reaper

**Ship:** A reaper that detects claimed items whose claimant PID is dead
and moves them back to `available/`.

**End-to-end test:**
```bash
macguffin new "Will be abandoned"
id=<created-id>
# Claim it in a subshell, then kill the subshell
( macguffin claim "$id"; sleep 999 ) &
claimer=$!
sleep 0.5
kill $claimer
# Item is now in claimed/ with a dead PID suffix
# Reap
macguffin reap
# Verify: item is back in available/
ls ~/.macguffin/work/available/ | grep "$id"
```

**Why this is a milestone:** Crash safety is proven. Without this, any
agent death permanently loses a work item. With this, the system
self-heals. This must work before agents run unattended.

**Delivers:** `macguffin reap` (one-shot), testable in isolation.

---

## M5 — Agent Registration + Liveness

**Ship:** An agent can register (PID file + optional socket), and the system
can report who's alive. `macguffin ps` lists agents with liveness status.

**End-to-end test:**
```bash
# Start a fake agent (just a sleep process that registers)
macguffin agent register arch &
agent_pid=$!
# Verify: PID file exists
test -f ~/.macguffin/agents/arch.pid
test "$(cat ~/.macguffin/agents/arch.pid)" = "$agent_pid"
# ps shows it alive
macguffin ps | grep "arch.*alive"
# Kill it
kill $agent_pid
# ps shows it dead
macguffin ps | grep "arch.*dead"
```

**Why this is a milestone:** You can now see who's running. This is the
minimum observability needed before building agent work loops. It also
validates the PID-file convention that the reaper (M4) already uses.

**Delivers:** `macguffin agent register <name>`, `macguffin ps`, PID file
lifecycle (write on start, remove on clean exit, detect on dirty exit).

---

## M6 — Agent Work Loop

**Ship:** A long-running agent process that watches `available/`, claims
work, runs a handler, and completes it. Integrates M2 + M3 + M4 + M5.

**End-to-end test:**
```bash
# Start an agent with a trivial handler (just appends "handled")
macguffin agent start arch --handler='echo handled >> /tmp/test.log'
# Drop work into the queue
macguffin new "Auto-process me"
# Wait briefly for fswatch to fire
sleep 2
# Verify: item is in done/
macguffin list --status=done | grep "Auto-process"
# Verify: handler ran
grep "handled" /tmp/test.log
# Verify: agent is still alive and idle
macguffin ps | grep "arch.*alive"
```

**Why this is a milestone:** This is the first milestone where an agent
runs autonomously. Work goes in, results come out, no human in the loop.
It's the proof that the filesystem-as-queue architecture actually works
as a runtime, not just a data model.

**Delivers:** `macguffin agent start <name> --handler=CMD`, fswatch-based
work loop, integration of claim + complete + reap.

---

## M7 — Mail

**Ship:** Maildir-style message delivery between agents. Atomic write to
`new/`, read marks to `cur/`.

**End-to-end test:**
```bash
macguffin init   # ensures mail dirs exist
# Send a message
macguffin mail send arch --from=mayor --subject="Review needed" \
    --body="Please review the auth refactor."
# Verify: file landed in new/
test "$(ls ~/.macguffin/mail/arch/new/ | wc -l)" -eq 1
# Read it
macguffin mail list arch | grep "Review needed"
macguffin mail read arch <msg-id>
# Verify: moved to cur/
test "$(ls ~/.macguffin/mail/arch/new/ | wc -l)" -eq 0
test "$(ls ~/.macguffin/mail/arch/cur/ | wc -l)" -eq 1
```

**Why this is a milestone:** Agents can now communicate without shared
mutable state. Mail is the coordination primitive for everything that
isn't "claim work" — status updates, reviews, nudges, handoffs.

**Delivers:** `macguffin mail send`, `macguffin mail list`, `macguffin mail read`,
Maildir layout with tmp→new atomic delivery.

---

## M8 — Dependency Scheduling

**Ship:** Work items declare `depends: [id1, id2]` in frontmatter. A
scheduler holds dependent items out of `available/` until their dependencies
land in `done/`.

**End-to-end test:**
```bash
macguffin new "Phase 1"        # → gt-aaa, lands in available/
macguffin new "Phase 2" --depends=gt-aaa  # → gt-bbb
# Phase 2 should NOT be in available/ yet
test "$(ls ~/.macguffin/work/available/ | grep gt-bbb | wc -l)" -eq 0
# Complete Phase 1
macguffin claim gt-aaa && macguffin done gt-aaa
# Run scheduler (or it fires automatically)
macguffin schedule
# NOW Phase 2 should be available
ls ~/.macguffin/work/available/ | grep gt-bbb
```

**Why this is a milestone:** Work can now have structure — DAGs, not just
flat queues. This is required for any non-trivial project where tasks
have ordering constraints. The claim primitive (M2) stays untouched;
scheduling is a separate concern layered on top.

**Delivers:** `depends:` frontmatter field, a `pending/` (or gated) directory
for items with unmet deps, `macguffin schedule` to promote items when deps clear.

**Design note:** Items with unmet deps live in a `pending/` directory (not
`available/`). The scheduler is the only thing that moves items from `pending/`
to `available/`. The claim primitive never sees pending items.

---

## M9 — Git Cold Path

**Ship:** Background snapshots of `~/.macguffin/` to git. Configurable
interval. `macguffin log` shows the audit trail.

**End-to-end test:**
```bash
macguffin init --git   # also runs git init
macguffin new "Tracked item"
macguffin snapshot     # manual trigger
git -C ~/.macguffin log --oneline | head -1  # snapshot commit exists
macguffin claim <id> && macguffin done <id>
macguffin snapshot
# Two commits, showing the item's lifecycle
git -C ~/.macguffin log --oneline | wc -l  # >= 2
```

**Why this is a milestone:** Durability and audit without touching the hot
path. If the machine crashes, you can recover to the last snapshot. The
separation is proven: no agent ever waits for git.

**Delivers:** `macguffin snapshot` (one-shot), `macguffin log` (wrapper
around git log), optional cron/timer setup for periodic snapshots.

---

## M10 — Pogo Integration

**Ship:** Per-project `.macguffin/` directories are discoverable by pogo's
indexer. Work items across repos are aggregated into a unified view.

**End-to-end test:**
```bash
# Two repos, each with project-local work items
cd ~/repo-a && macguffin init --project && macguffin new "Fix repo-a bug"
cd ~/repo-b && macguffin init --project && macguffin new "Fix repo-b bug"
# Pogo discovers them
pogo index
# Aggregated view
macguffin list --all-projects
# Shows items from both repos with project context
```

**Why this is a milestone:** MacGuffin becomes multi-project-aware. Work
is co-located with code (per-project), but you can still see everything
from one place. This is the bridge between "task tracker" and "development
environment."

**Delivers:** `--project` mode (`.macguffin/` in repo root), pogo discovery
hook, `macguffin list --all-projects` aggregation.

---

## Dependency Graph

```
M0 ─→ M1 ─→ M2 ─→ M3 ─→ M4 ─┐
                               ├─→ M6 ─→ M8
                   M5 ─────────┘
                   M7 (independent after M0)
                   M9 (independent after M0)
                   M10 (independent after M1, requires pogo)
```

M0–M3 are strictly sequential (each builds on the last).
M4 and M5 can run in parallel after M3.
M6 integrates M2–M5.
M7 and M9 only need M0 and can be built anytime.
M8 needs M6 (agents must exist to schedule for).
M10 needs M1 (work items must exist) plus pogo.
