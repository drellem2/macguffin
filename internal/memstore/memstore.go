// Package memstore is an in-memory store.Backend: a second backend that lives
// nowhere near the filesystem, used to PROVE that mg's commands reach storage
// through store.Backend and nothing else.
//
// A command that went around the seam — an os.ReadDir of work/available, an
// os.WriteFile of an item path — finds nothing here: the workspace root a test
// hands mg is an empty directory, and every item lives in a Go map. So a
// lifecycle that completes against this backend (mgcli's memstore test runs
// new -> claim -> done -> archive through mgcli.Run) is a lifecycle that only
// ever spoke store.Backend. It passes storetest, so it is held to the same
// contract as the filesystem backend.
//
// It deliberately implements NONE of store's optional extensions (Twins,
// IDLister, Unarchiver, Local, Initializer), so the paths mg takes for a
// backend without them are exercised too. Its claim holder is not a PID, for
// the same reason: mg must treat Holder as opaque.
//
// Its companions (result sidecars, saved bodies, the sweep stamp) are Go maps
// keyed by item id, so a result "travels" with its item by construction, and a
// command that still wrote a companion FILE would leave it in the workspace
// root the test requires to stay empty.
package memstore

import (
	"bytes"
	"fmt"
	"sync"
	"time"

	"github.com/drellem2/macguffin/store"
)

// Mem is the in-memory backend. The zero value is not usable; call New.
type Mem struct {
	mu     sync.Mutex
	items  map[string]*record
	events bytes.Buffer
	// Calls counts each Backend method invoked, by name. A test reads it to
	// show the backend was actually reached.
	calls map[string]int
	// AutoPromote is reported through Capabilities.
	AutoPromote bool

	results map[string][]byte
	bodies  map[string]map[string][]byte
	stamp   []byte
}

type record struct {
	state     store.State
	partition string
	content   []byte
	mtime     time.Time
}

var _ store.Backend = (*Mem)(nil)

// New returns an empty in-memory store.
func New() *Mem {
	return &Mem{
		items: map[string]*record{}, calls: map[string]int{}, AutoPromote: true,
		results: map[string][]byte{}, bodies: map[string]map[string][]byte{},
	}
}

// Calls returns how many times each Backend method has been invoked.
func (m *Mem) Calls() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]int, len(m.calls))
	for k, v := range m.calls {
		out[k] = v
	}
	return out
}

// Len is how many records the store holds, archive included.
func (m *Mem) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.items)
}

// holderFor derives a claim holder from the actor: deliberately NOT a bare
// PID, so a caller that parses the holder as one is caught.
func holderFor(st store.State, by store.Actor) store.State {
	if st.Status == store.Claimed && st.Holder == "" && by.PID != 0 {
		st.Holder = fmt.Sprintf("%s@%s/%d/%s", by.Name, by.Host, by.PID, by.Nonce)
	}
	return st
}

func (m *Mem) doc(id string, r *record) store.Doc {
	c := append([]byte(nil), r.content...)
	return store.Doc{
		ID:        id,
		State:     r.state,
		Partition: r.partition,
		Content:   c,
		Hash:      store.HashOf(c),
		Mtime:     r.mtime,
	}
}

func (m *Mem) lost(id string, expect store.State) error {
	e := &store.ErrLost{ID: id, Expect: expect}
	if r, ok := m.items[id]; ok {
		cur := r.state
		e.Current = &cur
	}
	return e
}

// Create implements store.Backend.
func (m *Mem) Create(id string, st store.State, content []byte, by store.Actor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls["Create"]++
	if st.Status == store.Archived {
		return fmt.Errorf("memstore: %s: items are archived with Archive", id)
	}
	if _, ok := m.items[id]; ok {
		return fmt.Errorf("%s: %w", id, store.ErrExists)
	}
	m.items[id] = &record{state: holderFor(st, by), content: append([]byte(nil), content...), mtime: time.Now()}
	return nil
}

// Get implements store.Backend.
func (m *Mem) Get(id string) (*store.Doc, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls["Get"]++
	r, ok := m.items[id]
	if !ok {
		return nil, fmt.Errorf("%s: %w", id, store.ErrNotFound)
	}
	d := m.doc(id, r)
	return &d, nil
}

// List implements store.Backend.
func (m *Mem) List(status store.Status) ([]store.Doc, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls["List"]++
	var out []store.Doc
	for id, r := range m.items {
		if r.state.Status == status {
			out = append(out, m.doc(id, r))
		}
	}
	return out, nil
}

// Transition implements store.Backend: a compare-and-swap under the mutex.
func (m *Mem) Transition(id string, expect, to store.State, by store.Actor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls["Transition"]++
	if expect.Status == store.Archived || to.Status == store.Archived {
		return fmt.Errorf("memstore: %s: archive moves go through Archive", id)
	}
	r, ok := m.items[id]
	if !ok || !r.state.Satisfies(expect) {
		return m.lost(id, expect)
	}
	r.state = holderFor(to, by)
	r.mtime = time.Now()
	return nil
}

