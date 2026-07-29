package workitem

import (
	"os/user"
	"strings"
	"testing"
)

// The invoker identity these tests assert against. It is deliberately a string
// that could be nothing else: if it turns up in `actor` it can only have come
// from the environment, never from an item field, a username, or a default.
const probeInvoker = "zzz-probe-invoker"

// probeAssignee is likewise unmistakable, and is deliberately DIFFERENT from
// probeInvoker. That difference is the whole point of every test in this file
// — see TestActor_IsTheInvokerNotTheAssignee.
const probeAssignee = "zzz-probe-assignee"

// asInvoker points the actor resolver at probeInvoker for the duration of a
// test. MG_ACTOR is used rather than POGO_AGENT_NAME so a test never depends on
// whether the process happens to have been spawned by pogod; the ordering
// between the two is asserted separately in TestActor_ResolutionOrder.
func asInvoker(t *testing.T) {
	t.Helper()
	t.Setenv("MG_ACTOR", probeInvoker)
}

// TestActor_IsTheInvokerNotTheAssignee is the POSITIVE CONTROL mg-3122 asks
// for: prove the attribution CAN be wrong before trusting it right.
//
// The trap it avoids: asserting merely that `actor` is non-empty passes with
// the bug fully present, because the assignee is a non-empty string too. So
// the assignee is set to a value that is neither the invoker nor the OS user
// nor anything else in the process, and the assertion names the invoker
// exactly. Run against the old actorFor(item), this reports
// actor="zzz-probe-assignee" and fails.
//
// It drives the edit with a BODY change on purpose. A metadata-only edit would
// also fail against the old code — but for defect B's reason (no event at all),
// which would mask defect A rather than isolate it. A body edit emitted
// work.edited before this ticket too, so the only thing this test can be
// failing on is the attribution itself.
func TestActor_IsTheInvokerNotTheAssignee(t *testing.T) {
	asInvoker(t)
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "bug", "Attribute me", nil, WithAssignee(probeAssignee))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if item.Assignee != probeAssignee {
		t.Fatalf("setup: assignee = %q, want %q — the control needs the assignee to DIFFER from the invoker", item.Assignee, probeAssignee)
	}
	if probeAssignee == probeInvoker {
		t.Fatal("setup: the assignee and the invoker are the same string; this test could not distinguish them")
	}

	before := len(readEvents(t, root))

	body := "# Attribute me\n\nA body edit, so the event exists either way.\n"
	if _, err := Update(root, item.ID, UpdateField{Body: &body}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Count first: a query that finds a stale event looks identical to one
	// that finds a fresh one, so the growth is asserted before the contents.
	entries := readEvents(t, root)
	if len(entries) != before+1 {
		t.Fatalf("event count %d → %d, want exactly one new event", before, len(entries))
	}

	e := entries[len(entries)-1]
	if e.Type != "work.edited" {
		t.Fatalf("newest event type = %q, want work.edited", e.Type)
	}
	if e.Extra["actor"] != probeInvoker {
		t.Errorf("actor = %q, want %q (the invoker)", e.Extra["actor"], probeInvoker)
	}
	if e.Extra["actor"] == probeAssignee {
		t.Errorf("actor = %q — that is the ASSIGNEE, which is the mg-3122 defect", e.Extra["actor"])
	}
}

// TestActor_IsTheInvokerOnEveryEmitter walks the lifecycle emitters, not just
// the edit path. actorFor was called from twelve sites; fixing the one the bug
// was measured on and leaving the rest would move the lie rather than remove
// it. Every event here belongs to an item assigned to someone other than the
// invoker.
func TestActor_IsTheInvokerOnEveryEmitter(t *testing.T) {
	asInvoker(t)
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "bug", "Whole lifecycle", nil, WithAssignee(probeAssignee))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := Claim(root, item.ID, 4242); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, _, err := Done(root, item.ID, nil); err != nil {
		t.Fatalf("Done: %v", err)
	}
	if _, err := Reopen(root, item.ID); err != nil {
		t.Fatalf("Reopen: %v", err)
	}

	entries := readEvents(t, root)
	if len(entries) < 4 {
		t.Fatalf("expected at least 4 events, got %d", len(entries))
	}
	saw := map[string]bool{}
	for _, e := range entries {
		saw[e.Type] = true
		if e.Extra["actor"] != probeInvoker {
			t.Errorf("%s: actor = %q, want %q (the invoker)", e.Type, e.Extra["actor"], probeInvoker)
		}
	}
	for _, want := range []string{"work.created", "work.claim", "work.done", "work.reopen"} {
		if !saw[want] {
			t.Errorf("no %s event emitted", want)
		}
	}
}

