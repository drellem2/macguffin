package workitem

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSchedule_PromotesWhenDepsMet(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	// Create Phase 1 (no deps) — lands in available/
	phase1, err := Create(root, "mg-", "task", "Phase 1", nil)
	if err != nil {
		t.Fatalf("Create phase1: %v", err)
	}

	// Create Phase 2 (depends on Phase 1) — lands in pending/
	phase2, err := Create(root, "mg-", "task", "Phase 2", []string{phase1.ID})
	if err != nil {
		t.Fatalf("Create phase2: %v", err)
	}

	// Phase 2 should be in pending/, not available/
	status, err := Status(root, phase2.ID)
	if err != nil {
		t.Fatalf("Status phase2: %v", err)
	}
	if status != "pending" {
		t.Errorf("phase2 status = %q, want pending", status)
	}

	// Schedule — Phase 1 not done yet, so Phase 2 stays pending
	promoted, err := Schedule(root)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if len(promoted) != 0 {
		t.Errorf("expected 0 promoted, got %d", len(promoted))
	}

	// Complete Phase 1: claim then done
	_, err = Claim(root, phase1.ID)
	if err != nil {
		t.Fatalf("Claim phase1: %v", err)
	}
	_, err = Done(root, phase1.ID, nil)
	if err != nil {
		t.Fatalf("Done phase1: %v", err)
	}

	// Schedule again — now Phase 2 should be promoted
	promoted, err = Schedule(root)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if len(promoted) != 1 {
		t.Fatalf("expected 1 promoted, got %d", len(promoted))
	}
	if promoted[0].ID != phase2.ID {
		t.Errorf("promoted ID = %q, want %q", promoted[0].ID, phase2.ID)
	}

	// Phase 2 should now be in available/
	status, err = Status(root, phase2.ID)
	if err != nil {
		t.Fatalf("Status phase2 after schedule: %v", err)
	}
	if status != "available" {
		t.Errorf("phase2 status = %q, want available", status)
	}
}

func TestSchedule_MultipleDeps(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	dep1, err := Create(root, "mg-", "task", "Dep A", nil)
	if err != nil {
		t.Fatalf("Create dep1: %v", err)
	}
	dep2, err := Create(root, "mg-", "task", "Dep B", nil)
	if err != nil {
		t.Fatalf("Create dep2: %v", err)
	}

	// Item depends on both
	item, err := Create(root, "mg-", "task", "Depends on both", []string{dep1.ID, dep2.ID})
	if err != nil {
		t.Fatalf("Create item: %v", err)
	}

	// Complete only dep1
	Claim(root, dep1.ID)
	Done(root, dep1.ID, nil)

	promoted, _ := Schedule(root)
	if len(promoted) != 0 {
		t.Errorf("should not promote with partial deps met, got %d", len(promoted))
	}

	// Complete dep2
	Claim(root, dep2.ID)
	Done(root, dep2.ID, nil)

	promoted, err = Schedule(root)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if len(promoted) != 1 {
		t.Fatalf("expected 1 promoted, got %d", len(promoted))
	}
	if promoted[0].ID != item.ID {
		t.Errorf("promoted ID = %q, want %q", promoted[0].ID, item.ID)
	}
}

func TestSchedule_NoPending(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	promoted, err := Schedule(root)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if len(promoted) != 0 {
		t.Errorf("expected 0 promoted with empty pending, got %d", len(promoted))
	}
}

func TestSchedule_Idempotent(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	dep, _ := Create(root, "mg-", "task", "Dep", nil)
	Create(root, "mg-", "task", "Child", []string{dep.ID})

	Claim(root, dep.ID)
	Done(root, dep.ID, nil)

	// First schedule promotes
	promoted, _ := Schedule(root)
	if len(promoted) != 1 {
		t.Fatalf("first schedule: expected 1 promoted, got %d", len(promoted))
	}

	// Second schedule: nothing left to promote
	promoted, _ = Schedule(root)
	if len(promoted) != 0 {
		t.Errorf("second schedule: expected 0 promoted, got %d", len(promoted))
	}
}

