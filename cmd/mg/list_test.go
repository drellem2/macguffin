package main

import (
	"testing"

	"github.com/drellem2/macguffin/internal/workitem"
)

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
