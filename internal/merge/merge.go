package merge

import (
	"cmp"
	"slices"

	"pm2logs/internal/parser"
)

// MergeEntries takes multiple slices of LogEntry and merges them into
// a single slice sorted by pm2_timestamp (ascending).
func MergeEntries(streams ...[]parser.LogEntry) []parser.LogEntry {
	// Count total entries across all streams.
	total := 0
	for _, s := range streams {
		total += len(s)
	}

	// Combine all into one slice.
	merged := make([]parser.LogEntry, 0, total)
	for _, s := range streams {
		merged = append(merged, s...)
	}

	// Sort by pm2_timestamp using parser.GetTimestamp(), preserving
	// original order for entries with identical timestamps.
	slices.SortStableFunc(merged, func(a, b parser.LogEntry) int {
		ta := parser.GetTimestamp(a)
		tb := parser.GetTimestamp(b)
		return cmp.Compare(ta.UnixNano(), tb.UnixNano())
	})

	return merged
}
