package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// This file is the acceptance suite for mg-6bc9: a work item's result sidecar
// can be RESOLVED, so nobody has to glob for it.
//
// The bug being closed is not that a glob is untidy. It is that
// `work/*/<id>.result.json` is a ONE-LEVEL pattern while the archive is nested
// by month (work/archive/2026-08/<id>.result.json), so the glob cannot match an
// archived sidecar by construction — and the archive holds the great majority
// of them. A failing glob does not error into the caller's result; it errors
// beside it, and the caller reads the empty set as "there is no sidecar". Two
// agents published exactly that on 2026-08-13, ninety minutes apart, for items
// that both recorded verdict=pass.
//
// So TestCLI_Sidecar_ArchivedIsInvisibleToOneLevelGlob is not decoration: it
// runs the mutant — the one-level glob — against the same store and asserts it
// finds NOTHING where the resolver finds the file. A resolver test that passes
// without that control cannot tell a correct implementation from the broken one
// it replaces.

// sidecarStore seeds a store under a fresh HOME with one item per lifecycle
// directory, each carrying a result sidecar. The archived item is placed in a
// MONTH PARTITION, which is the case the glob cannot see.
//
// Items are written directly rather than driven through the CLI so that every
// lifecycle directory — including pending/ and shelved/, which need dependency
// and shelve state to reach honestly — is covered by the same short seed.
type seeded struct {
	id      string
	dir     string // store-relative directory holding the .md
	mdName  string // basename of the .md (claimed items carry a PID suffix)
	sidecar string // sidecar contents
}

