package workitem

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"sort"
)

// SidecarRelation is the verdict on how a stray result sidecar relates to the
// authoritative copy beside the item's .md.
//
// It replaces a bare differs/identical bit, which collapsed four situations
// with four DIFFERENT safe actions into one alarming token and sent the
// operator off to open both files by hand — which is exactly where the mg-eb1e
// reconciliation errors came from (a size was read as a proxy for emptiness, a
// directory as a proxy for authority). Each verdict below names what to DO, not
// merely that something is unequal.
type SidecarRelation string

const (
	// RelationIdentical: same bytes. The stray carries nothing the
	// authoritative copy lacks; deleting it loses nothing.
	RelationIdentical SidecarRelation = "identical"

	// RelationEquivalent: different bytes, same JSON content — re-serialised
	// with different key order or whitespace. Anything that has round-tripped
	// through encoding/json normalises key order, so this is common and it is a
	// FALSE POSITIVE under a byte comparison: it sends an operator to open two
	// files that say the same thing. No human judgement required.
	RelationEquivalent SidecarRelation = "equivalent"

	// RelationSubset: one side's keys are contained in the other's and the two
	// agree on every shared key. Mechanically resolvable — keep the superset —
	// but see the deliberate non-action in the package docs: this tool
	// classifies and does not merge, because a subset relation can also be the
	// artefact of one side having been truncated.
	RelationSubset SidecarRelation = "subset"

	// RelationConflict: each side holds something the other lacks, or the two
	// disagree on a shared key. A wrong merge loses data silently; this needs a
	// human, and DifferingKeys names what they are choosing between.
	RelationConflict SidecarRelation = "conflict"

	// RelationOpaque: the bytes differ and at least one side is not a JSON
	// object, so the key-level analysis does not apply. Honest "cannot
	// classify" rather than a guess in either direction.
	RelationOpaque SidecarRelation = "opaque"

	// RelationUnknown: a side could not be READ. An errored probe and a
	// negative result must never share a token — under the old comparison an
	// unreadable stray reported as "differs", i.e. a failure masquerading as a
	// finding, which is the very defect class this tool exists to catch.
	RelationUnknown SidecarRelation = "unknown"
)

// Side names which copy a statement is about.
const (
	SideStray         = "stray"
	SideAuthoritative = "authoritative"
)

// SidecarComparison is the classified relationship between the stray and the
// authoritative copy, together with the detail that makes the verdict
// actionable. Naming the keys is the load-bearing part: "differs" tells an
// operator to look, "conflict: branch, completed_by, mr" tells them what they
// are choosing between, which is the difference between a decision and a guess.
type SidecarComparison struct {
	Relation SidecarRelation

	// Superset is SideStray or SideAuthoritative when Relation is
	// RelationSubset, naming the copy that contains the other. Empty otherwise.
	Superset string

	// Keys is the detail behind the verdict, sorted:
	//   - RelationSubset:   the keys the superset has and the other side lacks.
	//   - RelationConflict: every key the two do not agree on — present on one
	//                       side only, or present on both with unequal values.
	// Empty for the other relations.
	Keys []string

	// Note explains a verdict that key names cannot: why a read failed
	// (RelationUnknown) or why the structural comparison did not apply
	// (RelationOpaque). Empty otherwise.
	Note string
}

// Differs reports whether the two copies are known to hold different content.
// It is false for RelationUnknown: a probe that failed is not evidence of a
// difference. Use Relation for disposition; this is the coarse question only.
func (c SidecarComparison) Differs() bool {
	switch c.Relation {
	case RelationEquivalent, RelationSubset, RelationConflict, RelationOpaque:
		return true
	default:
		return false
	}
}

// SameContent reports whether the two copies provably carry the same content,
// whatever their formatting. Only RelationIdentical and RelationEquivalent
// qualify — both are established by comparing content, never inferred from a
// proxy such as size, mtime, or the directory a file was found in.
func (c SidecarComparison) SameContent() bool {
	return c.Relation == RelationIdentical || c.Relation == RelationEquivalent
}

// compareSidecarFiles classifies two sidecar paths, reading both.
//
// A read failure yields RelationUnknown rather than a difference. The caller
// has already established that both paths exist, so a failure here means
// unreadable (permissions, I/O), which is a fact about the probe and not about
// the files' contents.
func compareSidecarFiles(strayPath, authPath string) SidecarComparison {
	stray, err := os.ReadFile(strayPath)
	if err != nil {
		return SidecarComparison{Relation: RelationUnknown, Note: "stray is unreadable: " + err.Error()}
	}
	auth, err := os.ReadFile(authPath)
	if err != nil {
		return SidecarComparison{Relation: RelationUnknown, Note: "authoritative copy is unreadable: " + err.Error()}
	}
	return compareSidecarBytes(stray, auth)
}

// compareSidecarBytes classifies two sidecar payloads by content.
func compareSidecarBytes(stray, auth []byte) SidecarComparison {
	if bytes.Equal(stray, auth) {
		return SidecarComparison{Relation: RelationIdentical}
	}

	var strayObj, authObj map[string]any
	if err := json.Unmarshal(stray, &strayObj); err != nil || strayObj == nil {
		return SidecarComparison{Relation: RelationOpaque, Note: "stray is not a JSON object"}
	}
	if err := json.Unmarshal(auth, &authObj); err != nil || authObj == nil {
		return SidecarComparison{Relation: RelationOpaque, Note: "authoritative copy is not a JSON object"}
	}

	var onlyStray, onlyAuth, disagree []string
	for k, sv := range strayObj {
		av, ok := authObj[k]
		switch {
		case !ok:
			onlyStray = append(onlyStray, k)
		case !reflect.DeepEqual(sv, av):
			disagree = append(disagree, k)
		}
	}
	for k := range authObj {
		if _, ok := strayObj[k]; !ok {
			onlyAuth = append(onlyAuth, k)
		}
	}

	if len(disagree) == 0 {
		switch {
		case len(onlyStray) == 0 && len(onlyAuth) == 0:
			// Same keys, same values, different bytes: key order or whitespace.
			return SidecarComparison{Relation: RelationEquivalent}
		case len(onlyStray) == 0:
			return SidecarComparison{Relation: RelationSubset, Superset: SideAuthoritative, Keys: sorted(onlyAuth)}
		case len(onlyAuth) == 0:
			return SidecarComparison{Relation: RelationSubset, Superset: SideStray, Keys: sorted(onlyStray)}
		}
		// Each side has keys the other lacks: not a subset either way.
	}
	return SidecarComparison{Relation: RelationConflict, Keys: sorted(append(append(disagree, onlyStray...), onlyAuth...))}
}

func sorted(keys []string) []string {
	sort.Strings(keys)
	return keys
}
