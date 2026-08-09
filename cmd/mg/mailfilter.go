package main

import (
	"fmt"
	"strings"

	"github.com/drellem2/macguffin/internal/mail"
	"github.com/drellem2/macguffin/internal/mgerr"
)

// senderFilter is the sender predicate behind `mg mail list --from` /
// `--exclude-from`.
//
// It matches the From FIELD, exactly, and never as a substring. That is the
// whole point of it existing rather than documenting a text filter (mg-5168).
// The escape agents actually reached for was `mg mail list X | grep -v
// scheduler`, which matches the rendered LINE — so it discards any real message
// whose SUBJECT says "scheduler", which is to say exactly the correspondence
// about the noise it was introduced to escape. That form was retracted by name
// on 2026-08-09 after it was recommended, in writing, by the same agent that
// had just finished documenting why it fails.
//
// The failure is not a bad pattern; it is a category error. A pattern over a
// field-structured listing is unsound by construction, and no better pattern
// fixes it: a text filter cannot see which column it landed in. It also
// self-validates — both forms returned identical answers all morning, because
// no real message had yet mentioned the scheduler, and the collision is
// correlated with the topic, so it arrives exactly when the traffic matters
// most. Matching a parsed field cannot land in the wrong column at all.
type senderFilter struct {
	// include, when non-empty, restricts the listing to these senders.
	include map[string]bool
	// exclude hides these senders. Applied after include.
	exclude map[string]bool
	// desc renders the flags as given, for the suppression report. A filter
	// that hides mail has to be able to say what it hid it with.
	desc string
}

// normalizeSender is the single normalization both sides of the comparison go
// through: whitespace-trimmed, lowercased, then canonicalAgent'd. Canonicalizing
// keeps the predicate consistent with the rest of the mail subsystem, where the
// harness prefixes are already stripped from every mailbox argument — so
// `--from=mg-5168` matches a From of `5168` for the same reason mail addressed
// to `mg-5168` lands in box `5168`.
func normalizeSender(s string) string {
	return canonicalAgent(strings.ToLower(strings.TrimSpace(s)))
}

// newSenderFilter builds the predicate from the two flag slices, refusing the
// spellings that can only ever produce an empty listing.
//
// fromSet/excludeSet report whether the flag was given at all, which the slice
// alone cannot answer: pflag parses a string-slice value as CSV, and CSV of the
// empty string is zero fields — so `--exclude-from=` arrives here as an empty
// slice, indistinguishable from the flag being absent. Left alone it is a
// filter that looks applied and does nothing, which is a quieter version of the
// same false confidence this predicate exists to remove.
func newSenderFilter(from, excludeFrom []string, fromSet, excludeSet bool) (senderFilter, error) {
	f := senderFilter{include: map[string]bool{}, exclude: map[string]bool{}}

	if fromSet && len(from) == 0 {
		return f, mgerr.Usage("invalid_value",
			"--from was given an empty sender name",
			"pass an agent name, e.g. --from=mayor; run 'mg mail list' with no arguments to see every mailbox")
	}
	if excludeSet && len(excludeFrom) == 0 {
		return f, mgerr.Usage("invalid_value",
			"--exclude-from was given an empty sender name",
			"pass an agent name, e.g. --exclude-from=scheduler")
	}

	for _, raw := range from {
		key, err := senderKey("--from", raw)
		if err != nil {
			return f, err
		}
		f.include[key] = true
	}
	for _, raw := range excludeFrom {
		key, err := senderKey("--exclude-from", raw)
		if err != nil {
			return f, err
		}
		f.exclude[key] = true
	}

	// A name on both sides can never match anything, so the listing would be
	// empty for a reason that has nothing to do with the mailbox. That is the
	// manufactured-absence shape this flag exists to remove, so it is refused
	// at the callsite rather than reported after the fact.
	for key := range f.include {
		if f.exclude[key] {
			return f, mgerr.Usage("invalid_value",
				fmt.Sprintf("--from and --exclude-from both name %q, so no message can match", key),
				"drop one of them: --from selects senders, --exclude-from hides them")
		}
	}

	var parts []string
	for _, raw := range from {
		parts = append(parts, "--from="+strings.TrimSpace(raw))
	}
	for _, raw := range excludeFrom {
		parts = append(parts, "--exclude-from="+strings.TrimSpace(raw))
	}
	f.desc = strings.Join(parts, " ")

	return f, nil
}

