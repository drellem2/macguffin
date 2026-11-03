package mgcli

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/drellem2/macguffin/internal/workitem"

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

	// internal/workitem reaches every item through the same cached backend:
	// one instance per root per process, shared by the event log and the
	// lifecycle, so a backend with in-process state (an in-memory store, a
	// connection) is one store, not two.
	workitem.SetOpener(backendFor)
	workitem.SetActor(cliActor)

	event.SetLogOpener(func(root string) store.EventLog {
		b, err := backendFor(root)
		if err != nil {
			return failedLog{err}
		}
		return b.Events()
	})
}

// processNonce is unique to this process's run of mg: on a shared store every
// agent may authenticate as one user, and two claims by "the same" user need
// something per attempt to be told apart (store.Actor).
var processNonce = func() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}()

// cliActor is the store.Actor for this invocation's writes, built here at the
// CLI edge: the attributed name the audit log uses (MG_ACTOR /
// POGO_AGENT_NAME / OS user), this host, this process, this run's nonce. A
// command acting for another process (`mg claim --pid`) overrides the PID by
// passing it down explicitly.
func cliActor() store.Actor {
	host, _ := os.Hostname()
	return store.Actor{Name: workitem.Actor(), Host: host, PID: os.Getpid(), Nonce: processNonce}
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
