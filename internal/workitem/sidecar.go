package workitem

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/drellem2/macguffin/internal/mgerr"
	"github.com/drellem2/macguffin/store"
)

// writeResultBeside merges resultJSON into any prior result and stores it as
// the result of src, the record the item currently is (see Done for why the
// result lands before the guards run).
//
// The prior result is the one the coming move would leave at the
// destination: normally the copy travelling with src, or — when src has none —
// a copy already attached to the item's done record from an earlier
// completion (on the filesystem, a sidecar already sitting in done/). Folding
// it in BEFORE writing means an irreconcilable pair fails with both copies
// still stored rather than half-reconciled.
func writeResultBeside(c store.Companions, src store.Ref, resultJSON json.RawMessage) error {
	priorRef := src
	prior, err := c.ReadResult(src)
	if errors.Is(err, store.ErrNotFound) {
		priorRef = store.Ref{ID: src.ID, Status: store.Done}
		prior, err = c.ReadResult(priorRef)
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		prior = nil
	case err != nil:
		return fmt.Errorf("reading result sidecar: %w", err)
	}
	sidecarBytes, err := mergeResultSidecar(prior, c.ResultWhere(priorRef), src.ID, resultJSON)
	if err != nil {
		return err
	}
	if err := c.WriteResult(src, sidecarBytes); err != nil {
		return fmt.Errorf("writing result sidecar: %w", err)
	}
	return nil
}

// The result sidecar <id>.result.json moves from srcDir to dstDir with its item.
//
// The result sidecar is a companion to the work item's .md file and must travel
// with it across EVERY status transition. A sidecar left behind in the old
// status directory is an orphan: it asserts a completion for an item that has
// since moved on, corrupting the audit trail (a .result.json sitting in
// available/ next to no .md claims the item is done when it is really archived).
// This class of bug — mg status transitions orphaning the sidecar — is exactly
// what mg-ab67 fixed. The move is now the backend's, not mg's: every
// Transition, Archive and Unarchive of the filesystem backend carries the
// sidecar (fsstore.MoveSidecar) immediately after the status-defining rename,
// surfacing any failure loudly, so no call site in mg can forget it.

// mergeResultSidecar combines a freshly supplied --result with a result already
// stored (prior, nil when there is none; priorWhere names it for the refusal),
// returning the bytes Done should write.
//
// Done is the only transition that writes a sidecar as well as carrying one, and
// until now the write was an unconditional overwrite: the fresh result simply
// replaced whatever was there. mg-ab67 and mg-9795 both read that as correct
// ("the fresh result supersedes the carried one") because both were reasoning
// about a done -> reopen -> done round trip, where the prior copy really is a
// stale completion of the same shape.
//
// The dominant case in a real store is the opposite one. A polecat writes its
// findings to <id>.result.json in claimed/ and then completes the item with the
// protocol's own invocation, `mg done <id> --result='{"branch": "..."}'`. An
// unconditional overwrite destroys a report with a branch name. Measured on the
// live store at 1593 sidecars: 124 carry a report and 1469 hold nothing but
// merge bookkeeping. The field designed to record what the work found is
// occupied by the branch it landed on.
//
// Note what mg-9795 changed about the failure mode. Before it, the report was
// STRANDED in claimed/ — recoverable, and mg-eb1e is a report recovered exactly
// that way ("the strays are the SURVIVORS"). Now that the sidecar correctly
// follows the .md, the same overwrite ANNIHILATES it instead: the payload is
// carried into done/ and clobbered there, with no copy left anywhere. Fixing the
// move without fixing the write turned a recoverable bug into a lossy one.
//
// So the rule is that Done must never destroy a result it did not write:
//
//   - no prior result: the incoming bytes are written verbatim.
//   - prior result, no --result: the prior is carried forward untouched.
//   - both, and both JSON objects: shallow merge, incoming keys winning. The
//     supersession mg-9795 wanted is preserved key-by-key — `{"branch":"rev1"}`
//     then `{"branch":"rev2"}` still yields exactly `{"branch":"rev2"}` — while
//     keys the fresh result says nothing about survive.
//   - both, not both objects: refuse. See below.
//
// The merge is deliberately shallow. Deep-merging two reports is a judgement
// call about which nested value is authoritative, and guessing wrong silently
// corrupts a record; replacing a whole value under a key the caller explicitly
// set is a rule an operator can predict. Values are carried as RawMessage so
// nested content is never re-normalised.
//
// Refusing the non-object case rather than falling back to the overwrite is the
// same trade: an error leaves both copies on disk and is recoverable by hand,
// where a clobber is not. It costs a hand-reconcile in a case measured at 0 of
// 1593 sidecars in the live store.
func mergeResultSidecar(prior []byte, priorWhere, id string, incoming json.RawMessage) ([]byte, error) {
	if prior == nil {
		// Nothing to preserve — write the caller's bytes exactly as given,
		// rather than round-tripping them through the marshaller.
		return incoming, nil
	}

	priorObj, priorOK := asJSONObject(prior)
	incomingObj, incomingOK := asJSONObject(incoming)
	if !priorOK || !incomingOK {
		which := "the existing result"
		if !incomingOK {
			which = "--result"
		}
		return nil, mgerr.Conflict("sidecar_unmergeable",
			fmt.Sprintf("%s: cannot merge --result with the existing result because %s is not a JSON object; refusing to overwrite it.", id, which),
			fmt.Sprintf("The existing result is at %s. Reconcile the two by hand, then re-run with the combined JSON.", priorWhere))
	}

	for k, v := range incomingObj {
		priorObj[k] = v
	}
	return json.Marshal(priorObj)
}