// senderKey validates one sender name and returns its comparison key.
//
// A control character is refused, not silently accepted. The name is echoed
// back verbatim in the suppression report that sits directly above the rows, so
// a newline inside one would print an extra line among them — and this listing
// is exactly where an agent decides whether it has mail. Manufacturing a row
// that no message backs is the same defect as hiding one that a message does;
// the report is only load-bearing if nothing can forge it.
func senderKey(flag, raw string) (string, error) {
	if strings.ContainsFunc(raw, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", mgerr.Usage("invalid_value",
			fmt.Sprintf("%s contains a control character (%q); an agent name has none", flag, raw),
			"pass a plain agent name, e.g. "+flag+"=scheduler")
	}
	key := normalizeSender(raw)
	if key == "" {
		return "", mgerr.Usage("invalid_value",
			fmt.Sprintf("%s was given an empty sender name (%q)", flag, raw),
			"pass an agent name, e.g. "+flag+"=scheduler; run 'mg mail list' with no arguments to see every mailbox")
	}
	return key, nil
}

// active reports whether any sender predicate was given. An inactive filter
// changes nothing about the listing — no extra output, no extra JSON object —
// so every existing invocation is byte-identical to what it was.
func (f senderFilter) active() bool {
	return len(f.include) > 0 || len(f.exclude) > 0
}

// keep reports whether a message survives the predicate.
//
// A message whose From is EMPTY is never hidden by --exclude-from: a filter
// must not suppress mail it cannot classify, and an unattributed message is the
// one most likely to be the one nobody else is watching. It is still absent
// from an --from selection, because that form is an explicit allowlist.
func (f senderFilter) keep(m mail.Message) bool {
	sender := normalizeSender(m.From)
	if len(f.include) > 0 && !f.include[sender] {
		return false
	}
	if sender != "" && f.exclude[sender] {
		return false
	}
	return true
}

// apply returns the surviving messages, preserving order.
func (f senderFilter) apply(msgs []mail.Message) []mail.Message {
	if !f.active() {
		return msgs
	}
	kept := make([]mail.Message, 0, len(msgs))
	for _, m := range msgs {
		if f.keep(m) {
			kept = append(kept, m)
		}
	}
	return kept
}

// report is the line printed ABOVE the rows whenever a predicate is active.
//
// It is a header, not a footer, for the reason bodyMetrics puts its counts in
// the header block (mg-8a44): a footer is cut by the same bounded read it would
// warn about. This defect's own reproduction was `mg mail list architect |
// tail -40`, which keeps a footer and eats a header — but that direction is the
// harmless one. The reader at risk of concluding "no mail" is the one whose
// filtered listing came back short or empty, and a short listing is not cut by
// either bound, so the notice survives precisely in the case that matters.
//
// The counts are load-bearing rather than decorative. A filtered listing that
// printed nothing extra would be byte-identical to a quiet mailbox, which is
// the same false negative in a new place — the remedy exhibiting the defect it
// remedies. So the filter always says how many rows it removed, and the
// empty-result wording below says outright that the mailbox is not empty.
func (f senderFilter) report(shown, total int) string {
	return fmt.Sprintf("  sender filter: %s — %d of %d shown, %d hidden\n",
		f.desc, shown, total, total-shown)
}

// listingNoun names what `mg mail list` was asked for, so the filtered-to-empty
// line says "all 265 unread messages were hidden" rather than describing an
// archive sweep as an inbox.
func listingNoun() string {
	switch {
	case mailListArchived:
		return "archived messages"
	case mailListAll:
		return "messages"
	default:
		return "unread messages"
	}
}

// jsonNames normalizes a nil flag slice to an empty one, so the --json contract
// emits [] rather than null for the predicate half that was not given.
func jsonNames(names []string) []string {
	if names == nil {
		return []string{}
	}
	return names
}
