package main

import (
	"os"
	"os/exec"
	"path/filepath"
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
