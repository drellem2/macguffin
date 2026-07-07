package workitem

import (
	"errors"
	"strings"
	"testing"

	"github.com/drellem2/macguffin/internal/mgerr"
)

// leakMarkers are substrings that would indicate a state-transition error has
// leaked maildir internals (a path, the rename op, a ".md" filename, or ENOENT).
var leakMarkers = []string{
	"rename",
	"no such file or directory",
	"work/available",
	"work/claimed",
	"work/done",
	"work/shelved",
	".md",
}

func assertNoLeak(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	for _, m := range leakMarkers {
		if strings.Contains(err.Error(), m) {
			t.Errorf("error leaks internal detail %q: %q", m, err.Error())
		}
	}
}

// TestTransitionErrorMessages exercises every state-transition diagnosis at the
// package level: each must name the semantic problem and leak nothing. See
// mg-c835.
func TestTransitionErrorMessages(t *testing.T) {
	t.Run("claim already-claimed reports PID and hint", func(t *testing.T) {
		root := t.TempDir()
		setupDirs(t, root)
		item, err := Create(root, "mg-", "task", "claimed once", nil)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := Claim(root, item.ID, 4242); err != nil {
			t.Fatalf("first Claim: %v", err)
		}
		_, err = Claim(root, item.ID, 0)
		assertNoLeak(t, err)
		// Message carries the problem; the remediation now lives in the typed
		// error's Hint field (rendered on its own line by the CLI), not
		// flattened into Error(). See mg-931f / mgerr.
		for _, want := range []string{item.ID, "already claimed", "by PID 4242"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("Claim error %q missing %q", err.Error(), want)
			}
		}
		var me *mgerr.Error
		if !errors.As(err, &me) {
			t.Fatalf("Claim error is not an *mgerr.Error: %T", err)
		}
		if me.Category != mgerr.CatConflict || me.Code != "already_claimed" {
			t.Errorf("category/code = %v/%q, want conflict/already_claimed", me.Category, me.Code)
		}
		if !strings.Contains(me.Hint, "mg unclaim") {
			t.Errorf("Hint %q missing %q", me.Hint, "mg unclaim")
		}
	})

	t.Run("claim non-existent", func(t *testing.T) {
		root := t.TempDir()
		setupDirs(t, root)
		_, err := Claim(root, "mg-zzzz", 0)
		assertNoLeak(t, err)
		if !strings.Contains(err.Error(), "no such work item") {
			t.Errorf("got %q", err.Error())
		}
	})

	t.Run("done on available item", func(t *testing.T) {
		root := t.TempDir()
		setupDirs(t, root)
		item, _ := Create(root, "mg-", "task", "available", nil)
		_, _, err := Done(root, item.ID, nil)
		assertNoLeak(t, err)
		if !strings.Contains(err.Error(), "not claimed") {
			t.Errorf("got %q", err.Error())
		}
	})

	t.Run("done on already-done item", func(t *testing.T) {
		root := t.TempDir()
		setupDirs(t, root)
		item, _ := Create(root, "mg-", "task", "to be done twice", nil)
		if _, err := Claim(root, item.ID, 0); err != nil {
			t.Fatalf("Claim: %v", err)
		}
		if _, _, err := Done(root, item.ID, nil); err != nil {
			t.Fatalf("Done: %v", err)
		}
		_, _, err := Done(root, item.ID, nil)
		assertNoLeak(t, err)
		if !strings.Contains(err.Error(), "already done") {
			t.Errorf("got %q", err.Error())
		}
	})

	t.Run("unclaim on available item", func(t *testing.T) {
		root := t.TempDir()
		setupDirs(t, root)
		item, _ := Create(root, "mg-", "task", "never claimed", nil)
		_, err := Unclaim(root, item.ID)
		assertNoLeak(t, err)
		if !strings.Contains(err.Error(), "not claimed") {
			t.Errorf("got %q", err.Error())
		}
	})

	t.Run("reopen on available item", func(t *testing.T) {
		root := t.TempDir()
		setupDirs(t, root)
		item, _ := Create(root, "mg-", "task", "not done", nil)
		_, err := Reopen(root, item.ID)
		assertNoLeak(t, err)
		if !strings.Contains(err.Error(), "not done") {
			t.Errorf("got %q", err.Error())
		}
	})

	t.Run("unshelve on available item", func(t *testing.T) {
		root := t.TempDir()
		setupDirs(t, root)
		item, _ := Create(root, "mg-", "task", "not shelved", nil)
		_, err := Unshelve(root, item.ID)
		assertNoLeak(t, err)
		if !strings.Contains(err.Error(), "not shelved") {
			t.Errorf("got %q", err.Error())
		}
	})
}
