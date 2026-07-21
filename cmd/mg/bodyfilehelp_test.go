package main

import (
	"os/exec"
	"strings"
	"testing"
)

// bodyFileCmds are every mg subcommand that offers the --body/--body-file pair.
// A new one that grows the pair belongs here: the point of these tests is that
// one hazard has exactly one spelling of the cure, everywhere it appears.
var bodyFileCmds = [][]string{
	{"mail", "send"},
	{"new"},
	{"edit"},
}

// mgHelp runs `mg <args...> --help` and returns its combined output.
func mgHelp(t *testing.T, bin string, args ...string) string {
	t.Helper()
	out, err := exec.Command(bin, append(args, "--help")...).CombinedOutput()
	if err != nil {
		t.Fatalf("mg %s --help failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// TestBodyFileHelp_MatchesPogo is the MIRROR of pogo's
// TestSpawnPolecat_BodyFileHelpMatchesMg (pogo cmd/pogo/bodyfile_test.go).
//
// That test pins these same four substrings, but it executes pogo's help only.
// Its name claims it guards mg parity while nothing anywhere ran mg — so the
// only direction it could ever protect was "pogo must not drift from mg". mg
// could drift away freely and the pogo suite stayed green: a guard reporting
// success for a population it never observed, which is this repo's recurring
// disease in test form.
//
// This is the other half. Together the two tests close the loop: neither
// binary can restate the cure in its own words without a red suite somewhere.
//
// If you are changing this wording, you are editing a CROSS-REPO contract.
// pogo's copy of these literals must change in the same breath or one repo
// goes red for a reason nobody working in it can see.
func TestBodyFileHelp_MatchesPogo(t *testing.T) {
	bin := buildBinary(t)
	for _, args := range bodyFileCmds {
		t.Run(strings.Join(args, "-"), func(t *testing.T) {
			help := mgHelp(t, bin, args...)
			for _, want := range []string{
				"--body-file",
				`("-" for stdin)`,
				"mutually exclusive with --body",
				"verbatim",
			} {
				if !strings.Contains(help, want) {
					t.Errorf("mg %s --help must mention %q (pogo parity), got:\n%s",
						strings.Join(args, " "), want, help)
				}
			}
		})
	}
}

// TestBodyFileHelp_LeadsWithQuotedHeredoc is mg-63c2's substance: --body-file
// must be the FIRST thing the help teaches, with --body demoted to the
// inline-only shortcut beneath it.
//
// The ordering is the whole point. Both flags have existed since mg-7850 and
// both are documented; what bit callers was that the dangerous one was the one
// they read first. Documenting the safe path second is documenting it for
// people who already stopped reading.
//
// Checked structurally rather than by pinned prose, so the help can be
// rewritten freely as long as it still leads with the safe idiom.
func TestBodyFileHelp_LeadsWithQuotedHeredoc(t *testing.T) {
	bin := buildBinary(t)
	for _, args := range bodyFileCmds {
		t.Run(strings.Join(args, "-"), func(t *testing.T) {
			help := mgHelp(t, bin, args...)

			file := strings.Index(help, "--body-file")
			bare := firstBareBodyFlag(help)
			if file < 0 {
				t.Fatalf("mg %s --help never mentions --body-file:\n%s", strings.Join(args, " "), help)
			}
			if bare >= 0 && bare < file {
				t.Errorf("mg %s --help introduces --body (at %d) before --body-file (at %d); "+
					"the safe idiom must come first:\n%s", strings.Join(args, " "), bare, file, help)
			}

			// The canonical example, quoted heredoc and all.
			if !strings.Contains(help, "--body-file - <<'EOF'") {
				t.Errorf("mg %s --help must show the canonical `--body-file - <<'EOF'` example, got:\n%s",
					strings.Join(args, " "), help)
			}
		})
	}
}

// TestBodyFileHelp_NoUnquotedHeredoc: every heredoc mg ships as a COPYABLE
// EXAMPLE must be <<'EOF'. An unquoted <<EOF expands backticks, $VAR and
// $(cmd) exactly as --body="..." does, so an example that drops the quotes
// reintroduces the whole bug while looking like the fix — the next author
// copies the shape, not the caveat. That is a defect, not a style nit, which
// is why it gets a test rather than a review comment.
//
// Scoped to indented command lines on purpose. The prose is expected to write
// <<EOF unquoted when NAMING the hazard ("an unquoted <<EOF expands...") —
// that sentence is the warning, not a thing anyone pastes. An earlier draft of
// this check scanned the whole help and flagged exactly that sentence, which
// would have pressured the docs into dropping the one explanation that keeps
// the next author from making the mistake.
func TestBodyFileHelp_NoUnquotedHeredoc(t *testing.T) {
	bin := buildBinary(t)
	for _, args := range bodyFileCmds {
		t.Run(strings.Join(args, "-"), func(t *testing.T) {
			help := mgHelp(t, bin, args...)
			examples := 0
			for _, line := range strings.Split(help, "\n") {
				if !strings.HasPrefix(line, "  ") || !strings.Contains(line, "mg ") {
					continue
				}
				j := strings.Index(line, "<<")
				if j < 0 {
					continue
				}
				examples++
				if !strings.HasPrefix(line[j:], "<<'") {
					t.Errorf("mg %s --help ships an UNQUOTED heredoc example — <<EOF expands "+
						"exactly like --body=\"...\" and silently reintroduces the bug. Use <<'EOF'.\nline: %q",
						strings.Join(args, " "), line)
				}
			}
			// Guard the guard: if the help stops shipping heredoc examples
			// entirely, the loop above passes vacuously and this test stops
			// meaning anything.
			if examples == 0 {
				t.Errorf("mg %s --help ships no heredoc example at all, so this check is vacuous",
					strings.Join(args, " "))
			}
		})
	}
}

// firstBareBodyFlag returns the offset of the first "--body" in s that is NOT
// the "--body-file" prefix, or -1. Scanning for the bare flag this way keeps
// the ordering check independent of how either flag is worded around it.
func firstBareBodyFlag(s string) int {
	for i := 0; ; {
		j := strings.Index(s[i:], "--body")
		if j < 0 {
			return -1
		}
		at := i + j
		if !strings.HasPrefix(s[at:], "--body-file") {
			return at
		}
		i = at + len("--body-file")
	}
}
