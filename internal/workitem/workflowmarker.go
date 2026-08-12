package workitem

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/drellem2/macguffin/internal/mgerr"
)

// The gh-issue workflow is asserted by TWO markers carrying ONE fact:
//
//   - the tag `gh-issue`, which is how the workflow playbook queries the board
//     (`mg list --tag=gh-issue` shows every ticket in the chain), and
//   - the body's leading `workflow: gh-issue` line, which is what pogo's mayor
//     actually ROUTES on (--template=polecat-triage / -build-pr / -review).
//
// Only the body line routes. A filer who sets the tag and forgets the body line
// files an item that dispatches SUCCESSFULLY — to the DEFAULT BUILD template. A
// build polecat then opens a PR against an externally-reported GitHub issue with
// no human gate, silently bypassing the review gate that all reporter-facing
// output is supposed to pass through. That is the mg-560d near-miss (2026-07-20);
// it was caught by hand at dispatch, which is not a control we can rely on.
//
// The tag cannot simply be retired — the board query above is a real use. So
// rather than leave two independently authored markers that can disagree, the
// body line is made the single source of truth and the tag becomes a projection
// of it:
//
//	body declares it, tag missing     -> tag is ADDED (derived, never authored)
//	tag claims it, body silent        -> REFUSED
//	tag claims it, body declares other -> REFUSED (divergence)
//	body declares it, but buried in prose -> REFUSED (the router will not see it)
//
// mg deliberately does NOT repair the second case by injecting a carrier block:
// the block's other fields (`stage:`, `gh:`) name a state-machine position and a
// real GitHub issue, and mg cannot know either. Inventing a `gh:` ref would point
// a build polecat at the wrong issue — strictly worse than refusing. So mg
// derives only in the direction where the load-bearing marker is the source, and
// refuses in the direction where it would have to guess.
//
// This lives in the workitem package, not cmd/mg, so that every write path —
// Create, Update, and anything added later — inherits the invariant rather than
// each having to remember it.