// TestActor_ResolutionOrder pins the fallback chain. Each step is asserted with
// the steps above it cleared, so a reordering cannot pass by accident.
func TestActor_ResolutionOrder(t *testing.T) {
	t.Run("MG_ACTOR wins over POGO_AGENT_NAME", func(t *testing.T) {
		t.Setenv("MG_ACTOR", "explicit")
		t.Setenv("POGO_AGENT_NAME", "pogo-name")
		if got := actor(); got != "explicit" {
			t.Errorf("actor() = %q, want explicit", got)
		}
	})

	t.Run("POGO_AGENT_NAME when MG_ACTOR is unset", func(t *testing.T) {
		t.Setenv("MG_ACTOR", "")
		t.Setenv("POGO_AGENT_NAME", "cat-3122")
		if got := actor(); got != "cat-3122" {
			t.Errorf("actor() = %q, want cat-3122", got)
		}
	})

	t.Run("whitespace-only is not an identity", func(t *testing.T) {
		t.Setenv("MG_ACTOR", "   ")
		t.Setenv("POGO_AGENT_NAME", "cat-3122")
		if got := actor(); got != "cat-3122" {
			t.Errorf("actor() = %q, want cat-3122 — a blank MG_ACTOR must fall through", got)
		}
	})

	t.Run("OS user when neither is set", func(t *testing.T) {
		t.Setenv("MG_ACTOR", "")
		t.Setenv("POGO_AGENT_NAME", "")
		got := actor()
		if got == "" {
			t.Fatal("actor() = \"\" — must never be empty")
		}
		if u, err := user.Current(); err == nil && got != u.Username {
			t.Errorf("actor() = %q, want the OS user %q", got, u.Username)
		}
	})
}

// TestActor_IgnoresTheItemEntirely is the structural half of the fix. The
// resolver takes no item, so there is no argument through which an item field
// could reach it: changing the assignee cannot move the answer.
func TestActor_IgnoresTheItemEntirely(t *testing.T) {
	asInvoker(t)
	root := t.TempDir()
	setupDirs(t, root)

	// Seeded WITH an assignee so the first step of the walk below is a real
	// change and not a no-op that emits nothing.
	item, err := Create(root, "mg-", "bug", "Reassign me", nil, WithAssignee("mayor"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Each value differs from the one before it, so every step writes.
	for _, assignee := range []string{"", "parked", "human", probeAssignee, "daniel"} {
		before := len(readEvents(t, root))
		if _, err := Update(root, item.ID, UpdateField{Assignee: strPtr(assignee)}); err != nil {
			t.Fatalf("Update(assignee=%q): %v", assignee, err)
		}
		entries := readEvents(t, root)
		if len(entries) != before+1 {
			t.Fatalf("assignee=%q: event count %d → %d, want exactly one new event", assignee, before, len(entries))
		}
		e := entries[len(entries)-1]
		if e.Extra["actor"] != probeInvoker {
			t.Errorf("assignee=%q: actor = %q, want %q — attribution moved with the assignee", assignee, e.Extra["actor"], probeInvoker)
		}
	}
}

// TestActor_NeverEmpty guards the "unknown" floor. mg-3122 is explicit that an
// empty answer is recoverable and a confident wrong one is not — but an empty
// STRING in the field is neither: it is an absent value that consumers will
// read as "no actor recorded". "unknown" says the same thing out loud.
func TestActor_NeverEmpty(t *testing.T) {
	t.Setenv("MG_ACTOR", "")
	t.Setenv("POGO_AGENT_NAME", "")
	t.Setenv("USER", "")
	got := actor()
	if strings.TrimSpace(got) == "" {
		t.Errorf("actor() = %q, want a non-empty identity (at worst %q)", got, "unknown")
	}
}
