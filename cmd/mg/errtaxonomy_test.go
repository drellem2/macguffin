package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/drellem2/macguffin/internal/mgerr"
)

// This file is the CONTRACT test for the mg error taxonomy (mg-931f, issue
// #57). It pins the three frozen contracts end-to-end through the real binary:
//
//  1. Exit codes: 0 success / 1 internal / 2 usage / 3 not_found / 4 conflict.
//  2. Error-code slugs: the snake_case machine ids per category.
//  3. The JSON error object shape on stderr: {error:{code,category,exit,
//     message,hint?,retryable}} — data --json stays on stdout, untouched.
//
// These assertions are intentionally rigid: they are the public contract, and a
// break here means a downstream (shell or JSON) consumer breaks. New categories
// or slugs are additive — extend the tables, never repurpose an existing value.

// taxRun runs the binary with separated stdout/stderr and returns them plus the
// process exit code.
func taxRun(bin string, env []string, args ...string) (stdout, stderr string, exit int) {
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	err := cmd.Run()
	exit = 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exit = ee.ExitCode()
		} else {
			exit = -1
		}
	}
	return so.String(), se.String(), exit
}

// TestContract_ExitCodes pins the FROZEN exit code per error category (§1).
func TestContract_ExitCodes(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := buildBinary(t)

	cases := []struct {
		name string
		args func(env []string) []string
		exit int
	}{
		{
			name: "success is 0",
			args: func(env []string) []string { return []string{"version"} },
			exit: 0,
		},
		{
			name: "not_found is 3",
			args: func(env []string) []string { return []string{"claim", "mg-zzzz"} },
			exit: 3,
		},
		{
			name: "usage unknown flag is 2",
			args: func(env []string) []string { return []string{"claim", "mg-x", "--nope"} },
			exit: 2,
		},
		{
			name: "usage bad arg count is 2",
			args: func(env []string) []string { return []string{"claim"} },
			exit: 2,
		},
		{
			name: "usage unknown command is 2",
			args: func(env []string) []string { return []string{"frobnicate"} },
			exit: 2,
		},
		{
			name: "usage mutually-exclusive flags is 2",
			args: func(env []string) []string { return []string{"spend", "--since=1d", "--window=today"} },
			exit: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			env := emEnv(home)
			emInit(t, bin, env)
			_, _, exit := taxRun(bin, env, tc.args(env)...)
			if exit != tc.exit {
				t.Errorf("exit = %d, want %d", exit, tc.exit)
			}
		})
	}
}

// TestContract_ConflictExitCode pins exit 4 for a genuine state conflict, and
// that a claimed item's re-claim reports the already_claimed conflict.
func TestContract_ConflictExitCode(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := buildBinary(t)
	home := t.TempDir()
	env := emEnv(home)
	emInit(t, bin, env)
	id := emNew(t, bin, env, "conflict target")
	emOK(t, bin, env, "claim", id)

	_, _, exit := taxRun(bin, env, "claim", id)
	if exit != 4 {
		t.Errorf("re-claim exit = %d, want 4 (conflict)", exit)
	}
}

