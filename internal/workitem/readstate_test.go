package workitem

import (
	"strings"
	"testing"
)

// mg-43d0's second half. body_hash_before is read from the STORED body inside
// the edit, so it always equals the previous record's body_hash_after and any
// "zero lost updates" derived from the pair is true by construction. pm-pogo
// computed exactly that over the live log and retracted it.
//
// These tests pin the field that makes the difference sayable: whether the
// caller's own read-state was recorded, or is simply absent — and absent is not
// clean.

// editEvent applies fields and returns the single work.edited event the call
// emitted, so no assertion can read a stale line from earlier in the log.
func editEvent(t *testing.T, root, id string, fields UpdateField) eventView {
	t.Helper()
	return onlyEdit(t, editAndCountEvents(t, root, id, fields))
}

// The 93-of-138 case: a full-body replacement with no --if-unchanged. Nothing
// in the record says what the writer believed it was overwriting, and the log
// must SAY that rather than look complete.
func TestReadState_UnguardedReplaceIsUnmeasurableNotClean(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	id, _ := seedItem(t, root, "Contested", "\n# Contested\n\n## original\n\nprose.\n")

	newBody := "\n# Contested\n\n## rewritten\n\nreplaced wholesale from a read of unknown age.\n"
	got := editEvent(t, root, id, UpdateField{Body: &newBody})

	if got.Extra["mode"] != "replace" {
		t.Fatalf("mode = %q, want replace", got.Extra["mode"])
	}
	if want := "unmeasurable"; got.Extra["body_read_state"] != want {
		t.Errorf("body_read_state = %q, want %q — an unguarded replace's read-state is not recorded anywhere, and the log must say so",
			got.Extra["body_read_state"], want)
	}
	if v, ok := got.Extra["body_hash_asserted"]; ok {
		t.Errorf("body_hash_asserted = %q, want absent — the caller asserted nothing", v)
	}
}

// The measurable case. The caller named the version it read, so the log now
// carries both what was overwritten and what the writer believed it was
// overwriting — the two values that were previously the same value.
func TestReadState_GuardedReplaceRecordsTheCallersAssertedVersion(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	id, hash := seedItem(t, root, "Contested", "\n# Contested\n\n## original\n\nprose.\n")

	newBody := "\n# Contested\n\n## rewritten\n\nreplaced from a read this caller can name.\n"
	got := editEvent(t, root, id, UpdateField{Body: &newBody, IfUnchanged: hash})

	if want := "asserted"; got.Extra["body_read_state"] != want {
		t.Errorf("body_read_state = %q, want %q", got.Extra["body_read_state"], want)
	}
	if got.Extra["body_hash_asserted"] != hash {
		t.Errorf("body_hash_asserted = %q, want %q", got.Extra["body_hash_asserted"], hash)
	}
	// The asserted value is what makes the pre-existing field checkable: on a
	// satisfied guard the caller's version IS the overwritten one, and a future
	// reader can verify that rather than assume it.
	if got.Extra["body_hash_before"] != got.Extra["body_hash_asserted"] {
		t.Errorf("body_hash_before %q and body_hash_asserted %q disagree on a satisfied guard",
			got.Extra["body_hash_before"], got.Extra["body_hash_asserted"])
	}
}

// A prefix is a legal --if-unchanged value, and what gets recorded is the
// normalized form the guard actually compared — not the raw flag string.
func TestReadState_RecordsTheNormalizedAssertion(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	id, hash := seedItem(t, root, "Contested", "\n# Contested\n\n## original\n\nprose.\n")

	newBody := "\n# Contested\n\n## rewritten\n\nprose.\n"
	prefix := strings.ToUpper(hash[:12])
	got := editEvent(t, root, id, UpdateField{Body: &newBody, IfUnchanged: prefix})

	if want := hash[:12]; got.Extra["body_hash_asserted"] != want {
		t.Errorf("body_hash_asserted = %q, want the normalized %q", got.Extra["body_hash_asserted"], want)
	}
	if got.Extra["body_read_state"] != "asserted" {
		t.Errorf("body_read_state = %q, want asserted", got.Extra["body_read_state"])
	}
}

// The 919-of-1,287 case. An append composes against the body on disk at write
// time, so there is no read-state to be missing: calling it "unmeasurable"
// would manufacture a gap where none exists, and would drown the 93 replaces
// that are the real one.
func TestReadState_AppendAndMetadataAreNotAtRisk(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	id, _ := seedItem(t, root, "Contested", "\n# Contested\n\n## original\n\nprose.\n")

	cases := []struct {
		name   string
		fields UpdateField
		mode   string
	}{
		{"append", UpdateField{AppendBody: strPtr("## a dated section")}, "append"},
		{"metadata", UpdateField{Assignee: strPtr("mayor")}, "metadata"},
		{"incidental", UpdateField{Title: strPtr("Contested, retitled")}, "incidental"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := editEvent(t, root, id, tc.fields)
			if got.Extra["mode"] != tc.mode {
				t.Fatalf("mode = %q, want %q", got.Extra["mode"], tc.mode)
			}
			if want := "not_at_risk"; got.Extra["body_read_state"] != want {
				t.Errorf("body_read_state = %q, want %q", got.Extra["body_read_state"], want)
			}
		})
	}
}

// The field must be present on every work.edited line, not only the interesting
// ones. A field that appears sometimes cannot be counted: absent would mean
// both "not applicable" and "written by an older mg", which is the exact
// ambiguity this exists to remove.
func TestReadState_IsAlwaysPresent(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	id, _ := seedItem(t, root, "Contested", "\n# Contested\n\n## original\n\nprose.\n")

	for _, fields := range []UpdateField{
		{AppendBody: strPtr("## one")},
		{Priority: strPtr("high")},
		{AddTags: []string{"probe"}},
	} {
		got := editEvent(t, root, id, fields)
		if got.Extra["body_read_state"] == "" {
			t.Errorf("body_read_state absent on %+v", got.Extra)
		}
	}
}

// A refused edit writes nothing at all, including no read-state claim. The
// guard's whole property is that the stored item and the log are untouched.
func TestReadState_RefusedGuardEmitsNothing(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	id, hash := seedItem(t, root, "Contested", "\n# Contested\n\n## original\n\nprose.\n")

	// Somebody else writes, invalidating the hash the caller holds.
	if _, err := Update(root, id, UpdateField{AppendBody: strPtr("## somebody else")}); err != nil {
		t.Fatalf("interleaved Update: %v", err)
	}

	before := len(readEvents(t, root))
	newBody := "\n# Contested\n\n## rewritten\n\nfrom a stale read.\n"
	if _, err := Update(root, id, UpdateField{Body: &newBody, IfUnchanged: hash}); err == nil {
		t.Fatal("Update accepted a stale --if-unchanged")
	}
	if after := len(readEvents(t, root)); after != before {
		t.Errorf("event log grew by %d on a refused edit", after-before)
	}
}
