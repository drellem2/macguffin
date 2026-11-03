package mgcli

import (
	"sync"

	"github.com/drellem2/macguffin/internal/event"
	"github.com/drellem2/macguffin/store"
	"github.com/drellem2/macguffin/store/fsstore"
)

// The storage backend this build of mg runs against, as handed to Main.
//
// It is opened per workspace root, lazily, because the root is not known
// until a command's flags are bound (--root, MG_ROOT), and one process can
// touch more than one root (the mail commands' events land beside the mail
// root). Opened backends are cached so a backend with real setup cost pays it
// once per root per process.
var (
	backendMu    sync.Mutex
	backendOpen  store.Opener = fsstore.Open
	backendCache              = map[string]backendEntry{}
)

type backendEntry struct {
	b   store.Backend
	err error
}

// useBackend installs open as this process's backend and routes mg's event
// log through it. nil keeps the filesystem default.
func useBackend(open store.Opener) {
	backendMu.Lock()
	if open != nil {
		backendOpen = open
	}
	backendCache = map[string]backendEntry{}
	backendMu.Unlock()

	event.SetLogOpener(func(root string) store.EventLog {
		b, err := backendFor(root)
		if err != nil {
			return failedLog{err}
		}
		return b.Events()
	})
}

// backendFor returns the backend for root, opening it on first use.
func backendFor(root string) (store.Backend, error) {
	backendMu.Lock()
	defer backendMu.Unlock()
	if e, ok := backendCache[root]; ok {
		return e.b, e.err
	}
	b, err := backendOpen(root)
	backendCache[root] = backendEntry{b, err}
	return b, err
}

// failedLog is the event log of a backend that could not be opened: every
// operation reports the open error. Emit already treats a failed append as a
// warning, so a broken backend degrades the audit trail loudly, never
// silently — and never by falling back to a local file the backend's other
// readers would not see.
type failedLog struct{ err error }

func (l failedLog) AppendEvent([]byte) error              { return l.err }
func (l failedLog) ReadEvents() ([]byte, error)           { return nil, l.err }
func (l failedLog) StatEvents() (store.EventsInfo, error) { return store.EventsInfo{}, l.err }
