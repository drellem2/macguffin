# MacGuffin Milestones

Each milestone is the smallest complete product that can be tested end-to-end.
"Complete" means: a human or script can exercise the full behavior and verify
correctness without any future milestone existing.

---

## M0 — Init + Layout

**Ship:** `mg init` creates the canonical directory tree.

```
~/.macguffin/
├── work/{available,claimed,done}/
├── mail/
└── log/
```

**End-to-end test:**
```bash
mg init
test -d ~/.macguffin/work/available
test -d ~/.macguffin/work/claimed
test -d ~/.macguffin/work/done
test -d ~/.macguffin/mail
test -d ~/.macguffin/log
```

**Why this is a milestone:** Establishes the convention. Every subsequent
milestone assumes this layout exists. If the layout is wrong, everything
downstream is wrong. Ship it separately so it can be argued about in isolation.

**Delivers:** `mg init`, idempotent.

---

## M1 — Work Item Create + Read

**Ship:** Create a work item as a Markdown file with YAML frontmatter.
Read it back.

**End-to-end test:**
```bash
mg init
mg new --type=bug "Auth tokens not refreshing"
# Verify: exactly one .md file appeared in available/
ls ~/.macguffin/work/available/*.md
# Verify: frontmatter has id, type, created, creator
cat ~/.macguffin/work/available/gt-*.md | head -8
# Read it back via CLI
mg show gt-a3f
```

**Why this is a milestone:** You can now create and inspect work items. The
format is locked in and can be validated. No claim, no agents — just files
that represent work.

**Delivers:** `mg new`, `mg show`, `mg list`, ID generation.

---

## M2 — Atomic Claim

**Ship:** A single operation — `mg claim <id>` — that atomically moves
a work item from `available/` to `claimed/` using `rename(2)`. Racing processes
get exactly-once semantics.

**End-to-end test:**
```bash
mg new "Race target"
id=$(ls ~/.macguffin/work/available/ | head -1 | sed 's/.md//')
# 10 concurrent claimants
for i in $(seq 10); do
    mg claim "$id" &
done
wait
# Exactly 1 file in claimed/, 0 in available/
test "$(ls ~/.macguffin/work/claimed/ | wc -l)" -eq 1
test "$(ls ~/.macguffin/work/available/ | wc -l)" -eq 0
```

**Why this is a milestone:** This is the proof that the foundation works.
Every other coordination primitive (reaping, scheduling, agent loops) depends
on atomic claim being correct. It must be tested in isolation under contention.

**Delivers:** `mg claim <id>`. Returns success/failure. Appends PID suffix.

---

## M3 — Complete + Lifecycle Query

**Ship:** `mg done <id>` moves a claimed item to `done/`. The full
create→claim→done lifecycle works. `mg list` shows items grouped by state.

**End-to-end test:**
```bash
mg new "Full lifecycle"
id=<created-id>
mg claim "$id"
mg done "$id" --result '{"status":"fixed","commit":"abc123"}'
# Verify: item in done/, result sidecar exists
test -f ~/.macguffin/work/done/${id}.md
test -f ~/.macguffin/work/done/${id}.result.json
# Verify: list shows it as done
mg list --status=done | grep "$id"
# Verify: list shows nothing available or claimed
test "$(mg list --status=available | wc -l)" -eq 0
```

**Why this is a milestone:** The complete work item lifecycle is now
exercisable. You can create work, claim it, complete it, and query state.
This is the first milestone a team could actually use (manually) to track work.

**Delivers:** `mg done <id>`, `mg list [--status=STATE]`,
result sidecar write.

---

## M4 — Stale Claim Reaper

**Ship:** A reaper that detects claimed items whose claimant PID is dead
and moves them back to `available/`.

**End-to-end test:**
```bash
mg new "Will be abandoned"
id=<created-id>
# Claim it in a subshell, then kill the subshell
( mg claim "$id"; sleep 999 ) &
claimer=$!
sleep 0.5
kill $claimer
# Item is now in claimed/ with a dead PID suffix
# Reap
mg reap
# Verify: item is back in available/
ls ~/.macguffin/work/available/ | grep "$id"
```

