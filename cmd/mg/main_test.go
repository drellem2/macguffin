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
	if !strings.Contains(output, "Created gt-") {
		t.Errorf("expected 'Created gt-...' output, got %q", output)
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
