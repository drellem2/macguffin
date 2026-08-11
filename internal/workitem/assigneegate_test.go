package workitem

import (
	"errors"
	"strings"
	"testing"

	"github.com/drellem2/macguffin/internal/mgerr"
)

// The second route mg-5eee named: a typo in the gate vocabulary that yields a
// non-gating value with no error. Measured against the pre-fix binary, all six
// of these exited 0 and stored the value verbatim:
//
//	--assignee=blocekd:pm-pogo  -> exit 0, stored "blocekd:pm-pogo"
//	--assignee=blocked-pm-pogo  -> exit 0, stored "blocked-pm-pogo"
//	--assignee=blocked:         -> exit 0, stored "blocked:"
//	--assignee=Blocked:pm-pogo  -> exit 0, stored "Blocked:pm-pogo"
//	--assignee=humman           -> exit 0, stored "humman"
//	--assignee=parkd            -> exit 0, stored "parkd"
//
// Four of the six are refused now. Two are not, deliberately — see the
// documented residual below and the comment at the top of assigneegate.go.

func TestValidateAssignee_RefusesNearMissesOfTheGate(t *testing.T) {
	cases := []struct {
		value string
		why   string
	}{
		{"blocked:", "the prefix with no agent gates on a name that does not exist"},
		{"blocked: ", "whitespace is not an agent either"},
		{"blocekd:pm-pogo", "transposed prefix"},
		{"blockd:pm-pogo", "dropped letter"},
		{"block:pm-pogo", "truncated prefix"},
		{"Blocked:pm-pogo", "case — pogo compares the stored string"},
		{"BLOCKED:pm-pogo", "case"},
		{"blocked-pm-pogo", "hyphen instead of the colon"},
		{"blocked_pm-pogo", "underscore instead of the colon"},
		{"blockedpm-pogo", "no separator at all"},
		{"blocked", "the bare word names no agent"},
		{"Human", "case"},
		{"PARKED", "case"},
		{"Parked", "case"},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			err := ValidateAssignee(tc.value)
			if err == nil {
				t.Fatalf("accepted %q (%s) — it would not gate dispatch", tc.value, tc.why)
			}
			var mgErr *mgerr.Error
			if !errors.As(err, &mgErr) {
				t.Fatalf("error is not an *mgerr.Error: %v", err)
			}
			if got := mgErr.ExitCode(); got != 2 {
				t.Errorf("exit code = %d, want 2 (usage)", got)
			}
			// The refusal has to carry the spellings that work, because the
			// caller's next act is to retype the value.
			all := mgErr.Message + " " + mgErr.Hint
			if !strings.Contains(all, "blocked:") || !strings.Contains(all, "parked") {
				t.Errorf("refusal does not name the vocabulary that works:\n%s", all)
			}
		})
	}
}

// TestValidateAssignee_AcceptsEverythingElse is the half that decides whether
// the guard survives contact. mg has no register of legitimate assignees, so
// anything that is not a near-miss of the gate has to pass — a guard that
// refuses real names is one that gets switched off.
func TestValidateAssignee_AcceptsEverythingElse(t *testing.T) {
	ok := []string{
		"",                 // clears the field
		"human",            // the gate, exactly
		"parked",           // the gate, exactly
		"blocked:pm-pogo",  // the gate, exactly
		"blocked:daniel",   //
		"blocked:mg-5eee",  // an item id is a legal agent-ish name here
		"blocked: pm-pogo", // a space after the colon still names an agent
		"mayor",
		"pm-pogo",
		"daniel",
		"crew-hey",
		"polecat-5eee",
		"team:infra",  // colon-bearing and nowhere near `blocked`
		"parker",      // a real name one edit from `parked` — must pass
		"parkes",      //
		"humans",      // one edit from `human` — must pass
		"blockchain",  // begins with `bloc`, not `blocked`
		"unblocked-x", // does not BEGIN with the prefix
	}
	for _, v := range ok {
		t.Run(v, func(t *testing.T) {
			if err := ValidateAssignee(v); err != nil {
				t.Errorf("refused a legitimate assignee %q: %v", v, err)
			}
		})
	}
}

// TestValidateAssignee_DocumentedResidual is not a wish. It pins the two values
// the guard deliberately lets through, so that a later reader finds the choice
// recorded rather than rediscovering it as a hole. Widening the net to catch
// them means refusing by edit distance from a five-letter word, which is the
// neighbourhood `parker` and `humans` live in — see the test above.
func TestValidateAssignee_DocumentedResidual(t *testing.T) {
	for _, v := range []string{"parkd", "humman"} {
		if err := ValidateAssignee(v); err != nil {
			t.Errorf("ValidateAssignee(%q) now refuses; if that is intended, "+
				"move it into the refusal table and delete it here: %v", v, err)
		}
	}
}

// TestUpdateRefusesNonGatingAssignee is the same check on the write path: the
// value must never reach disk, and the refusal must leave the item alone.
func TestUpdateRefusesNonGatingAssignee(t *testing.T) {
	root := t.TempDir()
	item := seedGated(t, root, "blocked:pm-pogo")

	typo := "blocekd:pm-pogo"
	if _, err := Update(root, item.ID, UpdateField{Assignee: &typo}); err == nil {
		t.Fatal("Update stored a misspelled gate and reported success")
	}

	after, err := Read(root, item.ID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if after.Assignee != "blocked:pm-pogo" {
		t.Errorf("assignee = %q; the refusal should have left it at blocked:pm-pogo", after.Assignee)
	}
}

// TestUnclaimRefusesNonGatingAssignee covers the other writer. `mg unclaim
// --assignee` exists precisely so an item is never in available/ without the
// reason it is held (mg-ed7b); a misspelled reason releases it into available/
// carrying a hold that holds nothing, which is the same defect the flag was
// built to close.
func TestUnclaimRefusesNonGatingAssignee(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	item, err := Create(root, "mg-", "task", "Release me", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := Claim(root, item.ID, 4242); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	if _, err := Unclaim(root, item.ID, WithUnclaimAssignee("blocked-pm-pogo")); err == nil {
		t.Fatal("Unclaim released the item with a misspelled hold")
	}

	// The claim is still held: an item that stayed claimed is recoverable by
	// re-running, which is the trade the write-before-release ordering makes.
	m, err := ResolveUnique(root, item.ID)
	if err != nil {
		t.Fatalf("ResolveUnique: %v", err)
	}
	if m.Status != "claimed" {
		t.Errorf("status = %q, want claimed — the refusal released it anyway", m.Status)
	}
}
