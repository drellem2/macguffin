package workitem

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The four fixtures the whole classification exists to separate. They are
// deliberately shared between the "the old comparison collapses these" test and
// the "the new one separates them" test, so neither can drift into testing a
// different set of inputs than the other.
//
// Case 2 is not invented: it is the mg-a6c9 specimen from the mg-eb1e
// reconciliation incident. The archive held a refinery stub (branch,
// completed_by, mr); the stray held the polecat verdict. DISJOINT key sets, so
// a strict superset after merge — and `differs` said nothing about that, which
// is how a straight copy came within one review of destroying all three stub
// keys.
type compareCase struct {
	name       string
	stray      string
	auth       string
	unreadable bool // case 4: the stray exists but cannot be read
	want       SidecarRelation
}

func compareCases() []compareCase {
	return []compareCase{
		{
			name:  "equivalent/re-serialised with different key order",
			stray: `{"verdict":"pass","rounds":2}`,
			auth:  `{"rounds": 2, "verdict": "pass"}`,
			want:  RelationEquivalent,
		},
		{
			name:  "subset/mg-a6c9 stub vs polecat verdict",
			stray: `{"advisory":"none","folded_commits":3,"rounds":1,"summary":"ok","verdict":"pass"}`,
			auth:  `{"advisory":"none","branch":"polecat-a6c9","completed_by":"mg-a6c9","folded_commits":3,"mr":"mr-17","rounds":1,"summary":"ok","verdict":"pass"}`,
			want:  RelationSubset,
		},
		{
			name:  "conflict/same key, different values",
			stray: `{"branch":"polecat-a6c9","verdict":"pass"}`,
			auth:  `{"branch":"polecat-other","verdict":"fail"}`,
			want:  RelationConflict,
		},
		{
			name:       "unknown/stray exists but is unreadable",
			stray:      `{"verdict":"pass"}`,
			auth:       `{"verdict":"pass","branch":"b"}`,
			unreadable: true,
			want:       RelationUnknown,
		},
	}
}

// TestCompareSidecars_OldBytesEqualCollapsesAllFour is the discriminating
// direction, and it must be shown to FAIL FIRST. It asserts what the previous
// implementation did — `err != nil || !bytes.Equal(stray, auth)` — and pins
// that this predicate returns the SAME verdict, `differs`, for all four cases.
//
// Without this, a suite could pass while the tool reported one token for
// everything, which is precisely the behaviour being fixed. A test over the
// subset case alone would have done exactly that.
func TestCompareSidecars_OldBytesEqualCollapsesAllFour(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range compareCases() {
		strayPath := filepath.Join(dir, "stray")
		writeUnreadable(t, strayPath, tc.stray, tc.unreadable)

		// The old line, verbatim in behaviour.
		auth := []byte(tc.auth)
		stray, err := os.ReadFile(strayPath)
		oldVerdict := err != nil || !bytes.Equal(stray, auth)

		if !oldVerdict {
			t.Fatalf("%s: old predicate said identical; fixture is not a difference case", tc.name)
		}
		os.Remove(strayPath)
	}
	// All four produced `true` — one token, four opposite safe actions. That is
	// the defect. Everything below shows the new comparison separating them.
}