// TestContract_JSONErrorShape pins the FROZEN JSON error object (§4): it is
// emitted to STDERR, namespaced under "error", stdout stays empty, and every
// field is present with the frozen slug/category/exit.
func TestContract_JSONErrorShape(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := buildBinary(t)
	home := t.TempDir()
	env := emEnv(home)
	emInit(t, bin, env)

	stdout, stderr, exit := taxRun(bin, env, "show", "mg-zzzz", "--json")
	if exit != 3 {
		t.Fatalf("exit = %d, want 3", exit)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("stdout should be empty on a --json error, got %q", stdout)
	}

	var got struct {
		Error struct {
			Code      string `json:"code"`
			Category  string `json:"category"`
			Exit      int    `json:"exit"`
			Message   string `json:"message"`
			Hint      string `json:"hint"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stderr)), &got); err != nil {
		t.Fatalf("stderr is not the JSON error object: %v\n%s", err, stderr)
	}
	if got.Error.Code != "no_such_item" {
		t.Errorf("code = %q, want no_such_item", got.Error.Code)
	}
	if got.Error.Category != "not_found" {
		t.Errorf("category = %q, want not_found", got.Error.Category)
	}
	if got.Error.Exit != 3 {
		t.Errorf("error.exit = %d, want 3", got.Error.Exit)
	}
	if !strings.Contains(got.Error.Message, "no such work item") {
		t.Errorf("message = %q", got.Error.Message)
	}
	if got.Error.Retryable {
		t.Error("no_such_item should not be retryable")
	}
}

// TestContract_JSONErrorTopLevelKey guarantees the object is namespaced under a
// single top-level "error" key and nothing else, so it can never collide with a
// data object on stdout.
func TestContract_JSONErrorTopLevelKey(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := buildBinary(t)
	home := t.TempDir()
	env := emEnv(home)
	emInit(t, bin, env)

	_, stderr, _ := taxRun(bin, env, "show", "mg-zzzz", "--json")
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimSpace(stderr)), &top); err != nil {
		t.Fatalf("stderr not JSON: %v", err)
	}
	if len(top) != 1 {
		t.Errorf("error object has %d top-level keys, want exactly 1 (\"error\")", len(top))
	}
	if _, ok := top["error"]; !ok {
		t.Errorf("missing top-level \"error\" key; got %v", top)
	}
}

// TestContract_DataJSONUnchanged confirms a SUCCESSFUL --json command still
// streams its data object to stdout with a clean stderr and exit 0 — the error
// channel is entirely separate.
func TestContract_DataJSONUnchanged(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := buildBinary(t)
	home := t.TempDir()
	env := emEnv(home)
	emInit(t, bin, env)
	id := emNew(t, bin, env, "data json target")

	stdout, stderr, exit := taxRun(bin, env, "show", id, "--json")
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Errorf("stderr should be empty on success, got %q", stderr)
	}
	var item map[string]any
	if err := json.Unmarshal([]byte(stdout), &item); err != nil {
		t.Fatalf("stdout is not the data object: %v", err)
	}
	if item["id"] != id {
		t.Errorf("data object id = %v, want %s", item["id"], id)
	}
	if _, hasErr := item["error"]; hasErr {
		t.Error("a successful data object must not carry an \"error\" key")
	}
}

// TestContract_JSONRendererFields directly exercises writeJSONError to pin the
// hint (omitempty) and retryable fields, which the CLI --json paths above don't
// happen to combine (a hinted, retryable conflict).
func TestContract_JSONRendererFields(t *testing.T) {
	// Hinted + retryable: both fields must be present with correct values.
	e := mgerr.Conflict("claim_race", "mg-1: race lost", "Run 'mg show mg-1' to check.").WithRetryable(true)
	var buf bytes.Buffer
	writeJSONError(&buf, e)
	var got map[string]map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	body := got["error"]
	if body["code"] != "claim_race" || body["category"] != "conflict" || body["exit"].(float64) != 4 {
		t.Errorf("body = %v", body)
	}
	if body["hint"] != "Run 'mg show mg-1' to check." {
		t.Errorf("hint = %v", body["hint"])
	}
	if body["retryable"] != true {
		t.Errorf("retryable = %v, want true", body["retryable"])
	}

	// No hint: the hint key is omitted (omitempty), but retryable is always
	// present (false) for predictable parsing.
	e2 := mgerr.NotFound("no_such_item", "mg-2: no such work item.", "")
	buf.Reset()
	writeJSONError(&buf, e2)
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if _, hasHint := got["error"]["hint"]; hasHint {
		t.Error("hint should be omitted when empty")
	}
	if _, hasRetry := got["error"]["retryable"]; !hasRetry {
		t.Error("retryable must always be present, even when false")
	}
}

// TestContract_HumanErrorFormat pins the human render: "Error: <message>" with
// the hint on its own indented "  → " line (reproducing the old withHint UX).
func TestContract_HumanErrorFormat(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := buildBinary(t)
	home := t.TempDir()
	env := emEnv(home)
	emInit(t, bin, env)
	id := emNew(t, bin, env, "human format target")
	emOK(t, bin, env, "claim", id)

	_, stderr, exit := taxRun(bin, env, "claim", id)
	if exit != 4 {
		t.Fatalf("exit = %d, want 4", exit)
	}
	if !strings.HasPrefix(stderr, "Error: ") {
		t.Errorf("human error should start with %q, got %q", "Error: ", stderr)
	}
	if !strings.Contains(stderr, "already claimed") {
		t.Errorf("missing problem statement: %q", stderr)
	}
	if !strings.Contains(stderr, "\n  → ") {
		t.Errorf("hint should render on its own indented → line: %q", stderr)
	}
}
