# macguffin Multi-Tenant / Org-Mode — Design & Recommendation

**Status:** design / recommendation. Not implemented.
**Origin:** mg-5d8a (Daniel reminder 2026-05-11 03:44Z — *"orgs need to be able to use macguffin as a centralized ticket store across workers. Still enough entropy? What repos and license model? Dual license like pogo? Polecats can probably do the more specific design work"*).
**Author:** architect.
**Sibling docs:** `docs/mg-flow-redesign.md`, `docs/mg-custom-statuses-design.md` (also-recent mg core extensions — both compose with org-mode rather than conflicting).

## 0 · Why this lives in the macguffin repo

The ticket offered `pogo/docs/` or `macguffin/docs/` and asked architect to
argue for either. Per the unix-utility-principle: mg is its own thing; pogo
is one consumer among many; design docs for macguffin features belong in
macguffin. `mg-flow-redesign.md` set this precedent already. The reasoning
is the same one Daniel applied to the mg-roadmap utility (mg-3069): broad,
flexible utilities are owned by their own repo, not by whichever product
currently consumes them.

## TL;DR

Org-mode macguffin is a **client/server split** of the existing single-machine
tool. A new `mg-server` daemon owns the canonical store; the existing `mg`
CLI grows a `server_url` config that flips it from filesystem-mode to
client-mode. Single-machine users see nothing change unless they opt in.

**Five recommendations** (one per Daniel question, in order):

1. **Multi-tenancy via client/server with a `workspace` concept.** Server
   hosts N workspaces; each worker authenticates and sees only their
   workspace's tickets. Both intra-org-many-workers and SaaS-many-orgs map
   to the same primitive — they differ only in workspace ownership.
