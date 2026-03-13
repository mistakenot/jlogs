package merge

import (
	"testing"
	"time"

	"pm2logs/internal/parser"
)

func makeEntry(ts string, msg string) parser.LogEntry {
	return parser.LogEntry{
		Fields: []parser.Field{
			{Key: "message", Value: msg},
			{Key: "pm2_timestamp", Value: ts},
			{Key: "pm2_app_name", Value: "test"},
			{Key: "pm2_process_id", Value: float64(1)},
			{Key: "pm2_type", Value: "out"},
		},
	}
}

func getMsg(entry parser.LogEntry) string {
	for _, f := range entry.Fields {
		if f.Key == "message" {
			if s, ok := f.Value.(string); ok {
				return s
			}
		}
	}
	return ""
}

func TestMergeTwoSortedSlices(t *testing.T) {
	a := []parser.LogEntry{
		makeEntry("2024-01-01T00:00:01Z", "a1"),
		makeEntry("2024-01-01T00:00:03Z", "a2"),
	}
	b := []parser.LogEntry{
		makeEntry("2024-01-01T00:00:02Z", "b1"),
		makeEntry("2024-01-01T00:00:04Z", "b2"),
	}

	merged := MergeEntries(a, b)

	if len(merged) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(merged))
	}

	expected := []string{"a1", "b1", "a2", "b2"}
	for i, want := range expected {
		got := getMsg(merged[i])
		if got != want {
			t.Errorf("entry %d: expected message %q, got %q", i, want, got)
		}
	}
}

func TestMergeThreeInterleavedSlices(t *testing.T) {
	a := []parser.LogEntry{
		makeEntry("2024-01-01T00:00:01Z", "a1"),
		makeEntry("2024-01-01T00:00:04Z", "a2"),
	}
	b := []parser.LogEntry{
		makeEntry("2024-01-01T00:00:02Z", "b1"),
		makeEntry("2024-01-01T00:00:05Z", "b2"),
	}
	c := []parser.LogEntry{
		makeEntry("2024-01-01T00:00:03Z", "c1"),
		makeEntry("2024-01-01T00:00:06Z", "c2"),
	}

	merged := MergeEntries(a, b, c)

	if len(merged) != 6 {
		t.Fatalf("expected 6 entries, got %d", len(merged))
	}

	expected := []string{"a1", "b1", "c1", "a2", "b2", "c2"}
	for i, want := range expected {
		got := getMsg(merged[i])
		if got != want {
			t.Errorf("entry %d: expected message %q, got %q", i, want, got)
		}
	}
}

func TestMergeWithEmptySlice(t *testing.T) {
	a := []parser.LogEntry{
		makeEntry("2024-01-01T00:00:01Z", "a1"),
		makeEntry("2024-01-01T00:00:02Z", "a2"),
	}
	empty := []parser.LogEntry{}

	merged := MergeEntries(a, empty)

	if len(merged) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(merged))
	}

	expected := []string{"a1", "a2"}
	for i, want := range expected {
		got := getMsg(merged[i])
		if got != want {
			t.Errorf("entry %d: expected message %q, got %q", i, want, got)
		}
	}
}

func TestMergeSingleSlice(t *testing.T) {
	a := []parser.LogEntry{
		makeEntry("2024-01-01T00:00:02Z", "a1"),
		makeEntry("2024-01-01T00:00:01Z", "a2"),
	}

	merged := MergeEntries(a)

	if len(merged) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(merged))
	}

	// Should be sorted by timestamp, so a2 (earlier) comes first.
	expected := []string{"a2", "a1"}
	for i, want := range expected {
		got := getMsg(merged[i])
		if got != want {
			t.Errorf("entry %d: expected message %q, got %q", i, want, got)
		}
	}
}

func TestMergePreservesAllFields(t *testing.T) {
	entry := makeEntry("2024-01-01T00:00:01Z", "hello")
	merged := MergeEntries([]parser.LogEntry{entry})

	if len(merged) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(merged))
	}

	result := merged[0]
	if len(result.Fields) != 5 {
		t.Fatalf("expected 5 fields, got %d", len(result.Fields))
	}

	checks := map[string]any{
		"message":        "hello",
		"pm2_timestamp":  "2024-01-01T00:00:01Z",
		"pm2_app_name":   "test",
		"pm2_process_id": float64(1),
		"pm2_type":       "out",
	}

	for _, f := range result.Fields {
		want, ok := checks[f.Key]
		if !ok {
			t.Errorf("unexpected field %q", f.Key)
			continue
		}
		if f.Value != want {
			t.Errorf("field %q: expected %v, got %v", f.Key, want, f.Value)
		}
	}

	// Verify the timestamp parses correctly.
	ts := parser.GetTimestamp(result)
	expectedTime, _ := time.Parse(time.RFC3339, "2024-01-01T00:00:01Z")
	if !ts.Equal(expectedTime) {
		t.Errorf("expected timestamp %v, got %v", expectedTime, ts)
	}
}
