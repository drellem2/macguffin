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

	// Complete Phase 1: claim then done — auto-promotes Phase 2
	_, err = Claim(root, phase1.ID, 0)
	if err != nil {
		t.Fatalf("Claim phase1: %v", err)
	}
	_, autoPromoted, err := Done(root, phase1.ID, nil)
	if err != nil {
		t.Fatalf("Done phase1: %v", err)
	}
	if len(autoPromoted) != 1 {
		t.Fatalf("expected 1 auto-promoted, got %d", len(autoPromoted))
	}
	if autoPromoted[0].ID != phase2.ID {
		t.Errorf("auto-promoted ID = %q, want %q", autoPromoted[0].ID, phase2.ID)
	}

	// Phase 2 should now be in available/
	status, err = Status(root, phase2.ID)
	if err != nil {
		t.Fatalf("Status phase2 after done: %v", err)
	}
	if status != "available" {
		t.Errorf("phase2 status = %q, want available", status)
	}

	// Schedule is now a no-op — already promoted
	promoted, err = Schedule(root)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if len(promoted) != 0 {
		t.Errorf("expected 0 promoted after auto-promote, got %d", len(promoted))
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

	// Complete only dep1 — should not promote (partial deps)
	Claim(root, dep1.ID, 0)
	_, autoPromoted, _ := Done(root, dep1.ID, nil)
	if len(autoPromoted) != 0 {
		t.Errorf("should not promote with partial deps met, got %d", len(autoPromoted))
	}

	// Complete dep2 — should auto-promote item
	Claim(root, dep2.ID, 0)
	_, autoPromoted, err = Done(root, dep2.ID, nil)
	if err != nil {
		t.Fatalf("Done dep2: %v", err)
	}
	if len(autoPromoted) != 1 {
		t.Fatalf("expected 1 auto-promoted, got %d", len(autoPromoted))
	}
	if autoPromoted[0].ID != item.ID {
		t.Errorf("promoted ID = %q, want %q", autoPromoted[0].ID, item.ID)
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

	Claim(root, dep.ID, 0)
	_, autoPromoted, _ := Done(root, dep.ID, nil)

	// Done auto-promotes
	if len(autoPromoted) != 1 {
		t.Fatalf("expected 1 auto-promoted, got %d", len(autoPromoted))
	}

	// Schedule is now a no-op
	promoted, _ := Schedule(root)
	if len(promoted) != 0 {
		t.Errorf("schedule after auto-promote: expected 0 promoted, got %d", len(promoted))
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

	// Complete Phase 1 — auto-promotes Phase 2
	_, err = Claim(root, phase1.ID, 0)
	if err != nil {
		t.Fatalf("Claim phase1: %v", err)
	}
	_, autoPromoted, err := Done(root, phase1.ID, nil)
	if err != nil {
		t.Fatalf("Done phase1: %v", err)
	}

	// Phase 2 should be auto-promoted
	if len(autoPromoted) != 1 || autoPromoted[0].ID != phase2.ID {
		t.Fatalf("expected Phase 2 auto-promoted, got %v", autoPromoted)
	}

	avail, _ = ListByStatus(root, "available")
	found = false
	for _, item := range avail {
		if item.ID == phase2.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("Phase 2 should be available after Phase 1 done")
	}
}

func TestSchedule_ArchivedDepSatisfied(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	// Create dep (no deps) and a child that depends on it
	dep, err := Create(root, "mg-", "task", "Dep", nil)
	if err != nil {
		t.Fatalf("Create dep: %v", err)
	}
	child, err := Create(root, "mg-", "task", "Child", []string{dep.ID})
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}

	// Complete the dep — auto-promotes child
	_, err = Claim(root, dep.ID, 0)
	if err != nil {
		t.Fatalf("Claim dep: %v", err)
	}
	_, autoPromoted, err := Done(root, dep.ID, nil)
	if err != nil {
		t.Fatalf("Done dep: %v", err)
	}
	if len(autoPromoted) != 1 || autoPromoted[0].ID != child.ID {
		t.Fatalf("expected child auto-promoted, got %v", autoPromoted)
	}

	// Verify child is available
	status, _ := Status(root, child.ID)
	if status != "available" {
		t.Errorf("child status = %q, want available", status)
	}
}

func TestSchedule_ArchivedDepSatisfied_ViaSchedule(t *testing.T) {
	// Test that Schedule() can promote when dep is only in archive/
	// (simulates a case where auto-promote didn't run, e.g., manual file moves)
	root := t.TempDir()
	setupDirs(t, root)

	dep, _ := Create(root, "mg-", "task", "Dep", nil)
	child, _ := Create(root, "mg-", "task", "Child", []string{dep.ID})

	// Manually move dep to archive (bypassing Done)
	archiveDir := filepath.Join(root, "work", "archive", "2026-03")
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		t.Fatalf("mkdir archive: %v", err)
	}
	src := filepath.Join(root, "work", "available", dep.ID+".md")
	dst := filepath.Join(archiveDir, dep.ID+".md")
	if err := os.Rename(src, dst); err != nil {
		t.Fatalf("archive rename: %v", err)
	}

	// Schedule should promote child since dep is in archive/
	promoted, err := Schedule(root)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if len(promoted) != 1 {
		t.Fatalf("expected 1 promoted, got %d", len(promoted))
	}
	if promoted[0].ID != child.ID {
		t.Errorf("promoted ID = %q, want %q", promoted[0].ID, child.ID)
	}
}

func TestSchedule_MixedDoneAndArchivedDeps(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	// Create two deps and a child depending on both
	dep1, err := Create(root, "mg-", "task", "Dep 1", nil)
	if err != nil {
		t.Fatalf("Create dep1: %v", err)
	}
	dep2, err := Create(root, "mg-", "task", "Dep 2", nil)
	if err != nil {
		t.Fatalf("Create dep2: %v", err)
	}
	child, err := Create(root, "mg-", "task", "Child", []string{dep1.ID, dep2.ID})
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}

	// Complete dep1 — child not yet promoted (still waiting on dep2)
	Claim(root, dep1.ID, 0)
	_, autoPromoted, _ := Done(root, dep1.ID, nil)
	if len(autoPromoted) != 0 {
		t.Errorf("should not promote with partial deps, got %d", len(autoPromoted))
	}

	// Complete dep2 — child auto-promoted
	Claim(root, dep2.ID, 0)
	_, autoPromoted, err = Done(root, dep2.ID, nil)
	if err != nil {
		t.Fatalf("Done dep2: %v", err)
	}
	if len(autoPromoted) != 1 {
		t.Fatalf("expected 1 auto-promoted, got %d", len(autoPromoted))
	}
	if autoPromoted[0].ID != child.ID {
		t.Errorf("promoted ID = %q, want %q", autoPromoted[0].ID, child.ID)
	}
}
