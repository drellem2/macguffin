package mgerr

import (
	"fmt"
	"unicode/utf8"
)

// CheckUTF8 refuses text that is not valid UTF-8, naming the field and the
// byte offset of the first invalid byte (drellem2/macguffin#41).
//
// mg used to write whatever bytes it was handed. A body piped in as Latin-1,
// or cut mid-character by a byte-sliced `cut -c`, landed on disk verbatim and
// exited 0; the damage surfaced later and elsewhere — as U+FFFD in --json, or
// as a file a byte-oriented grep treats as binary and reports absent. The
// only moment the caller still holds the source bytes and can fix them is
// the write, so that is where this refuses.
//
// Call it ONLY on text the current write introduces, never on a body already
// in the store: an item that is already damaged must stay editable, because a
// full --body replacement is how it gets repaired. There is deliberately no
// read-side scan or repair.
//
// The offset is measured inside s, the text the caller supplied, so it points
// into the caller's own input rather than into a composed file they never saw.
func CheckUTF8(field, s string) error {
	if utf8.ValidString(s) {
		return nil
	}
	off := invalidUTF8Offset(s)
	return Usage("invalid_utf8",
		fmt.Sprintf("%s: invalid UTF-8 at byte offset %d", field, off),
		"mg stores text as UTF-8. Re-encode the input (e.g. 'iconv -f latin1 -t utf-8') and retry. Nothing was written.")
}

// invalidUTF8Offset returns the byte offset of the first invalid UTF-8
// sequence in s, or -1 if s is valid.
func invalidUTF8Offset(s string) int {
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return i
		}
		i += size
	}
	return -1
}