func sidecarStore(t *testing.T, bin string) (env []string, home string, items []seeded) {
	t.Helper()
	home = t.TempDir()
	env = emEnv(home)
	emInit(t, bin, env)

	items = []seeded{
		{id: "mg-a001", dir: "work/available", mdName: "mg-a001.md"},
		{id: "mg-a002", dir: "work/claimed", mdName: "mg-a002.md.4242"},
		{id: "mg-a003", dir: "work/done", mdName: "mg-a003.md"},
		{id: "mg-a004", dir: "work/pending", mdName: "mg-a004.md"},
		{id: "mg-a005", dir: "work/shelved", mdName: "mg-a005.md"},
		// The case at issue: archived, and therefore nested under a month
		// partition rather than sitting one level under work/.
		{id: "mg-a006", dir: "work/archive/2026-08", mdName: "mg-a006.md"},
	}
	for i := range items {
		it := &items[i]
		it.sidecar = fmt.Sprintf("{\"verdict\":\"pass\",\"where\":%q}\n", it.dir)
		dir := filepath.Join(home, ".macguffin", it.dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf("---\nid: %s\ntype: task\ncreated: 2026-08-01T00:00:00Z\ncreator: test\ndepends: []\npriority: medium\n---\n\n# task seeded in %s\n", it.id, it.dir)
		if err := os.WriteFile(filepath.Join(dir, it.mdName), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, it.id+".result.json"), []byte(it.sidecar), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return env, home, items
}

func sidecarPath(home string, it seeded) string {
	return filepath.Join(home, ".macguffin", it.dir, it.id+".result.json")
}

// TestCLI_Sidecar_ResolvesInEveryLifecycleDir is the primary acceptance
// criterion: the resolver returns the correct path for an item in EVERY
// lifecycle directory, the month-nested archive included.
func TestCLI_Sidecar_ResolvesInEveryLifecycleDir(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := buildBinary(t)
	env, home, items := sidecarStore(t, bin)

	for _, it := range items {
		t.Run(strings.ReplaceAll(it.dir, "/", "_"), func(t *testing.T) {
			want := sidecarPath(home, it)

			stdout, stderr, exit := taxRun(bin, env, "sidecar", it.id, "--path")
			if exit != 0 {
				t.Fatalf("--path: exit %d, want 0\nstderr: %s", exit, stderr)
			}
			if got := strings.TrimSpace(stdout); got != want {
				t.Errorf("--path = %q, want %q", got, want)
			}

			stdout, stderr, exit = taxRun(bin, env, "sidecar", it.id)
			if exit != 0 {
				t.Fatalf("sidecar: exit %d, want 0\nstderr: %s", exit, stderr)
			}
			if stdout != it.sidecar {
				t.Errorf("contents = %q, want %q", stdout, it.sidecar)
			}
		})
	}
}

// TestCLI_Sidecar_ArchivedIsInvisibleToOneLevelGlob is the POSITIVE CONTROL.
//
// It asserts two things about the SAME store: the resolver finds the archived
// sidecar, and the one-level glob every reader reached for finds nothing at
// all. The second assertion is what gives the first any force — it is the exact
// mutant, run in-process, and it shows the test fails against the broken
// implementation rather than merely passing against the fixed one.
func TestCLI_Sidecar_ArchivedIsInvisibleToOneLevelGlob(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := buildBinary(t)
	env, home, items := sidecarStore(t, bin)

	archived := items[len(items)-1]
	if archived.dir != "work/archive/2026-08" {
		t.Fatalf("seed changed: expected the last item to be month-partitioned, got %q", archived.dir)
	}

	// The mutant: what every consumer wrote before this command existed.
	pattern := filepath.Join(home, ".macguffin", "work", "*", archived.id+".result.json")
	hits, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("the one-level glob %q found %v — the store no longer reproduces the bug, so this control proves nothing", pattern, hits)
	}

	// The variant that follows the documented "ask for the status first"
	// advice is broken too: the STATUS is "archived" and the DIRECTORY is
	// "archive", so work/archived/ names nothing. This is the second of the
	// two false negatives on record.
	if _, err := os.Stat(filepath.Join(home, ".macguffin", "work", "archived")); !os.IsNotExist(err) {
		t.Fatalf("work/archived/ exists; the status-directory confusion is no longer reproducible: %v", err)
	}

	// The resolver, on the same store.
	stdout, stderr, exit := taxRun(bin, env, "sidecar", archived.id, "--path")
	if exit != 0 {
		t.Fatalf("resolver failed where it must succeed: exit %d\nstderr: %s", exit, stderr)
	}
	if got, want := strings.TrimSpace(stdout), sidecarPath(home, archived); got != want {
		t.Fatalf("resolver = %q, want %q", got, want)
	}
}

// TestCLI_Sidecar_AbsentExitsNonZeroAndPrintsNothing pins the second acceptance
// criterion. An empty stdout with exit 0 is the shape that let a shell
// conditional render "no sidecar anywhere"; both halves are asserted.
func TestCLI_Sidecar_AbsentExitsNonZeroAndPrintsNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := buildBinary(t)
	env, _, _ := sidecarStore(t, bin)
	id := emNew(t, bin, env, "an item that recorded no result")

	for _, args := range [][]string{
		{"sidecar", id},
		{"sidecar", id, "--path"},
		{"sidecar", id, "--json"},
	} {
		stdout, stderr, exit := taxRun(bin, env, args...)
		if exit != 3 {
			t.Errorf("mg %v: exit %d, want 3 (not_found)\nstderr: %s", args, exit, stderr)
		}
		if stdout != "" {
			t.Errorf("mg %v wrote %q to stdout; an absent sidecar must write nothing there", args, stdout)
		}
		if !strings.Contains(stderr, "no result sidecar") {
			t.Errorf("mg %v: stderr does not say the sidecar is absent: %q", args, stderr)
		}
	}

	// An id that names nothing is a DIFFERENT answer from an item with no
	// result, and carries a different slug even though both are not_found.
	stdout, stderr, exit := taxRun(bin, env, "sidecar", "mg-zzzz", "--json")
	if exit != 3 {
		t.Errorf("unknown id: exit %d, want 3\nstderr: %s", exit, stderr)
	}
	if stdout != "" {
		t.Errorf("unknown id wrote %q to stdout", stdout)
	}
	if !strings.Contains(stderr, "no_such_item") {
		t.Errorf("unknown id should carry the no_such_item slug, got: %q", stderr)
	}
}

