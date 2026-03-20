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
		t.Fatalf("macguffin version failed: %v\n%s", err, out)
	}
	if want := "macguffin " + version + "\n"; string(out) != want {
		t.Errorf("version output = %q, want %q", out, want)
	}
}

func TestCLI_Help(t *testing.T) {
	bin := buildBinary(t)
	out, err := exec.Command(bin, "help").CombinedOutput()
	if err != nil {
		t.Fatalf("macguffin help failed: %v\n%s", err, out)
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
		t.Fatalf("macguffin init failed: %v\n%s", err, out)
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

func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "macguffin")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = filepath.Join(testProjectRoot(t), "cmd", "macguffin")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}
	return bin
}

func testProjectRoot(t *testing.T) string {
	t.Helper()
	// Walk up from cmd/macguffin to find go.mod
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
