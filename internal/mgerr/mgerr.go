// Package mgerr defines the typed error carrying mg's stable error taxonomy:
// a coarse Category (which maps 1:1 onto the process exit code), a fine-grained
// machine-readable Code slug, a human Message, an optional remediation Hint,
// and a Retryable flag. It is a leaf package importable by workitem, mail,
// workspace, and cmd/mg so every producer can emit a categorized error and the
// single exit seam in main() can render it consistently (human or JSON).
//
// THREE FROZEN CONTRACTS live here (additive-only, same governance as the
// --json data-field contract):
//
//  1. Exit codes (Category.ExitCode): 0 success / 1 internal / 2 usage /
//     3 not_found / 4 conflict. Keeping 1=internal makes 2/3/4 a
//     backward-compatible refinement — they carve specific categories out of
//     the old exit-1 catch-all. New categories take 5, 6, … — existing numbers
//     never change.
//  2. Error-code slugs (Code): stable snake_case machine ids. Frozen/additive
//     like JSON field names — add freely, never rename or repurpose.
//  3. The JSON error object (rendered in cmd/mg): {error:{code,category,exit,
//     message,hint,retryable}} on stderr.
//
// See docs/error-taxonomy.md for the public contract.
package mgerr

import "errors"

// Category is the coarse error class. Each value maps to exactly one process
// exit code (ExitCode) and one frozen JSON name (String). The zero value is
// Internal, so an accidentally-zero Category degrades safely to the exit-1
// catch-all rather than to a more specific (and wrong) category.
type Category int

// The category constants are prefixed Cat* so the bare names (Usage, NotFound,
// Conflict, Internal) can serve as the ergonomic constructor functions the
// design's call sites use (e.g. mgerr.Conflict(...)). Go forbids a const and a
// func sharing a name in one package, and the constructors are the far
// more-used surface.
const (
	// CatInternal is the uncategorized catch-all: unexpected/IO/bug conditions
	// (fs failures, JSON marshal errors, git exec failures). Exit 1 — the same
	// code every mg error used before the taxonomy, so any 0-vs-nonzero
	// consumer is unaffected.
	CatInternal Category = iota // exit 1
	// CatUsage means the caller misused the CLI: bad arg count, unknown
	// flag/subcommand, mutually-exclusive flags, invalid value. Exit 2 (the
	// long-standing getopt/argparse convention).
	CatUsage // exit 2
	// CatNotFound means a named entity does not exist: unknown work-item ID,
	// unknown mailbox/message, a message file that exists but is unparseable.
	// Exit 3.
	CatNotFound // exit 3
	// CatConflict means the entity exists but is in the wrong state for the
	// operation: already claimed/done, unmet deps, not-shelved, race-lost
	// claim. Exit 4. (Retryability is a FIELD, not a code — a race-lost claim
	// and a hard conflict are both exit 4.)
	CatConflict // exit 4
)

// ExitCode returns the frozen process exit code for the category (1..4). An
// out-of-range Category defaults to 1 (internal) so the mapping can never
// produce a 0 "success" exit for an error.
func (c Category) ExitCode() int {
	switch c {
	case CatUsage:
		return 2
	case CatNotFound:
		return 3
	case CatConflict:
		return 4
	default:
		return 1
	}
}

// String returns the frozen JSON category name. These names are part of the
// public contract (§4 of the design) — never rename them.
func (c Category) String() string {
	switch c {
	case CatUsage:
		return "usage"
	case CatNotFound:
		return "not_found"
	case CatConflict:
		return "conflict"
	default:
		return "internal"
	}
}

// Error is mg's typed error. Its Error() method returns Message ALONE (no hint);
// the human renderer appends the Hint on its own line and the JSON renderer
// emits Hint as a separate field, so the two levels never get flattened
// together the way the old withHint()/remediation() string-joining did.
type Error struct {
	Category  Category
	Code      string // stable machine slug (frozen/additive) — see docs/error-taxonomy.md
	Message   string // human problem statement, WITHOUT the hint appended
	Hint      string // remediation, optional
	Retryable bool   // true e.g. for a race-lost claim; a FIELD, not an exit code
	wrapped   error  // preserved for errors.Is/As chains (ErrMalformed, *os.LinkError, git errs)
}

// Error implements the error interface. It deliberately returns only Message:
// the hint is presentation concern owned by the renderer, not part of the raw
// error text.
func (e *Error) Error() string { return e.Message }

// Unwrap exposes the wrapped cause so errors.Is/As keep working across the
// typed boundary (e.g. errors.Is(err, mail.ErrMalformed)).
func (e *Error) Unwrap() error { return e.wrapped }

// ExitCode is the process exit code this error should produce.
func (e *Error) ExitCode() int { return e.Category.ExitCode() }

// Usage builds an exit-2 error. code is a snake_case slug (e.g.
// "mutually_exclusive_flags"); hint is optional remediation.
func Usage(code, msg, hint string) *Error {
	return &Error{Category: CatUsage, Code: code, Message: msg, Hint: hint}
}

// NotFound builds an exit-3 error (e.g. "no_such_item").
func NotFound(code, msg, hint string) *Error {
	return &Error{Category: CatNotFound, Code: code, Message: msg, Hint: hint}
}

// Conflict builds an exit-4 error (e.g. "already_claimed").
func Conflict(code, msg, hint string) *Error {
	return &Error{Category: CatConflict, Code: code, Message: msg, Hint: hint}
}

// Internal wraps an arbitrary error as the exit-1 catch-all with Code
// "internal". The wrapped error is preserved for errors.Is/As but its text is
// used verbatim as the message; producers that need to sanitize IO leaks should
// build the *Error explicitly (see Wrap) rather than pass a raw *os.LinkError.
func Internal(err error) *Error {
	if err == nil {
		return &Error{Category: CatInternal, Code: "internal"}
	}
	return &Error{Category: CatInternal, Code: "internal", Message: err.Error(), wrapped: err}
}

// Wrap builds a categorized error that preserves err in the Is/As chain while
// presenting a controlled Message (err.Error()) and Hint. Use it when the
// underlying error should stay matchable (errors.Is) but needs a category/slug.
func Wrap(cat Category, code string, err error, hint string) *Error {
	e := &Error{Category: cat, Code: code, Hint: hint, wrapped: err}
	if err != nil {
		e.Message = err.Error()
	}
	return e
}

// WithRetryable returns a copy of e with Retryable set to v. Constructors
// default Retryable to false; producers flip it for the retryable cases (e.g.
// claim_race) via this fluent helper.
func (e *Error) WithRetryable(v bool) *Error {
	e.Retryable = v
	return e
}

// Coerce turns any error into an *Error for the renderer at the single exit
// seam. If err is already an *Error it is returned unchanged. Otherwise err is
// classified: cobra-origin usage errors (unknown command/flag, bad arg count)
// map to Usage(exit 2); everything else defaults to Internal(exit 1). The
// Internal default is what preserves backward-compat for any un-retrofitted
// producer — an unclassified error still exits 1, exactly as before.
func Coerce(err error) *Error {
	if err == nil {
		return nil
	}
	var me *Error
	if errors.As(err, &me) {
		return me
	}
	if isCobraUsageError(err) {
		return &Error{Category: CatUsage, Code: "usage", Message: err.Error(), wrapped: err}
	}
	return &Error{Category: CatInternal, Code: "internal", Message: err.Error(), wrapped: err}
}
