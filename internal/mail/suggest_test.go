package mail

import (
	"reflect"
	"testing"
)

// TestSuggest_CatchesTheDroppedCharacter is the case the whole suggester exists
// for: "9ecf" typed where the reviewer is "v9ecf". One dropped character, four
// mails into a box nobody opens, forty minutes of a stalled review loop with
// both ends healthy.
func TestSuggest_CatchesTheDroppedCharacter(t *testing.T) {
	got := Suggest([]string{"v9ecf", "mayor", "architect"}, "9ecf", 3)
	if !reflect.DeepEqual(got, []string{"v9ecf"}) {
		t.Errorf("Suggest = %v, want [v9ecf]", got)
	}
}

func TestSuggest(t *testing.T) {
	crew := []string{"mayor", "architect", "human", "pm-pogo"}

	cases := []struct {
		name       string
		candidates []string
		input      string
		max        int
		want       []string
	}{
		{
			name:       "a transposition in a crew name",
			candidates: crew,
			input:      "mayro",
			max:        3,
			want:       []string{"mayor"},
		},
		{
			name:       "an exact match is not a correction",
			candidates: crew,
			input:      "mayor",
			max:        3,
			want:       nil,
		},
		{
			name:       "nothing resembling it suggests nothing",
			candidates: crew,
			input:      "definitely-nobody-9ecf",
			max:        3,
			want:       nil,
		},
		{
			// A dense 4-hex id space is why short names are held to distance 1:
			// at distance 2 every id neighbours every other and the list stops
			// being a correction. Two substitutions away is not a suggestion.
			name:       "short ids stay at distance 1",
			candidates: []string{"1234", "12ab", "9999"},
			input:      "12cd",
			max:        3,
			want:       nil,
		},
		{
			name:       "one substitution away in a short id still suggests",
			candidates: []string{"1234", "12ab", "9999"},
			input:      "12ac",
			max:        3,
			want:       []string{"12ab"},
		},
		{
			name:       "nearest first, alphabetical within a distance",
			candidates: []string{"bf3af", "bf3ae", "bf3ce"},
			input:      "bf3ad",
			max:        3,
			want:       []string{"bf3ae", "bf3af"},
		},
		{
			name:       "max caps the list",
			candidates: []string{"bf3ae", "bf3af", "bf3ac"},
			input:      "bf3ad",
			max:        2,
			want:       []string{"bf3ac", "bf3ae"},
		},
		{
			name:       "an empty name suggests nothing",
			candidates: crew,
			input:      "",
			max:        3,
			want:       nil,
		},
		{
			name:       "max 0 suggests nothing",
			candidates: crew,
			input:      "mayro",
			max:        0,
			want:       nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Suggest(tc.candidates, tc.input, tc.max)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Suggest(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestEditDistance(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"", "abc", 3},
		{"abc", "", 3},
		{"abc", "abc", 0},
		{"9ecf", "v9ecf", 1},  // insertion
		{"mayor", "mayr", 1},  // deletion
		{"mayor", "mayer", 1}, // substitution
		{"mayor", "mayro", 1}, // an adjacent transposition costs one (see editDistance)
		{"kitten", "sitting", 3},
		{"héllo", "hello", 1}, // measured in runes, not bytes
	}
	for _, tc := range cases {
		if got := editDistance(tc.a, tc.b); got != tc.want {
			t.Errorf("editDistance(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
		// Distance is symmetric; a suggester that ranked differently by
		// argument order would suggest different names to sender and reader.
		if got := editDistance(tc.b, tc.a); got != tc.want {
			t.Errorf("editDistance(%q, %q) = %d, want %d (asymmetric)", tc.b, tc.a, got, tc.want)
		}
	}
}
