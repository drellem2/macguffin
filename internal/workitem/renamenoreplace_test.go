package workitem

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drellem2/macguffin/internal/mgerr"
)

// twinBody is a record that shares an id with the item under test but is a
// different record — the shape of the legacy pre-2026-07-10 same-id twins that
// mg-38b9 deliberately did not renumber.
const twinBody = "---\nid: %s\ntitle: the OTHER record with this id\ntype: task\n---\n\nThis body must survive.\n"

func readBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return b
}

// assertDestinationRefusal checks err is the destination_exists conflict and
// that its message names both paths.
func assertDestinationRefusal(t *testing.T, err error, src, dst string) {
	t.Helper()
	if err == nil {
		t.Fatal("move over an existing same-id record succeeded; want a refusal")
	}
	var me *mgerr.Error
	if !errors.As(err, &me) || me.Code != "destination_exists" || me.Category != mgerr.CatConflict {
		t.Fatalf("err = %#v, want conflict/destination_exists", err)
	}
	if !strings.Contains(me.Message, src) || !strings.Contains(me.Message, dst) {
		t.Errorf("refusal %q does not name both %s and %s", me.Message, src, dst)
	}
}

// TestUnarchiveRefusesToReplaceSameIDTwin is the mg-1096 repro: unarchiving an
// item into a status directory that already holds a different record with the
// same id used to exit 0 and leave one file — the live record destroyed by
// rename(2). It must refuse, and both files must survive byte-identical.
// withTwin=false is the positive control: the identical move succeeds.
func TestUnarchiveRefusesToReplaceSameIDTwin(t *testing.T) {
	for _, withTwin := range []bool{true, false} {
		name := "twin"
		if !withTwin {
			name = "control-no-twin"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			setupDirs(t, root)

			item, partition := archiveItem(t, root, "archived half of a twin pair", nil)
			src := filepath.Join(root, "work", "archive", partition, item.ID+".md")
			dst := filepath.Join(root, "work", "shelved", item.ID+".md")
			srcBefore := readBytes(t, src)

			var dstBefore []byte
			if withTwin {
				dstBefore = []byte(strings.ReplaceAll(twinBody, "%s", item.ID))
				if err := os.WriteFile(dst, dstBefore, 0o644); err != nil {
					t.Fatal(err)
				}
			}

			_, _, err := Unarchive(root, item.ID+"@"+partition, "shelved")

			if !withTwin {
				if err != nil {
					t.Fatalf("control: Unarchive with no twin: %v", err)
				}
				if !bytes.Equal(readBytes(t, dst), srcBefore) {
					t.Error("control: restored record differs from the archived one")
				}
				if _, err := os.Stat(src); !os.IsNotExist(err) {
					t.Error("control: archived copy still present after unarchive")
				}
				return
			}

			assertDestinationRefusal(t, err, src, dst)
			if !bytes.Equal(readBytes(t, src), srcBefore) {
				t.Error("archived record changed after refusal")
			}
			if !bytes.Equal(readBytes(t, dst), dstBefore) {
				t.Error("live twin changed after refusal — its body was destroyed")
			}
		})
	}
}

// TestArchiveRefusesToReplaceSameIDTwin is the other direction: archiveFile
// moving a done item into a partition that already holds an archived record
// with the same id. withTwin=false is the positive control.
func TestArchiveRefusesToReplaceSameIDTwin(t *testing.T) {
	for _, withTwin := range []bool{true, false} {
		name := "twin"
		if !withTwin {
			name = "control-no-twin"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			setupDirs(t, root)

			item := doneItem(t, root, "live half of a twin pair")
			src := filepath.Join(root, "work", "done", item.ID+".md")
			old := time.Now().Add(-10 * 24 * time.Hour)
			if err := os.Chtimes(src, old, old); err != nil {
				t.Fatal(err)
			}
			partDir := filepath.Join(root, "work", "archive", old.Format("2006-01"))
			dst := filepath.Join(partDir, item.ID+".md")
			srcBefore := readBytes(t, src)

			var dstBefore []byte
			if withTwin {
				if err := os.MkdirAll(partDir, 0o755); err != nil {
					t.Fatal(err)
				}
				dstBefore = []byte(strings.ReplaceAll(twinBody, "%s", item.ID))
				if err := os.WriteFile(dst, dstBefore, 0o644); err != nil {
					t.Fatal(err)
				}
			}

			_, _, err := Archive(root, 7*24*time.Hour)

			if !withTwin {
				if err != nil {
					t.Fatalf("control: Archive with no twin: %v", err)
				}
				if !bytes.Equal(readBytes(t, dst), srcBefore) {
					t.Error("control: archived record differs from the done one")
				}
				if _, err := os.Stat(src); !os.IsNotExist(err) {
					t.Error("control: done copy still present after archive")
				}
				return
			}

			assertDestinationRefusal(t, err, src, dst)
			if !bytes.Equal(readBytes(t, src), srcBefore) {
				t.Error("done record changed after refusal")
			}
			if !bytes.Equal(readBytes(t, dst), dstBefore) {
				t.Error("archived twin changed after refusal — its body was destroyed")
			}
		})
	}
}
