package workitem

import (
	"strings"
	"testing"
)

// THE ACCEPTANCE TEST FOR mg-5eee, AND IT PASSES AGAINST THE CODE IT WAS
// WRITTEN TO INDICT. That is the finding, so it is recorded here rather than
// only in the ticket.
//
// mg-5eee reported, from two observations, that `mg edit --append-body-file`
// silently reverted `assignee` to `mayor` — a value nobody had set — and asked
// for a test that appends to a body and asserts every OTHER field is unchanged,
// "shown to FAIL against the current code, or it is not testing the bug". The
// leading hypothesis, at n=3, was that the body is scanned for field-shaped
// lines the way the title is derived from the first `# ` heading: both
// clobbering appends contained assignee-shaped prose, the one harmless probe did
// not.
//
// The hypothesis is dead and the append is innocent. What actually happened is
// in events.jsonl, which has recorded every assignee move with its actor and its
// before/after pair since mg-3122:
//
//	23:17:34 mayor    assignee ""              → blocked:pm-pogo
//	23:17:34 mayor    append, 38 → 47 lines          (no assignee field)
//	23:18:35 pm-pogo  append, 47 → 125 lines         (no assignee field)
//	23:18:35 pm-pogo  assignee "blocked:pm-pogo" → "mayor"
//	23:19:29 mayor    assignee "mayor"          → "blocked:pm-pogo"
//	23:19:41 mayor    append, 125 → 128 lines        (no assignee field)
//	23:19:54 pm-pogo  assignee "blocked:pm-pogo" → "mayor"
//
// pm-pogo — the agent the gate named — handed the item back to the mayor, twice,
// each time within a minute of reading a note the mayor had just appended. Every
// append in that sequence left the assignee alone. The correlation the mayor
// observed is real and the causation is inverted: the appends did not clobber
// the field, they NOTIFIED the agent that then set it. The 3-line HTML-comment
// probe did not clobber because it said nothing pm-pogo needed to answer.
//
// So this file cannot fail against current code, because the defect it was
// specified to catch is not in the code. It is kept as a REGRESSION GUARD, which
// is a weaker thing than a repro and is labelled as one: it locks in the
// property the ticket wanted proven — an append is a body operation and touches
// no other field — across bodies chosen to attack it.
//
// The exposure mg-5eee is really about is one field over and one caller over,
// and it has its own tests: nothing let the mayor SAY the hold was still in
// place. See TestIfAssignee_* in ifassignee_test.go, which do fail without their
// fix, and TestValidateAssignee_* in assigneegate_test.go for the second route
// the ticket named.

// hostileAppends are bodies chosen to break a body-is-scanned-for-fields
// implementation if one existed. Each is appended to an item whose every field
// is set to a known value, and every field is then read back off disk.
var hostileAppends = []struct {
	name string
	text string
}{
	{"assignee as yaml", "## note\n\nassignee: mayor\n"},
	{"assignee at column zero, no space", "assignee:mayor\n"},
	{"assignee as prose", "The gate was set with `--assignee=blocked:pm-pogo` and reads assignee=[mayor].\n"},
	{"assignee=[mayor]", "assignee=[mayor]\n"},
	{"every field as yaml", "id: mg-0000\ntype: bug\ncreator: nobody\nassignee: mayor\n" +
		"priority: low\nrepo: /dev/null\ntags: [wrong]\ndepends: [mg-9999]\nbudget: 1\nbranch: wrong\n"},
	{"a whole frontmatter block", "---\nid: mg-0000\nassignee: mayor\npriority: low\ntags: [wrong]\n---\n"},
	{"a horizontal rule", "some prose\n\n---\n\nmore prose\n"},
	{"html comment probe", "<!-- probe\n     three lines\n-->\n"},
	{"snooze line", "snooze: 2099-01-01T00:00:00Z\n"},
	{"the mayor's own report", "## 2026-08-12 — observation\n\n" +
		"`mg edit <id> --assignee=blocked:pm-pogo` took effect and verified;\n" +
		"a subsequent `mg edit <id> --append-body-file -` left assignee=[mayor].\n"},
}

func TestAppendBodyPreservesEveryOtherField(t *testing.T) {
	for _, tc := range hostileAppends {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			setupDirs(t, root)

			budget := 4200
			item, err := Create(root, "mg-", "design", "Gated item", []string{},
				WithAssignee("blocked:pm-pogo"),
				WithPriority("high"),
				WithRepo("/Users/daniel/dev/macguffin"),
				WithTags([]string{"macguffin", "silent-failure"}),
				WithBudget(budget),
				WithBody("# Gated item\n\noriginal prose\n"),
			)
			if err != nil {
				t.Fatalf("Create: %v", err)
			}

			before, err := Read(root, item.ID)
			if err != nil {
				t.Fatalf("Read before: %v", err)
			}

			if _, err := Update(root, item.ID, UpdateField{AppendBody: &tc.text}); err != nil {
				t.Fatalf("append: %v", err)
			}

			after, err := Read(root, item.ID)
			if err != nil {
				t.Fatalf("Read after: %v", err)
			}

			// The append is the ONLY thing that may have moved. Every field is
			// named explicitly rather than compared with reflect.DeepEqual, so a
			// field added later is a compile-clean omission that the next reader
			// can see, not a silent pass.
			assertUnchanged(t, "assignee", before.Assignee, after.Assignee)
			assertUnchanged(t, "priority", before.Priority, after.Priority)
			assertUnchanged(t, "type", before.Type, after.Type)
			assertUnchanged(t, "repo", before.Repo, after.Repo)
			assertUnchanged(t, "creator", before.Creator, after.Creator)
			assertUnchanged(t, "id", before.ID, after.ID)
			assertUnchanged(t, "title", before.Title, after.Title)
			assertUnchanged(t, "branch", before.Branch, after.Branch)
			assertUnchanged(t, "snooze", before.SnoozeRaw, after.SnoozeRaw)
			assertUnchanged(t, "tags", strings.Join(before.Tags, ","), strings.Join(after.Tags, ","))
			assertUnchanged(t, "depends", strings.Join(before.Depends, ","), strings.Join(after.Depends, ","))
			assertUnchanged(t, "budget", budgetString(before.Budget), budgetString(after.Budget))

			// And the append did land — otherwise every assertion above passes
			// vacuously on an edit that did nothing.
			if !strings.Contains(after.Body, strings.TrimRight(tc.text, "\n")) {
				t.Errorf("appended text is missing from the stored body:\n%s", after.Body)
			}
			if !strings.Contains(after.Body, "original prose") {
				t.Errorf("append destroyed the prior body:\n%s", after.Body)
			}
		})
	}
}

func assertUnchanged(t *testing.T, field, before, after string) {
	t.Helper()
	if before != after {
		t.Errorf("append changed %s: %q → %q (it should have touched the body only)", field, before, after)
	}
}
