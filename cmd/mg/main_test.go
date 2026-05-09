package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCLI_Version(t *testing.T) {
	bin := buildBinary(t)
	out, err := exec.Command(bin, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("mg version failed: %v\n%s", err, out)
	}
	if want := "mg " + version + "\n"; string(out) != want {
		t.Errorf("version output = %q, want %q", out, want)
	}
}

func TestCLI_Help(t *testing.T) {
	bin := buildBinary(t)
	out, err := exec.Command(bin, "help").CombinedOutput()
	if err != nil {
		t.Fatalf("mg help failed: %v\n%s", err, out)
	}
	if len(out) == 0 {
		t.Error("help output should not be empty")
	}
}

func TestCLI_UnknownCommand(t *testing.T) {
	bin := buildBinary(t)
	cmd := exec.Command(bin, "bogus")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Error("expected non-zero exit for unknown command")
	}
	if !strings.Contains(string(out), "Error:") {
		t.Errorf("expected error message on stderr, got %q", out)
	}
}

func TestCLI_NoArgs(t *testing.T) {
	bin := buildBinary(t)
	cmd := exec.Command(bin)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Error("expected non-zero exit for no args")
	}
	if !strings.Contains(string(out), "Error:") {
		t.Errorf("expected error message on stderr, got %q", out)
	}
}

func TestCLI_ErrorOnStderr(t *testing.T) {
	bin := buildBinary(t)
	cmd := exec.Command(bin, "bogus")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	cmd.Stdout = nil
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected non-zero exit")
	}
	if !strings.Contains(stderr.String(), "Error:") {
		t.Errorf("expected error on stderr, got %q", stderr.String())
	}
}

func TestCLI_New(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)

	// Init first
	cmd := exec.Command(bin, "init")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	// Create a work item
	cmd = exec.Command(bin, "new", "--type=bug", "Auth tokens not refreshing")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg new failed: %v\n%s", err, out)
	}

	output := string(out)
	if !strings.Contains(output, "Created mg-") {
		t.Errorf("expected 'Created mg-...' output, got %q", output)
	}

	// Verify exactly one .md file in available/
	avail := filepath.Join(tmpHome, ".macguffin", "work", "available")
	entries, err := os.ReadDir(avail)
	if err != nil {
		t.Fatalf("reading available/: %v", err)
	}
	mdCount := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			mdCount++
		}
	}
	if mdCount != 1 {
		t.Errorf("expected 1 .md file in available/, got %d", mdCount)
	}

	// Verify frontmatter has required fields
	var mdFile string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			mdFile = filepath.Join(avail, e.Name())
		}
	}
	data, err := os.ReadFile(mdFile)
	if err != nil {
		t.Fatalf("reading work item file: %v", err)
	}
	content := string(data)
	for _, field := range []string{"id:", "type:", "created:", "creator:"} {
		if !strings.Contains(content, field) {
			t.Errorf("frontmatter missing %q in:\n%s", field, content)
		}
	}
}

func TestCLI_Show(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)

	// Init + create
	cmd := exec.Command(bin, "init")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	cmd = exec.Command(bin, "new", "--type=bug", "Test show item")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg new failed: %v\n%s", err, out)
	}

	// Extract ID from "Created gt-XXXX: ..."
	output := string(out)
	id := strings.TrimPrefix(strings.Split(output, ":")[0], "Created ")

	// Show it
	cmd = exec.Command(bin, "show", id)
	cmd.Env = append(os.Environ(), "HOME="+tmpHome)
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg show failed: %v\n%s", err, out)
	}

	showOutput := string(out)
	if !strings.Contains(showOutput, id) {
		t.Errorf("show output should contain ID %q, got:\n%s", id, showOutput)
	}
	if !strings.Contains(showOutput, "bug") {
		t.Errorf("show output should contain type 'bug', got:\n%s", showOutput)
	}
	if !strings.Contains(showOutput, "Test show item") {
		t.Errorf("show output should contain title, got:\n%s", showOutput)
	}
}

func TestCLI_ShowNotFound(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)

	cmd := exec.Command(bin, "init")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	cmd = exec.Command(bin, "show", "gt-000")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome)
	err := cmd.Run()
	if err == nil {
		t.Error("expected non-zero exit for nonexistent ID")
	}
}

func TestCLI_List(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)

	cmd := exec.Command(bin, "init")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	// Empty list
	cmd = exec.Command(bin, "list")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg list failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "No work items") {
		t.Errorf("expected 'No work items' for empty list, got %q", out)
	}

	// Create two items
	cmd = exec.Command(bin, "new", "--type=bug", "First bug")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg new failed: %v\n%s", err, out)
	}

	cmd = exec.Command(bin, "new", "--type=task", "Second task")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg new failed: %v\n%s", err, out)
	}

	// List should show both
	cmd = exec.Command(bin, "list")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome)
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg list failed: %v\n%s", err, out)
	}
	listOutput := string(out)
	if !strings.Contains(listOutput, "First bug") {
		t.Errorf("list output should contain 'First bug', got:\n%s", listOutput)
	}
	if !strings.Contains(listOutput, "Second task") {
		t.Errorf("list output should contain 'Second task', got:\n%s", listOutput)
	}
}

func TestCLI_Claim(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := append(os.Environ(), "HOME="+tmpHome)

	// Init
	cmd := exec.Command(bin, "init")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	// Create a work item
	cmd = exec.Command(bin, "new", "--type=bug", "Claimable item")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg new failed: %v\n%s", err, out)
	}
	id := strings.TrimPrefix(strings.Split(string(out), ":")[0], "Created ")

	// Claim it
	cmd = exec.Command(bin, "claim", id)
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg claim failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Claimed "+id) {
		t.Errorf("expected 'Claimed %s' output, got %q", id, out)
	}

	// available/ should be empty
	avail := filepath.Join(tmpHome, ".macguffin", "work", "available")
	entries, _ := os.ReadDir(avail)
	mdCount := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			mdCount++
		}
	}
	if mdCount != 0 {
		t.Errorf("expected 0 .md files in available/ after claim, got %d", mdCount)
	}

	// claimed/ should have 1 file with PID suffix
	claimed := filepath.Join(tmpHome, ".macguffin", "work", "claimed")
	entries, _ = os.ReadDir(claimed)
	found := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), id+".md.") {
			found = true
		}
	}
	if !found {
		t.Error("expected claimed file with PID suffix in claimed/")
	}

	// Second claim should fail
	cmd = exec.Command(bin, "claim", id)
	cmd.Env = env
	err = cmd.Run()
	if err == nil {
		t.Error("expected non-zero exit for already-claimed item")
	}
}

