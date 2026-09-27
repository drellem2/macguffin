package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This file is the end-to-end contract for the 4→5 id widening
// (drellem2/macguffin#33). New items are minted with 5 hex characters; every
// item minted before that keeps its 4-character id, and nothing that READS an
// id may care which width it was given. A store therefore holds both widths
// side by side, forever — there is no migration and no renumbering.
//
// The legacy item is planted by minting a normal item and rewriting its file
// name and `id:` line to a 4-character id. That is byte-for-byte what a
// pre-widening store holds: the widening changed the minter and nothing else.

// legacyID is a 4-character id no 5-character mint can ever produce.
const legacyID = "mg-0a1b"

// plantLegacy mints an item with the given extra `mg new` args and renames it
// to id, in whatever active directory it landed in. It returns id.
func plantLegacy(t *testing.T, bin string, env []string, home, id, title string, args ...string) string {
	t.Helper()
	out := emOK(t, bin, env, append(append([]string{"new", "--no-repo"}, args...), title)...)
	fields := strings.Fields(out)
	if len(fields) < 2 || fields[0] != "Created" {
		t.Fatalf("unexpected `mg new` output: %q", out)
	}
	minted := strings.TrimSuffix(fields[1], ":")

	matches, _ := filepath.Glob(filepath.Join(home, ".macguffin", "work", "*", minted+".md"))
	if len(matches) != 1 {
		t.Fatalf("expected one file for %s, found %v", minted, matches)
	}
	src := matches[0]
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	rewritten := strings.Replace(string(body), "\nid: "+minted+"\n", "\nid: "+id+"\n", 1)
	if rewritten == string(body) {
		t.Fatalf("no `id: %s` line to rewrite in:\n%s", minted, body)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(src), id+".md"), []byte(rewritten), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestCLI_NewMintsFiveHexChars pins the width of a fresh mint at the CLI.
func TestCLI_NewMintsFiveHexChars(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := buildBinary(t)
	env := emEnv(t.TempDir())
	emInit(t, bin, env)

	for _, title := range []string{"one", "two", "three"} {
		id := emNew(t, bin, env, title)
		hex, ok := strings.CutPrefix(id, "mg-")
		if !ok || len(hex) != 5 || strings.Trim(hex, "0123456789abcdef") != "" {
			t.Errorf("mg new minted %q, want mg- plus 5 lowercase hex chars", id)
		}
	}
}

// TestCLI_LegacyFourCharIDRoundTrips walks one pre-widening id through every
// command the widening must not break — show, edit, claim, done, depends,
// successor/predecessor links in both directions, archive, and the @YYYY-MM
// partition qualifier — in a store that also holds 5-character ids.
func TestCLI_LegacyFourCharIDRoundTrips(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := buildBinary(t)
	home := t.TempDir()
	env := emEnv(home)
	emInit(t, bin, env)

	id := plantLegacy(t, bin, env, home, legacyID, "legacy item")

	// show
	if out := emOK(t, bin, env, "show", id); !strings.Contains(out, "ID:        "+id+"\n") {
		t.Fatalf("show %s did not render it:\n%s", id, out)
	}

	// depends: a 5-char item waits on the 4-char one.
	child := emNew(t, bin, env, "placeholder") // widen the store before the dependent
	out := emOK(t, bin, env, "new", "--no-repo", "--depends="+id, "five-char dependent")
	dependent := strings.TrimSuffix(strings.Fields(out)[1], ":")
	if len(dependent) != len("mg-")+5 {
		t.Fatalf("dependent minted %q, want a 5-char id", dependent)
	}
	if !strings.Contains(out, id+" (pending)") {
		t.Errorf("new --depends=%s did not report the dependency as pending:\n%s", id, out)
	}

	// edit
	emOK(t, bin, env, "edit", id, "--priority=high")
	if out := emOK(t, bin, env, "show", id); !strings.Contains(out, "Priority:  high") {
		t.Errorf("edit %s did not stick:\n%s", id, out)
	}

	// claim + done; done promotes the 5-char dependent.
	emOK(t, bin, env, "claim", id)
	out = emOK(t, bin, env, "done", id)
	if !strings.Contains(out, "Promoted "+dependent) {
		t.Errorf("done %s did not promote its 5-char dependent %s:\n%s", id, dependent, out)
	}

	// successor: a 5-char design names the 4-char item as its successor, and
	// the reverse predecessor: tag lands on the 4-char item.
	design := emOK(t, bin, env, "new", "--no-repo", "--type=design", "five-char design")
	designID := strings.TrimSuffix(strings.Fields(design)[1], ":")
	emOK(t, bin, env, "claim", designID)
	emOK(t, bin, env, "done", designID, "--successor="+id)
	if out := emOK(t, bin, env, "show", designID); !strings.Contains(out, "Successor:   "+id+" (done)") {
		t.Errorf("show %s does not resolve its 4-char successor %s:\n%s", designID, id, out)
	}
	if out := emOK(t, bin, env, "show", id); !strings.Contains(out, "Predecessor: "+designID+" (done)") {
		t.Errorf("show %s does not resolve its 5-char predecessor %s:\n%s", id, designID, out)
	}

	// The other direction: a 4-char design names a 5-char successor.
	legacyDesign := plantLegacy(t, bin, env, home, "mg-0a1c", "legacy design", "--type=design")
	emOK(t, bin, env, "claim", legacyDesign)
	emOK(t, bin, env, "done", legacyDesign, "--successor="+child)
	if out := emOK(t, bin, env, "show", legacyDesign); !strings.Contains(out, "Successor:   "+child) {
		t.Errorf("show %s does not resolve its 5-char successor %s:\n%s", legacyDesign, child, out)
	}
	if out := emOK(t, bin, env, "show", child); !strings.Contains(out, "Predecessor: "+legacyDesign) {
		t.Errorf("show %s does not resolve its 4-char predecessor %s:\n%s", child, legacyDesign, out)
	}

	// archive, then the @YYYY-MM qualifier. An archived twin of the 4-char id
	// is planted in a second partition so the bare id is ambiguous and only
	// the qualifier can pick one — the qualifier is exercised, not bypassed.
	emOK(t, bin, env, "archive", id)
	archived, _ := filepath.Glob(filepath.Join(home, ".macguffin", "work", "archive", "*", id+".md"))
	if len(archived) != 1 {
		t.Fatalf("archive %s: want one partitioned record, found %v", id, archived)
	}
	partition := filepath.Base(filepath.Dir(archived[0]))
	twinDir := filepath.Join(home, ".macguffin", "work", "archive", "2001-01")
	if err := os.MkdirAll(twinDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(archived[0])
	if err != nil {
		t.Fatal(err)
	}
	twin := strings.Replace(string(body), "legacy item", "legacy twin", -1)
	if err := os.WriteFile(filepath.Join(twinDir, id+".md"), []byte(twin), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, stderr, exit := taxRun(bin, env, "show", id); exit == 0 || !strings.Contains(stderr, "ambiguous") {
		t.Errorf("show %s with archived twins in two partitions: exit=%d, want an ambiguity refusal:\n%s", id, exit, stderr)
	}
	for part, title := range map[string]string{partition: "legacy item", "2001-01": "legacy twin"} {
		out := emOK(t, bin, env, "show", id+"@"+part)
		if !strings.Contains(out, "Title:     "+title) {
			t.Errorf("show %s@%s did not select the %q record:\n%s", id, part, title, out)
		}
	}
}

// TestCLI_MixedWidthsCoexist is the store-level claim: a 4-char id and a
// 5-char id that share their first four characters are two different items,
// and every reader keeps them apart.
func TestCLI_MixedWidthsCoexist(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := buildBinary(t)
	home := t.TempDir()
	env := emEnv(home)
	emInit(t, bin, env)

	five := emNew(t, bin, env, "five-char item")
	four := plantLegacy(t, bin, env, home, five[:len(five)-1], "four-char item")
	if !strings.HasPrefix(five, four) {
		t.Fatalf("setup: %s should be a prefix of %s", four, five)
	}

	for id, title := range map[string]string{four: "four-char item", five: "five-char item"} {
		stdout, stderr, exit := taxRun(bin, env, "show", id, "--json")
		if exit != 0 {
			t.Fatalf("show %s: exit %d\n%s", id, exit, stderr)
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(stdout), &got); err != nil {
			t.Fatalf("show %s --json: %v\n%s", id, err, stdout)
		}
		if got["id"] != id || got["title"] != title {
			t.Errorf("show %s resolved to id=%v title=%v, want %s %q", id, got["id"], got["title"], id, title)
		}
		if strings.Contains(stderr, "note:") {
			t.Errorf("show %s printed a shadow note — the widths were read as one id:\n%s", id, stderr)
		}
	}

	// Claiming one width leaves the other alone.
	emOK(t, bin, env, "claim", four)
	if out := emOK(t, bin, env, "show", five); !strings.Contains(out, "Status:    available") {
		t.Errorf("claiming %s moved %s:\n%s", four, five, out)
	}

	stdout, _, exit := taxRun(bin, env, "list", "--all", "--json")
	if exit != 0 {
		t.Fatalf("list --all --json: exit %d", exit)
	}
	seen := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("non-JSON list line %q: %v", line, err)
		}
		if s, _ := rec["id"].(string); s != "" {
			seen[s]++
		}
	}
	if seen[four] != 1 || seen[five] != 1 {
		t.Errorf("list --all --json: %s×%d, %s×%d, want each exactly once", four, seen[four], five, seen[five])
	}
}
