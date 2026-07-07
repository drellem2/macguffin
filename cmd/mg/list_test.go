package main

import (
	"strings"
	"testing"

	"github.com/drellem2/macguffin/internal/workitem"
)

// TestListStatus_UsageMatchesValidation is the gh drellem2/pogo#58 regression:
// the --status flag-usage string and the RunE validation must agree on the set
// of accepted statuses. Both now derive from listStatusValues, so this test
// fails loudly if a future change reintroduces a separately-maintained list
// that drifts (the original bug: usage listed "archived" not "pending" while
// validation accepted "pending" not "archived").
func TestListStatus_UsageMatchesValidation(t *testing.T) {
	// Every canonical value must be accepted by the validator...
	for _, s := range listStatusValues {
		if !isValidListStatus(s) {
			t.Errorf("status %q is in listStatusValues but isValidListStatus rejects it", s)
		}
	}
	// ...a value NOT in the slice must be rejected.
	if isValidListStatus("bogus") {
		t.Error("isValidListStatus accepted a value not in listStatusValues")
	}

	// The actual registered flag-usage string must mention exactly the
	// canonical values — no more, no fewer. This ties the user-facing help
	// to the validation set, catching drift in either direction.
	usage := listCmd.Flags().Lookup("status").Usage
	for _, s := range listStatusValues {
		if !strings.Contains(usage, s) {
			t.Errorf("flag usage %q does not document accepted status %q", usage, s)
		}
	}
	// Guard the other direction: any status token in the usage string must be
	// a value the validator accepts (so the docs can't advertise a status the
	// command would reject).
	for _, tok := range []string{"available", "claimed", "pending", "done", "shelved", "archived", "blocked", "wip"} {
		documented := strings.Contains(usage, tok)
		accepted := isValidListStatus(tok)
		if documented != accepted {
			t.Errorf("status %q: documented-in-usage=%v but accepted-by-validation=%v", tok, documented, accepted)
		}
	}
}

func TestFormatAssignee_Human(t *testing.T) {
	// "human" assignee should render as blue "human" when current user is set
	result := formatAssignee("human", "alice")
	if result != " \033[34mhuman\033[0m" {
		t.Errorf("formatAssignee(\"human\", \"alice\") = %q, want blue human label", result)
	}
}

func TestFormatAssignee_CurrentUser(t *testing.T) {
	// Assignee matching current user should also render as blue "human"
	result := formatAssignee("alice", "alice")
	if result != " \033[34mhuman\033[0m" {
		t.Errorf("formatAssignee(\"alice\", \"alice\") = %q, want blue human label", result)
	}
}

func TestFormatAssignee_OtherUser(t *testing.T) {
	result := formatAssignee("bob", "alice")
	if result != " \033[2mbob\033[0m" {
		t.Errorf("formatAssignee(\"bob\", \"alice\") = %q, want dim bob label", result)
	}
}

func TestFormatAssignee_Empty(t *testing.T) {
	result := formatAssignee("", "alice")
	if result != "" {
		t.Errorf("formatAssignee(\"\", \"alice\") = %q, want empty string", result)
	}
}

func TestFilterByAssignee_HumanResolvesToCurrentUser(t *testing.T) {
	items := []*workitem.Item{
		{ID: "a1", Assignee: "human"},
		{ID: "a2", Assignee: "bob"},
		{ID: "a3", Assignee: ""},
	}
	// "human" should match items assigned to "human" literally
	filtered := filterByAssignee(items, "human")
	if len(filtered) != 1 || filtered[0].ID != "a1" {
		t.Errorf("filterByAssignee with 'human' should match item with assignee 'human', got %d items", len(filtered))
	}
}

func TestFilterByAssignee_EmptyReturnsAll(t *testing.T) {
	items := []*workitem.Item{
		{ID: "a1", Assignee: "alice"},
		{ID: "a2", Assignee: ""},
	}
	filtered := filterByAssignee(items, "")
	if len(filtered) != len(items) {
		t.Errorf("filterByAssignee with empty string should return all items, got %d", len(filtered))
	}
}