2. **6-character hex IDs with optional per-workspace prefix.** ~16.7M IDs
   per workspace (≈250× current capacity); prefix composes with the existing
   `--prefix` work (gh issue #2) for human-readability across workspaces.
   Single-machine mode keeps 4-char default for backwards compatibility.
3. **Single repo, two binaries.** `cmd/mg/` (CLI) and `cmd/mg-server/`
   (daemon) share `internal/`. One source of truth, one release cycle,
   easy ops; no new repo.
4. **Split licensing exactly like pogo: Apache 2.0 for the CLI, BSL 1.1
   for the server + internal/.** BSL converts to Apache 2.0 after 4 years.
   Closes the SaaS hole for the 4-year window without going as aggressive
   as AGPL. Matches Daniel's existing monetization story.
5. **Polecat-scoped follow-on tickets, not in this design.** Per Daniel's
   "polecats can probably do the more specific design work" — §9 roadmaps
   the impl phases at coarse granularity; each phase becomes architect-led
   sub-design + polecat impl when Daniel picks options here.

**One unrelated gap surfaced:** macguffin has no LICENSE file today; README
just says "see repository for license details." Fix as a one-line follow-up
ticket regardless of org-mode outcome — see §6.

**Cost estimate (full org-mode):** large. Order-of-magnitude ~3000-5000 LOC
across `internal/storage/`, `internal/server/`, `internal/auth/`, the
new `cmd/mg-server/`, and CLI client-mode wiring. Phased rollout (§9):
phase 1 (storage abstraction + client mode against local-filesystem
"server") is the highest-leverage minimum, ~800 LOC, gets the architecture
right without shipping any networking. Phase 2 (real server + auth) is the
next ~2000 LOC. Phase 3 (workspace isolation + admin tooling) ~1000 LOC.

---

## 1 · Baseline: the single-machine model today

`~/.macguffin/` is a per-user directory holding:

```
~/.macguffin/
  available/        # markdown files, one per ticket
    mg-1234.md
  claimed/          mg-XXXX.md + mg-XXXX.claim sidecars
  done/
  shelved/
  archived/
  events.log        # NDJSON event stream
  spend/            # per-mg-id NDJSON spend store (added mg-d66b)
  schedules.json    # pogod's schedule registry (pogo-side concern but lives here)
  mail/             # maildir-style per-agent inboxes
```

ID generation: 4-char hex, uniform random, retry-on-collision. Namespace
~65k. Birthday-collision 50% at ~256 items in flight; current Daniel-
workspace has hundreds of items across all statuses, so collisions are
already rare but plausible at the bursty end (`mg new` 10× in a few
seconds).

Concurrency: implicit — single-machine, single-process flock around the
state directories during writes. Read paths are lock-free (markdown +
sidecars are atomic-write friendly).

Backwards-compat invariant for org-mode: the on-disk layout above is the
authoritative shape. The server's storage layer presents the *same* shape
to client `mg` commands, just via a network API instead of direct `os.ReadFile`.

---

## 2 · Q1 — Multi-tenancy via client/server + workspace abstraction

### Three architectural options, recommendation: B

| Option | Description | Concurrency | Auth | Ops cost | Recommendation |
|--------|-------------|-------------|------|----------|----------------|
| A. Shared filesystem (NFS / SMB) | `~/.macguffin/` is a network mount | Filesystem locks (unreliable on NFS) | Filesystem perms | Low | **Rejected** — NFS flock semantics are fragile, ticketing on a shared fs leads to corruption races |
| B. Client/server | `mg-server` daemon hosts store; `mg` CLI is thin client over HTTP | Server-mediated | Auth tokens / mTLS | Medium | **Recommended** |
| C. CRDT / distributed | Each worker has local copy; sync via CRDT merge | Eventual consistency | Per-peer auth | High | **Rejected** — CRDTs for ticket workflows have semantically-wrong merges (`mg done` and `mg unclaim` racing should not silently both "win") |

### Why B wins

- **Authoritative concurrency.** Server is single decision-maker for "did this `mg claim` succeed?" No filesystem-lock theater across machines.
- **Auth surface fits.** Token-per-worker or workspace-scoped tokens are easy to bolt on. (A) inherits filesystem perms which don't map to multi-tenancy. (C) needs per-peer trust which is ops-heavy.
- **Easier path to SaaS.** Once the server speaks an HTTP API, hosting it for multiple orgs is the obvious next step. (A) and (C) don't get there without a rewrite.
- **CLI UX unchanged.** `mg new "..."`, `mg list`, etc. all work identically; the network round-trip is invisible. Local single-machine mode is preserved by *not setting* the server URL (filesystem-mode path).

### The `workspace` abstraction

The server's tenancy primitive. A workspace is a named, isolated container
with its own ticket store, event log, mail directory, and schedule
registry. The server hosts N workspaces.

```
mg-server-data/
  workspaces/
    daniel/          # "workspace = single user" — backwards-compat shape
      available/ claimed/ done/ ... events.log spend/ mail/
    acme-corp/       # "workspace = org" — many workers attach to it
      available/ ...
    initech/         # SaaS scenario — multiple orgs on one server
      available/ ...
```

A client `mg` command is bound to exactly one workspace at a time, via
config (`workspace = "acme-corp"`) or flag (`--workspace`). Cross-workspace
queries are a Phase-3 concern; for the bulk of usage, a worker only
ever sees their own workspace and the design treats it as the unit of
isolation.

**The intra-org-many-workers case** (Daniel's primary): one workspace
(`acme-corp`), many `mg` clients hitting the same server. Each worker
gets their own auth token tied to that workspace.

**The SaaS-many-orgs case**: one server, many workspaces (one per org).
Cross-org isolation is *the workspace boundary itself*; the server refuses
cross-workspace reads/writes at the auth layer.

### Wire protocol

HTTP/1.1 + JSON, not gRPC. Reasons: ops simplicity (curl-debuggable),
no codegen, matches the existing mg CLI's affection for plain JSON.
Authentication via Bearer tokens (workspace-scoped) in an `Authorization`
header. TLS mandatory in any non-`localhost` deployment — the server
binds `127.0.0.1` by default to make accidental public exposure impossible.

The full API is a Phase-2 design ticket per §9; this design only commits
to "HTTP+JSON" as the shape.

### Backwards-compat path

`~/.macguffin/config.toml` adds optional fields:

```toml
# default: single-machine mode (today's behaviour)
# org mode opt-in:
server_url = "https://mg.acme.internal"
workspace  = "acme-corp"
auth_token = "mgt_..."         # or via $MACGUFFIN_TOKEN
```

If `server_url` is set, `mg` operates as client. If not, filesystem-mode.
**No existing user is affected** until they edit the config. Single-machine
mode stays first-class indefinitely — many users will never need
org-mode and it would be a betrayal to deprecate it.

---

## 3 · Q2 — ID entropy

### Current: 4-char hex

`mg-XXXX` — 16^4 = 65,536 namespace. Per the birthday paradox, 50%
collision probability at √(2 × 65536 × ln 2) ≈ 301 concurrent in-flight
items. mg already retries on collision in the `mg new` path, so users
see no failures, but the retry cost grows quadratically with namespace
fill, and gets worse with concurrent writers (which org-mode introduces).

### Options analyzed

| Option | Namespace | Collision-at-50% | Length | Greppability | Notes |
|--------|-----------|------------------|--------|--------------|-------|
| 4-char (current) | 65,536 | 301 items | 7 chars (`mg-XXXX`) | excellent | Fine single-machine; fragile for orgs |
| 6-char hex | 16.7M | 4,824 items | 9 chars | very good | ~250× more headroom |
| 8-char hex | 4.3B | 77,235 items | 11 chars | good | Overkill |
| UUIDv7 | 2^128 | infinite | 36 chars | poor | Time-ordered + globally unique, but ugly |
| 4-char hex + per-workspace prefix | 65,536 × N workspaces | 301 per workspace | varies | excellent within workspace | Composes with gh-issue-#2 |

### Recommendation: 6-char hex + optional per-workspace prefix

**Default ID format in org mode: `mg-XXXXXX` (6-char hex).** 16.7M per
workspace. 50% birthday collision at ~5,000 in-flight items — comfortable
for any realistic org-workspace.

**Optional per-workspace prefix** via gh-issue-#2's `--prefix` mechanism.
Workspaces configure a default prefix (e.g. `acme-` → `acme-mg-XXXXXX`)
for human-readability in cross-workspace contexts. Within a workspace,
no prefix is needed — the workspace itself is the namespace.

**Single-machine mode keeps 4-char hex** as the default. Users who want
6-char can set `id_length = 6` in config. This avoids breaking existing
greppability for current single-machine users.

**Why not UUIDv7:** mg IDs appear in titles, commit messages, mail
subjects, sweep.log lines. They get typed by humans. The conciseness of
4-6 hex chars is load-bearing for the human-readable filing system mg
optimizes for. UUIDv7's 36 chars destroy that.

**Why not "go bigger to be safe":** 6-char hex with retry-on-collision
gives orgs ~5000 in-flight items before collisions become measurable
(~1%). Beyond that, the prefix mechanism extends the namespace without
lengthening every ID. The cost of 8-char defaults is paid by every
single mg item forever.

---

## 4 · Q3 — Repo + module split

### Three options

| Option | Layout | Pros | Cons |
|--------|--------|------|------|
| A. Single repo, single binary | `mg` subcommands include `mg serve` | Simplest ops; one go install | Binary always contains server code; harder to lock down attack surface |
| B. Single repo, two binaries | `cmd/mg/` + `cmd/mg-server/` share `internal/` | One source of truth; independent binaries; clean ops | Slightly more build complexity |
| C. Two repos | `macguffin` (CLI) + `macguffin-server` (daemon) | Independent release cadence | Repo proliferation; shared `internal/` becomes a third repo or duplicated |

### Recommendation: B

`cmd/mg/` and `cmd/mg-server/` in the same repo, sharing `internal/storage/`,
`internal/event/`, `internal/mail/`, etc. Two binaries built from the
same module.

**Why B:**

- Single source of truth for the storage layer — the same code that the
  server uses to persist tickets is the code that the single-machine CLI
  uses to persist tickets. No drift between "server view" and "CLI view"
  of what an mg item is.
- Independent deployable artifacts — orgs deploy only `mg-server`; clients
  install only `mg`. The server binary doesn't ship CLI subcommands that
  could be invoked accidentally; the CLI doesn't ship server code that
  could expose ports.
- No repo proliferation. C creates a third "shared core" repo or duplicate
  packages. B avoids both.

### Module structure (sketch)

```
github.com/drellem2/macguffin/
├── cmd/
│   ├── mg/                CLI binary (single-machine + client-mode)
│   └── mg-server/         server daemon binary (org-mode)
├── internal/
│   ├── storage/           filesystem-backed store (used by both CLI's local mode and server)
│   ├── client/            HTTP client used by CLI in client-mode
│   ├── server/            HTTP handlers (used only by mg-server)
│   ├── workspace/         tenancy primitives (used only by mg-server)
│   ├── auth/              token validation (used only by mg-server)
│   ├── event/, mail/, spend/, schedule/    (existing, used by both)
│   └── ...
└── go.mod
```

The CLI in single-machine mode imports `internal/storage` directly. In
client-mode, it imports `internal/client`. The server imports
`internal/storage` + `internal/server` + `internal/workspace` + `internal/auth`.
No CLI code imports server code; no server code imports CLI code. Build
tags or simple package boundaries keep this honest.

---

## 5 · Q4 — License model

### Current state

**Macguffin has no LICENSE file today.** README says "see repository for
license details" but there are no details in the repository. Legally this
means all-rights-reserved by default in the US (copyright vests at
authorship). This is a gap independent of org-mode and should be fixed
either way — see §6.

### Pogo's actual model (verified)

Not GPL+commercial as I initially assumed. Pogo uses **split licensing**:

- **Apache 2.0** (`LICENSE-APACHE`) — `cmd/pogo/`, `cmd/lsp/`, integrations
  (emacs, nvim, vscode, tmux, shell). I.e. the parts users embed in their
  workflows.
- **BSL 1.1** (`LICENSE-BSL`) — `cmd/pogod/`, `internal/`, `pkg/`. I.e. the
  daemon and shared infrastructure. **Converts to Apache 2.0 four years after
  each release.**

The BSL is a source-available license that prohibits commercial use as a
service (the SaaS hole closer) but converts to permissive after 4 years.
This is exactly the structure that fits a tool you want orgs to deploy
internally but not to resell as SaaS during the commercial-opportunity window.

### Recommendation: same split for macguffin

| Component | License | Why |
|-----------|---------|-----|
| `cmd/mg/` (CLI) | **Apache 2.0** | Embeddable unix tool; permissive license maximizes adoption; matches pogo's CLI treatment |
| `cmd/mg-server/` (daemon) | **BSL 1.1** | The org-mode value; SaaS hole closer; converts to Apache 2.0 after 4y |
| `internal/` | **BSL 1.1** | Where the actual logic lives; bundled with the daemon's license |

This makes the macguffin license story a one-line precedent: *"licensed
exactly like pogo."* Consistent ops, consistent legal review, consistent
4-year-conversion behaviour.

**Why not AGPL:** considered, rejected. AGPL is harsher (network use
triggers source-disclosure for the whole derived work), which closes more
holes but also turns many corporate procurement teams away as a matter of
policy. BSL closes the specific hole (SaaS resale) while staying acceptable
to internal-deployment use. Daniel can re-evaluate if pogo itself ever
moves off BSL.

**Why not MIT-only:** misses the commercial opportunity. Org-mode macguffin
is the kind of code that becomes infrastructure orgs depend on; the BSL
window is when commercial sales can fund the work.

**Why not GPL-only:** doesn't close the SaaS hole. Companies can run a
GPL'd server internally for their customers without ever distributing
the source.

### Existing single-machine users

By splitting at the binary level, single-machine `mg` stays under Apache
2.0 — no change for current users. The BSL terms only apply to anyone
running `mg-server` (i.e. the org-mode operators), and only for the 4-year
window per release.

### Independent action (not blocking org-mode)

**Ship a LICENSE file regardless.** Filing as a one-line ticket: choose
between (a) Apache 2.0-only for now, deferring split until mg-server lands,
or (b) the full split now so future contributors know the structure.
Recommendation: (a) — Apache 2.0 only on current macguffin, with a note
in LICENSE-FUTURE.md that `cmd/mg-server/` and post-split `internal/` will
be BSL when added. This avoids licensing code that doesn't exist yet while
unblocking external contributors.

---

## 6 · Q5 — Polecat-scoped follow-on tickets

Daniel: *"polecats can probably do the more specific design work."* Read
as: this high-level design picks the shape (client/server, 6-char IDs,
split repo, BSL+Apache split). The concrete impl-ticket scoping is a
follow-up phase that polecats can do once shape is locked.

§9 lists the rollout phases at coarse granularity. Each phase becomes a
follow-up architect-design-micro + polecat-impl sequence when Daniel
picks the §8 options here. **Not pre-empting** the polecat design work —
this doc commits only to the high-level shape, not to the wire protocol,
not to the auth scheme details, not to the storage migration path.

---

## 7 · Threat model — refinery-as-attack-surface (per `feedback_refinery_attack_surface`)

Org-mode macguffin introduces a **new highest-value security target**: the
`mg-server` daemon. Today single-machine macguffin has zero network
surface; org-mode publishes one. This needs to be named in the design.

### What an attacker gains by compromising `mg-server`

- Read all tickets across all workspaces hosted on the server (intellectual
  property, prioritization signal, ongoing security work in progress).
- Forge or modify tickets (insert malicious work into a worker's queue,
  e.g. `mg new "run this script"` that a polecat picks up).
- Forge events (poison observability — make stalled agents look healthy).
- Forge mail (impersonate one agent to another).

### Required mitigations (must-have for v1 server)

1. **Bind `127.0.0.1` by default.** Public exposure requires explicit
   `bind = "0.0.0.0:..."` in `mg-server.toml`. No accidental public
   deploys.
2. **TLS mandatory for non-`127.0.0.1` binds.** Refuse to start without
   `tls_cert` + `tls_key` if not localhost.
3. **Auth tokens with workspace scope.** Every API call requires a valid
   token; tokens are workspace-scoped at issuance and cannot escalate.
4. **Audit log.** Every mutating API call is logged with `(workspace,
   token_id, action, ts, target)` to a file the server operator can read
   but workers cannot. Append-only.
5. **No anonymous reads.** Even `mg list` requires a token. Discourages
   accidental discovery of the API.

### Should-have for v2

6. mTLS option for worker-to-server. Removes the bearer-token theft
   class. Heavier ops cost.
7. Rate limiting per token. Defends against poisoned-worker scenarios
   where one compromised polecat tries to spam tickets.
8. Workspace-level RBAC (read-only token vs read-write token vs admin
   token). For now, workspace-scoped binary token is the v1 simplification.

### Refinery interaction

Refinery (`drellem2/refinery`) currently watches `~/.macguffin/` directly
for ticket transitions and triggers PR auto-merges. In org-mode, refinery
becomes a client of `mg-server` and watches via a server-side event
subscription (e.g. SSE or websocket on `/events/stream`).

The refinery-launchd auth issue referenced in pm-pogo memory becomes
moot in org-mode: refinery uses an auth token like any other client; no
launchd-OS-level interaction needed.

**Critical:** refinery must not authenticate as a worker token (which has
write scope). It needs a *refinery* token — workspace-scoped, event-stream
read-only. Add to v2 RBAC.

---

## 8 · Open choices for Daniel

These are the only unresolved high-level points; concrete details are
deferred to per-phase follow-up designs (§9):

1. **Confirm B (single repo, two binaries) over A (single binary) or C
   (two repos).** Recommended B (§4).
2. **Default ID format in org-mode workspaces: 6-char hex (§3) vs 8-char
   for more headroom vs UUIDv7.** Recommended 6-char.
3. **License split timing for current (pre-org-mode) macguffin: ship
   Apache-2.0-only LICENSE now (§5 ending), or wait for the full split
   when `mg-server` lands?** Recommended ship Apache 2.0 now — closes the
   no-LICENSE gap immediately.
4. **Server bind default — `127.0.0.1` (recommended, §7) vs `0.0.0.0` with
   warning.** Recommended `127.0.0.1` (fail-safe; explicit opt-in to
   public bind).
5. **Phase 1 scope (§9): just storage abstraction + client-mode against
   a local "server" stub (no network), or include a minimum HTTP server
   in phase 1?** Recommended storage-abstraction-only — gets the
   architecture right with zero networking risk; phase 2 adds the server.

---

## 9 · Rollout phases (high-level — concrete tickets defer to per-phase architect designs)

Per Daniel's "polecats can probably do the more specific design work," each
phase below gets its own follow-up architect-design-micro ticket + polecat
impl when Daniel picks §8 options.

| Phase | Scope | Rough size | Architect follow-up |
|-------|-------|------------|---------------------|
| **0** | Ship LICENSE file (Apache 2.0) for current macguffin | ~5 LOC + LICENSE | tiny ticket; not blocking |
| **1** | Storage abstraction: extract today's filesystem ops behind an interface; CLI uses interface; "server" is local stub (in-process). No networking, no auth. | ~800 LOC | architect-design-micro: interface signatures, migration of existing tests |
| **2** | Real `mg-server` HTTP daemon; auth tokens; workspace primitive; CLI client-mode over HTTP. | ~2000 LOC | architect-design: wire protocol, auth scheme, workspace operations |
| **3** | SaaS-multi-org refinements: admin tooling for workspace CRUD; RBAC; rate limiting; audit log surfaces. | ~1000 LOC | architect-design: RBAC model, admin tooling shape |
| **4** | Refinery client-mode + event subscription. | ~300 LOC + refinery edits | architect-design-micro: event protocol; refinery PR coordination |

Phase 1 alone unlocks 80% of the architectural value (the storage interface
makes everything else clean) with zero networking risk. Phase 2 is where
org-mode becomes real. Phases 3-4 are the polish layer.

**No phase is committed yet.** This is the high-level shape; Daniel picks
§8 to unlock Phase 1's follow-up architect-design-micro.

---

## 10 · Generalizability check (per `feedback_generalizability_filter`)

Would another downstream consumer hit this shape?

- **Daniel's own work pattern** (the immediate driver) — yes.
- **Any other operator deploying macguffin for their org** — yes; the
  workspace abstraction means deploy-once-serve-many.
- **A SaaS operator running macguffin for paying customers** — yes; the
  BSL window is what makes this revenue path real.
- **A research org using macguffin to coordinate experiment workers across
  a cluster** — yes; same shape as the intra-org-many-workers case.
- **Pogo's current PM agents** — they keep using single-machine macguffin
  exactly as today (the workspace = local user mode); when org-mode lands,
  PMs in an org deployment opt in by setting `server_url`. No breaking
  change.

The design is *not* Daniel-specific. The workspace primitive + token auth
generalizes to any operator. The split licensing pattern generalizes to
any commercial-ops org. The 6-char IDs scale to org-size workloads without
breaking single-machine.

---

## 11 · Routing & rollout (this ticket)

Per `feedback_design_vs_exec_routing`:

- **Architect (this doc):** high-level design complete; pending Daniel's §8
  picks.
- **Per-phase follow-up:** each rollout phase (§9) gets its own
  architect-design-micro ticket once Daniel picks §8. Daniel reviews each
  per-phase design before polecat impl. This is the "polecats can probably
  do the more specific design work" path Daniel asked for.
- **Phase 0 (LICENSE file) is independent** and can ship immediately on
  Daniel's nod — not blocking the rest.

**References:** mg-5d8a (this directive). mg-72bf (architect-led design
shape precedent). mg-7488 (architect-led design shape precedent).
mg-flow-redesign.md (sibling macguffin design doc). mg-custom-statuses-design.md
(also-recent macguffin extension; composes with workspace abstraction
naturally — custom statuses become workspace-scoped). `feedback_generalizability_filter`,
`feedback_refinery_attack_surface`, `feedback_os_agnostic_design`,
`feedback_pogo_genericity`. Pogo's `LICENSE`, `LICENSE-APACHE`, `LICENSE-BSL`
(the precedent for §5).