// Edit implements store.Backend. State is never touched.
func (m *Mem) Edit(id string, ifUnchanged store.Hash, content []byte, _ store.Actor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls["Edit"]++
	r, ok := m.items[id]
	if !ok {
		return fmt.Errorf("%s: %w", id, store.ErrNotFound)
	}
	if ifUnchanged != "" {
		if cur := store.HashOf(r.content); cur != ifUnchanged {
			return &store.ErrChanged{ID: id, Want: ifUnchanged, Current: cur}
		}
	}
	r.content = append([]byte(nil), content...)
	r.mtime = time.Now()
	return nil
}

// Archive implements store.Backend.
func (m *Mem) Archive(id string, partition string, _ store.Actor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls["Archive"]++
	expect := store.State{Status: store.Done}
	r, ok := m.items[id]
	if !ok || !r.state.Satisfies(expect) {
		return m.lost(id, expect)
	}
	r.state = store.State{Status: store.Archived}
	r.partition = partition
	return nil
}

// Events implements store.Backend: an in-memory log.
func (m *Mem) Events() store.EventLog { return memLog{m} }

// Capabilities implements store.Backend.
func (m *Mem) Capabilities() store.Capabilities {
	return store.Capabilities{AutoPromote: m.AutoPromote}
}

type memLog struct{ m *Mem }

func (l memLog) AppendEvent(line []byte) error {
	l.m.mu.Lock()
	defer l.m.mu.Unlock()
	l.m.calls["AppendEvent"]++
	l.m.events.Write(line)
	return nil
}

func (l memLog) ReadEvents() ([]byte, error) {
	l.m.mu.Lock()
	defer l.m.mu.Unlock()
	if l.m.events.Len() == 0 {
		return nil, nil
	}
	return append([]byte(nil), l.m.events.Bytes()...), nil
}

func (l memLog) StatEvents() (store.EventsInfo, error) {
	l.m.mu.Lock()
	defer l.m.mu.Unlock()
	return store.EventsInfo{Exists: l.m.events.Len() > 0, Size: int64(l.m.events.Len()), Where: "memstore"}, nil
}

// Companions implements store.Backend.
func (m *Mem) Companions() store.Companions { return memCompanions{m} }

type memCompanions struct{ m *Mem }

var _ store.Companions = memCompanions{}

func (c memCompanions) lock(op string) func() {
	c.m.mu.Lock()
	c.m.calls[op]++
	return c.m.mu.Unlock
}

func clone(b []byte) []byte { return append([]byte(nil), b...) }

func (c memCompanions) ReadResult(ref store.Ref) ([]byte, error) {
	defer c.lock("ReadResult")()
	r, ok := c.m.results[ref.ID]
	if !ok {
		return nil, fmt.Errorf("%s result: %w", ref.ID, store.ErrNotFound)
	}
	return clone(r), nil
}

func (c memCompanions) WriteResult(ref store.Ref, data []byte) error {
	defer c.lock("WriteResult")()
	if _, ok := c.m.items[ref.ID]; !ok {
		return fmt.Errorf("%s: %w", ref.ID, store.ErrNotFound)
	}
	c.m.results[ref.ID] = clone(data)
	return nil
}

func (c memCompanions) ResultWhere(ref store.Ref) string { return "memstore:" + ref.ID + "/result" }

// Results reports each result beside its item: keyed by id, one cannot stray.
func (c memCompanions) Results() ([]store.Ref, error) {
	defer c.lock("Results")()
	var out []store.Ref
	for id := range c.m.results {
		ref := store.Ref{ID: id}
		if r, ok := c.m.items[id]; ok {
			ref.Status, ref.Partition = r.state.Status, r.partition
		}
		out = append(out, ref)
	}
	return out, nil
}

func (c memCompanions) SaveBody(ref store.Ref, name string, body []byte) error {
	defer c.lock("SaveBody")()
	if c.m.bodies[ref.ID] == nil {
		c.m.bodies[ref.ID] = map[string][]byte{}
	}
	c.m.bodies[ref.ID][name] = clone(body)
	return nil
}

func (c memCompanions) Bodies(ref store.Ref) ([]string, error) {
	defer c.lock("Bodies")()
	var out []string
	for name := range c.m.bodies[ref.ID] {
		out = append(out, name)
	}
	return out, nil
}

func (c memCompanions) ReadBody(ref store.Ref, name string) ([]byte, error) {
	defer c.lock("ReadBody")()
	b, ok := c.m.bodies[ref.ID][name]
	if !ok {
		return nil, fmt.Errorf("%s body %s: %w", ref.ID, name, store.ErrNotFound)
	}
	return clone(b), nil
}

func (c memCompanions) RemoveBody(ref store.Ref, name string) error {
	defer c.lock("RemoveBody")()
	delete(c.m.bodies[ref.ID], name)
	return nil
}

// MoveBodies: bodies are keyed by id, so they are already wherever the item is.
func (c memCompanions) MoveBodies(from, to store.Ref) error {
	defer c.lock("MoveBodies")()
	return nil
}

func (c memCompanions) BodiesWhere(ref store.Ref) string { return "memstore:" + ref.ID + "/bodies" }

func (c memCompanions) ReadSweepStamp() ([]byte, error) {
	defer c.lock("ReadSweepStamp")()
	if c.m.stamp == nil {
		return nil, fmt.Errorf("sweep stamp: %w", store.ErrNotFound)
	}
	return clone(c.m.stamp), nil
}

func (c memCompanions) WriteSweepStamp(data []byte) error {
	defer c.lock("WriteSweepStamp")()
	c.m.stamp = clone(data)
	return nil
}