// carrierFieldRe matches a leading-block metadata line such as "stage: triage"
// or "gh: owner/repo#12" — a bare lowercase key, a colon, then a value. It is
// deliberately narrow: prose lines ("Triage this issue: ...") start with a
// capital or contain spaces before the colon, so they end the leading block.
var carrierFieldRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*:\s`)

// workflowTags maps a tag name to the workflow it claims the item belongs to.
// A tag listed here is welded to a body `workflow:` line of the same name; tags
// absent from this map are free-form labels and are never inspected.
var workflowTags = map[string]string{
	"gh-issue": "gh-issue",
}

// carrierSkeleton is the full leading block the gh-issue workflow expects, shown
// in the refusal so the filer can paste and fill it rather than go hunting for
// the format.
var carrierSkeleton = map[string]string{
	"gh-issue": "workflow: gh-issue\nstage: triage\ngh: <owner>/<repo>#<n>",
}

// leadingWorkflow reports the workflow declared by the body's carrier block.
//
// found is true when a `workflow:` line opens the body's LEADING BLOCK: scanning
// from the top, blank lines and a markdown heading are skipped, and the first
// content line after them must be the `workflow:` line.
//
// The heading is skipped deliberately rather than as a courtesy. mayor.md
// describes the routing condition as "body starts with workflow: gh-issue", but
// the stored body does not always start there: Render inserts a "# Title"
// heading above the body whenever the body does not already contain one, which
// is exactly what the workflow playbook's own `mg new --body="workflow: ..."`
// invocation produces. Requiring literal line 1 would therefore refuse the
// canonical filing command. What the condition means in practice is that the
// carrier block leads the body, ahead of any prose.
//
// misplaced is true when a `workflow:` line exists but only further down, buried
// after prose. Such an item looks correctly marked to a human skimming `mg show`
// while routing exactly like an unmarked one, so it is refused rather than
// silently accepted.
// A line inside a fenced code block is an EXAMPLE, never a declaration, and it
// is skipped by both scans below. Without that, a body documenting the carrier
// convention refused itself: the fence line ends the leading block, the scan
// then walks on into the fence, finds the `workflow:` line inside it, and calls
// it misplaced. `mg new --body-file` on a fenced example exited 2 (measured
// 2026-08-12, mg-f928), which made the ticket that explains the block one of the
// tickets that could not be filed. Dispatch never sees a fenced line either — it
// stops at the fence — so skipping is fidelity to the reader, not leniency.
func leadingWorkflow(body string) (name string, found, misplaced bool) {
	inLeadingBlock := true
	inFence := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if isFenceLine(line) {
			inFence = !inFence
			inLeadingBlock = false
			continue
		}
		if inFence {
			continue
		}
		if v, ok := strings.CutPrefix(trimmed, "workflow:"); ok {
			return strings.TrimSpace(v), inLeadingBlock, !inLeadingBlock
		}
		// Blank lines, the generated title heading, and sibling carrier fields
		// (stage:, gh:, qa:, ...) do not end the leading block — the carrier
		// fields may be written in any order. The first line of real prose does.
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || carrierFieldRe.MatchString(trimmed) {
			continue
		}
		inLeadingBlock = false
	}
	return "", false, false
}

// leadingCarrierValue returns the value of the named carrier field (`stage`,
// `gh`, ...) when it appears in the body's LEADING BLOCK, or "" when it appears
// only below prose or not at all.
//
// It applies exactly the block rule leadingWorkflow does, for exactly the same
// reason: a `stage:` line buried in prose is a mention, not a declaration, and
// treating the two alike is how a marker comes to mean whatever the nearest
// paragraph happens to say. The scan stops at the first line of prose, so a body
// that discusses stages further down cannot reach back and change the block.
func leadingCarrierValue(body, key string) string {
	prefix := key + ":"
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if isFenceLine(line) {
			// A fence ends the leading block exactly as any other non-carrier
			// line does. This scan already stops at the first such line, so it
			// needs no fence-skipping of its own — the fence IS the stop.
			return ""
		}
		if v, ok := strings.CutPrefix(trimmed, prefix); ok {
			return strings.TrimSpace(v)
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || carrierFieldRe.MatchString(trimmed) {
			continue
		}
		return ""
	}
	return ""
}

// writeShape tells reconcileWorkflowMarkers what the write it is guarding
// actually does, so the guard can distinguish a write that INTRODUCES a
// marker/tag disagreement from one that merely INHERITS a pre-existing one.
//
// WHY THIS EXISTS (mg-d878). The guard above validates the item's effective tag
// set against its resulting body on every write. That is exactly right for a
// filing — the shape it was built for — and exactly wrong for a correction to an
// item filed BEFORE the carrier block was a convention. Of the 92 items tagged
// gh-issue on 2026-07-29, 41 carried no leading carrier block, and for those the
// guard refused *every* edit that touched the body. That includes
// `--append-body-file`, which is the append-only correction protocol mg-f326
// exists to make the default: an append composes against the body on disk, so it
// is the one write shape that cannot clobber a section it never saw.
//
// The trap that leaves is total. The three ways out were: rewrite the body from
// a captured read (the clobber mg-f326 forbids), --rm-tags=gh-issue (destroys
// the board query, and re-adding the tag trips the same guard), or give up and
// mail the finding instead — which is a finding that dies with the thread. The
// mg-d489 docs audit took the third, on mg-ace6, whose body still asserts an
// open bug that shipped fixed on 2026-07-11.
//
// So the relaxation is deliberately narrow. An append cannot change the body's
// leading block by construction — appended text lands below the prose, and a
// carrier block written there is `misplaced`, which stays refused. It therefore
// cannot turn a declared workflow into an undeclared one; it can only inherit an
// item that was already undeclared. A pure append onto a tag the item ALREADY
// carried is thus the one case where the `!found` refusal protects nothing and
// costs the item its only route to correction.
//
// Everything else keeps the guard as it was, and the ordering matters: it is the
// `!found` direction that stops a gh-issue item routing to the default build
// template and opening a PR against an externally-reported issue with no human
// gate (the mg-560d near-miss). A write that ADDS the workflow tag — even
// alongside an append — is introducing the disagreement, not inheriting it, and
// is still refused.
type writeShape struct {
	// appendOnly is true when the write only adds text to the end of the
	// existing body: 'mg edit --append-body-file' and nothing that replaces the
	// body wholesale.
	appendOnly bool
	// priorTags is the tag set the item carried on disk before this write, so
	// the guard can tell an inherited workflow tag from a newly added one. It is
	// empty for a Create, where every tag is new by definition.
	priorTags []string
}

// grandfathers reports whether tag may keep its missing carrier block through
// this write. Both halves are required: the write must be unable to author the
// leading block, and the tag must already have been on the item.
func (s writeShape) grandfathers(tag string) bool {
	if !s.appendOnly {
		return false
	}
	for _, prior := range s.priorTags {
		if prior == tag {
			return true
		}
	}
	return false
}

// reconcileWorkflowMarkers enforces the one-fact-one-marker invariant described
// above. It returns the tags the item should be stored with — identical to the
// input except that a workflow declared in the body adds its tag — or an error
// that refuses the write outright.
//
// It is total over its inputs and performs no I/O, so both Create and Update can
// call it before touching the store: a refusal means nothing was written.
func reconcileWorkflowMarkers(body string, tags []string, shape writeShape) ([]string, error) {
	declared, found, misplaced := leadingWorkflow(body)

	// A carrier block buried below prose is a silent misroute dressed up as a
	// correct filing: it reads as marked, but dispatch never sees it. Refuse
	// regardless of the tags.
	if misplaced && workflowTagFor(declared) != "" {
		return nil, mgerr.Usage("workflow_marker_misplaced",
			fmt.Sprintf("body declares %q, but below the body's opening block, so dispatch will not see it and the item would route to the default build template.", "workflow: "+declared),
			"Move the carrier block to the top of the body, ahead of any prose:\n\n"+carrierSkeleton[declared])
	}

	// Direction 1: the tag claims a workflow the body does not declare. Refuse;
	// mg will not invent the rest of the carrier block.
	for _, tag := range tags {
		wf, isWorkflowTag := workflowTags[tag]
		if !isWorkflowTag {
			continue
		}
		switch {
		case !found && shape.grandfathers(tag):
			// A pure append onto a tag the item already carried. The write
			// inherits the disagreement rather than creating it, and refusing
			// here would leave the item with no append-only route to a
			// correction at all. The item stays as unroutable as it was — see
			// MissingWorkflowCarrier, which is how callers say so out loud.
			continue
		case !found:
			return nil, mgerr.Usage("workflow_marker_missing",
				fmt.Sprintf("--tags=%s marks this as a %s workflow item, but the body does not declare it. Dispatch routes on the body, not the tag, so this item would go to the DEFAULT BUILD template — on a gh-issue that means a PR opened against an externally-reported issue with no human gate.", tag, wf),
				"Start the body with the carrier block (mg cannot fill in stage/gh for you):\n\n"+carrierSkeleton[wf]+
					"\n\nOr drop --tags="+tag+" if this is not a "+wf+" workflow item.")
		case declared != wf:
			return nil, mgerr.Usage("workflow_marker_conflict",
				fmt.Sprintf("--tags=%s claims the %s workflow but the body declares %q. Dispatch routes on the body, so the tag is silently wrong.", tag, wf, "workflow: "+declared),
				"Make the two agree: fix the body's leading 'workflow:' line, or drop --tags="+tag+".")
		}
	}

	// Direction 2: the body declares a workflow, so the tag is DERIVED from it
	// rather than authored alongside it. This is what keeps the board query
	// (`mg list --tag=gh-issue`) complete without asking the filer to remember a
	// second marker — and it is safe precisely because it copies the
	// load-bearing marker instead of guessing at it.
	if found {
		if tag := workflowTagFor(declared); tag != "" {
			return addUnique(tags, []string{tag}), nil
		}
	}

	return tags, nil
}

// MissingWorkflowCarrier returns the workflow tag the item carries whose body
// does not declare the matching carrier block, or "" when the two agree.
//
// This is the residue of the grandfathering above. Letting an append through on
// a legacy item is the right call — the alternative is no correction at all —
// but it must not be a SILENT right call: the item is still one that dispatch
// will route to the default build template, and the agent appending to it is the
// one agent guaranteed to be looking. mg reports the state; it does not repair
// it, because repairing means inventing a `gh:` ref and a `stage:`, and a wrong
// `gh:` ref points a build polecat at the wrong issue.
func MissingWorkflowCarrier(item *Item) string {
	_, found, _ := leadingWorkflow(composeBody(item))
	if found {
		return ""
	}
	for _, tag := range item.Tags {
		if _, isWorkflowTag := workflowTags[tag]; isWorkflowTag {
			return tag
		}
	}
	return ""
}

// workflowTagFor returns the tag that projects the named workflow, or "" if the
// name is not a known workflow.
func workflowTagFor(workflow string) string {
	for tag, wf := range workflowTags {
		if wf == workflow {
			return tag
		}
	}
	return ""
}

// knownWorkflowTags lists the welded tags in a stable order, for help text and
// test diagnostics.
func knownWorkflowTags() []string {
	out := make([]string, 0, len(workflowTags))
	for tag := range workflowTags {
		out = append(out, tag)
	}
	sort.Strings(out)
	return out
}
