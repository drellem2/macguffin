package spend

import (
	"bytes"
	"path/filepath"
	"testing"
)

const sampleAssistant = `{"type":"user","message":{"role":"user","content":"hi"},"uuid":"u-1","sessionId":"sess-A","timestamp":"2026-04-26T12:00:00Z"}
{"type":"assistant","uuid":"a-1","sessionId":"sess-A","timestamp":"2026-04-26T12:00:01Z","message":{"model":"claude-opus-4-7","usage":{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":30,"cache_creation_input_tokens":40}}}
{"type":"assistant","uuid":"a-2","sessionId":"sess-A","timestamp":"2026-04-26T12:00:02Z","message":{"model":"claude-opus-4-7","usage":{"input_tokens":1,"output_tokens":2}}}
{"type":"file-history-snapshot","messageId":"snap-1"}
not-json
{"type":"assistant","sessionId":"sess-A","message":{"model":"x","usage":{"input_tokens":1}}}
`

func TestParseTranscript_PinsKnownFieldsAndSkipsUnknown(t *testing.T) {
	var warns []string
	msgs, err := parseTranscriptReader(bytes.NewBufferString(sampleAssistant), "pc-beda", "sample.jsonl", func(s string) { warns = append(warns, s) })
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d assistant messages, want 2 (skipping non-assistant + unparseable + missing-uuid)", len(msgs))
	}

	if msgs[0].MessageUUID != "a-1" || msgs[0].Session != "sess-A" || msgs[0].Agent != "pc-beda" {
		t.Errorf("first msg = %+v", msgs[0])
	}
	if msgs[0].Input != 10 || msgs[0].Output != 20 || msgs[0].CacheRead != 30 || msgs[0].CacheCreate != 40 {
		t.Errorf("usage tokens not pinned: %+v", msgs[0])
	}
	if msgs[0].Model != "claude-opus-4-7" {
		t.Errorf("model not pinned: %q", msgs[0].Model)
	}

	// We expect at least two warnings: the not-json line, and the
	// assistant-without-uuid line. Both must not crash the parse.
	if len(warns) < 2 {
		t.Errorf("expected ≥ 2 warns for malformed lines, got %d: %v", len(warns), warns)
	}
}

func TestAgentFromProjectDir(t *testing.T) {
	cases := []struct {
		dir, want string
	}{
		{"-Users-daniel--pogo-polecats-pc-beda2", "pc-beda2"},
		{"-Users-daniel--pogo-agents-architect", "architect"},
		{"-Users-daniel--pogo-crews-pm-pogo", "pm-pogo"},
		{"-Users-daniel-projects-someproj", ""},
		{"random-dir", ""},
	}
	for _, c := range cases {
		got := AgentFromProjectDir(c.dir)
		if got != c.want {
			t.Errorf("AgentFromProjectDir(%q) = %q, want %q", c.dir, got, c.want)
		}
	}
}

func TestParseTranscript_FilePath(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "session.jsonl")
	if err := writeFile(p, sampleAssistant); err != nil {
		t.Fatal(err)
	}
	msgs, err := ParseTranscript(p, "pc-test", nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d, want 2", len(msgs))
	}
}
