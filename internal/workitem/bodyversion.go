package workitem

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// This file defines the body version identity that `mg edit --if-unchanged`
// and `mg show --body-hash` / `--json`.body_hash are built on.
//
// WHY A HASH AND NOT AN mtime OR A COUNTER. mtime has second (sometimes
// coarser) granularity and moves for reasons that are not content changes; a
// counter would have to live in the frontmatter, which makes every existing
// stored item version-less and forces a migration. The body's own bytes are
// already the thing every writer cares about, they are already exposed to
// callers verbatim by `mg show --json`.body, and a hash of them is derivable by
// anyone at any time with no schema change at all. A caller that read the body
// can therefore always name the version it read, including for items written
// before this existed.
//
// WHAT IS COVERED. BodyHash covers the STORED body — everything after the
// closing frontmatter delimiter, which includes the "# Title" heading. That is
// deliberate and load-bearing: `mg edit --title` rewrites the heading line in
// place, so a title clobber (mg-f326 incident 2, where one agent's retitle was
// overwritten seconds later) moves the body hash and is caught by the same
// guard as a body clobber. Frontmatter-only fields — tags, assignee, priority,
// budget, depends — are NOT covered; --if-unchanged guards prose, not metadata.

// minHashPrefix is the shortest --if-unchanged value accepted. Eight hex
// characters is 32 bits: enough that an accidental prefix collision between two
// versions of one item's body is not a thing that happens, and short enough to
// retype from a terminal. Anything shorter is rejected as a usage error rather
// than silently treated as a weak guard — a control that quietly weakens itself
// is the failure mode this whole feature exists to remove.
const minHashPrefix = 8

// BodyHash returns the lowercase hex SHA-256 of a work item body.
//
// The input is the body EXACTLY as stored — the same bytes `mg show ID --json`
// emits in its "body" field — so a caller can reproduce this value independently
// from data mg already gave them.
func BodyHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// normalizeHashArg validates a user-supplied --if-unchanged value and returns
// it lowercased. It rejects anything that is not hex or is shorter than
// minHashPrefix, because both cases mean the caller is holding something that
// is not a body hash — and a guard fed a non-hash would either never match
// (wedging a writer) or match by accident (guarding nothing).
func normalizeHashArg(given string) (string, error) {
	h := strings.ToLower(strings.TrimSpace(given))
	if len(h) < minHashPrefix {
		return "", fmt.Errorf("must be at least %d hex characters (got %d)", minHashPrefix, len(h))
	}
	if len(h) > 64 {
		return "", fmt.Errorf("must be at most 64 hex characters (got %d)", len(h))
	}
	for _, r := range h {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return "", fmt.Errorf("must be hex characters only (got %q)", given)
		}
	}
	return h, nil
}

// bodyHashMatches reports whether a normalized --if-unchanged value names the
// given stored body. A prefix matches, so callers can paste a short form.
func bodyHashMatches(normalized, body string) bool {
	return strings.HasPrefix(BodyHash(body), normalized)
}

// countBodyLines counts lines the way `wc -l`-minded readers expect: a trailing
// newline does not add a phantom empty line, and the empty body is 0 lines.
// Used only for the human-facing "body 142 → 227 lines" report, never for any
// decision.
func countBodyLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}
