package stats

import (
	"sort"
	"strings"
	"testing"
	"time"

	"pm2logs/internal/parser"
)

// --- helpers ---

// makeEntry builds a LogEntry from key-value pairs. Values can be strings,
// map[string]any, or any other type.
func makeEntry(kvs ...any) parser.LogEntry {
	var fields []parser.Field
	for i := 0; i+1 < len(kvs); i += 2 {
		fields = append(fields, parser.Field{
			Key:   kvs[i].(string),
			Value: kvs[i+1],
		})
	}
	return parser.LogEntry{Fields: fields}
}

// makePlainEntry builds a LogEntry representing a plain-text PM2 line.
func makePlainEntry(msg string, ts time.Time, app string) parser.LogEntry {
	return makeEntry(
		"message", msg,
		"pm2_timestamp", ts.Format(time.RFC3339Nano),
		"pm2_app_name", app,
		"pm2_process_id", 0,
		"pm2_type", "out",
	)
}

// makeJSONEntry builds a LogEntry representing a JSON-message PM2 line with
// flattened fields, message, message_json, and pm2_* metadata.
func makeJSONEntry(innerFields map[string]any, rawMsg string, ts time.Time, app string) parser.LogEntry {
	var fields []parser.Field

	// Flattened inner fields (sorted, skip "message").
	var keys []string
	for k := range innerFields {
		if k == "message" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fields = append(fields, parser.Field{Key: k, Value: innerFields[k]})
	}

	// message (raw string).
	fields = append(fields, parser.Field{Key: "message", Value: rawMsg})

	// message_json (parsed object).
	fields = append(fields, parser.Field{Key: "message_json", Value: innerFields})

	// PM2 metadata.
	fields = append(fields,
		parser.Field{Key: "pm2_timestamp", Value: ts.Format(time.RFC3339Nano)},
		parser.Field{Key: "pm2_app_name", Value: app},
		parser.Field{Key: "pm2_process_id", Value: 0},
		parser.Field{Key: "pm2_type", Value: "out"},
	)

	return parser.LogEntry{Fields: fields}
}

// --- GatherSchema tests ---

func TestGatherSchema_MixedPlainAndJSON(t *testing.T) {
	ts := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	entries := []parser.LogEntry{
		makePlainEntry("hello world", ts, "web"),
		makeJSONEntry(
			map[string]any{"level": "info", "class": "App"},
			`{"level":"info","class":"App"}`,
			ts.Add(time.Second), "web",
		),
		makePlainEntry("plain text again", ts.Add(2*time.Second), "web"),
	}

	schema := GatherSchema(entries)

	// "message" should appear for all 3 entries.
	if schema["message"] != 3 {
		t.Errorf("expected message count 3, got %d", schema["message"])
	}

	// "message_json" should appear only for the JSON entry.
	if schema["message_json"] != 1 {
		t.Errorf("expected message_json count 1, got %d", schema["message_json"])
	}

	// Nested paths from message_json.
	if schema["message_json.level"] != 1 {
		t.Errorf("expected message_json.level count 1, got %d", schema["message_json.level"])
	}
	if schema["message_json.class"] != 1 {
		t.Errorf("expected message_json.class count 1, got %d", schema["message_json.class"])
	}
}

func TestGatherSchema_NestedJSON(t *testing.T) {
	ts := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	// Simulate a double-nested JSON message where message_json itself has a
	// message_json with a component field.
	innerInner := map[string]any{
		"component": "AuthService",
		"action":    "login",
	}
	inner := map[string]any{
		"level":        "debug",
		"message_json": innerInner,
	}

	entries := []parser.LogEntry{
		makeJSONEntry(inner, `{"level":"debug","message_json":{"component":"AuthService","action":"login"}}`, ts, "web"),
	}

	schema := GatherSchema(entries)

	// Should have the deeply nested path.
	if schema["message_json.message_json.component"] != 1 {
		t.Errorf("expected message_json.message_json.component count 1, got %d",
			schema["message_json.message_json.component"])
	}
	if schema["message_json.message_json.action"] != 1 {
		t.Errorf("expected message_json.message_json.action count 1, got %d",
			schema["message_json.message_json.action"])
	}

	// Intermediate path should also be counted.
	if schema["message_json.message_json"] != 1 {
		t.Errorf("expected message_json.message_json count 1, got %d",
			schema["message_json.message_json"])
	}
}

func TestGatherSchema_KeysSortable(t *testing.T) {
	ts := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	entries := []parser.LogEntry{
		makeJSONEntry(
			map[string]any{"zebra": "z", "alpha": "a", "middle": "m"},
			`{"zebra":"z","alpha":"a","middle":"m"}`,
			ts, "app",
		),
	}

	schema := GatherSchema(entries)

	var keys []string
	for k := range schema {
		keys = append(keys, k)
	}

	sorted := make([]string, len(keys))
	copy(sorted, keys)
	sort.Strings(sorted)

	// Verify that the keys can be sorted (they are just strings).
	for i, k := range sorted {
		if i > 0 && sorted[i-1] > k {
			t.Errorf("keys not sortable: %q > %q", sorted[i-1], k)
		}
	}
}

