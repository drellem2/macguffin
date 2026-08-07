package mail

import "sort"

// This file answers "did you mean ...?" for a mailbox name mg has never seen.
//
// It exists because the failure it serves is a TYPO, and a typo's defining
// property is that the name the caller meant is still sitting there, one or two
// characters away. Sending to "9ecf" when the reviewer is "v9ecf" produced four
// mails into a box nobody opens; the refusal that now catches it is only half
// the fix, because "no such mailbox: 9ecf" leaves the caller to work out what
// the right name was. Naming the neighbour turns a refusal into a correction.

// suggestThreshold is the largest edit distance that still counts as "probably
// the same name, mistyped", scaled to the length of what was typed. Agent names
// here are short — a 4-hex work-item id, a crew name like "mayor" — so a fixed
// threshold is either useless on the short ones or absurd on the long ones.
//
// Short names get distance 1 and no more, because the id space is dense: a store
// holding ~1200 four-hex mailboxes puts a couple of dozen of them within
// distance 2 of any given id, and a list of two dozen "did you mean"s is not a
// correction, it is a haystack. Distance 1 still catches the failure that
// motivated this — "9ecf" typed for "v9ecf", one dropped character.
func suggestThreshold(n int) int {
	switch {
	case n <= 5:
		return 1
	case n <= 8:
		return 2
	default:
		return 3
	}
}

// Suggest returns up to max candidates that look like a misspelling of name,
// nearest first and alphabetical within a distance. An exact match is never
// suggested (there is nothing to correct), and a candidate further away than
// suggestThreshold is not a suggestion but a guess — those are dropped rather
// than padded in to fill max, so an empty result means "nothing here resembles
// it", which is itself the useful answer.
//
// Candidates are supplied by the caller rather than read from disk: the useful
// set is mailboxes AND work-item ids (a polecat's box is routinely named for
// its work item, so the name the sender meant may not be a mailbox yet), and
// this package knows nothing about work items.
func Suggest(candidates []string, name string, max int) []string {
	if name == "" || max <= 0 {
		return nil
	}
	limit := suggestThreshold(len([]rune(name)))

	type scored struct {
		name string
		dist int
	}
	var hits []scored
	for _, c := range candidates {
		if c == name {
			continue // an exact match is not a correction
		}
		d := editDistance(name, c)
		if d <= limit {
			hits = append(hits, scored{name: c, dist: d})
		}
	}

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].dist != hits[j].dist {
			return hits[i].dist < hits[j].dist
		}
		return hits[i].name < hits[j].name
	})

	if len(hits) > max {
		hits = hits[:max]
	}
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.name)
	}
	return out
}

// editDistance is optimal string alignment distance over runes: insert, delete
// and substitute each cost 1, and so does TRANSPOSING two adjacent characters.
// Runes rather than bytes, so a non-ASCII name is measured in the characters a
// human typing it would count.
//
// The transposition rule is not a refinement, it is the difference between the
// suggester working and not working on the names it is pointed at. Swapping two
// adjacent characters is among the commonest typos there is, and plain
// Levenshtein scores it 2 — the same as two unrelated substitutions. Short names
// here are held to distance 1 (the id space is too dense for more), so under
// plain Levenshtein "mayro" would earn no suggestion at all while "mayor" sat
// one swap away in the mailbox list.
func editDistance(a, b string) int {
	ar, br := []rune(a), []rune(b)
	if len(ar) == 0 {
		return len(br)
	}
	if len(br) == 0 {
		return len(ar)
	}

	// Three rows: the transposition case reads the row from TWO steps back,
	// which is why this cannot be done with the usual two.
	prev2 := make([]int, len(br)+1)
	prev := make([]int, len(br)+1)
	curr := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(ar); i++ {
		curr[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			d := min3(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && ar[i-1] == br[j-2] && ar[i-2] == br[j-1] {
				if t := prev2[j-2] + 1; t < d {
					d = t
				}
			}
			curr[j] = d
		}
		prev2, prev, curr = prev, curr, prev2
	}
	return prev[len(br)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}
