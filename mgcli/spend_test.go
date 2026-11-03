package mgcli

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/drellem2/macguffin/internal/spend"
)

func mkGroup(key string, input, cacheRead, cacheCreate, output, items int) spend.Group {
	return spend.Group{
		Key: key,
		Totals: spend.Totals{
			ItemCount:   items,
			Input:       input,
			CacheRead:   cacheRead,
			CacheCreate: cacheCreate,
			Output:      output,
		},
	}
}

func TestSumGroups_ColumnSums(t *testing.T) {
	groups := []spend.Group{
		mkGroup("mg-aaa", 10, 100, 5, 20, 1),
		mkGroup("mg-bbb", 3, 30, 1, 7, 1),
	}
	tot := sumGroups(groups)
	if tot.Input != 13 || tot.CacheRead != 130 || tot.CacheCreate != 6 || tot.Output != 27 {
		t.Errorf("component sums wrong: %+v", tot)
	}
	if tot.ItemCount != 2 {
		t.Errorf("ItemCount = %d, want 2", tot.ItemCount)
	}
	// TotalIn = input + cache_read + cache_create; TotalOut = output.
	if tot.TotalIn() != 13+130+6 {
		t.Errorf("TotalIn = %d, want %d", tot.TotalIn(), 13+130+6)
	}
	if tot.TotalOut() != 27 {
		t.Errorf("TotalOut = %d, want 27", tot.TotalOut())
	}
}

func TestSumGroups_Empty(t *testing.T) {
	tot := sumGroups(nil)
	if tot.TotalIn() != 0 || tot.TotalOut() != 0 || tot.ItemCount != 0 {
		t.Errorf("empty sum should be zero, got %+v", tot)
	}
}

// The grand-total key must be uppercase so it can never collide with a real
// group key (item ids, tags, repos, agents are all lowercase).
func TestSpendTotalKey_IsReservedUppercase(t *testing.T) {
	if spendTotalKey != "TOTAL" {
		t.Errorf("spendTotalKey = %q, want TOTAL", spendTotalKey)
	}
}

// captureSpendStdout runs fn with os.Stdout redirected and returns what it wrote.
func captureSpendStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = old }()
	fn()
	w.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// --by tag is not a partitioning axis (an item with N tags sits in N rows),
// so it gets no TOTAL; every other axis keeps it (drellem2/macguffin#30).
func TestHasGrandTotal(t *testing.T) {
	for _, by := range []string{"item", "agent", "repo", "priority", "assignee", "tag:ux"} {
		if !hasGrandTotal(by) {
			t.Errorf("hasGrandTotal(%q) = false, want true", by)
		}
	}
	if hasGrandTotal("tag") {
		t.Error("hasGrandTotal(\"tag\") = true, want false")
	}
}

func TestWriteSpendJSON_TotalIsLastElementExceptByTag(t *testing.T) {
	groups := []spend.Group{
		mkGroup("ux", 10, 0, 0, 1, 1),
		mkGroup("infra", 10, 0, 0, 1, 1), // the same item, tagged twice
	}
	decode := func(by string) []jsonGroup {
		out := captureSpendStdout(t, func() {
			if err := writeSpendJSON(groups, by); err != nil {
				t.Fatal(err)
			}
		})
		var got []jsonGroup
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("--json is no longer an array of groups: %v\n%s", err, out)
		}
		return got
	}

	got := decode("item")
	if len(got) != 3 || got[2].Key != spendTotalKey || got[2].TotalIn != 20 {
		t.Errorf("--by item: want 2 groups + trailing TOTAL(total_in=20), got %+v", got)
	}

	got = decode("tag")
	if len(got) != 2 {
		t.Fatalf("--by tag: want 2 groups and no TOTAL, got %+v", got)
	}
	for _, g := range got {
		if g.Key == spendTotalKey {
			t.Errorf("--by tag emitted a TOTAL object: %+v", g)
		}
	}
}

func TestWriteSpendTable_NoTotalRowByTag(t *testing.T) {
	groups := []spend.Group{mkGroup("ux", 10, 0, 0, 1, 1)}
	if out := captureSpendStdout(t, func() { writeSpendTable(groups, "tag") }); strings.Contains(out, "\n"+spendTotalKey+" ") {
		t.Errorf("--by tag table has a TOTAL row:\n%s", out)
	}
	if out := captureSpendStdout(t, func() { writeSpendTable(groups, "item") }); !strings.Contains(out, "\n"+spendTotalKey+" ") {
		t.Errorf("--by item table lost its TOTAL row:\n%s", out)
	}
}
