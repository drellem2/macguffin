package main

import (
	"fmt"
	"strings"

	"github.com/drellem2/macguffin/internal/mgerr"
)

// deriveSubject takes a mail subject from the FIRST LINE of the body — the
// RFC822 / git-commit convention — and is what 'mg mail send' does when
// --subject is omitted (mg-158e).
//
// WHY THIS EXISTS. --subject is a short free-text field that could only ever be
// answered inline, and inline means the shell expands `backticks`, $VAR and
// $(cmd) before mg is executed, exactly as it does inside --body="..." (see
// bodyFromFlags for the body half of the same hazard). mg cannot detect it
// afterwards: the shell has already eaten the metacharacters by the time mg
// sees argv, so a downstream guard can only fire on the inputs that were NOT
// corrupted.
//
// Every cheaper remedy was refuted before this one was adopted (mg-2da4).
// Single-quoting fails SILENTLY on ordinary English, because English carries
// apostrophes in pairs: 'Daniel's call on Monday's park' has an EVEN number of
// them, so the shell hands mg argv[1]="Daniels park" — five of six words gone,
// exit 0, nothing on stderr. Passing a variable only relocates the hazard to
// the assignment line, where the same two failures live and the empty result is
// quieter still.
//
// Deriving inverts the incentive gradient instead of documenting it. Omitting
// --subject is SHORTER than passing it, and the subject text then rides inside
// the same quoted heredoc as the body — bytes already proven verbatim by
// mg-7850, with no shell in their path at all:
//
//	mg mail send mayor --from=me --body-file - <<'EOF'
//	Daniel's call on Monday's park
//
//	body text, `backticks` and $VARS and all
//	EOF
//
// THE FIRST LINE IS COPIED, NOT MOVED. The delivered body keeps every byte it
// arrived with, including the line the subject was taken from. Cutting it would
// make deriving a body-MUTATING operation, and this entire family of tickets is
// about tools that quietly deliver something other than what they were handed.
// A duplicated line is visible and costs nothing; a deleted one is the disease.
//
// It refuses rather than guesses. mg-b5d3 is what a malformed subject header
// costs — a bare CR silently dropped a message for four days — so an empty or
// control-character-bearing first line is a loud error naming --subject, never
// a header written anyway.
func deriveSubject(body string) (string, error) {
	// Derive from the body as it will be STORED and read back: the mail store
	// trims the body, so the first line the recipient sees is its first
	// NON-BLANK line. Deriving from anything else would echo a subject that
	// does not appear at the top of the message actually delivered.
	line, _, _ := strings.Cut(strings.TrimLeft(body, " \t\r\n"), "\n")

	// A trailing CR is the other half of a CRLF terminator, not content: a
	// body-file written with Windows line endings would otherwise derive a
	// subject ending in a control character and be refused for its line
	// endings alone. Surrounding spaces and tabs go the same way — the margins
	// of a header value are layout, not text. Anything else control-shaped is
	// left in place, so the check below can see it.
	subject := strings.Trim(strings.TrimSuffix(line, "\r"), " \t")

	if subject == "" {
		return "", mgerr.Usage("missing_required",
			"cannot derive a subject: the body's first line is blank",
			"start the body with the subject line, or pass --subject explicitly")
	}
	for _, r := range subject {
		if r < 0x20 || r == 0x7f {
			return "", mgerr.Usage("invalid_header_value",
				fmt.Sprintf("cannot derive a subject from the body's first line %q: control characters (including CR) are not allowed in a header value", subject),
				"remove the control character from the body's first line, or pass --subject explicitly")
		}
	}
	return subject, nil
}
