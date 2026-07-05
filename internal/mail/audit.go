package mail

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const auditFile = "mail-audit.log"

// AuditLogPath returns the mail audit log path for a mail root:
// <workspace>/log/mail-audit.log (the mail root is <workspace>/mail).
func AuditLogPath(mailRoot string) string {
	return filepath.Join(eventsRoot(mailRoot), "log", auditFile)
}

// Audit appends a caller-attributed line to the mail audit log. Every
// mailbox mutation (send, read, archive) and every refused or forced
// cross-box read is recorded with the caller's pid and POGO_AGENT_NAME, so
// a message that changes state unexpectedly (the mg-6ae0 / mg-f73e
// silent-drop class) is attributable without transcript archaeology —
// events.jsonl records that a read happened, but not who issued it.
//
// Line format is greppable key=value pairs:
//
//	ts=<RFC3339> op=<op> box=<mailbox> msg=<msgID> pid=<pid> caller=<POGO_AGENT_NAME|-> [extra...]
//
// Best-effort like event.Emit: mail operations must never fail because the
// audit log is unwritable; failures are reported on stderr only.
func Audit(mailRoot, op, box, msgID string, extra map[string]string) {
	caller := os.Getenv("POGO_AGENT_NAME")
	if caller == "" {
		caller = "-"
	}
	line := fmt.Sprintf("ts=%s op=%s box=%s msg=%s pid=%d caller=%s",
		time.Now().UTC().Format(time.RFC3339), op, box, msgID, os.Getpid(), caller)

	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		line += fmt.Sprintf(" %s=%s", k, extra[k])
	}

	p := AuditLogPath(mailRoot)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "warning: mail audit: %s\n", fsErrText(err))
		return
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: mail audit: %s\n", fsErrText(err))
		return
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		fmt.Fprintf(os.Stderr, "warning: mail audit: %s\n", fsErrText(err))
	}
}
