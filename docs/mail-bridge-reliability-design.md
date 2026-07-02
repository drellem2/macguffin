# Ocean→Land mail-bridge reliability (mg-a594, gh #13 / #16)

Design note. Facts verified against macguffin `ebc0c93` on 2026-07-02.
Caveat up front: **the bridge implementation lives only on the Ocean
droplet** — it is in neither macguffin nor pogo (exhaustive grep incl.
branches/history), and this machine has no `ocean` ssh host configured.
Root cause is therefore a ranked hypothesis set from behavioral evidence
plus mg's delivery semantics; discriminating H1 vs H2 definitively
requires reading the bridge script or its `receiver-audit.jsonl` on
Ocean. The recommended fix is robust to both.

## Answering the ticket's four questions

### 1. Where does mail drop?

In the transfer leg, *after* the bridge has already consumed the mail on
Ocean. The chain: `mg mail send` on Ocean is **local-filesystem only** —
it writes `tmp/<id>` then renames into the recipient's `new/`
(`internal/mail/mail.go:53-76`) and returns success; cross-machine
transfer is not mg's job. The only code paths that move mail into `cur/`
are `Read()` (mail.go:136-163) — so issue #16's key observation that
dropped mail sits in Ocean `cur/` "as delivered" means **the bridge marks
mail consumed (new/→cur/) before or without confirming the Land-side
write**.

Ranked hypotheses:

- **H1 — consume-before-confirm (high confidence, ~70%).** Bridge reads
  (consumes) each mail at fetch time, then the ssh/transfer leg fails
  intermittently (network blip, Land Mac asleep) with no retry and no
  dead-letter. Explains: `cur/` placement, intermittency, silence.
- **H2 — poll-window sweep race (medium, ~40%; can co-exist with H1).**
  Bridge snapshots `new/`, transfers the snapshot, bulk-moves
  `new/* → cur/`; mail arriving mid-transfer is swept untransferred.
  Fits the burst-correlated drops (12:21Z + 12:48Z in one active
  session, gh #16).
- **H3 — non-atomic remote write + silent skip (low-medium).** A partial
  file scp'd into Land `new/` is *invisible forever*:
  `listDir` silently skips malformed messages (mail.go:120-122), no log.
- **H4 — mail-root divergence (low as primary, real as hygiene).** gh #13
  quotes Ocean paths as `~/.pogo/mail/...`; the repo hardwires
  `~/.macguffin/mail` (cmd/mg/mail.go:13-19, workspace.go:12-18). A
  mismatch would drop 100% not intermittently, but the divergence is
  un-audited.

### 2. Is the failure detectable? — No. Fully silent, both sides.

Sender success asserts only a local Ocean write. Ocean `cur/` looks
identical for "delivered" and "consumed then dropped". mg emits **zero
events for mail operations** (no `event.Emit` anywhere in
internal/mail or cmd/mg/mail.go). No receiver audit exists on Land. The
Land side has no expectation signal. Every drop to date was found by a
human/agent stage-ping. **Fail-loud is therefore the primary fix**, ahead
of any transport change.

### 3. The right fix — ack semantics, not a smarter transport

The invariant to enforce: **a mail leaves Ocean `new/` only after its
bytes are verified on Land.** Concretely:

1. Per-mail processing — no bulk sweeps (kills H2).
2. Transfer with maildir discipline on the remote leg: write Land
   `tmp/<id>`, rename into Land `new/<id>`, then verify (remote
   `test -f` + size/checksum via exit code) (kills H3).
3. Only after verification, move Ocean `new/<id> → cur/<id>` (or a
   bridge-owned `sent/`) (kills H1).
4. On failure: leave the mail in `new/`, retry with backoff; after N
   attempts move to a `deadletter/` dir **and send a failure
   notification mail to the original sender** — a drop becomes a loud,
   attributable event.

Idempotency is free: msgID filenames are collision-resistant
(mail.go:58) and a re-transferred duplicate rename is a no-op, so
at-least-once delivery is safe.

### 4. Promote the ssh-poll workaround? — Yes, with one structural change.

The 5-min Land-side pull is the right *shape* (Land knows when it's
awake; pull survives Ocean-side ignorance of Land's state), but today it
reads Ocean `new/` only — mail already consumed into `cur/` by the
broken bridge is invisible to it, which is why drops still needed manual
recovery. Promotion to canonical requires: **the puller becomes the only
consumer** — it copies, verifies locally, and then moves the Ocean-side
file `new/→cur/` itself (steps 1-4 above, initiated from Land). The
Ocean-side push bridge is then retired or demoted to a latency
optimization that never consumes.

## Observability hooks (mg changes, this repo)

> **Status:** the first two hooks shipped as mg-9696 — events are named
> `mail.sent` / `mail.read` / `mail.archived` / `mail.malformed`
> (dot-style, matching the existing `work.*` types). The audit verb
> remains unimplemented.

Independent of the bridge, mg itself should stop being blind:

- `event.Emit` on mail send/read/archive (package exists, unused by
  mail) — keyed by msgID, sender, recipient.
- Replace the silent malformed-file skip (mail.go:120-122) with a logged
  skip + surfaced count in `mg mail list`.
- Optional reconciliation verb (`mg mail audit --remote <host>`): diff
  Ocean `cur/` msgIDs for `land-*` mailboxes against Land
  `new/+cur/+archive/` — turns any future silent drop into a detectable
  discrepancy.

## Child tickets

1. **macguffin, polecat-executable:** mail observability (events +
   logged malformed-skip + list surfacing). Filed off this design — see
   mg-a594 result note for ID.
2. **Ocean-side, needs routing:** rework the bridge to the
   pull-verify-consume protocol above (or patch the push bridge to
   transfer-verify-then-consume as an interim). Cannot be dispatched as
   a local polecat — the code and the audit log live on the droplet.
   Routed via mayor.
3. **Hygiene, folded into #2:** audit the `~/.pogo/mail` vs
   `~/.macguffin/mail` divergence on both machines; pin one root.
