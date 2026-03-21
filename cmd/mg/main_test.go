package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	err := exec.Command(bin, "bogus").Run()
	if err == nil {
		t.Error("expected non-zero exit for unknown command")
	}
}

func TestCLI_NoArgs(t *testing.T) {
	bin := buildBinary(t)
	err := exec.Command(bin).Run()
	if err == nil {
		t.Error("expected non-zero exit for no args")
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

	// Without --archived, done item should NOT appear in grouped list
	cmd = exec.Command(bin, "list")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg list failed: %v\n%s", err, out)
	}
	listOutput := string(out)
	if strings.Contains(listOutput, "Archived item") {
		t.Errorf("list without --archived should not show done items, got:\n%s", listOutput)
	}
	if !strings.Contains(listOutput, "Active item") {
		t.Errorf("list should show active items, got:\n%s", listOutput)
	}

	// With --archived, done item SHOULD appear
	cmd = exec.Command(bin, "list", "--archived")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg list --archived failed: %v\n%s", err, out)
	}
	listOutput = string(out)
	if !strings.Contains(listOutput, "Archived item") {
		t.Errorf("list --archived should show done items, got:\n%s", listOutput)
	}
	if !strings.Contains(listOutput, "done:") {
		t.Errorf("list --archived should contain 'done:' group, got:\n%s", listOutput)
	}

	// With -a (short form), done item SHOULD also appear
	cmd = exec.Command(bin, "list", "-a")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg list -a failed: %v\n%s", err, out)
	}
	listOutput = string(out)
	if !strings.Contains(listOutput, "Archived item") {
		t.Errorf("list -a should show done items, got:\n%s", listOutput)
	}
}

func TestCLI_ClaimNoID(t *testing.T) {
	bin := buildBinary(t)
	err := exec.Command(bin, "claim").Run()
	if err == nil {
		t.Error("expected non-zero exit for claim without ID")
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
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mg done phase1 failed: %v\n%s", err, out)
	}

	// Schedule — should promote Phase 2
	cmd = exec.Command(bin, "schedule")
	cmd.Env = env
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mg schedule failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Promoted "+id2) {
		t.Errorf("expected 'Promoted %s' output, got %q", id2, out)
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
