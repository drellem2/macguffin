package flow

import "time"

// AgeBuckets are the four standard buckets used by `--group-by age` and
// `--age-distribution`, in display order.
var AgeBuckets = []string{"<24h", "24h–7d", "7d–30d", ">30d"}

// AgeDistribution counts items across the four standard age buckets.
type AgeDistribution struct {
	LessThan24h    int
	OneDayToWeek   int // 24h–7d
	OneWeekToMonth int // 7d–30d
	OverAMonth     int // >30d
	Total          int
}

// Count returns the count for the given bucket label, or 0 if unknown.
func (d AgeDistribution) Count(bucket string) int {
	switch bucket {
	case "<24h":
		return d.LessThan24h
	case "24h–7d":
		return d.OneDayToWeek
	case "7d–30d":
		return d.OneWeekToMonth
	case ">30d":
		return d.OverAMonth
	}
	return 0
}

// ComputeAgeDistribution histograms the given records into the four buckets.
// Both active and done items are counted — the design's spec talks about
// "items" generally, not just active.
func ComputeAgeDistribution(records []GroupRec) AgeDistribution {
	var d AgeDistribution
	for _, r := range records {
		switch r.AgeBucket {
		case "<24h":
			d.LessThan24h++
		case "24h–7d":
			d.OneDayToWeek++
		case "7d–30d":
			d.OneWeekToMonth++
		default:
			d.OverAMonth++
		}
		d.Total++
	}
	return d
}

// ageBucket maps a duration to the matching bucket label. Negative durations
// (e.g. items with future-dated `created` due to clock skew) land in <24h.
func ageBucket(age time.Duration) string {
	switch {
	case age < 24*time.Hour:
		return "<24h"
	case age < 7*24*time.Hour:
		return "24h–7d"
	case age < 30*24*time.Hour:
		return "7d–30d"
	default:
		return ">30d"
	}
}
