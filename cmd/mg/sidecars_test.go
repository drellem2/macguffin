package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedStray writes an item in done/ with an authoritative sidecar and a stray
// copy in claimed/, so `mg sidecars` has something to classify.
func seedStray(t *testing.T, root, id, auth, stray string) {
	t.Helper()
	write := func(rel, content string) string {
		p := filepath.Join(root, "work", rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("done/"+id+".md", "# item")
	write("done/"+id+".result.json", auth)
	write("claimed/"+id+".result.json", stray)
}

// TestSidecars_SubsetAndConflictAreClassifiedDifferently is the acceptance
// criterion, run through the actual CLI.
//
// The positive control is that BOTH cases are present in one store: a test
// using only the subset pair would pass while the tool reported the same token
// for everything, which was the behaviour before mg-dcb1. What makes this test
// load-bearing is the final assertion that the two verdicts are not equal.
func TestSidecars_SubsetAndConflictAreClassifiedDifferently(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()

	// Strict subset: the authoritative copy holds three keys the stray lacks
	// and they agree everywhere else. This is the mg-a6c9 shape.
	seedStray(t, root, "mg-s001",
		`{"verdict":"pass","branch":"b","completed_by":"c","mr":"m"}`,
		`{"verdict":"pass"}`)
	// Genuine conflict: the two disagree on a shared key.
	seedStray(t, root, "mg-s002",
		`{"verdict":"fail","rounds":2}`,
		`{"verdict":"pass","rounds":2}`)

	out, code := mgArchive(t, bin, root, "sidecars")
	if code != 0 {
		t.Fatalf("mg sidecars: exit %d\n%s", code, out)
	}

	subsetBlock, conflictBlock := blockFor(t, out, "mg-s001"), blockFor(t, out, "mg-s002")

	if !strings.Contains(subsetBlock, "SUBSET") {
		t.Errorf("subset pair not reported as SUBSET:\n%s", subsetBlock)
	}
	// Naming the superset is half the acceptance criterion.
	if !strings.Contains(subsetBlock, "the authoritative copy is the superset") {
		t.Errorf("subset verdict does not name the superset:\n%s", subsetBlock)
	}
	for _, k := range []string{"branch", "completed_by", "mr"} {
		if !strings.Contains(subsetBlock, k) {
			t.Errorf("subset verdict does not name key %q:\n%s", k, subsetBlock)
		}
	}

	if !strings.Contains(conflictBlock, "CONFLICT") {
		t.Errorf("conflicting pair not reported as CONFLICT:\n%s", conflictBlock)
	}
	// Naming the differing keys is the other half: "differs" tells an operator
	// to look, a key list tells them what they are choosing between.
	if !strings.Contains(conflictBlock, "verdict") {
		t.Errorf("conflict verdict does not name the differing key:\n%s", conflictBlock)
	}
	if strings.Contains(conflictBlock, "rounds") {
		t.Errorf("conflict names 'rounds', on which the two copies AGREE:\n%s", conflictBlock)
	}

	if subsetBlock == conflictBlock {
		t.Error("subset and conflict rendered identically — the report does not discriminate")
	}
}

// TestSidecarsJSON_CarriesRelationAndKeys pins the machine-readable contract,
// which is what a script would branch on.
func TestSidecarsJSON_CarriesRelationAndKeys(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	seedStray(t, root, "mg-j001",
		`{"verdict":"pass","branch":"b"}`,
		`{"verdict":"pass"}`)
	// Same object, re-serialised: differing bytes, identical content. Under a
	// byte comparison this was a false positive that sent an operator to open
	// two files saying the same thing.
	seedStray(t, root, "mg-j002",
		"{\n  \"rounds\": 2,\n  \"verdict\": \"pass\"\n}\n",
		`{"verdict":"pass","rounds":2}`)

	out, code := mgArchive(t, bin, root, "sidecars", "--json")
	if code != 0 {
		t.Fatalf("mg sidecars --json: exit %d\n%s", code, out)
	}
	var got []struct {
		ID            string   `json:"id"`
		Relation      string   `json:"relation"`
		Superset      string   `json:"superset"`
		DifferingKeys []string `json:"differing_keys"`
		Differs       bool     `json:"differs"`
		Redundant     bool     `json:"redundant"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	byID := map[string]int{}
	for i, g := range got {
		byID[g.ID] = i
	}

	sub := got[byID["mg-j001"]]
	if sub.Relation != "subset" || sub.Superset != "authoritative" {
		t.Errorf("mg-j001: relation/superset = %q/%q, want subset/authoritative", sub.Relation, sub.Superset)
	}
	if len(sub.DifferingKeys) != 1 || sub.DifferingKeys[0] != "branch" {
		t.Errorf("mg-j001: differing_keys = %v, want [branch]", sub.DifferingKeys)
	}
	if sub.Redundant {
		t.Error("mg-j001: redundant = true for a subset; keeping the superset is a judgement call")
	}

	eq := got[byID["mg-j002"]]
	if eq.Relation != "equivalent" {
		t.Errorf("mg-j002: relation = %q, want equivalent", eq.Relation)
	}
	if !eq.Redundant {
		t.Error("mg-j002: redundant = false for the same object re-serialised")
	}
	// differing_keys must be an array even when empty, so a consumer can
	// iterate it without a nil check.
	if eq.DifferingKeys == nil {
		t.Error("mg-j002: differing_keys is null, want []")
	}
	if !strings.Contains(out, `"differing_keys": []`) {
		t.Error("empty differing_keys not serialised as []")
	}
}

// TestSidecars_UnreadableStrayIsUnknownNotDiffers is the fourth conflation: a
// read FAILURE and a real difference produced the identical verdict. An errored
// probe and a negative result must never share a token.
func TestSidecars_UnreadableStrayIsUnknownNotDiffers(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: chmod 0000 does not make a file unreadable")
	}
	bin := buildBinary(t)
	root := t.TempDir()
	seedStray(t, root, "mg-u001", `{"verdict":"pass"}`, `{"verdict":"pass"}`)

	stray := filepath.Join(root, "work", "claimed", "mg-u001.result.json")
	if err := os.Chmod(stray, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(stray, 0o644) })

	out, code := mgArchive(t, bin, root, "sidecars", "--json")
	if code != 0 {
		t.Fatalf("mg sidecars --json: exit %d\n%s", code, out)
	}
	var got []struct {
		Relation  string `json:"relation"`
		Differs   bool   `json:"differs"`
		Redundant bool   `json:"redundant"`
		Note      string `json:"note"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
	if got[0].Relation != "unknown" {
		t.Errorf("relation = %q for an unreadable stray, want unknown", got[0].Relation)
	}
	if got[0].Differs {
		t.Error("differs = true for a file that could not be READ; a failed probe is not a finding")
	}
	if got[0].Redundant {
		t.Error("redundant = true on an unread comparison — safety inferred from a failed probe")
	}
	if got[0].Note == "" {
		t.Error("note is empty: the operator cannot tell what failed")
	}
}

// blockFor returns the report stanza for one id, so assertions about one
// stray's verdict cannot be satisfied by text belonging to another.
func blockFor(t *testing.T, out, id string) string {
	t.Helper()
	for _, block := range strings.Split(out, "\n\n") {
		if strings.Contains(block, id+".result.json") {
			return block
		}
	}
	t.Fatalf("no report block for %s in:\n%s", id, out)
	return ""
}