// TestCompareSidecars_FourCasesFourVerdicts is the positive control the ticket
// demands: one fixture per case, each yielding a DIFFERENT verdict, with the
// unreadable case proven by making a file genuinely unreadable rather than
// absent.
func TestCompareSidecars_FourCasesFourVerdicts(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth")

	seen := map[SidecarRelation]string{}
	for _, tc := range compareCases() {
		if tc.unreadable && os.Geteuid() == 0 {
			t.Skip("running as root: chmod 0000 does not make a file unreadable")
		}
		strayPath := filepath.Join(dir, "stray")
		writeUnreadable(t, strayPath, tc.stray, tc.unreadable)
		if err := os.WriteFile(authPath, []byte(tc.auth), 0o644); err != nil {
			t.Fatal(err)
		}

		got := compareSidecarFiles(strayPath, authPath)
		if got.Relation != tc.want {
			t.Errorf("%s: Relation = %q, want %q (note %q)", tc.name, got.Relation, tc.want, got.Note)
		}
		if prev, dup := seen[got.Relation]; dup {
			t.Errorf("%s and %s both classified %q — the verdicts do not discriminate",
				tc.name, prev, got.Relation)
		}
		seen[got.Relation] = tc.name

		// An errored probe must not masquerade as a finding.
		if tc.want == RelationUnknown {
			if got.Differs() {
				t.Error("unreadable stray reported as a difference; a failed probe is not evidence")
			}
			if got.Note == "" {
				t.Error("RelationUnknown with no Note: the operator cannot tell what failed")
			}
		}
		os.Remove(strayPath)
	}
	if len(seen) != 4 {
		t.Fatalf("four cases produced %d distinct verdicts: %v", len(seen), seen)
	}
}

// TestCompareSidecars_SubsetNamesTheSuperset covers the acceptance criterion in
// both directions. Which side is bigger is an accident of which copy went
// stray, so the verdict must name the superset rather than assume one.
func TestCompareSidecars_SubsetNamesTheSuperset(t *testing.T) {
	small := []byte(`{"verdict":"pass"}`)
	big := []byte(`{"verdict":"pass","branch":"b","completed_by":"c","mr":"m"}`)
	wantKeys := []string{"branch", "completed_by", "mr"}

	authBigger := compareSidecarBytes(small, big)
	if authBigger.Relation != RelationSubset || authBigger.Superset != SideAuthoritative {
		t.Errorf("Relation/Superset = %q/%q, want subset/authoritative", authBigger.Relation, authBigger.Superset)
	}
	if !reflect.DeepEqual(authBigger.Keys, wantKeys) {
		t.Errorf("Keys = %v, want %v", authBigger.Keys, wantKeys)
	}

	strayBigger := compareSidecarBytes(big, small)
	if strayBigger.Relation != RelationSubset || strayBigger.Superset != SideStray {
		t.Errorf("Relation/Superset = %q/%q, want subset/stray", strayBigger.Relation, strayBigger.Superset)
	}
	if !reflect.DeepEqual(strayBigger.Keys, wantKeys) {
		t.Errorf("Keys = %v, want %v", strayBigger.Keys, wantKeys)
	}
}

// TestCompareSidecars_ConflictNamesEveryDisagreeingKey pins the load-bearing
// part of the conflict verdict. "conflict" tells an operator to look; naming
// the keys tells them what they are choosing between, which is the difference
// between a decision and a guess. Both flavours of disagreement must appear:
// a shared key whose values differ, and a key only one side holds.
func TestCompareSidecars_ConflictNamesEveryDisagreeingKey(t *testing.T) {
	stray := []byte(`{"verdict":"pass","rounds":2,"only_stray":1}`)
	auth := []byte(`{"verdict":"fail","rounds":2,"only_auth":1}`)

	got := compareSidecarBytes(stray, auth)
	if got.Relation != RelationConflict {
		t.Fatalf("Relation = %q, want conflict", got.Relation)
	}
	want := []string{"only_auth", "only_stray", "verdict"}
	if !reflect.DeepEqual(got.Keys, want) {
		t.Errorf("Keys = %v, want %v", got.Keys, want)
	}
	if got.Superset != "" {
		t.Errorf("Superset = %q for a conflict; there is no superset to keep", got.Superset)
	}
}

// TestCompareSidecars_DisjointKeysAreAConflictNotASubset guards the direction a
// merge would get wrong. Disjoint key sets are not a subset relation: neither
// copy contains the other, so there is no superset to keep and the merge is a
// judgement call.
func TestCompareSidecars_DisjointKeysAreAConflictNotASubset(t *testing.T) {
	got := compareSidecarBytes([]byte(`{"a":1}`), []byte(`{"b":2}`))
	if got.Relation != RelationConflict {
		t.Errorf("Relation = %q for disjoint key sets, want conflict", got.Relation)
	}
}

