package mgcli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// drellem2/macguffin#48: --since/--until on `mg event list` are parsed as times.

func TestParseTimeBound(t *testing.T) {
	loc := time.FixedZone("UTC-7", -7*3600)
	now := time.Date(2026, 11, 20, 15, 0, 0, 0, loc)

	cases := []struct {
		in   string
		want time.Time
	}{
		{"", time.Time{}},
		{"2026-11-21T00:00:00+05:00", time.Date(2026, 11, 20, 19, 0, 0, 0, time.UTC)},
		{"2026-11-20T19:00:00Z", time.Date(2026, 11, 20, 19, 0, 0, 0, time.UTC)},
		// A bare date is LOCAL midnight, in now's location — not UTC midnight.
		{"2026-11-19", time.Date(2026, 11, 19, 0, 0, 0, 0, loc)},
		{"24h", now.Add(-24 * time.Hour)},
		{"7d", now.Add(-7 * 24 * time.Hour)},
	}
	for _, c := range cases {
		got, err := parseTimeBound("--since", c.in, now)
		if err != nil {
			t.Errorf("parseTimeBound(%q): %v", c.in, err)
			continue
		}
		if !got.Equal(c.want) {
			t.Errorf("parseTimeBound(%q) = %s, want %s", c.in, got, c.want)
		}
	}

	for _, bad := range []string{"yesterday", "2026-13-01", "2026-11-21 00:00", "7x", "-24h", "-7d", "-30m", " -24h", "-0s"} {
		if _, err := parseTimeBound("--until", bad, now); err == nil {
			t.Errorf("parseTimeBound(%q) should be a usage error", bad)
		} else if !strings.Contains(err.Error(), "--until") {
			t.Errorf("error for %q should name the flag, got %v", bad, err)
		}
	}
}

func TestEventList_WindowTitleAndUsage(t *testing.T) {
	bin := buildBinary(t)
	root := filepath.Join(t.TempDir(), "store")
	if out, code := mgArchive(t, bin, root, "init"); code != 0 {
		t.Fatalf("mg init: exit %d\n%s", code, out)
	}
	out, code := mgArchive(t, bin, root, "new", "task", "original title")
	if code != 0 {
		t.Fatalf("mg new: exit %d\n%s", code, out)
	}
	_, rest, _ := strings.Cut(out, "Created ")
	id, _, _ := strings.Cut(rest, ":")
	if out, code := mgArchive(t, bin, root, "edit", id, "--title=current title"); code != 0 {
		t.Fatalf("mg edit: exit %d\n%s", code, out)
	}

	// Replace the log with fixed timestamps so the window is deterministic.
	lines := []string{
		`{"ts":"2026-11-20T18:59:59Z","type":"work.done","item_id":"` + id + `"}`,
		`{"ts":"2026-11-20T22:14:15Z","type":"work.done","item_id":"` + id + `"}`,
		`{"ts":"2026-11-20T22:30:00Z","type":"work.done","item_id":"mg-gone0"}`,
		`{"ts":"2026-11-20T23:00:00Z","type":"mail.read","mailbox":"mayor"}`,
		`{"ts":"2026-11-20T22:20:00Z","type":"work.note","item_id":"` + id + `","title":"title written at event time"}`,
	}
	if err := os.WriteFile(filepath.Join(root, "events.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code = mgArchive(t, bin, root, "event", "list",
		"--since=2026-11-21T00:00:00+05:00", "--until=2026-11-20T23:00:00Z", "--with-title")
	if code != 0 {
		t.Fatalf("mg event list: exit %d\n%s", code, out)
	}
	var got []map[string]string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		var m map[string]string
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("bad NDJSON line %q: %v", l, err)
		}
		got = append(got, m)
	}
	byTs := map[string]map[string]string{}
	for _, m := range got {
		byTs[m["ts"]] = m
	}
	if len(got) != 3 {
		t.Fatalf("want 3 events (22:14:15Z, 22:20Z and 22:30Z; 18:59:59Z before since, 23:00Z at exclusive until), got:\n%s", out)
	}
	if e := byTs["2026-11-20T22:14:15Z"]; e["title"] != "current title" {
		t.Errorf("22:14:15Z should carry the CURRENT title, got %v", e)
	}
	// An event that already carries a 'title' keeps it (mg-730e9).
	if e := byTs["2026-11-20T22:20:00Z"]; e["title"] != "title written at event time" {
		t.Errorf("--with-title must not overwrite an existing 'title' key, got %v", e)
	}
	if _, ok := byTs["2026-11-20T22:30:00Z"]["title"]; ok {
		t.Errorf("an item that cannot be found should get no title field, got %v", byTs["2026-11-20T22:30:00Z"])
	}

	// Without --with-title, no title field appears.
	out, _ = mgArchive(t, bin, root, "event", "list", "--since=2026-11-20T22:00:00Z", "--until=2026-11-20T22:19:00Z")
	if strings.Contains(out, `"title"`) {
		t.Errorf("title must only appear with --with-title, got:\n%s", out)
	}

	// An unparseable bound is a usage error (exit 2), not an empty result.
	out, code = mgArchive(t, bin, root, "event", "list", "--since=yesterday")
	if code != 2 {
		t.Errorf("--since=yesterday: want exit 2 (usage), got %d\n%s", code, out)
	}

	// A signed duration is a usage error on both flags, the same outcome for
	// -24h as for -7d (mg-730e9). -24h used to resolve to now+24h and print
	// nothing at exit 0. Positive control: the unsigned form still succeeds.
	for _, flag := range []string{"--since", "--until"} {
		for _, v := range []string{"-24h", "-7d"} {
			out, code = mgArchive(t, bin, root, "event", "list", flag+"="+v)
			if code != 2 {
				t.Errorf("%s=%s: want exit 2 (usage), got %d\n%s", flag, v, code, out)
			} else if !strings.Contains(out, "durations count back from now") {
				t.Errorf("%s=%s: error should explain the sign, got:\n%s", flag, v, out)
			}
		}
	}
	if out, code = mgArchive(t, bin, root, "event", "list", "--since=24h"); code != 0 {
		t.Errorf("--since=24h (positive control): want exit 0, got %d\n%s", code, out)
	}
}