func TestCLI_Done(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := append(os.Environ(), "HOME="+tmpHome)

	// Init
	cmd := exec.Command(bin, "init")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	// Create
	cmd = exec.Command(bin, "new", "--type=bug", "Done test item")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg new failed: %v\n%s", err, out)
	}
	id := strings.TrimPrefix(strings.Split(string(out), ":")[0], "Created ")

	// Claim
	cmd = exec.Command(bin, "claim", id)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg claim failed: %v\n%s", err, out)
	}

	// Done with result
	cmd = exec.Command(bin, "done", id, "--result", `{"status":"fixed","commit":"abc123"}`)
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg done failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Done "+id) {
		t.Errorf("expected 'Done %s' output, got %q", id, out)
	}

	// Verify: item in done/
	donePath := filepath.Join(tmpHome, ".macguffin", "work", "done", id+".md")
	if _, err := os.Stat(donePath); err != nil {
		t.Errorf("expected done file at %s: %v", donePath, err)
	}

	// Verify: result sidecar exists
	sidecarPath := filepath.Join(tmpHome, ".macguffin", "work", "done", id+".result.json")
	if _, err := os.Stat(sidecarPath); err != nil {
		t.Errorf("expected sidecar at %s: %v", sidecarPath, err)
	}

	// Verify: not in available/ or claimed/
	availDir := filepath.Join(tmpHome, ".macguffin", "work", "available")
	entries, _ := os.ReadDir(availDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), id) {
			t.Errorf("item still in available/: %s", e.Name())
		}
	}
	claimedDir := filepath.Join(tmpHome, ".macguffin", "work", "claimed")
	entries, _ = os.ReadDir(claimedDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), id) {
			t.Errorf("item still in claimed/: %s", e.Name())
		}
	}
}

func TestCLI_DoneNoID(t *testing.T) {
	bin := buildBinary(t)
	err := exec.Command(bin, "done").Run()
	if err == nil {
		t.Error("expected non-zero exit for done without ID")
	}
}

func TestCLI_FullLifecycle(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := append(os.Environ(), "HOME="+tmpHome)

	// Init
	cmd := exec.Command(bin, "init")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	// Create
	cmd = exec.Command(bin, "new", "--type=task", "Full lifecycle")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg new failed: %v\n%s", err, out)
	}
	id := strings.TrimPrefix(strings.Split(string(out), ":")[0], "Created ")

	// List --status=available should show it
	cmd = exec.Command(bin, "list", "--status=available")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg list --status=available failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), id) {
		t.Errorf("list --status=available should contain %s, got %q", id, out)
	}

	// Claim
	cmd = exec.Command(bin, "claim", id)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg claim failed: %v\n%s", err, out)
	}

	// List --status=claimed should show it
	cmd = exec.Command(bin, "list", "--status=claimed")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg list --status=claimed failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), id) {
		t.Errorf("list --status=claimed should contain %s, got %q", id, out)
	}

	// Done
	cmd = exec.Command(bin, "done", id, "--result", `{"status":"fixed","commit":"abc123"}`)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg done failed: %v\n%s", err, out)
	}

	// List --status=done should show it
	cmd = exec.Command(bin, "list", "--status=done")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg list --status=done failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), id) {
		t.Errorf("list --status=done should contain %s, got %q", id, out)
	}

	// List --status=available should be empty
	cmd = exec.Command(bin, "list", "--status=available")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg list --status=available failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "No available work items") {
		t.Errorf("expected 'No available work items', got %q", out)
	}
}

func TestCLI_ListGrouped(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := append(os.Environ(), "HOME="+tmpHome)

	// Init
	cmd := exec.Command(bin, "init")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	// Create two items
	cmd = exec.Command(bin, "new", "--type=bug", "Available item")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg new failed: %v\n%s", err, out)
	}

	cmd = exec.Command(bin, "new", "--type=task", "To be claimed")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg new failed: %v\n%s", err, out)
	}
	id2 := strings.TrimPrefix(strings.Split(string(out), ":")[0], "Created ")

	// Claim one
	cmd = exec.Command(bin, "claim", id2)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg claim failed: %v\n%s", err, out)
	}

	// List without --status shows grouped
	cmd = exec.Command(bin, "list")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg list failed: %v\n%s", err, out)
	}
	listOutput := string(out)
	if !strings.Contains(listOutput, "available:") {
		t.Errorf("grouped list should contain 'available:', got:\n%s", listOutput)
	}
	if !strings.Contains(listOutput, "claimed:") {
		t.Errorf("grouped list should contain 'claimed:', got:\n%s", listOutput)
	}
}

func TestCLI_ListArchived(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := append(os.Environ(), "HOME="+tmpHome)

	// Init
	cmd := exec.Command(bin, "init")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	// Create an item, claim it, mark done
	cmd = exec.Command(bin, "new", "--type=task", "Archived item")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg new failed: %v\n%s", err, out)
	}
	id := strings.TrimPrefix(strings.Split(string(out), ":")[0], "Created ")

	cmd = exec.Command(bin, "claim", id)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg claim failed: %v\n%s", err, out)
	}

	cmd = exec.Command(bin, "done", id)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg done failed: %v\n%s", err, out)
	}

	// Create a second item that stays available
	cmd = exec.Command(bin, "new", "--type=bug", "Active item")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg new failed: %v\n%s", err, out)
	}

	// Default list should show done items (but not archived)
	cmd = exec.Command(bin, "list")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg list failed: %v\n%s", err, out)
	}
	listOutput := string(out)
	if !strings.Contains(listOutput, "Archived item") {
		t.Errorf("list should show done items by default, got:\n%s", listOutput)
	}
	if !strings.Contains(listOutput, "done:") {
		t.Errorf("list should contain 'done:' group by default, got:\n%s", listOutput)
	}
	if !strings.Contains(listOutput, "Active item") {
		t.Errorf("list should show active items, got:\n%s", listOutput)
	}

	// Archive the done item (--days=0 archives even freshly done items)
	cmd = exec.Command(bin, "archive", "--days=0")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg archive failed: %v\n%s", err, out)
	}

	// Default list should NOT show archived items
	cmd = exec.Command(bin, "list")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg list failed: %v\n%s", err, out)
	}
	listOutput = string(out)
	if strings.Contains(listOutput, "Archived item") {
		t.Errorf("list should not show archived items by default, got:\n%s", listOutput)
	}

	// With --archived, archived item SHOULD appear
	cmd = exec.Command(bin, "list", "--archived")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg list --archived failed: %v\n%s", err, out)
	}
	listOutput = string(out)
	if !strings.Contains(listOutput, "Archived item") {
		t.Errorf("list --archived should show archived items, got:\n%s", listOutput)
	}
	if !strings.Contains(listOutput, "archived:") {
		t.Errorf("list --archived should contain 'archived:' group, got:\n%s", listOutput)
	}

	// With -a (short form), archived item SHOULD also appear
	cmd = exec.Command(bin, "list", "-a")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg list -a failed: %v\n%s", err, out)
	}
	listOutput = string(out)
	if !strings.Contains(listOutput, "Archived item") {
		t.Errorf("list -a should show archived items, got:\n%s", listOutput)
	}
}