// TestCompareSidecars_OverlapDisagreementBeatsSubset is the trap in the subset
// rule. Keys can be a strict subset while a SHARED key disagrees — keeping the
// superset would then silently overwrite a value, so containment alone must
// never be enough to call it mergeable.
func TestCompareSidecars_OverlapDisagreementBeatsSubset(t *testing.T) {
	stray := []byte(`{"verdict":"pass"}`)
	auth := []byte(`{"verdict":"fail","branch":"b"}`)

	got := compareSidecarBytes(stray, auth)
	if got.Relation != RelationConflict {
		t.Fatalf("Relation = %q, want conflict: keys are a subset but the overlap disagrees", got.Relation)
	}
	if !containsKey(got.Keys, "verdict") {
		t.Errorf("Keys = %v, want the disagreeing shared key 'verdict' named", got.Keys)
	}
}

// TestCompareSidecars_EquivalentIsSafeToDelete pins case 1 as a FALSE POSITIVE
// under the old comparison: identical content, differing bytes. Reporting it as
// a difference sends an operator to open two files that say the same thing.
func TestCompareSidecars_EquivalentIsSafeToDelete(t *testing.T) {
	got := compareSidecarBytes(
		[]byte(`{"verdict":"pass","rounds":2}`),
		[]byte("{\n  \"rounds\": 2,\n  \"verdict\": \"pass\"\n}\n"),
	)
	if got.Relation != RelationEquivalent {
		t.Fatalf("Relation = %q, want equivalent", got.Relation)
	}
	if !got.SameContent() {
		t.Error("SameContent() = false for the same object re-serialised")
	}
}

// TestCompareSidecars_NonJSONIsOpaqueNotConflict keeps the honest-uncertainty
// promise: if the key-level analysis does not apply, say so rather than
// defaulting to the alarming or the reassuring verdict.
func TestCompareSidecars_NonJSONIsOpaqueNotConflict(t *testing.T) {
	got := compareSidecarBytes([]byte("not json at all"), []byte(`{"verdict":"pass"}`))
	if got.Relation != RelationOpaque {
		t.Errorf("Relation = %q, want opaque", got.Relation)
	}
	if got.Note == "" {
		t.Error("RelationOpaque with no Note: the operator cannot tell which side is unparseable")
	}
	// A JSON array is valid JSON but not an object, so the key analysis still
	// does not apply.
	if arr := compareSidecarBytes([]byte(`[1,2]`), []byte(`{"a":1}`)); arr.Relation != RelationOpaque {
		t.Errorf("Relation = %q for a JSON array, want opaque", arr.Relation)
	}
}

// TestStraySidecar_RedundantOnlyForProvenSameContent guards the safety
// predicate against the classification widening it. Only a proven-equal
// content may be called safe to delete; a subset must not be, because the
// subset relation can be the artefact of one side having been TRUNCATED.
func TestStraySidecar_RedundantOnlyForProvenSameContent(t *testing.T) {
	cases := []struct {
		rel  SidecarRelation
		want bool
	}{
		{RelationIdentical, true},
		{RelationEquivalent, true},
		{RelationSubset, false},
		{RelationConflict, false},
		{RelationOpaque, false},
		{RelationUnknown, false},
	}
	for _, tc := range cases {
		s := StraySidecar{AuthoritativeExists: true, Comparison: SidecarComparison{Relation: tc.rel}}
		if got := s.Redundant(); got != tc.want {
			t.Errorf("Redundant() = %v for %q, want %v", got, tc.rel, tc.want)
		}
	}
	// No authoritative copy at all: never redundant, whatever the relation.
	s := StraySidecar{Comparison: SidecarComparison{Relation: RelationIdentical}}
	if s.Redundant() {
		t.Error("Redundant() = true with no authoritative copy — that would delete the only record")
	}
}