// TestCLI_Sidecar_UnreadableStoreIsNotAnAbsentResult is the other half of the
// same criterion, and the one a naive implementation gets wrong: a store it
// could not READ must not be reported as a store with no result. The two exit
// with different codes AND different slugs, so neither a code check nor a slug
// check can conflate them.
func TestCLI_Sidecar_UnreadableStoreIsNotAnAbsentResult(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	if os.Geteuid() == 0 {
		t.Skip("permission-based cases are meaningless as root")
	}
	bin := buildBinary(t)
	env, home, items := sidecarStore(t, bin)

	done := items[2]
	if done.dir != "work/done" {
		t.Fatalf("seed changed: expected items[2] in work/done, got %q", done.dir)
	}
	// The sidecar is THERE and cannot be read. stat(2) still succeeds without
	// read permission, so this is precisely the case an implementation that
	// only stats gets right and one that swallows the read error gets wrong.
	path := sidecarPath(home, done)
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0o644) })

	for _, args := range [][]string{
		{"sidecar", done.id},
		{"sidecar", done.id, "--json"},
		{"show", done.id, "--json"},
	} {
		stdout, stderr, exit := taxRun(bin, env, args...)
		if exit != 1 {
			t.Errorf("mg %v: exit %d, want 1 (internal) — an unreadable file is not an absent one\nstdout: %s\nstderr: %s", args, exit, stdout, stderr)
		}
		if stdout != "" {
			t.Errorf("mg %v wrote %q to stdout while unable to read the sidecar", args, stdout)
		}
		if strings.Contains(stderr, "no result sidecar") {
			t.Errorf("mg %v reported an unreadable sidecar as an absent one — the exact conflation mg-6bc9 exists to stop:\n%s", args, stderr)
		}
		if !strings.Contains(stderr, "cannot read") {
			t.Errorf("mg %v: stderr does not say the read failed: %q", args, stderr)
		}
	}

	// The distinction is legible to a machine as well as to a person: the
	// absent case is not_found/3 with slug no_sidecar (asserted above) and this
	// one is internal/1 with slug io_error. Neither code nor slug can conflate
	// them.
	_, stderr, _ := taxRun(bin, env, "sidecar", done.id, "--json")
	if !strings.Contains(stderr, `"code":"io_error"`) {
		t.Errorf("unreadable sidecar does not carry the io_error slug: %q", stderr)
	}

	// An unlistable lifecycle DIRECTORY is a separate, pre-existing story —
	// Resolve skips directories it cannot read, so the item stops resolving at
	// all. That is still not a clean "no sidecar", which is the property under
	// test here.
	dir := filepath.Join(home, ".macguffin", done.dir)
	if err := os.Chmod(dir, 0o111); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	stdout, stderr, exit := taxRun(bin, env, "sidecar", done.id)
	if exit == 0 {
		t.Fatalf("unlistable store exited 0\nstdout: %s", stdout)
	}
	if stdout != "" {
		t.Errorf("unlistable store wrote %q to stdout", stdout)
	}
	if strings.Contains(stderr, "no result sidecar") {
		t.Errorf("an unlistable store was reported as an absent result:\n%s", stderr)
	}
}