func TestCLI_ListShelved(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := append(os.Environ(), "HOME="+tmpHome)

	// Init
	cmd := exec.Command(bin, "init")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	// Create an item that we'll shelve
	cmd = exec.Command(bin, "new", "--type=task", "Shelved item")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg new failed: %v\n%s", err, out)
	}
	id := strings.TrimPrefix(strings.Split(string(out), ":")[0], "Created ")

	// Create a second item that stays available
	cmd = exec.Command(bin, "new", "--type=bug", "Active item")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg new failed: %v\n%s", err, out)
	}

	// Shelve the first item
	cmd = exec.Command(bin, "shelve", id)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg shelve failed: %v\n%s", err, out)
	}

	// Default list should NOT show shelved items
	cmd = exec.Command(bin, "list")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg list failed: %v\n%s", err, out)
	}
	listOutput := string(out)
	if strings.Contains(listOutput, "Shelved item") {
		t.Errorf("list should not show shelved items by default, got:\n%s", listOutput)
	}
	if !strings.Contains(listOutput, "Active item") {
		t.Errorf("list should show active items, got:\n%s", listOutput)
	}

	// With --shelved, shelved item SHOULD appear
	cmd = exec.Command(bin, "list", "--shelved")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg list --shelved failed: %v\n%s", err, out)
	}
	listOutput = string(out)
	if !strings.Contains(listOutput, "Shelved item") {
		t.Errorf("list --shelved should show shelved items, got:\n%s", listOutput)
	}
	if !strings.Contains(listOutput, "shelved:") {
		t.Errorf("list --shelved should contain 'shelved:' group, got:\n%s", listOutput)
	}

	// With --all, shelved item SHOULD also appear
	cmd = exec.Command(bin, "list", "--all")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg list --all failed: %v\n%s", err, out)
	}
	listOutput = string(out)
	if !strings.Contains(listOutput, "Shelved item") {
		t.Errorf("list --all should show shelved items, got:\n%s", listOutput)
	}
}

func TestCLI_ListTag(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := append(os.Environ(), "HOME="+tmpHome)

	// Init
	cmd := exec.Command(bin, "init")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	// Create two items
	cmd = exec.Command(bin, "new", "--type=bug", "Tagged bug")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg new failed: %v\n%s", err, out)
	}
	id1 := strings.TrimPrefix(strings.Split(string(out), ":")[0], "Created ")

	cmd = exec.Command(bin, "new", "--type=task", "Untagged task")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg new failed: %v\n%s", err, out)
	}

	// Tag the first item
	cmd = exec.Command(bin, "edit", id1, "--tags=urgent")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg edit failed: %v\n%s", err, out)
	}

	// List with --tag=urgent should show only the tagged item
	cmd = exec.Command(bin, "list", "--tag=urgent")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg list --tag failed: %v\n%s", err, out)
	}
	listOutput := string(out)
	if !strings.Contains(listOutput, "Tagged bug") {
		t.Errorf("list --tag=urgent should show 'Tagged bug', got:\n%s", listOutput)
	}
	if strings.Contains(listOutput, "Untagged task") {
		t.Errorf("list --tag=urgent should NOT show 'Untagged task', got:\n%s", listOutput)
	}

	// List with --tag=nonexistent should show no items
	cmd = exec.Command(bin, "list", "--tag=nonexistent")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg list --tag failed: %v\n%s", err, out)
	}
	listOutput = string(out)
	if !strings.Contains(listOutput, "No work items") {
		t.Errorf("list --tag=nonexistent should show 'No work items', got:\n%s", listOutput)
	}

	// List with --status and --tag combined
	cmd = exec.Command(bin, "list", "--status=available", "--tag=urgent")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg list --status --tag failed: %v\n%s", err, out)
	}
	listOutput = string(out)
	if !strings.Contains(listOutput, "Tagged bug") {
		t.Errorf("list --status=available --tag=urgent should show 'Tagged bug', got:\n%s", listOutput)
	}
	if strings.Contains(listOutput, "Untagged task") {
		t.Errorf("list --status=available --tag=urgent should NOT show 'Untagged task', got:\n%s", listOutput)
	}
}

func TestCLI_ListAssignee(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := append(os.Environ(), "HOME="+tmpHome)

	// Init
	cmd := exec.Command(bin, "init")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	// Create two items
	cmd = exec.Command(bin, "new", "--type=bug", "Assigned bug")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg new failed: %v\n%s", err, out)
	}
	id1 := strings.TrimPrefix(strings.Split(string(out), ":")[0], "Created ")

	cmd = exec.Command(bin, "new", "--type=task", "Unassigned task")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg new failed: %v\n%s", err, out)
	}

	// Assign the first item to "alice"
	cmd = exec.Command(bin, "edit", id1, "--assignee=alice")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg edit failed: %v\n%s", err, out)
	}

	// List with --assignee=alice should show only the assigned item
	cmd = exec.Command(bin, "list", "--assignee=alice")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg list --assignee failed: %v\n%s", err, out)
	}
	listOutput := string(out)
	if !strings.Contains(listOutput, "Assigned bug") {
		t.Errorf("list --assignee=alice should show 'Assigned bug', got:\n%s", listOutput)
	}
	if strings.Contains(listOutput, "Unassigned task") {
		t.Errorf("list --assignee=alice should NOT show 'Unassigned task', got:\n%s", listOutput)
	}

	// List with --assignee=nobody should show no items
	cmd = exec.Command(bin, "list", "--assignee=nobody")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg list --assignee failed: %v\n%s", err, out)
	}
	listOutput = string(out)
	if !strings.Contains(listOutput, "No work items") {
		t.Errorf("list --assignee=nobody should show 'No work items', got:\n%s", listOutput)
	}

	// List with --status and --assignee combined
	cmd = exec.Command(bin, "list", "--status=available", "--assignee=alice")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg list --status --assignee failed: %v\n%s", err, out)
	}
	listOutput = string(out)
	if !strings.Contains(listOutput, "Assigned bug") {
		t.Errorf("list --status=available --assignee=alice should show 'Assigned bug', got:\n%s", listOutput)
	}
	if strings.Contains(listOutput, "Unassigned task") {
		t.Errorf("list --status=available --assignee=alice should NOT show 'Unassigned task', got:\n%s", listOutput)
	}

	// List with --assignee=human should resolve to current user
	cmd = exec.Command(bin, "list", "--assignee=human")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg list --assignee=human failed: %v\n%s", err, out)
	}
	// "human" resolves to the current OS user, not "alice", so should not show the item
	listOutput = string(out)
	if strings.Contains(listOutput, "Assigned bug") {
		// Unless current user happens to be "alice", this should not appear
		u, _ := user.Current()
		if u == nil || u.Username != "alice" {
			t.Errorf("list --assignee=human should NOT show 'Assigned bug' (assigned to alice), got:\n%s", listOutput)
		}
	}
}

