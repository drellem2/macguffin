package workitem

import (
	"strings"
	"testing"
)

// These pin the PREDICATE itself, below the CLI. The CLI tests prove the guard
// fires end to end; these fix where its edge sits, because the edge is where a
// silent false pass would live and a false pass here is permanent.

func TestBlockedOnTags_Matching(t *testing.T) {
	tests := []struct {
		name string
		tags []string
		want []string
	}{
		{"no tags at all", nil, nil},
		{"the live convention", []string{"blocked-on-daniel"}, []string{"blocked-on-daniel"}},
		{"a qualified variant", []string{"blocked-on-daniel-confirm"}, []string{"blocked-on-daniel-confirm"}},
		{"found among others, in tag order", []string{"infra", "blocked-on-daniel", "cleanup"}, []string{"blocked-on-daniel"}},
		{"more than one", []string{"blocked-on-daniel", "blocked-on-legal"}, []string{"blocked-on-daniel", "blocked-on-legal"}},
		// Case folding and a bare prefix both err toward REFUSING. This
		// predicate only ever causes a refusal, so over-matching costs one
		// command and under-matching loses an item.
		{"case is folded", []string{"Blocked-On-Daniel"}, []string{"Blocked-On-Daniel"}},
		{"a bare prefix still blocks", []string{"blocked-on-"}, []string{"blocked-on-"}},
		// The trigger is the prefix and nothing adjacent to it. A guard that
		// fires on ordinary tags is a guard that gets switched off.
		{"blocked alone is not the convention", []string{"blocked"}, nil},
		{"the prefix must lead", []string{"was-blocked-on-daniel"}, nil},
		{"a different word", []string{"unblocked", "needs-daniel"}, nil},
		{"the successor tag is a different guard", []string{"successor:mg-1234"}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BlockedOnTags(&Item{ID: "mg-test", Tags: tt.tags})
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("BlockedOnTags(%v) = %v, want %v", tt.tags, got, tt.want)
			}

			err := requireNotBlocked(&Item{ID: "mg-test", Tags: tt.tags})
			if len(tt.want) == 0 {
				if err != nil {
					t.Errorf("requireNotBlocked(%v) = %v, want nil", tt.tags, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("requireNotBlocked(%v) = nil, want a refusal", tt.tags)
			}
			// The refusal must quote every tag it found back verbatim: the
			// operator's first question is "which one?", and --rm-tags needs
			// the literal string, not a normalized rendering of it.
			for _, tag := range tt.want {
				if !strings.Contains(err.Error(), tag) {
					t.Errorf("refusal %q does not name the tag %q it found", err, tag)
				}
			}
		})
	}
}

// TestRequireNotBlocked_IgnoresType is the whole point of this guard existing
// separately from requireSuccessor: the population is named by the TAG, not the
// type. Scoping it to a type would rebuild the defect it was written to fix.
func TestRequireNotBlocked_IgnoresType(t *testing.T) {
	for _, typ := range []string{"task", "bug", "chore", "design", ""} {
		item := &Item{ID: "mg-test", Type: typ, Tags: []string{"blocked-on-daniel"}}
		if err := requireNotBlocked(item); err == nil {
			t.Errorf("type %q tagged blocked-on-daniel was permitted", typ)
		}
	}
}
