package workitem

import (
	"os"
	"path/filepath"
	"time"

	"github.com/drellem2/macguffin/store/fsstore"
)

// The companion files moved behind store.Companions (mg-7153e). These are the
// filesystem-shaped names the existing tests were written against, kept here so
// those tests run unmodified and keep exercising the production logic: each
// one routes into the same code the backend path uses.

// liveBodyBackupParent is work/.bodybak/ on the filesystem backend.
func liveBodyBackupParent(root string) string { return fsstore.BodyBackupParent(root) }

// resultSidecarName is the filesystem backend's sidecar file name.
func resultSidecarName(id string) string { return fsstore.ResultSidecarName(id) }

// saveBodyBackup saves body into an arbitrary directory through the production
// naming and pruning (saveBody), on a shelf that is that directory.
func saveBodyBackup(dir, body string, now time.Time) (string, error) {
	return saveBody(dirShelf(dir), body, now)
}

// compareSidecarFiles classifies two sidecar files by content.
func compareSidecarFiles(strayPath, authPath string) SidecarComparison {
	stray, err := os.ReadFile(strayPath)
	if err != nil {
		return SidecarComparison{Relation: RelationUnknown, Note: "stray is unreadable: " + err.Error()}
	}
	auth, err := os.ReadFile(authPath)
	if err != nil {
		return SidecarComparison{Relation: RelationUnknown, Note: "authoritative copy is unreadable: " + err.Error()}
	}
	return compareSidecarBytes(stray, auth)
}

// dirShelf is a bodyShelf that is a plain directory.
type dirShelf string

func (d dirShelf) names() ([]string, error) {
	entries, err := os.ReadDir(string(d))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

func (d dirShelf) put(name string, b []byte) error {
	if err := os.MkdirAll(string(d), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(string(d), name), b, 0o644)
}

func (d dirShelf) get(name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(string(d), name))
}

func (d dirShelf) remove(name string) error {
	if err := os.Remove(filepath.Join(string(d), name)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (d dirShelf) where() string { return string(d) }