func TestCLI_UpdateAlias(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := append(os.Environ(), "HOME="+tmpHome)

	// Init
	cmd := exec.Command(bin, "init")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	// Create a work item
	cmd = exec.Command(bin, "new", "--type=bug", "Alias test item")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg new failed: %v\n%s", err, out)
	}
	id := strings.TrimPrefix(strings.Split(string(out), ":")[0], "Created ")

	// Use 'update' alias to change the title
	cmd = exec.Command(bin, "update", id, "--title=Updated title")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg update (alias for edit) failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Updated "+id) {
		t.Errorf("expected 'Updated %s' in output, got: %s", id, out)
	}

	// Verify the title was changed via show
	cmd = exec.Command(bin, "show", id)
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg show failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Updated title") {
		t.Errorf("expected 'Updated title' in show output, got: %s", out)
	}
}

func TestCLI_TagDependsFlagAliases(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := append(os.Environ(), "HOME="+tmpHome)

	cmd := exec.Command(bin, "init")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	// mg new --tags (alias for --tag)
	cmd = exec.Command(bin, "new", "--tags=alpha,beta", "Tagged via --tags")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg new --tags failed: %v\n%s", err, out)
	}
	id1 := strings.TrimPrefix(strings.Split(string(out), ":")[0], "Created ")

	cmd = exec.Command(bin, "show", id1)
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg show failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "alpha") || !strings.Contains(string(out), "beta") {
		t.Errorf("expected tags alpha,beta in show output, got:\n%s", out)
	}

	// mg new --tag (canonical) still works
	cmd = exec.Command(bin, "new", "--tag=gamma", "Tagged via --tag")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg new --tag failed: %v\n%s", err, out)
	}
	id2 := strings.TrimPrefix(strings.Split(string(out), ":")[0], "Created ")

	// mg new --depend (alias for --depends)
	cmd = exec.Command(bin, "new", "--depend="+id1, "Depends via --depend")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg new --depend failed: %v\n%s", err, out)
	}
	id3 := strings.TrimPrefix(strings.Split(string(out), ":")[0], "Created ")
	// Should be in pending/ since dep is unmet
	pendPath := filepath.Join(tmpHome, ".macguffin", "work", "pending", id3+".md")
	if _, err := os.Stat(pendPath); err != nil {
		t.Errorf("expected %s in pending/ via --depend alias: %v", id3, err)
	}

	// mg edit --tag (alias for --tags)
	cmd = exec.Command(bin, "edit", id2, "--tag=delta")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg edit --tag failed: %v\n%s", err, out)
	}
	cmd = exec.Command(bin, "show", id2)
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg show failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "delta") {
		t.Errorf("expected 'delta' tag after edit --tag, got:\n%s", out)
	}
	// --tag should have replaced gamma (acts identically to --tags)
	if strings.Contains(string(out), "gamma") {
		t.Errorf("expected --tag to replace tags (gamma should be gone), got:\n%s", out)
	}

	// mg edit --depend (alias for --depends)
	cmd = exec.Command(bin, "edit", id2, "--depend="+id1)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg edit --depend failed: %v\n%s", err, out)
	}
	cmd = exec.Command(bin, "show", id2)
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg show failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), id1) {
		t.Errorf("expected dep %s after edit --depend, got:\n%s", id1, out)
	}
}

func TestCLI_ClaimWithPIDFlag(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := append(os.Environ(), "HOME="+tmpHome)

	// Init
	cmd := exec.Command(bin, "init")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	// Create
	cmd = exec.Command(bin, "new", "--type=bug", "PID flag test")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg new failed: %v\n%s", err, out)
	}
	id := strings.TrimPrefix(strings.Split(string(out), ":")[0], "Created ")

	// Claim with --pid
	cmd = exec.Command(bin, "claim", "--pid=12345", id)
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg claim --pid failed: %v\n%s", err, out)
	}

	// Verify file has PID 12345 suffix
	claimedDir := filepath.Join(tmpHome, ".macguffin", "work", "claimed")
	entries, _ := os.ReadDir(claimedDir)
	found := false
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".12345") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected claimed file with .12345 suffix, got %v", entries)
	}
}

func TestCLI_ClaimWithPOGOPID(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := append(os.Environ(), "HOME="+tmpHome, "POGO_PID=67890")

	// Init
	cmd := exec.Command(bin, "init")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	// Create
	cmd = exec.Command(bin, "new", "--type=bug", "POGO_PID test")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg new failed: %v\n%s", err, out)
	}
	id := strings.TrimPrefix(strings.Split(string(out), ":")[0], "Created ")

	// Claim (should pick up POGO_PID)
	cmd = exec.Command(bin, "claim", id)
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg claim with POGO_PID failed: %v\n%s", err, out)
	}

	// Verify file has PID 67890 suffix
	claimedDir := filepath.Join(tmpHome, ".macguffin", "work", "claimed")
	entries, _ := os.ReadDir(claimedDir)
	found := false
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".67890") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected claimed file with .67890 suffix, got %v", entries)
	}
}

func TestCLI_ClaimPIDFlagOverridesEnv(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := append(os.Environ(), "HOME="+tmpHome, "POGO_PID=67890")

	// Init
	cmd := exec.Command(bin, "init")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	// Create
	cmd = exec.Command(bin, "new", "--type=bug", "PID override test")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg new failed: %v\n%s", err, out)
	}
	id := strings.TrimPrefix(strings.Split(string(out), ":")[0], "Created ")

	// Claim with --pid (should override POGO_PID)
	cmd = exec.Command(bin, "claim", "--pid=11111", id)
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg claim --pid override failed: %v\n%s", err, out)
	}

	// Verify file has PID 11111 suffix (flag wins over env)
	claimedDir := filepath.Join(tmpHome, ".macguffin", "work", "claimed")
	entries, _ := os.ReadDir(claimedDir)
	found := false
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".11111") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected claimed file with .11111 suffix (flag overrides env), got %v", entries)
	}
}

func TestCLI_ClaimNoID(t *testing.T) {
	bin := buildBinary(t)
	err := exec.Command(bin, "claim").Run()
	if err == nil {
		t.Error("expected non-zero exit for claim without ID")
	}
}

