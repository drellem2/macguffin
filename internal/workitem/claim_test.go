package workitem

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestClaim(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "bug", "Fix the widget", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	claimed, err := Claim(root, item.ID)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	if claimed.ID != item.ID {
		t.Errorf("ID = %q, want %q", claimed.ID, item.ID)
	}
	if claimed.Title != item.Title {
		t.Errorf("Title = %q, want %q", claimed.Title, item.Title)
	}

	// File should be gone from available/
	avail := filepath.Join(root, "work", "available")
	entries, _ := os.ReadDir(avail)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), item.ID) {
			t.Errorf("item still in available/: %s", e.Name())
		}
	}

	// File should be in claimed/ with PID suffix
	claimedDir := filepath.Join(root, "work", "claimed")
	entries, _ = os.ReadDir(claimedDir)
	found := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), item.ID+".md.") {
			found = true
		}
	}
	if !found {
		t.Error("claimed file not found in claimed/")
	}
}

func TestClaimNotFound(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	_, err := Claim(root, "gt-000")
	if err == nil {
		t.Error("expected error for nonexistent ID")
	}
}

func TestClaimAlreadyClaimed(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "task", "Do the thing", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// First claim succeeds
	_, err = Claim(root, item.ID)
	if err != nil {
		t.Fatalf("first Claim: %v", err)
	}

	// Second claim fails (file already moved)
	_, err = Claim(root, item.ID)
	if err == nil {
		t.Error("expected error for already-claimed item")
	}
}

func TestClaimReadable(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "bug", "Readable after claim", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = Claim(root, item.ID)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	// Read should still find the item in claimed/
	found, err := Read(root, item.ID)
	if err != nil {
		t.Fatalf("Read after claim: %v", err)
	}
	if found.ID != item.ID {
		t.Errorf("Read ID = %q, want %q", found.ID, item.ID)
	}
}

// TestClaimConcurrent verifies exactly-once semantics: 10 goroutines race to
// claim the same work item. Exactly 1 must succeed; the other 9 must fail.
func TestClaimConcurrent(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "task", "Race condition target", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	const n = 10
	var (
		wins  atomic.Int32
		wg    sync.WaitGroup
		start = make(chan struct{})
	)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // synchronize all goroutines

			// Each goroutine needs a unique destination to simulate separate PIDs.
			// We can't use Claim directly because all goroutines share the same PID.
			src := filepath.Join(root, "work", "available", item.ID+".md")
			dst := filepath.Join(root, "work", "claimed", fmt.Sprintf("%s.md.%d", item.ID, 90000+i))

			if err := os.Rename(src, dst); err == nil {
				wins.Add(1)
			}
		}(i)
	}

	close(start) // fire!
	wg.Wait()

	if got := wins.Load(); got != 1 {
		t.Errorf("expected exactly 1 winner, got %d", got)
	}

	// Verify: available/ is empty, claimed/ has exactly 1 file
	avail, _ := os.ReadDir(filepath.Join(root, "work", "available"))
	if len(avail) != 0 {
		t.Errorf("expected 0 files in available/, got %d", len(avail))
	}

	claimed, _ := os.ReadDir(filepath.Join(root, "work", "claimed"))
	count := 0
	for _, e := range claimed {
		if strings.HasPrefix(e.Name(), item.ID) {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 file in claimed/, got %d", count)
	}
}
