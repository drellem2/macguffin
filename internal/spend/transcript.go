// Package spend harvests Claude Code transcripts + macguffin events to attribute
// per-message token usage to mg work items.
//
// transcript.go parses Claude Code session JSONL files at
// ~/.claude/projects/<encoded-cwd>/<session-uuid>.jsonl. Each assistant message
// in those files contains a `usage` object (input/output/cache tokens) and a
// `model` name; the parser pins to those known fields and skips other shapes.
package spend

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Message is a single assistant turn extracted from a transcript file.
type Message struct {
	Ts          time.Time
	Session     string
	MessageUUID string
	Agent       string // derived from the transcript's project directory name
	Model       string
	Input       int
	CacheRead   int
	CacheCreate int
	Output      int
}

// transcriptLine is the subset of fields the aggregator pins to. Anything
// outside this shape is skipped (with an optional warning) so a Claude Code
// version bump that adds fields cannot crash us.
type transcriptLine struct {
	Type      string    `json:"type"`
	UUID      string    `json:"uuid"`
	SessionID string    `json:"sessionId"`
	Timestamp time.Time `json:"timestamp"`
	Message   *struct {
		Model string `json:"model"`
		Usage *struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// ParseTranscript reads a Claude Code .jsonl session file and emits one
// Message per assistant turn that has a usage object. Lines with unknown
// shapes are reported via the warn callback (may be nil).
func ParseTranscript(path string, agent string, warn func(string)) ([]Message, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening transcript %s: %w", path, err)
	}
	defer f.Close()

	return parseTranscriptReader(f, agent, path, warn)
}

func parseTranscriptReader(r io.Reader, agent, path string, warn func(string)) ([]Message, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 64*1024*1024)

	var msgs []Message
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		raw := scanner.Bytes()
		if len(raw) == 0 {
			continue
		}
		var line transcriptLine
		if err := json.Unmarshal(raw, &line); err != nil {
			if warn != nil {
				warn(fmt.Sprintf("%s:%d unparseable line: %v", path, lineNo, err))
			}
			continue
		}
		if line.Type != "assistant" || line.Message == nil || line.Message.Usage == nil {
			continue
		}
		if line.UUID == "" || line.SessionID == "" {
			if warn != nil {
				warn(fmt.Sprintf("%s:%d assistant message missing uuid/sessionId", path, lineNo))
			}
			continue
		}
		u := line.Message.Usage
		msgs = append(msgs, Message{
			Ts:          line.Timestamp.UTC(),
			Session:     line.SessionID,
			MessageUUID: line.UUID,
			Agent:       agent,
			Model:       line.Message.Model,
			Input:       u.InputTokens,
			CacheRead:   u.CacheReadInputTokens,
			CacheCreate: u.CacheCreationInputTokens,
			Output:      u.OutputTokens,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning %s: %w", path, err)
	}
	return msgs, nil
}

// DefaultProjectsDir returns ~/.claude/projects, the root for Claude Code
// transcript directories.
func DefaultProjectsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

// AgentFromProjectDir derives an agent name from a Claude Code project
// directory name. Project dir names look like:
//
//	-Users-daniel--pogo-polecats-pc-beda2     -> "pc-beda2"
//	-Users-daniel--pogo-agents-architect      -> "architect"
//	-Users-daniel--pogo-crews-pm-pogo         -> "pm-pogo"
//
// Returns "" when the directory does not match a known pogo layout — those
// transcripts are not from pogo agents and are skipped by the aggregator.
func AgentFromProjectDir(dir string) string {
	for _, marker := range []string{
		"-pogo-polecats-",
		"-pogo-agents-",
		"-pogo-crews-",
	} {
		if i := strings.Index(dir, marker); i >= 0 {
			return dir[i+len(marker):]
		}
	}
	return ""
}
