package mgerr

import (
	"errors"
	"fmt"
	"testing"
)

func TestCheckUTF8(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		offset int // -1 means valid
	}{
		{"empty", "", -1},
		{"ascii", "hello", -1},
		{"multibyte", "café — ✓ 🚀", -1},
		{"leading 0xFF", "\xffabc", 0},
		{"latin1 e-acute", "caf\xe9 au lait", 3},
		{"truncated 2-byte at end", "abc\xc3", 3},
		{"lone continuation", "ok\x80", 2},
		{"after valid multibyte", "é\xfe", 2},
		{"surrogate encoding", "x\xed\xa0\x80", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckUTF8("body", tc.in)
			if tc.offset < 0 {
				if err != nil {
					t.Fatalf("valid input refused: %v", err)
				}
				return
			}
			var me *Error
			if !errors.As(err, &me) {
				t.Fatalf("want *mgerr.Error, got %v", err)
			}
			if me.Category != CatUsage || me.Category.ExitCode() != 2 {
				t.Errorf("category = %v, want usage (exit 2)", me.Category)
			}
			if me.Code != "invalid_utf8" {
				t.Errorf("code = %q, want invalid_utf8", me.Code)
			}
			want := fmt.Sprintf("body: invalid UTF-8 at byte offset %d", tc.offset)
			if me.Message != want {
				t.Errorf("message = %q, want %q", me.Message, want)
			}
		})
	}
}