// TestCLI_Sidecar_JSONCarriesPathAndParsedResult pins the --json contract.
func TestCLI_Sidecar_JSONCarriesPathAndParsedResult(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := buildBinary(t)
	env, home, items := sidecarStore(t, bin)
	archived := items[len(items)-1]

	stdout, stderr, exit := taxRun(bin, env, "sidecar", archived.id, "--json")
	if exit != 0 {
		t.Fatalf("exit %d, want 0\nstderr: %s", exit, stderr)
	}
	var got struct {
		ID        string          `json:"id"`
		Status    string          `json:"status"`
		Partition string          `json:"partition"`
		Path      string          `json:"path"`
		Result    json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if got.ID != archived.id {
		t.Errorf("id = %q, want %q", got.ID, archived.id)
	}
	if got.Status != "archived" {
		t.Errorf("status = %q, want archived", got.Status)
	}
	if got.Partition != "2026-08" {
		t.Errorf("partition = %q, want 2026-08", got.Partition)
	}
	if got.Path != sidecarPath(home, archived) {
		t.Errorf("path = %q, want %q", got.Path, sidecarPath(home, archived))
	}
	var verdict struct {
		Verdict string `json:"verdict"`
	}
	if err := json.Unmarshal(got.Result, &verdict); err != nil {
		t.Fatalf("result is not the sidecar's JSON: %v (%s)", err, got.Result)
	}
	if verdict.Verdict != "pass" {
		t.Errorf("result.verdict = %q, want pass", verdict.Verdict)
	}
}

// TestCLI_Show_JSONCarriesResultPathAndResult covers the third form the ticket
// asked for: the verdict is reachable from the object `mg show --json` already
// returns, so a consumer never has to construct a path to answer "what did this
// item conclude".
func TestCLI_Show_JSONCarriesResultPathAndResult(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := buildBinary(t)
	env, home, items := sidecarStore(t, bin)
	archived := items[len(items)-1]

	stdout, stderr, exit := taxRun(bin, env, "show", archived.id, "--json")
	if exit != 0 {
		t.Fatalf("exit %d, want 0\nstderr: %s", exit, stderr)
	}
	var got struct {
		ResultPath *string         `json:"result_path"`
		Result     json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if got.ResultPath == nil {
		t.Fatal("result_path is null for an item that has a sidecar")
	}
	if *got.ResultPath != sidecarPath(home, archived) {
		t.Errorf("result_path = %q, want %q", *got.ResultPath, sidecarPath(home, archived))
	}
	if !strings.Contains(string(got.Result), `"verdict"`) {
		t.Errorf("result does not carry the sidecar's content: %s", got.Result)
	}

	// The human view names the path too — that is the surface a reader who is
	// about to construct one actually looks at.
	stdout, _, exit = taxRun(bin, env, "show", archived.id)
	if exit != 0 {
		t.Fatalf("mg show: exit %d", exit)
	}
	if !strings.Contains(stdout, sidecarPath(home, archived)) {
		t.Errorf("mg show does not print the resolved sidecar path:\n%s", stdout)
	}

	// An item with no result must say so by OMISSION, never by an empty path.
	id := emNew(t, bin, env, "no result recorded")
	stdout, _, exit = taxRun(bin, env, "show", id, "--json")
	if exit != 0 {
		t.Fatalf("mg show --json: exit %d", exit)
	}
	got.ResultPath, got.Result = nil, nil
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatal(err)
	}
	if got.ResultPath != nil {
		t.Errorf("result_path = %q for an item with no sidecar; want null", *got.ResultPath)
	}
	if s := string(got.Result); s != "" && s != "null" {
		t.Errorf("result = %s for an item with no sidecar; want null", s)
	}
}

// TestCLI_Sidecar_AmbiguousIdIsRefusedNotGuessed pins "resolve, do not glob"
// at its sharpest point. Two archived twins in different month partitions are
// the one case where the store genuinely holds two candidate sidecars — and the
// answer is a refusal plus the @partition escape hatch, never a list for the
// caller to pick from. Picking from a list is the failure this whole command
// replaces, so producing one here would reintroduce it under a better name.
func TestCLI_Sidecar_AmbiguousIdIsRefusedNotGuessed(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := buildBinary(t)
	home := t.TempDir()
	env := emEnv(home)
	emInit(t, bin, env)

	const id = "mg-tw01"
	for _, part := range []string{"2026-04", "2026-05"} {
		dir := filepath.Join(home, ".macguffin", "work", "archive", part)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf("---\nid: %s\ntype: task\ncreated: 2026-04-01T00:00:00Z\ncreator: test\ndepends: []\npriority: medium\n---\n\n# twin in %s\n", id, part)
		if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, id+".result.json"), []byte(fmt.Sprintf("{%q:%q}\n", "partition", part)), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	stdout, stderr, exit := taxRun(bin, env, "sidecar", id, "--path")
	if exit == 0 {
		t.Fatalf("an ambiguous id resolved to something: %q", stdout)
	}
	if stdout != "" {
		t.Errorf("an ambiguous id wrote %q to stdout; there must be no candidate list to pick from", stdout)
	}
	if !strings.Contains(stderr, "ambiguous") {
		t.Errorf("stderr does not name the ambiguity: %q", stderr)
	}

	// The qualifier picks the twin, and picks the RIGHT one — the sidecar comes
	// from the partition named, not from whichever was scanned first.
	stdout, stderr, exit = taxRun(bin, env, "sidecar", id+"@2026-05")
	if exit != 0 {
		t.Fatalf("qualified lookup: exit %d\nstderr: %s", exit, stderr)
	}
	if !strings.Contains(stdout, "2026-05") || strings.Contains(stdout, "2026-04") {
		t.Errorf("qualified lookup returned the wrong twin's result: %q", stdout)
	}
}

// TestHelp_NoCommandRecommendsAGlob is the ticket's last acceptance line, and
// it is a guard rather than a one-off check: any help text that shows the
// one-level pattern must also say, in that same text, not to use it. A future
// edit that reintroduces the glob as advice fails here.
//
// It walks the live command tree in-process, so a new command is covered the
// day it is added rather than the day someone remembers to list it.
func TestHelp_NoCommandRecommendsAGlob(t *testing.T) {
	texts := helpTexts()
	if len(texts) == 0 {
		t.Fatal("walked no commands; the traversal is broken and this test asserts nothing")
	}

	var offenders []string
	for name, long := range texts {
		if !strings.Contains(long, "work/*/") {
			continue
		}
		if !strings.Contains(long, "DO NOT GLOB") && !strings.Contains(long, "WRONG") {
			offenders = append(offenders, name)
		}
	}
	if len(offenders) > 0 {
		t.Errorf("help text shows work/*/ without forbidding it: %v", offenders)
	}

	// And the one text that warned about the WRONG hazard must now name the
	// real one. The ordering caution is true and has never bitten anyone here;
	// the nesting hazard bit two agents in seventy-five minutes, and a warning
	// that names only the lesser one occupies the slot the real caution needs.
	long, ok := texts["sidecars"]
	if !ok {
		t.Fatal("no help text for `mg sidecars`")
	}
	for _, want := range []string{"archive/2026-08", "ONE level", "mg sidecar <id>"} {
		if !strings.Contains(long, want) {
			t.Errorf("mg sidecars --help does not mention %q:\n%s", want, long)
		}
	}
}

// helpTexts collects every command's Long help, keyed by command name, by
// walking the live command tree in-process. A command added tomorrow is covered
// tomorrow, without anyone maintaining a list.
func helpTexts() map[string]string {
	out := map[string]string{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		out[c.Name()] = c.Long
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(rootCmd)
	return out
}
