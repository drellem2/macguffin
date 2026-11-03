package spend

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// LockPath returns the harvest lock file. It lives beside the spend/ tree,
// not inside it, so `mg spend --rebuild` can remove the whole store while
// still holding the lock (drellem2/macguffin#30).
func LockPath(root string) string {
	return filepath.Join(root, "spend.lock")
}

// errLockHeld reports that another process (or another goroutine with its own
// descriptor) holds the harvest lock and the caller asked not to wait.
var errLockHeld = errors.New("spend harvest lock held")

// acquireHarvestLock takes an exclusive flock(2) on LockPath(root). With
// block=false it returns errLockHeld immediately on contention instead of
// waiting. The returned release func closes the descriptor, which drops the
// lock; the kernel also drops it if the process dies, so a crashed harvest
// can never wedge the next one.
//
// flock locks belong to the open file description, so two descriptors opened
// in the same process contend exactly as two processes do — which is what
// lets the in-process concurrency test exercise the real lock.
func acquireHarvestLock(root string, block bool) (func(), error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("creating %s: %w", root, err)
	}
	path := LockPath(root)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	how := syscall.LOCK_EX
	if !block {
		how |= syscall.LOCK_NB
	}
	for {
		err = syscall.Flock(int(f.Fd()), how)
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errLockHeld
		}
		return nil, fmt.Errorf("locking %s: %w", path, err)
	}
	return func() { f.Close() }, nil
}