// TestFindStraySidecars_ClassifiesEndToEnd runs the four cases through the real
// scan, so the classification is proven to survive the path from disk to
// StraySidecar and is not merely a property of the comparison helper.
func TestFindStraySidecars_ClassifiesEndToEnd(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: chmod 0000 does not make a file unreadable")
	}
	root := t.TempDir()
	want := map[string]SidecarRelation{
		"mg-e001": RelationEquivalent,
		"mg-e002": RelationSubset,
		"mg-e003": RelationConflict,
		"mg-e004": RelationUnknown,
	}
	for i, tc := range compareCases() {
		id := []string{"mg-e001", "mg-e002", "mg-e003", "mg-e004"}[i]
		writeStoreFile(t, root, "done/"+id+".md", "# item")
		writeStoreFile(t, root, "done/"+id+".result.json", tc.auth)
		p := writeStoreFile(t, root, "claimed/"+id+".result.json", tc.stray)
		if tc.unreadable {
			makeUnreadable(t, p)
		}
	}

	strays, err := FindStraySidecars(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(strays) != 4 {
		t.Fatalf("expected 4 strays, got %d", len(strays))
	}
	for _, s := range strays {
		if got := s.Comparison.Relation; got != want[s.ID] {
			t.Errorf("%s: Relation = %q, want %q (note %q)", s.ID, got, want[s.ID], s.Comparison.Note)
		}
		// Only the equivalent case is safe to delete — that is the point of
		// separating it from the other three, which all keep the stray.
		wantRedundant := want[s.ID] == RelationEquivalent
		if got := s.Redundant(); got != wantRedundant {
			t.Errorf("%s: Redundant() = %v for %q, want %v", s.ID, got, want[s.ID], wantRedundant)
		}
	}
}

// TestFindStraySidecars_UnreadableAuthoritativeIsNotMissing separates presence
// from readability on the OTHER side. An authoritative copy that exists but
// cannot be read must not be reported as MISSING — that would tell the operator
// the stray is the only surviving record when it may not be.
func TestFindStraySidecars_UnreadableAuthoritativeIsNotMissing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: chmod 0000 does not make a file unreadable")
	}
	root := t.TempDir()
	writeStoreFile(t, root, "done/mg-f001.md", "# item")
	auth := writeStoreFile(t, root, "done/mg-f001.result.json", `{"verdict":"pass"}`)
	writeStoreFile(t, root, "claimed/mg-f001.result.json", `{"verdict":"pass"}`)
	makeUnreadable(t, auth)

	strays, err := FindStraySidecars(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(strays) != 1 {
		t.Fatalf("expected 1 stray, got %d", len(strays))
	}
	s := strays[0]
	if !s.AuthoritativeExists {
		t.Error("AuthoritativeExists = false for a file that is present but unreadable")
	}
	if s.Comparison.Relation != RelationUnknown {
		t.Errorf("Relation = %q, want unknown", s.Comparison.Relation)
	}
	if s.Redundant() {
		t.Error("Redundant() = true on an unread comparison — safety inferred from a failed probe")
	}
}

func writeUnreadable(t *testing.T, path, content string, unreadable bool) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if unreadable {
		makeUnreadable(t, path)
	}
}

// makeUnreadable strips every permission bit so the file EXISTS but cannot be
// read — the ticket's case 4, proven rather than simulated by deleting it. The
// mode is restored on cleanup so t.TempDir can remove the tree.
func makeUnreadable(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0o644) })
	if _, err := os.ReadFile(path); err == nil {
		t.Fatalf("%s is still readable after chmod 0000; the case-4 fixture is not real", path)
	}
}

func containsKey(keys []string, want string) bool {
	for _, k := range keys {
		if k == want {
			return true
		}
	}
	return false
}
