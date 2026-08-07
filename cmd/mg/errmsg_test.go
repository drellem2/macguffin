package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// emEnv builds a clean environment rooted at home. POGO_PID is stripped so
// `mg claim` falls back to the test process PID deterministically.
func emEnv(home string) []string {
	var env []string
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "HOME=") || strings.HasPrefix(e, "POGO_PID=") {
			continue
		}
		env = append(env, e)
	}
	return append(env, "HOME="+home)
}

// emRun runs the mg binary and returns combined output plus the exec error.
func emRun(bin string, env []string, args ...string) (string, error) {
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// emOK runs an mg command that is expected to succeed.
func emOK(t *testing.T, bin string, env []string, args ...string) string {
	t.Helper()
	out, err := emRun(bin, env, args...)
	if err != nil {
		t.Fatalf("mg %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// emInit initializes a fresh workspace under the test HOME.
func emInit(t *testing.T, bin string, env []string) {
	t.Helper()
	emOK(t, bin, env, "init")
}

// emNew creates a work item (no repo breadcrumb) and returns its generated ID.
func emNew(t *testing.T, bin string, env []string, title string) string {
	t.Helper()
	out := emOK(t, bin, env, "new", "--no-repo", title)
	// Output shape: "Created mg-ab12: <title>\n"
	fields := strings.Fields(out)
	if len(fields) < 2 || fields[0] != "Created" {
		t.Fatalf("unexpected `mg new` output: %q", out)
	}
	return strings.TrimSuffix(fields[1], ":")
}

// TestCLI_ErrorMessages is a table-driven check that the failure paths of the
// most-hit commands (claim/done/show/new/mail, plus unclaim/reopen/unshelve)
// produce user-readable messages and never leak maildir internals: paths, the
// rename op, the PID-suffix filename, or a raw ENOENT. See mg-c835 and
// docs/error-message-audit.md.
func TestCLI_ErrorMessages(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission-based cases are meaningless as root")
	}
	bin := buildBinary(t)

	// Substrings that would indicate an internal-detail leak. None of the new
	// messages contain a filesystem path, the word "rename", a ".md" filename,
	// or "no such file or directory".
	leakMarkers := []string{
		"rename",
		".macguffin",
		"no such file or directory",
		"work/available",
		"work/claimed",
		"work/done",
		"work/shelved",
		"work/pending",
		".md",
	}

	cases := []struct {
		name  string
		setup func(t *testing.T, home string, env []string) []string // returns mg args to run
		want  []string
	}{
		{
			name: "claim already-claimed names the PID",
			setup: func(t *testing.T, home string, env []string) []string {
				emInit(t, bin, env)
				id := emNew(t, bin, env, "Already claimed target")
				emOK(t, bin, env, "claim", id)
				return []string{"claim", id}
			},
			want: []string{"already claimed", "by PID"},
		},
		{
			name: "claim non-existent id",
			setup: func(t *testing.T, home string, env []string) []string {
				emInit(t, bin, env)
				return []string{"claim", "mg-zzzz"}
			},
			want: []string{"mg-zzzz: no such work item"},
		},
		{
			name: "claim already-done item",
			setup: func(t *testing.T, home string, env []string) []string {
				emInit(t, bin, env)
				id := emNew(t, bin, env, "Done then re-claimed")
				emOK(t, bin, env, "claim", id)
				emOK(t, bin, env, "done", id)
				return []string{"claim", id}
			},
			want: []string{"already done", "mg reopen"},
		},
		{
			name: "done on un-claimed item",
			setup: func(t *testing.T, home string, env []string) []string {
				emInit(t, bin, env)
				id := emNew(t, bin, env, "Available not claimed")
				return []string{"done", id}
			},
			want: []string{"not claimed", "mg claim"},
		},
		{
			name: "done on non-existent id",
			setup: func(t *testing.T, home string, env []string) []string {
				emInit(t, bin, env)
				return []string{"done", "mg-zzzz"}
			},
			want: []string{"mg-zzzz: no such work item"},
		},
		{
			name: "show non-existent id",
			setup: func(t *testing.T, home string, env []string) []string {
				emInit(t, bin, env)
				return []string{"show", "mg-zzzz"}
			},
			want: []string{"mg-zzzz: no such work item"},
		},
		{
			name: "new without a title",
			setup: func(t *testing.T, home string, env []string) []string {
				emInit(t, bin, env)
				return []string{"new", "--no-repo"}
			},
			want: []string{"title is required"},
		},
		{
			name: "new write failure is sanitized",
			setup: func(t *testing.T, home string, env []string) []string {
				emInit(t, bin, env)
				avail := filepath.Join(home, ".macguffin", "work", "available")
				if err := os.Chmod(avail, 0o500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(avail, 0o755) })
				return []string{"new", "--no-repo", "Cannot be written"}
			},
			want: []string{"could not create work item"},
		},
		{
			name: "mail send to a broken mailbox is sanitized",
			setup: func(t *testing.T, home string, env []string) []string {
				emInit(t, bin, env)
				// A regular file where the mailbox dir should be makes maildir
				// creation fail.
				box := filepath.Join(home, ".macguffin", "mail", "bob")
				if err := os.WriteFile(box, []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
				return []string{"mail", "send", "--create", "bob", "--from=alice", "--subject=hi", "--body=yo"}
			},
			want: []string{"could not deliver message to bob"},
		},
		{
			name: "unclaim on un-claimed item",
			setup: func(t *testing.T, home string, env []string) []string {
				emInit(t, bin, env)
				id := emNew(t, bin, env, "Nothing to release")
				return []string{"unclaim", id}
			},
			want: []string{"not claimed"},
		},
		{
			name: "reopen on not-done item",
			setup: func(t *testing.T, home string, env []string) []string {
				emInit(t, bin, env)
				id := emNew(t, bin, env, "Not done yet")
				return []string{"reopen", id}
			},
			want: []string{"not done"},
		},
		{
			name: "unshelve on not-shelved item",
			setup: func(t *testing.T, home string, env []string) []string {
				emInit(t, bin, env)
				id := emNew(t, bin, env, "Not shelved")
				return []string{"unshelve", id}
			},
			want: []string{"not shelved"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			env := emEnv(home)
			args := tc.setup(t, home, env)

			out, err := emRun(bin, env, args...)
			if err == nil {
				t.Fatalf("expected non-zero exit, got success:\n%s", out)
			}
			if !strings.HasPrefix(out, "Error: ") {
				t.Errorf("error output should start with %q, got %q", "Error: ", out)
			}
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("output %q is missing expected substring %q", out, w)
				}
			}
			for _, m := range leakMarkers {
				if strings.Contains(out, m) {
					t.Errorf("output leaks internal detail %q: %q", m, out)
				}
			}
		})
	}
}
