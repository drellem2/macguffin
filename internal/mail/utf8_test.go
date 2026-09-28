package mail

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/drellem2/macguffin/internal/mgerr"
)

// drellem2/macguffin#41: a message whose from, subject or body is not valid
// UTF-8 is refused before anything is written — no message, no mailbox.
func TestSend_RefusesInvalidUTF8(t *testing.T) {
	cases := []struct {
		name                string
		from, subject, body string
		want                string
	}{
		{"from", "may\xffor", "s", "b", "from: invalid UTF-8 at byte offset 3"},
		{"subject", "mayor", "caf\xe9", "b", "subject: invalid UTF-8 at byte offset 3"},
		{"body", "mayor", "s", "line one\n\xc3", "body: invalid UTF-8 at byte offset 9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			_, err := Send(root, "arch", tc.from, tc.subject, tc.body)
			var me *mgerr.Error
			if !errors.As(err, &me) || me.Code != "invalid_utf8" || me.ExitCode() != 2 {
				t.Fatalf("want an exit-2 invalid_utf8 refusal, got %v", err)
			}
			if !strings.Contains(me.Message, tc.want) {
				t.Errorf("message %q does not name %q", me.Message, tc.want)
			}
			if entries, _ := os.ReadDir(root); len(entries) != 0 {
				t.Fatalf("a refused send left %d entr(ies) behind", len(entries))
			}
		})
	}

	// Negative control: multibyte text is delivered.
	if _, err := Send(t.TempDir(), "arch", "zoë", "café ✓", "naïve — 🚀\n"); err != nil {
		t.Fatalf("valid UTF-8 refused: %v", err)
	}
}