**Why this is a milestone:** Crash safety is proven. Without this, any
claimant crash permanently loses a work item. With this, the system
self-heals.

**Delivers:** `mg reap` (one-shot), testable in isolation.

---

## M5 — Mail

**Ship:** Maildir-style message delivery between agents. Atomic write to
`new/`, read marks to `cur/`.

**End-to-end test:**
```bash
mg init   # ensures mail dirs exist
# Send a message
mg mail send arch --from=mayor --subject="Review needed" \
    --body="Please review the auth refactor."
# Verify: file landed in new/
test "$(ls ~/.macguffin/mail/arch/new/ | wc -l)" -eq 1
# Read it
mg mail list arch | grep "Review needed"
mg mail read arch <msg-id>
# Verify: moved to cur/
test "$(ls ~/.macguffin/mail/arch/new/ | wc -l)" -eq 0
test "$(ls ~/.macguffin/mail/arch/cur/ | wc -l)" -eq 1
```

**Why this is a milestone:** Agents can now communicate without shared
mutable state. Mail is the coordination primitive for everything that
isn't "claim work" — status updates, reviews, nudges, handoffs.

**Delivers:** `mg mail send`, `mg mail list`, `mg mail read`,
Maildir layout with tmp→new atomic delivery.

---

## M6 — Dependency Scheduling

**Ship:** Work items declare `depends: [id1, id2]` in frontmatter. A
scheduler holds dependent items out of `available/` until their dependencies
land in `done/`.

**End-to-end test:**
```bash
mg new "Phase 1"        # → gt-aaa, lands in available/
mg new "Phase 2" --depends=gt-aaa  # → gt-bbb
# Phase 2 should NOT be in available/ yet
test "$(ls ~/.macguffin/work/available/ | grep gt-bbb | wc -l)" -eq 0
# Complete Phase 1
mg claim gt-aaa && mg done gt-aaa
# Run scheduler (or it fires automatically)
mg schedule
# NOW Phase 2 should be available
ls ~/.macguffin/work/available/ | grep gt-bbb
```

**Why this is a milestone:** Work can now have structure — DAGs, not just
flat queues. This is required for any non-trivial project where tasks
have ordering constraints. The claim primitive (M2) stays untouched;
scheduling is a separate concern layered on top.

**Delivers:** `depends:` frontmatter field, a `pending/` (or gated) directory
for items with unmet deps, `mg schedule` to promote items when deps clear.

**Design note:** Items with unmet deps live in a `pending/` directory (not
`available/`). The scheduler is the only thing that moves items from `pending/`
to `available/`. The claim primitive never sees pending items.

---

## M7 — Git Cold Path

**Ship:** Background snapshots of `~/.macguffin/` to git. Configurable
interval. `mg log` shows the audit trail.

**End-to-end test:**
```bash
mg init --git   # also runs git init
mg new "Tracked item"
mg snapshot     # manual trigger
git -C ~/.macguffin log --oneline | head -1  # snapshot commit exists
mg claim <id> && mg done <id>
mg snapshot
# Two commits, showing the item's lifecycle
git -C ~/.macguffin log --oneline | wc -l  # >= 2
```

**Why this is a milestone:** Durability and audit without touching the hot
path. If the machine crashes, you can recover to the last snapshot. The
separation is proven: no agent ever waits for git.

**Delivers:** `mg snapshot` (one-shot), `mg log` (wrapper
around git log), optional cron/timer setup for periodic snapshots.

---

## Dependency Graph

```
M0 ─→ M1 ─→ M2 ─→ M3 ─→ M4
                          │
                          └─→ M6
M5 (independent after M0)
M7 (independent after M0)
```

M0–M3 are strictly sequential (each builds on the last).
M4 follows M3 (reaper needs the claim lifecycle).
M5 and M7 only need M0 and can be built anytime.
M6 needs M3 (complete lifecycle must exist to schedule against).
