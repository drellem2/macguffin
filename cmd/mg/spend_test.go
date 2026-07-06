package main

import (
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
