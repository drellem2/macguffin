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
func leadingWorkflow(body string) (name string, found, misplaced bool) {
	inLeadingBlock := true
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
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

// reconcileWorkflowMarkers enforces the one-fact-one-marker invariant described
// above. It returns the tags the item should be stored with — identical to the
// input except that a workflow declared in the body adds its tag — or an error
// that refuses the write outright.
//
// It is total over its inputs and performs no I/O, so both Create and Update can
// call it before touching the store: a refusal means nothing was written.
func reconcileWorkflowMarkers(body string, tags []string) ([]string, error) {
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