func TestCLI_NewWithRepo(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := append(os.Environ(), "HOME="+tmpHome)

	// Init
	cmd := exec.Command(bin, "init")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	// Create with explicit --repo flag
	customRepo := "/custom/repo/path"
	cmd = exec.Command(bin, "new", "--type=task", "--repo="+customRepo, "Repo flag test")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg new --repo failed: %v\n%s", err, out)
	}
	id := strings.TrimPrefix(strings.Split(string(out), ":")[0], "Created ")

	// Verify the repo field is set in the work item
	cmd = exec.Command(bin, "show", id)
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg show failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), customRepo) {
		t.Errorf("show output should contain repo %q, got:\n%s", customRepo, out)
	}
}

func TestCLI_NewNoRepo(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := append(os.Environ(), "HOME="+tmpHome)

	// Init a git repo as the run-from directory so auto-detection would
	// otherwise pick up a non-empty toplevel.
	repoDir := t.TempDir()
	if out, err := exec.Command("git", "-C", repoDir, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v\n%s", err, out)
	}

	cmd := exec.Command(bin, "init")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	// 1. --no-repo opts out of auto-detection.
	cmd = exec.Command(bin, "new", "--type=task", "--no-repo", "no repo flag test")
	cmd.Env = env
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg new --no-repo failed: %v\n%s", err, out)
	}
	id := strings.TrimPrefix(strings.Split(string(out), ":")[0], "Created ")

	cmd = exec.Command(bin, "show", id)
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg show failed: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "Repo:") {
		t.Errorf("show output should not contain Repo: line for --no-repo item, got:\n%s", out)
	}
	if strings.Contains(string(out), repoDir) {
		t.Errorf("show output should not contain auto-detected repo %q, got:\n%s", repoDir, out)
	}

	// 2. --repo="" also opts out (explicit empty string).
	cmd = exec.Command(bin, "new", "--type=task", "--repo=", "empty repo flag test")
	cmd.Env = env
	cmd.Dir = repoDir
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg new --repo='' failed: %v\n%s", err, out)
	}
	id = strings.TrimPrefix(strings.Split(string(out), ":")[0], "Created ")

	cmd = exec.Command(bin, "show", id)
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg show failed: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "Repo:") {
		t.Errorf("show output should not contain Repo: line for --repo='' item, got:\n%s", out)
	}

	// 3. --no-repo combined with a non-empty --repo is an error.
	cmd = exec.Command(bin, "new", "--type=task", "--no-repo", "--repo=/some/path", "conflict test")
	cmd.Env = env
	cmd.Dir = repoDir
	out, err = cmd.CombinedOutput()
	if err == nil {
		t.Errorf("expected error for --no-repo combined with --repo, got success:\n%s", out)
	}
}

func TestCLI_NewNoTitle(t *testing.T) {
	bin := buildBinary(t)
	err := exec.Command(bin, "new").Run()
	if err == nil {
		t.Error("expected non-zero exit for new without title")
	}
}

func TestCLI_Init(t *testing.T) {
	// Init uses $HOME, so override it with a temp dir
	tmpHome := t.TempDir()
	bin := buildBinary(t)

	cmd := exec.Command(bin, "init")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	expected := []string{
		".macguffin/work/available",
		".macguffin/work/claimed",
		".macguffin/work/done",
		".macguffin/work/pending",
		".macguffin/agents",
		".macguffin/mail",
		".macguffin/log",
	}
	for _, rel := range expected {
		path := filepath.Join(tmpHome, rel)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("expected %s to exist after init: %v", rel, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("expected %s to be a directory", rel)
		}
	}
}

func TestCLI_MailE2E(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := append(os.Environ(), "HOME="+tmpHome)

	// Init first (creates mail/ dir)
	cmd := exec.Command(bin, "init")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	// Send a message
	cmd = exec.Command(bin, "mail", "send", "arch",
		"--from=mayor", "--subject=Review needed", "--body=Please review the auth refactor.")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg mail send failed: %v\n%s", err, out)
	}

	// Verify file in new/
	newDir := filepath.Join(tmpHome, ".macguffin", "mail", "arch", "new")
	entries, err := os.ReadDir(newDir)
	if err != nil {
		t.Fatalf("reading new/: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 file in new/, got %d", len(entries))
	}
	msgID := entries[0].Name()

	// List shows the message
	cmd = exec.Command(bin, "mail", "list", "arch")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg mail list failed: %v\n%s", err, out)
	}
	if got := string(out); !contains(got, "Review needed") {
		t.Errorf("list output should contain subject, got: %s", got)
	}

	// Read the message
	cmd = exec.Command(bin, "mail", "read", "arch", msgID)
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg mail read failed: %v\n%s", err, out)
	}
	if got := string(out); !contains(got, "Please review the auth refactor.") {
		t.Errorf("read output should contain body, got: %s", got)
	}

	// Verify: moved to cur/
	entries, _ = os.ReadDir(newDir)
	if len(entries) != 0 {
		t.Errorf("expected 0 files in new/ after read, got %d", len(entries))
	}
	curDir := filepath.Join(tmpHome, ".macguffin", "mail", "arch", "cur")
	entries, _ = os.ReadDir(curDir)
	if len(entries) != 1 {
		t.Errorf("expected 1 file in cur/ after read, got %d", len(entries))
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestCLI_InitGit(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)

	cmd := exec.Command(bin, "init", "--git")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg init --git failed: %v\n%s", err, out)
	}

	gitDir := filepath.Join(tmpHome, ".macguffin", ".git")
	info, err := os.Stat(gitDir)
	if err != nil {
		t.Fatalf(".git should exist after init --git: %v", err)
	}
	if !info.IsDir() {
		t.Fatal(".git should be a directory")
	}
}

func TestCLI_SnapshotAndLog(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)

	// Init with git
	cmd := exec.Command(bin, "init", "--git")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg init --git failed: %v\n%s", err, out)
	}

	// Create a work item file
	itemPath := filepath.Join(tmpHome, ".macguffin", "work", "available", "gt-test.md")
	if err := os.WriteFile(itemPath, []byte("---\nid: gt-test\n---\nTracked item\n"), 0o644); err != nil {
		t.Fatalf("writing item: %v", err)
	}

	// Snapshot
	cmd = exec.Command(bin, "snapshot")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome)
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg snapshot failed: %v\n%s", err, out)
	}

	// Verify git log shows the commit
	cmd = exec.Command(bin, "log", "--oneline")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome)
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg log failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "state snapshot") {
		t.Errorf("expected 'state snapshot' in log output, got: %s", out)
	}

	// Move item to done and snapshot again
	donePath := filepath.Join(tmpHome, ".macguffin", "work", "done", "gt-test.md")
	if err := os.Rename(itemPath, donePath); err != nil {
		t.Fatalf("moving item to done: %v", err)
	}

	cmd = exec.Command(bin, "snapshot")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome)
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("second snapshot failed: %v\n%s", err, out)
	}

	// Verify >= 2 commits
	cmd = exec.Command(bin, "log", "--oneline")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome)
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg log failed: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		t.Errorf("expected >= 2 commits, got %d: %s", len(lines), out)
	}
}