// asJSONObject decodes data as a JSON object, reporting whether it is one.
// A JSON `null` unmarshals into a nil map without error, so the nil check is
// what distinguishes "an object" from "valid JSON that is not an object".
func asJSONObject(data []byte) (map[string]json.RawMessage, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil || obj == nil {
		return nil, false
	}
	return obj, true
}

// StraySidecar is a result sidecar sitting in a directory that does not hold
// its work item's .md file.
//
// The sidecar is a companion to the .md and moveResultSidecar keeps the pair
// together across every transition, so a stray is by definition a leftover from
// a transition that predates that guarantee. What a stray is NOT, necessarily,
// is a redundant copy — see Comparison.
type StraySidecar struct {
	ID       string // the work item the sidecar names
	Path     string // absolute path to the stray .result.json
	Location string // lifecycle dir the stray sits in (available|claimed|done|pending|shelved|archived)

	// ItemStatus is where the item's .md actually lives — the directory a
	// reader should take the item's result from. Empty when the item cannot be
	// resolved at all, in which case the stray is the only record left.
	ItemStatus string

	// Authoritative is the path to the sidecar beside the item's .md, and
	// AuthoritativeExists reports whether that file is actually present. An
	// absent authoritative copy makes the stray load-bearing: deleting it
	// destroys the item's only result.
	Authoritative       string
	AuthoritativeExists bool

	// Comparison classifies how the stray relates to the authoritative copy.
	// It is the field that decides disposition, and the reason this type exists
	// rather than a bare list of paths.
	//
	// It is a classification and not a bit because "differs" collapsed four
	// situations with four different safe actions (mg-dcb1): same content
	// re-serialised, a mechanically mergeable subset, a genuine conflict, and
	// an unreadable file. An operator handed one alarming token for all four
	// has to open both files to learn which they have — and reconciling by eye
	// is precisely what produced the mg-eb1e errors. See SidecarRelation.
	//
	// Only meaningful when AuthoritativeExists; the zero value is the empty
	// relation, which no output path reads.
	Comparison SidecarComparison
}

// Differs reports whether the stray's content is known to differ from the
// authoritative copy. Retained as the coarse question; read Comparison.Relation
// to decide what to do, and note that an UNREADABLE stray is not a difference.
func (s StraySidecar) Differs() bool { return s.Comparison.Differs() }