func TestCreate_WithDeps_GoesToPending(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Has deps", []string{"gt-abc"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Should be in pending/
	pendingDir := filepath.Join(root, "work", "pending")
	entries, _ := os.ReadDir(pendingDir)
	found := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), item.ID) {
			found = true
		}
	}
	if !found {
		t.Error("item with deps should be in pending/")
	}

	// Should NOT be in available/
	availDir := filepath.Join(root, "work", "available")
	entries, _ = os.ReadDir(availDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), item.ID) {
			t.Error("item with deps should NOT be in available/")
		}
	}
}

func TestCreate_NoDeps_GoesToAvailable(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "No deps", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Should be in available/
	availDir := filepath.Join(root, "work", "available")
	entries, _ := os.ReadDir(availDir)
	found := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), item.ID) {
			found = true
		}
	}
	if !found {
		t.Error("item without deps should be in available/")
	}
}

func TestParse_Depends(t *testing.T) {
	content := `---
id: gt-abc
type: task
created: 2026-03-20T16:00:00Z
creator: alice
depends: [gt-111, gt-222]
---

# Has deps
`

	item, err := Parse(content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(item.Depends) != 2 {
		t.Fatalf("Depends len = %d, want 2", len(item.Depends))
	}
	if item.Depends[0] != "gt-111" {
		t.Errorf("Depends[0] = %q, want %q", item.Depends[0], "gt-111")
	}
	if item.Depends[1] != "gt-222" {
		t.Errorf("Depends[1] = %q, want %q", item.Depends[1], "gt-222")
	}
}

func TestParse_EmptyDepends(t *testing.T) {
	content := `---
id: gt-abc
type: task
created: 2026-03-20T16:00:00Z
creator: alice
depends: []
---

# No deps
`

	item, err := Parse(content)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(item.Depends) != 0 {
		t.Errorf("Depends len = %d, want 0", len(item.Depends))
	}
}

func TestSchedule_E2E(t *testing.T) {
	// Full E2E test matching the milestone spec
	root := t.TempDir()
	setupDirs(t, root)

	// Create Phase 1 — lands in available/
	phase1, err := Create(root, "mg-", "task", "Phase 1", nil)
	if err != nil {
		t.Fatalf("Create phase1: %v", err)
	}

	// Create Phase 2 depending on Phase 1
	phase2, err := Create(root, "mg-", "task", "Phase 2", []string{phase1.ID})
	if err != nil {
		t.Fatalf("Create phase2: %v", err)
	}

	// Phase 2 should NOT be in available/
	avail, _ := ListByStatus(root, "available")
	for _, item := range avail {
		if item.ID == phase2.ID {
			t.Fatal("Phase 2 should not be available before Phase 1 is done")
		}
	}

	// Phase 2 should be in pending/
	pending, _ := ListByStatus(root, "pending")
	found := false
	for _, item := range pending {
		if item.ID == phase2.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("Phase 2 should be in pending/")
	}

	// Complete Phase 1
	_, err = Claim(root, phase1.ID)
	if err != nil {
		t.Fatalf("Claim phase1: %v", err)
	}
	_, err = Done(root, phase1.ID, nil)
	if err != nil {
		t.Fatalf("Done phase1: %v", err)
	}

	// Run scheduler
	promoted, err := Schedule(root)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	// Phase 2 should now be available
	if len(promoted) != 1 || promoted[0].ID != phase2.ID {
		t.Fatalf("expected Phase 2 promoted, got %v", promoted)
	}

	avail, _ = ListByStatus(root, "available")
	found = false
	for _, item := range avail {
		if item.ID == phase2.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("Phase 2 should be available after Phase 1 done + schedule")
	}
}