func TestCLI_ScheduleE2E(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := append(os.Environ(), "HOME="+tmpHome)

	// Init
	cmd := exec.Command(bin, "init")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	// Create Phase 1 (no deps) → available/
	cmd = exec.Command(bin, "new", "Phase 1")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg new phase1 failed: %v\n%s", err, out)
	}
	id1 := strings.TrimPrefix(strings.Split(string(out), ":")[0], "Created ")

	// Create Phase 2 (depends on Phase 1) → pending/
	cmd = exec.Command(bin, "new", "--depends="+id1, "Phase 2")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg new phase2 failed: %v\n%s", err, out)
	}
	id2 := strings.TrimPrefix(strings.Split(string(out), ":")[0], "Created ")

	// Phase 2 should NOT be in available/
	cmd = exec.Command(bin, "list", "--status=available")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg list available failed: %v\n%s", err, out)
	}
	if strings.Contains(string(out), id2) {
		t.Errorf("Phase 2 should not be in available/ yet, got:\n%s", out)
	}

	// Phase 2 should be in pending/
	cmd = exec.Command(bin, "list", "--status=pending")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg list pending failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), id2) {
		t.Errorf("Phase 2 should be in pending/, got:\n%s", out)
	}

	// Complete Phase 1: claim + done
	cmd = exec.Command(bin, "claim", id1)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg claim phase1 failed: %v\n%s", err, out)
	}
	cmd = exec.Command(bin, "done", id1)
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg done phase1 failed: %v\n%s", err, out)
	}

	// done should auto-promote Phase 2
	if !strings.Contains(string(out), "Promoted "+id2) {
		t.Errorf("expected 'Promoted %s' in done output, got %q", id2, out)
	}

	// Phase 2 should now be in available/
	cmd = exec.Command(bin, "list", "--status=available")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg list available failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), id2) {
		t.Errorf("Phase 2 should now be in available/, got:\n%s", out)
	}
}

func TestCLI_EventListEmpty(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := append(os.Environ(), "HOME="+tmpHome)

	initCmd := exec.Command(bin, "init")
	initCmd.Env = env
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	// Missing log file: stdout empty, stderr has hint with the path.
	cmd := exec.Command(bin, "event", "list")
	cmd.Env = env
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("mg event list failed: %v\nstderr: %s", err, stderr.String())
	}
	if stdout.String() != "" {
		t.Errorf("expected empty stdout, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "no events found at ") {
		t.Errorf("expected 'no events found at ...' on stderr, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "events.jsonl") {
		t.Errorf("expected stderr to include the log path, got %q", stderr.String())
	}

	// Empty log file (touched, zero bytes): same hint.
	logPath := filepath.Join(tmpHome, ".macguffin", "events.jsonl")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if f, err := os.Create(logPath); err != nil {
		t.Fatal(err)
	} else {
		f.Close()
	}

	cmd = exec.Command(bin, "event", "list")
	cmd.Env = env
	stdout.Reset()
	stderr.Reset()
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("mg event list (empty file) failed: %v\nstderr: %s", err, stderr.String())
	}
	if stdout.String() != "" {
		t.Errorf("expected empty stdout for empty log, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "no events found at ") {
		t.Errorf("expected stderr hint for empty log, got %q", stderr.String())
	}

	// After appending, hint should not appear and stdout should have the entry.
	appendCmd := exec.Command(bin, "event", "append", "test.event", "--key=val")
	appendCmd.Env = env
	if out, err := appendCmd.CombinedOutput(); err != nil {
		t.Fatalf("mg event append failed: %v\n%s", err, out)
	}

	cmd = exec.Command(bin, "event", "list")
	cmd.Env = env
	stdout.Reset()
	stderr.Reset()
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("mg event list after append failed: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "test.event") {
		t.Errorf("expected event on stdout, got %q", stdout.String())
	}
	if strings.Contains(stderr.String(), "no events found") {
		t.Errorf("did not expect 'no events found' hint when log has entries, got %q", stderr.String())
	}
}

func TestCLI_NewWithBudget(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := append(os.Environ(), "HOME="+tmpHome)

	cmd := exec.Command(bin, "init")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	cmd = exec.Command(bin, "new", "--budget=200000", "--title=budgeted task")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg new --budget failed: %v\n%s", err, out)
	}
	id := strings.TrimPrefix(strings.Split(string(out), ":")[0], "Created ")

	// Frontmatter should contain "budget: 200000".
	avail := filepath.Join(tmpHome, ".macguffin", "work", "available")
	data, err := os.ReadFile(filepath.Join(avail, id+".md"))
	if err != nil {
		t.Fatalf("read item: %v", err)
	}
	if !strings.Contains(string(data), "budget: 200000") {
		t.Errorf("frontmatter should contain 'budget: 200000', got:\n%s", string(data))
	}

	// mg show should display the budget with comma formatting.
	cmd = exec.Command(bin, "show", id)
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg show failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Budget:") {
		t.Errorf("show output should contain 'Budget:', got:\n%s", out)
	}
	if !strings.Contains(string(out), "200,000 tokens") {
		t.Errorf("show output should contain '200,000 tokens', got:\n%s", out)
	}
}

// writeSpendRecord appends a single spend NDJSON entry under
// $HOME/.macguffin/spend/by-item/<id>.jsonl. Used by TestCLI_ShowSpentLine_*
// to exercise mg show's Spent/budget rendering without depending on the
// transcript aggregator.
func writeSpendRecord(t *testing.T, home, itemID string, input, cacheRead, cacheCreate, output int) {
	t.Helper()
	dir := filepath.Join(home, ".macguffin", "spend", "by-item")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir spend dir: %v", err)
	}
	rec := map[string]any{
		"ts":           time.Now().UTC().Format(time.RFC3339),
		"agent":        "test",
		"model":        "test-model",
		"input":        input,
		"cache_read":   cacheRead,
		"cache_create": cacheCreate,
		"output":       output,
		"session":      "s-test",
		"message_uuid": fmt.Sprintf("u-%d-%d-%d-%d", input, cacheRead, cacheCreate, output),
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, itemID+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open spend file: %v", err)
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		t.Fatalf("write record: %v", err)
	}
}