// Redundant reports whether the stray is provably safe to delete: the
// authoritative copy exists and provably holds the same content, so the stray
// carries nothing that would be lost.
//
// "Same content" means byte-identical or JSON-equivalent — a stray that is the
// same object re-serialised with different key order carries no information the
// authoritative copy lacks. It deliberately does NOT include a subset relation:
// keeping the superset is a judgement call, not a mechanical fact, because the
// subset can be the artefact of one side having been truncated.
func (s StraySidecar) Redundant() bool { return s.AuthoritativeExists && s.Comparison.SameContent() }

// FindStraySidecars reports every result sidecar in the store that is not
// beside its item's .md file.
//
// It resolves each sidecar's ID through Resolve rather than trusting the
// directory the file was found in. That distinction is the whole point:
// `ls work/*/<id>.result.json` is wrong twice over. It is ONE level deep, so it
// cannot see the month-partitioned archive that holds most of the store's
// sidecars — the failure that produced two false "no sidecar" findings on
// 2026-08-13 — and among what it CAN see it returns directories in ALPHABETICAL
// order, so an orphan in available/ or claimed/ comes back ahead of the real
// copy in done/.
//
// Callers wanting ONE item's result must not come here at all: this is a scan
// over the whole store, and the answer to "where is this item's result" is
// SidecarFor, which derives the path from the item's own location.
//
// Unreadable directories are skipped rather than failing the scan, matching
// Resolve: a missing work/shelved/ must not hide strays elsewhere.
func FindStraySidecars(root string) ([]StraySidecar, error) {
	// The scan itself is the backend's (store.Companions.Results): on the
	// filesystem it covers the active lifecycle dirs, every archive partition
	// and the loose archive root — the same surface Resolve covers, so a stray
	// cannot hide in a corner the resolver can already see.
	c, err := companionsOf(root)
	if err != nil {
		return nil, err
	}
	refs, err := c.Results()
	if err != nil {
		return nil, err
	}

	var strays []StraySidecar
	for _, ref := range refs {
		id := ref.ID
		// Beside-ness is decided by the result sharing a RECORD location with
		// one of the item's records, not by the resolver picking a winner.
		// Archived twins of one ID can sit in two partitions (see the
		// @partition qualifier), which makes the ID ambiguous to ResolveUnique
		// — but a sidecar sharing a partition with one of those twins is still
		// beside its item, not a stray. Checking co-location first is what
		// keeps every archived twin's own sidecar from being reported as an
		// orphan.
		matches, err := Resolve(root, id)
		if err != nil {
			continue
		}
		beside := false
		for _, m := range matches {
			if store.Status(m.Status) == ref.Status && m.Partition == ref.Partition {
				beside = true
				break
			}
		}
		if beside {
			continue
		}

		s := StraySidecar{ID: id, Path: c.ResultWhere(ref), Location: string(ref.Status)}
		if len(matches) != 1 {
			// No item, or several equally plausible ones: nothing here is
			// authoritative, so the stray is the only record. Report it
			// rather than skipping — that is the load-bearing case.
			strays = append(strays, s)
			continue
		}
		auth := refOf(matches[0])
		s.ItemStatus = matches[0].Status
		s.Authoritative = c.ResultWhere(auth)
		// Presence and readability are separate questions. "Is there an
		// authoritative copy at all" is answered by NotFound; a failure to
		// READ either copy is reported as RelationUnknown by the comparison,
		// never as a difference and never as absence.
		authBytes, aerr := c.ReadResult(auth)
		if errors.Is(aerr, store.ErrNotFound) {
			strays = append(strays, s)
			continue
		}
		s.AuthoritativeExists = true
		strayBytes, serr := c.ReadResult(ref)
		switch {
		case serr != nil:
			s.Comparison = SidecarComparison{Relation: RelationUnknown, Note: "stray is unreadable: " + serr.Error()}
		case aerr != nil:
			s.Comparison = SidecarComparison{Relation: RelationUnknown, Note: "authoritative copy is unreadable: " + aerr.Error()}
		default:
			s.Comparison = compareSidecarBytes(strayBytes, authBytes)
		}
		strays = append(strays, s)
	}
	return strays, nil
}
