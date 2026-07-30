package main

import (
	"strings"
	"testing"
)

// mg-35fc: `mg event list` reads <workspace>/events.jsonl, every work.* line in
// that file carries an `actor`, and `mg show --json` does not expose the field —
// so this help IS the read surface, and before this ticket it said nothing.
// Measured then: `actor` appeared 0 times in the help for show, event list, log,
// list, done and claim.
//
// These tests pin the two things the help has to say, and one thing it must not.

// TestEventListHelp_DocumentsActor: the field is named and resolved where it is
// read. Pinned on the resolution chain rather than on prose, because the chain
// is the part a reader has to be able to reproduce.
func TestEventListHelp_DocumentsActor(t *testing.T) {
	bin := buildBinary(t)
	help := mgHelp(t, bin, "event", "list")

	for _, want := range []string{
		"actor",
		"MG_ACTOR",
		"POGO_AGENT_NAME",
		"OS user",
		"unknown",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("mg event list --help must mention %q — it is the only read surface for `actor`, got:\n%s", want, help)
		}
	}
}

// TestEventListHelp_MatchesCreatorHonesty: `mg show --help` already tells a
// reader that `creator` is self-asserted, that it is attribution and not
// authentication, and how it degrades. `actor` resolves through the identical
// chain and is read in the same fleet by the same agents, so the same two
// sentences have to be reachable from where it is read. One field documented
// honestly and its twin documented not at all is how a reader learns to trust
// the twin.
func TestEventListHelp_MatchesCreatorHonesty(t *testing.T) {
	bin := buildBinary(t)
	eventHelp := mgHelp(t, bin, "event", "list")
	showHelp := mgHelp(t, bin, "show")

	// Guard the guard: if `mg show --help` ever stops making these claims, the
	// parity below is satisfied vacuously by deleting the actor text.
	for _, want := range []string{"self-asserted", "attribution and not"} {
		if !strings.Contains(strings.ToLower(showHelp), want) {
			t.Fatalf("mg show --help no longer says %q about `creator`; this parity check is now vacuous:\n%s", want, showHelp)
		}
	}
	for _, want := range []string{"self-asserted", "attribution and not"} {
		if !strings.Contains(strings.ToLower(eventHelp), want) {
			t.Errorf("mg event list --help must carry the same honesty as `mg show --help` does for `creator`, missing %q:\n%s", want, eventHelp)
		}
	}
}

// TestEventListHelp_NamesThePogodFallback is the substance of mg-35fc, not a
// doc-completeness nit.
//
// `actor` reads `daniel` only when pogod itself acts, and pogod acts on exactly
// the claim and completion paths. Reading that value as Daniel is the same
// misreading that stalled an escalation on `creator` once already, so the help
// must name the daemon and both event types. A help that documented the
// resolution chain and stopped would leave a reader able to derive the chain and
// still conclude a human claimed the item.
func TestEventListHelp_NamesThePogodFallback(t *testing.T) {
	bin := buildBinary(t)
	help := mgHelp(t, bin, "event", "list")

	if !strings.Contains(strings.ToLower(help), "pogod") {
		t.Errorf("mg event list --help must name pogod as what an `actor: daniel` line actually means:\n%s", help)
	}
	for _, ty := range []string{"work.claim", "work.done"} {
		if !strings.Contains(help, ty) {
			t.Errorf("mg event list --help must name %s — the pogod fallback is confined to the claim and completion types, and those are the two a reader most wants to attribute:\n%s", ty, help)
		}
	}
}

// TestEventListHelp_DoesNotCallActorTheAssignee guards against the wrong premise
// coming back.
//
// "The audit log's `actor` records the item's ASSIGNEE, not whoever acted"
// circulated in this fleet after mg-3122 already fixed it, and reached at least
// two agents' durable notes. It is true only of lines written BEFORE that fix.
// The help is allowed — encouraged — to say so about old lines; it must not say
// it unscoped, because an unscoped sentence is exactly the artifact that keeps
// the premise travelling.
//
// Checked structurally at SENTENCE scope, so the wording stays free: a sentence
// that LINKS `actor` to the assignee must carry a marker putting it in the past
// or negating it. A sentence naming the assignee without naming `actor` is not
// the premise at all — the discriminating evidence for this ticket is precisely
// such a sentence ("a work.claim on an item that had no assignee"), and a
// paragraph-scoped version of this check flagged it.
func TestEventListHelp_DoesNotCallActorTheAssignee(t *testing.T) {
	bin := buildBinary(t)
	help := mgHelp(t, bin, "event", "list")

	scoped := 0
	for _, s := range helpSentences(help) {
		lower := strings.ToLower(s)
		if !strings.Contains(lower, "actor") || !strings.Contains(lower, "assignee") {
			continue
		}
		if !assigneeLinkIsScoped(lower) {
			t.Errorf("mg event list --help links `actor` to the assignee with nothing scoping it to "+
				"pre-mg-3122 lines; unscoped, that sentence IS the wrong premise:\n%s", s)
			continue
		}
		scoped++
	}
	if scoped == 0 {
		t.Error("mg event list --help never relates `actor` to the assignee at all. The log is append-only, " +
			"so pre-mg-3122 lines really do carry that meaning, and a reader of those lines needs to be told.")
	}
}

// assigneeLinkIsScoped reports whether a lowercased sentence that mentions both
// `actor` and the assignee puts that link in the past or denies it outright.
//
// The markers are deliberately narrow. An earlier draft accepted a bare "not",
// which the wrong premise itself satisfies — "`actor` records the item's
// assignee, not whoever acted" — so the guard would have passed the one string
// it exists to catch. TestAssigneeLinkIsScoped_NegativeControl pins that.
func assigneeLinkIsScoped(lower string) bool {
	for _, m := range []string{"mg-3122", "used to", "old meaning", "never"} {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

// TestAssigneeLinkIsScoped_NegativeControl proves the guard above can fail
// before any test trusts it to pass. The first string is the exact
// characterization that circulated in this fleet; a check that green-lights it
// is worse than no check, because it reports coverage of the one case it misses.
func TestAssigneeLinkIsScoped_NegativeControl(t *testing.T) {
	rejected := []string{
		"the audit log's `actor` records the item's assignee, not whoever acted",
		"'actor' is the assignee",
		"actor resolves to the item's assignee first, then its creator",
	}
	accepted := []string{
		"events written before mg-3122 carry the old meaning, in which 'actor' resolved to the item's assignee first",
		"'actor' used to be the item's assignee",
		"'actor' is never the assignee",
	}
	for _, s := range rejected {
		if assigneeLinkIsScoped(s) {
			t.Errorf("guard accepts the WRONG premise as scoped: %q", s)
		}
	}
	for _, s := range accepted {
		if !assigneeLinkIsScoped(s) {
			t.Errorf("guard rejects a correctly scoped historical statement: %q", s)
		}
	}
}

// helpSentences splits help text into sentences with wrapping newlines
// collapsed, so a check can reason about one claim at a time rather than about
// however the paragraph happened to wrap.
func helpSentences(help string) []string {
	flat := strings.Join(strings.Fields(help), " ")
	var out []string
	for _, s := range strings.Split(flat, ". ") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