func TestCLI_ShowSpentLine_UnderBudget(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := append(os.Environ(), "HOME="+tmpHome)

	cmd := exec.Command(bin, "init")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	cmd = exec.Command(bin, "new", "--budget=200000", "--title=under budget")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg new --budget failed: %v\n%s", err, out)
	}
	id := strings.TrimPrefix(strings.Split(string(out), ":")[0], "Created ")

	// 142,308 tokens total = 71% of 200,000 — matches design §3 example.
	writeSpendRecord(t, tmpHome, id, 100000, 20000, 12308, 10000)

	cmd = exec.Command(bin, "show", id)
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg show failed: %v\n%s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, "Spent:") {
		t.Errorf("show output should contain 'Spent:', got:\n%s", got)
	}
	if !strings.Contains(got, "142,308 tokens") {
		t.Errorf("show output should contain '142,308 tokens', got:\n%s", got)
	}
	if !strings.Contains(got, "(71% of budget)") {
		t.Errorf("show output should contain '(71%% of budget)', got:\n%s", got)
	}
	if strings.Contains(got, "⚠") {
		t.Errorf("under-budget show output should NOT contain warning, got:\n%s", got)
	}
}

func TestCLI_ShowSpentLine_OverBudget(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := append(os.Environ(), "HOME="+tmpHome)

	cmd := exec.Command(bin, "init")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	cmd = exec.Command(bin, "new", "--budget=100000", "--title=over budget")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg new --budget failed: %v\n%s", err, out)
	}
	id := strings.TrimPrefix(strings.Split(string(out), ":")[0], "Created ")

	// 150,000 tokens total = 150% of 100,000 — should append ⚠.
	writeSpendRecord(t, tmpHome, id, 100000, 0, 0, 50000)

	cmd = exec.Command(bin, "show", id)
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg show failed: %v\n%s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, "Spent:") {
		t.Errorf("show output should contain 'Spent:', got:\n%s", got)
	}
	if !strings.Contains(got, "150,000 tokens") {
		t.Errorf("show output should contain '150,000 tokens', got:\n%s", got)
	}
	if !strings.Contains(got, "(150% of budget)") {
		t.Errorf("show output should contain '(150%% of budget)', got:\n%s", got)
	}
	if !strings.Contains(got, "⚠") {
		t.Errorf("over-budget show output should contain warning ⚠, got:\n%s", got)
	}
}

func TestCLI_ShowSpentLine_NoSpendRecords(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := append(os.Environ(), "HOME="+tmpHome)

	cmd := exec.Command(bin, "init")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	cmd = exec.Command(bin, "new", "--budget=200000", "--title=no spend yet")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg new --budget failed: %v\n%s", err, out)
	}
	id := strings.TrimPrefix(strings.Split(string(out), ":")[0], "Created ")

	// No spend records written — Spent line must be omitted.
	cmd = exec.Command(bin, "show", id)
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg show failed: %v\n%s", err, out)
	}
	got := string(out)
	if !strings.Contains(got, "Budget:") {
		t.Errorf("show output should still contain 'Budget:', got:\n%s", got)
	}
	if strings.Contains(got, "Spent:") {
		t.Errorf("show output should NOT contain 'Spent:' when no records exist, got:\n%s", got)
	}
}

func TestCLI_EditBudgetSetAndUnset(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := append(os.Environ(), "HOME="+tmpHome)

	cmd := exec.Command(bin, "init")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	cmd = exec.Command(bin, "new", "--title=edit budget")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg new failed: %v\n%s", err, out)
	}
	id := strings.TrimPrefix(strings.Split(string(out), ":")[0], "Created ")

	// Initially: no budget shown.
	cmd = exec.Command(bin, "show", id)
	cmd.Env = env
	out, _ = cmd.CombinedOutput()
	if strings.Contains(string(out), "Budget:") {
		t.Errorf("show output should not contain 'Budget:' before set, got:\n%s", out)
	}

	// Set budget via edit.
	cmd = exec.Command(bin, "edit", id, "--budget=300000")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg edit --budget failed: %v\n%s", err, out)
	}

	avail := filepath.Join(tmpHome, ".macguffin", "work", "available")
	data, err := os.ReadFile(filepath.Join(avail, id+".md"))
	if err != nil {
		t.Fatalf("read item: %v", err)
	}
	if !strings.Contains(string(data), "budget: 300000") {
		t.Errorf("frontmatter should contain 'budget: 300000', got:\n%s", string(data))
	}

	// Unset via --budget=0.
	cmd = exec.Command(bin, "edit", id, "--budget=0")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg edit --budget=0 failed: %v\n%s", err, out)
	}

	data, err = os.ReadFile(filepath.Join(avail, id+".md"))
	if err != nil {
		t.Fatalf("read item: %v", err)
	}
	if strings.Contains(string(data), "budget:") {
		t.Errorf("frontmatter should not contain budget: after --budget=0, got:\n%s", string(data))
	}
}

func TestCLI_UpdateAliasBudget(t *testing.T) {
	// `update` is an alias for `edit`; --budget should work either way.
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := append(os.Environ(), "HOME="+tmpHome)

	cmd := exec.Command(bin, "init")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	cmd = exec.Command(bin, "new", "--title=update alias")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg new failed: %v\n%s", err, out)
	}
	id := strings.TrimPrefix(strings.Split(string(out), ":")[0], "Created ")

	cmd = exec.Command(bin, "update", id, "--budget=400000")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg update --budget failed: %v\n%s", err, out)
	}

	avail := filepath.Join(tmpHome, ".macguffin", "work", "available")
	data, err := os.ReadFile(filepath.Join(avail, id+".md"))
	if err != nil {
		t.Fatalf("read item: %v", err)
	}
	if !strings.Contains(string(data), "budget: 400000") {
		t.Errorf("frontmatter should contain 'budget: 400000', got:\n%s", string(data))
	}
}

func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "mg")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = filepath.Join(testProjectRoot(t), "cmd", "mg")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}
	return bin
}

func testProjectRoot(t *testing.T) string {
	t.Helper()
	// Walk up from cmd/mg to find go.mod
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find project root (go.mod)")
		}
		dir = parent
	}
}

// jsonItem mirrors the public NDJSON shape emitted by `mg list --json`.
// Tests parse stdout into this to assert the contract holds.
type jsonItem struct {
	ID       string    `json:"id"`
	Type     string    `json:"type"`
	Status   string    `json:"status"`
	Title    string    `json:"title"`
	Tags     []string  `json:"tags"`
	Assignee string    `json:"assignee"`
	Priority string    `json:"priority"`
	Repo     string    `json:"repo"`
	Branch   string    `json:"branch"`
	Depends  []string  `json:"depends"`
	Created  time.Time `json:"created"`
	Mtime    time.Time `json:"mtime"`
}

func parseNDJSON(t *testing.T, out []byte) []jsonItem {
	t.Helper()
	var items []jsonItem
	for i, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		var it jsonItem
		if err := json.Unmarshal([]byte(line), &it); err != nil {
			t.Fatalf("line %d not valid JSON: %v\nline: %s", i+1, err, line)
		}
		items = append(items, it)
	}
	return items
}