// --- walkPaths tests ---

func TestWalkPaths_FlatObject(t *testing.T) {
	obj := map[string]any{
		"name":  "alice",
		"age":   30.0,
		"email": "alice@example.com",
	}

	counts := make(map[string]int)
	walkPaths(obj, "root", counts)

	expected := []string{"root.name", "root.age", "root.email"}
	for _, p := range expected {
		if counts[p] != 1 {
			t.Errorf("expected %s count 1, got %d", p, counts[p])
		}
	}

	if len(counts) != len(expected) {
		t.Errorf("expected %d paths, got %d", len(expected), len(counts))
	}
}

func TestWalkPaths_NestedObject(t *testing.T) {
	obj := map[string]any{
		"user": map[string]any{
			"name": "alice",
			"address": map[string]any{
				"city": "NYC",
			},
		},
		"status": "active",
	}

	counts := make(map[string]int)
	walkPaths(obj, "data", counts)

	expectedPaths := map[string]int{
		"data.user":              1,
		"data.user.name":         1,
		"data.user.address":      1,
		"data.user.address.city": 1,
		"data.status":            1,
	}

	for path, want := range expectedPaths {
		if counts[path] != want {
			t.Errorf("path %q: expected %d, got %d", path, want, counts[path])
		}
	}

	if len(counts) != len(expectedPaths) {
		t.Errorf("expected %d paths, got %d: %v", len(expectedPaths), len(counts), counts)
	}
}

// --- FormatStats tests ---

func TestFormatStats_AlignedTable(t *testing.T) {
	ts1 := time.Date(2025, 3, 15, 12, 4, 52, 0, time.UTC)
	ts2 := time.Date(2025, 3, 15, 12, 32, 31, 0, time.UTC)

	stats := []AppStats{
		{AppName: "caddy", FileCount: 2, LineCount: 142, StartTime: ts1, EndTime: ts2},
		{AppName: "web", FileCount: 2, LineCount: 4998, StartTime: ts1.Add(2 * time.Second), EndTime: ts2},
	}

	output := FormatStats(stats)

	// Should contain a header line.
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (header + 2 data), got %d:\n%s", len(lines), output)
	}

	// Header should contain column names.
	if !strings.Contains(lines[0], "App") {
		t.Error("header missing 'App'")
	}
	if !strings.Contains(lines[0], "Files") {
		t.Error("header missing 'Files'")
	}
	if !strings.Contains(lines[0], "Lines") {
		t.Error("header missing 'Lines'")
	}
	if !strings.Contains(lines[0], "Time Range") {
		t.Error("header missing 'Time Range'")
	}

	// Data lines should contain app names.
	if !strings.Contains(lines[1], "caddy") {
		t.Error("first data line missing 'caddy'")
	}
	if !strings.Contains(lines[2], "web") {
		t.Error("second data line missing 'web'")
	}

	// Time format should be HH:MM:SS for same-day ranges.
	if !strings.Contains(lines[1], "12:04:52") {
		t.Errorf("expected time 12:04:52 in line: %s", lines[1])
	}
	if !strings.Contains(lines[1], "12:32:31") {
		t.Errorf("expected time 12:32:31 in line: %s", lines[1])
	}

	// Check that columns are aligned: the "Files" values should start at the
	// same column position across rows.
	headerFilesIdx := strings.Index(lines[0], "Files")
	row1FilesIdx := strings.Index(lines[1], "2")
	if headerFilesIdx != row1FilesIdx {
		t.Errorf("Files column not aligned: header at %d, data at %d", headerFilesIdx, row1FilesIdx)
	}
}

func TestFormatStats_MultiDayRange(t *testing.T) {
	ts1 := time.Date(2025, 3, 14, 23, 59, 0, 0, time.UTC)
	ts2 := time.Date(2025, 3, 15, 0, 1, 0, 0, time.UTC)

	stats := []AppStats{
		{AppName: "app", FileCount: 1, LineCount: 10, StartTime: ts1, EndTime: ts2},
	}

	output := FormatStats(stats)

	// Should use full date-time format when spanning multiple days.
	if !strings.Contains(output, "2025-03-14") {
		t.Errorf("expected full date in output for multi-day range: %s", output)
	}
	if !strings.Contains(output, "2025-03-15") {
		t.Errorf("expected full date in output for multi-day range: %s", output)
	}
}

func TestFormatStats_Empty(t *testing.T) {
	output := FormatStats(nil)
	if output != "" {
		t.Errorf("expected empty string for nil stats, got %q", output)
	}
}
