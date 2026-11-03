package mgcli

import (
	"strings"
	"testing"

	"github.com/drellem2/macguffin/internal/event"
	"github.com/drellem2/macguffin/store"
	"github.com/drellem2/macguffin/store/fsstore"
	"github.com/spf13/cobra"
)

// noPromoteBackend is the filesystem backend with the per-command promoter
// switched off, the shape a shared-store backend declares.
type noPromoteBackend struct{ store.Backend }

func (noPromoteBackend) Capabilities() store.Capabilities {
	return store.Capabilities{AutoPromote: false}
}

// recordingLog captures what mg appends, proving events reach the backend's
// hook rather than a file mg opened behind its back.
type recordingLog struct{ lines *[]string }

func (l recordingLog) AppendEvent(b []byte) error  { *l.lines = append(*l.lines, string(b)); return nil }
func (l recordingLog) ReadEvents() ([]byte, error) { return []byte(strings.Join(*l.lines, "")), nil }
func (l recordingLog) StatEvents() (store.EventsInfo, error) {
	return store.EventsInfo{Exists: len(*l.lines) > 0, Where: "memory"}, nil
}

type eventsBackend struct {
	store.Backend
	log recordingLog
}

func (b eventsBackend) Events() store.EventLog { return b.log }

func withRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	old := rootFlag
	rootFlag = root
	t.Cleanup(func() { rootFlag = old })
	return root
}

func TestAutoPromoteFollowsBackendCapability(t *testing.T) {
	withRoot(t)
	t.Setenv(envNoAutoPromote, "")
	defer useBackend(fsstore.Open)
	cmd := &cobra.Command{Use: "list"}

	// Control: the filesystem backend keeps the promoter on.
	useBackend(fsstore.Open)
	startAutoPromote(cmd)
	if promoteCh == nil {
		t.Fatal("control: the filesystem backend should start the per-command promoter")
	}
	awaitAutoPromote()

	useBackend(func(root string) (store.Backend, error) {
		b, err := fsstore.Open(root)
		return noPromoteBackend{b}, err
	})
	startAutoPromote(cmd)
	if promoteCh != nil {
		awaitAutoPromote()
		t.Fatal("a backend with AutoPromote=false still got a per-command promoter")
	}
}

func TestEventsGoThroughBackendHook(t *testing.T) {
	root := withRoot(t)
	defer func() { useBackend(fsstore.Open); event.SetLogOpener(nil) }()
	var lines []string
	useBackend(func(r string) (store.Backend, error) {
		b, err := fsstore.Open(r)
		return eventsBackend{b, recordingLog{&lines}}, err
	})
	event.Emit(root, "test.hook", map[string]string{"k": "v"})
	if len(lines) != 1 || !strings.Contains(lines[0], `"type":"test.hook"`) {
		t.Fatalf("backend log saw %q", lines)
	}
	entries, err := event.List(root, event.ListOpts{})
	if err != nil || len(entries) != 1 || entries[0].Type != "test.hook" {
		t.Fatalf("event.List through the hook = %v, %v", entries, err)
	}
	if info, _ := event.Stat(root); info.Where != "memory" {
		t.Fatalf("event.Stat did not go through the hook: %+v", info)
	}
}