func TestCLI_ListJSON_Empty(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := append(os.Environ(), "HOME="+tmpHome)

	cmd := exec.Command(bin, "init")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	cmd = exec.Command(bin, "list", "--json")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg list --json failed: %v\n%s", err, out)
	}
	if len(strings.TrimSpace(string(out))) != 0 {
		t.Errorf("expected empty output for no work items, got %q", out)
	}
}

func TestCLI_ListJSON_Fields(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := append(os.Environ(), "HOME="+tmpHome)

	cmd := exec.Command(bin, "init")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	// Create a work item with all the optional fields set
	cmd = exec.Command(bin, "new", "--type=bug", "--assignee=alice",
		"--priority=high", "--tag=urgent,backend",
		"--branch=feature/x", "JSON test item")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg new failed: %v\n%s", err, out)
	}
	id := strings.TrimPrefix(strings.Split(string(out), ":")[0], "Created ")

	// `mg new` doesn't accept --repo (auto-detected from cwd). Set it via edit.
	cmd = exec.Command(bin, "edit", id, "--repo=/tmp/foo")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg edit failed: %v\n%s", err, out)
	}

	cmd = exec.Command(bin, "list", "--json")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg list --json failed: %v\n%s", err, out)
	}

	items := parseNDJSON(t, out)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d: %s", len(items), out)
	}
	it := items[0]

	if it.ID != id {
		t.Errorf("id = %q, want %q", it.ID, id)
	}
	if it.Type != "bug" {
		t.Errorf("type = %q, want %q", it.Type, "bug")
	}
	if it.Status != "available" {
		t.Errorf("status = %q, want %q", it.Status, "available")
	}
	if it.Title != "JSON test item" {
		t.Errorf("title = %q, want %q", it.Title, "JSON test item")
	}
	if it.Assignee != "alice" {
		t.Errorf("assignee = %q, want %q", it.Assignee, "alice")
	}
	if it.Priority != "high" {
		t.Errorf("priority = %q, want %q", it.Priority, "high")
	}
	if it.Repo != "/tmp/foo" {
		t.Errorf("repo = %q, want %q", it.Repo, "/tmp/foo")
	}
	if it.Branch != "feature/x" {
		t.Errorf("branch = %q, want %q", it.Branch, "feature/x")
	}
	if len(it.Tags) != 2 || it.Tags[0] != "urgent" || it.Tags[1] != "backend" {
		t.Errorf("tags = %v, want [urgent backend]", it.Tags)
	}
	if it.Depends == nil {
		t.Errorf("depends should be [] not null")
	}
	if it.Created.IsZero() {
		t.Errorf("created should be set, got zero")
	}
	if it.Mtime.IsZero() {
		t.Errorf("mtime should be set, got zero")
	}
}

func TestCLI_ListJSON_NDJSON(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := append(os.Environ(), "HOME="+tmpHome)

	cmd := exec.Command(bin, "init")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	for _, title := range []string{"Item one", "Item two", "Item three"} {
		cmd = exec.Command(bin, "new", "--type=bug", title)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("mg new %q failed: %v\n%s", title, err, out)
		}
	}

	cmd = exec.Command(bin, "list", "--json")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg list --json failed: %v\n%s", err, out)
	}

	// Each line should be a complete, parseable JSON object.
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 NDJSON lines, got %d:\n%s", len(lines), out)
	}
	for i, line := range lines {
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			t.Errorf("line %d not parseable as JSON object: %v\n%s", i, err, line)
		}
	}
}

func TestCLI_ListJSON_StatusFilter(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := append(os.Environ(), "HOME="+tmpHome)

	cmd := exec.Command(bin, "init")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	cmd = exec.Command(bin, "new", "--type=bug", "Available item")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg new failed: %v\n%s", err, out)
	}

	cmd = exec.Command(bin, "new", "--type=task", "Claim me")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg new failed: %v\n%s", err, out)
	}
	id2 := strings.TrimPrefix(strings.Split(string(out), ":")[0], "Created ")

	cmd = exec.Command(bin, "claim", id2)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg claim failed: %v\n%s", err, out)
	}

	cmd = exec.Command(bin, "list", "--json", "--status=claimed")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg list --json --status=claimed failed: %v\n%s", err, out)
	}

	items := parseNDJSON(t, out)
	if len(items) != 1 {
		t.Fatalf("expected 1 claimed item, got %d:\n%s", len(items), out)
	}
	if items[0].ID != id2 {
		t.Errorf("expected id=%s, got %s", id2, items[0].ID)
	}
	if items[0].Status != "claimed" {
		t.Errorf("expected status=claimed, got %s", items[0].Status)
	}
}

func TestCLI_ListJSON_TagFilter(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := append(os.Environ(), "HOME="+tmpHome)

	cmd := exec.Command(bin, "init")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	cmd = exec.Command(bin, "new", "--type=bug", "--tag=urgent", "Tagged item")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg new failed: %v\n%s", err, out)
	}
	cmd = exec.Command(bin, "new", "--type=bug", "Untagged item")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg new failed: %v\n%s", err, out)
	}

	cmd = exec.Command(bin, "list", "--json", "--tag=urgent")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg list --json --tag=urgent failed: %v\n%s", err, out)
	}

	items := parseNDJSON(t, out)
	if len(items) != 1 {
		t.Fatalf("expected 1 tagged item, got %d:\n%s", len(items), out)
	}
	if items[0].Title != "Tagged item" {
		t.Errorf("expected title=Tagged item, got %s", items[0].Title)
	}
}

func TestCLI_ListJSON_GroupedStatuses(t *testing.T) {
	tmpHome := t.TempDir()
	bin := buildBinary(t)
	env := append(os.Environ(), "HOME="+tmpHome)

	cmd := exec.Command(bin, "init")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg init failed: %v\n%s", err, out)
	}

	cmd = exec.Command(bin, "new", "--type=bug", "Stays available")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg new failed: %v\n%s", err, out)
	}
	cmd = exec.Command(bin, "new", "--type=task", "Becomes claimed")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg new failed: %v\n%s", err, out)
	}
	id2 := strings.TrimPrefix(strings.Split(string(out), ":")[0], "Created ")
	cmd = exec.Command(bin, "claim", id2)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg claim failed: %v\n%s", err, out)
	}

	cmd = exec.Command(bin, "list", "--json")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg list --json failed: %v\n%s", err, out)
	}

	items := parseNDJSON(t, out)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d:\n%s", len(items), out)
	}
	statuses := map[string]bool{}
	for _, it := range items {
		statuses[it.Status] = true
	}
	if !statuses["available"] || !statuses["claimed"] {
		t.Errorf("expected items in both available and claimed, got statuses %v", statuses)
	}
}
