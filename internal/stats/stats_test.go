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

	schema := GatherSchema(entries, 20)

	// "message" should appear for all 3 entries.
	if schema["message"].Count != 3 {
		t.Errorf("expected message count 3, got %d", schema["message"].Count)
	}

	// "message_json" should appear only for the JSON entry.
	if schema["message_json"].Count != 1 {
		t.Errorf("expected message_json count 1, got %d", schema["message_json"].Count)
	}

	// Nested paths from message_json.
	if schema["message_json.level"].Count != 1 {
		t.Errorf("expected message_json.level count 1, got %d", schema["message_json.level"].Count)
	}
	if schema["message_json.class"].Count != 1 {
		t.Errorf("expected message_json.class count 1, got %d", schema["message_json.class"].Count)
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

	schema := GatherSchema(entries, 20)

	// Should have the deeply nested path.
	if schema["message_json.message_json.component"].Count != 1 {
		t.Errorf("expected message_json.message_json.component count 1, got %d",
			schema["message_json.message_json.component"].Count)
	}
	if schema["message_json.message_json.action"].Count != 1 {
		t.Errorf("expected message_json.message_json.action count 1, got %d",
			schema["message_json.message_json.action"].Count)
	}

	// Intermediate path should also be counted.
	if schema["message_json.message_json"].Count != 1 {
		t.Errorf("expected message_json.message_json count 1, got %d",
			schema["message_json.message_json"].Count)
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

	schema := GatherSchema(entries, 20)

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

func TestGatherSchema_ValuesTracking(t *testing.T) {
	ts := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	entries := []parser.LogEntry{
		makeJSONEntry(
			map[string]any{"level": "info", "active": true},
			`{"level":"info","active":true}`,
			ts, "web",
		),
		makeJSONEntry(
			map[string]any{"level": "error", "active": false},
			`{"level":"error","active":false}`,
			ts.Add(time.Second), "web",
		),
		makeJSONEntry(
			map[string]any{"level": "info", "active": true},
			`{"level":"info","active":true}`,
			ts.Add(2*time.Second), "web",
		),
	}

	schema := GatherSchema(entries, 20)

	// "level" should have 2 distinct values: "info" and "error".
	fs := schema["level"]
	if fs == nil {
		t.Fatal("expected 'level' in schema")
	}
	if fs.Count != 3 {
		t.Errorf("expected level count 3, got %d", fs.Count)
	}
	if len(fs.Values) != 2 {
		t.Errorf("expected 2 distinct values for level, got %d: %v", len(fs.Values), fs.Values)
	}

	// "active" should have 2 distinct values: true and false.
	fs = schema["active"]
	if fs == nil {
		t.Fatal("expected 'active' in schema")
	}
	if len(fs.Values) != 2 {
		t.Errorf("expected 2 distinct values for active, got %d: %v", len(fs.Values), fs.Values)
	}
}

func TestGatherSchema_ValuesExceedThreshold(t *testing.T) {
	ts := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	// Create entries with 5 distinct levels, but set maxValues=3.
	var entries []parser.LogEntry
	levels := []string{"info", "warn", "error", "debug", "trace"}
	for i, lvl := range levels {
		entries = append(entries, makeJSONEntry(
			map[string]any{"level": lvl},
			`{"level":"`+lvl+`"}`,
			ts.Add(time.Duration(i)*time.Second), "web",
		))
	}

	schema := GatherSchema(entries, 3)

	fs := schema["level"]
	if fs == nil {
		t.Fatal("expected 'level' in schema")
	}
	if fs.Count != 5 {
		t.Errorf("expected count 5, got %d", fs.Count)
	}
	// Values should be nil because 5 > 3.
	if fs.Values != nil {
		t.Errorf("expected nil values when exceeding threshold, got %v", fs.Values)
	}
}

func TestGatherSchema_NumbersNotTracked(t *testing.T) {
	ts := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	entries := []parser.LogEntry{
		makeJSONEntry(
			map[string]any{"status": 200.0},
			`{"status":200}`,
			ts, "web",
		),
	}

	schema := GatherSchema(entries, 20)

	fs := schema["status"]
	if fs == nil {
		t.Fatal("expected 'status' in schema")
	}
	// Numbers should not be tracked.
	if fs.Values != nil {
		t.Errorf("expected nil values for numeric field, got %v", fs.Values)
	}
}

func TestGatherSchema_EmptyStringsSkipped(t *testing.T) {
	ts := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	entries := []parser.LogEntry{
		makeJSONEntry(
			map[string]any{"tag": ""},
			`{"tag":""}`,
			ts, "web",
		),
		makeJSONEntry(
			map[string]any{"tag": "important"},
			`{"tag":"important"}`,
			ts.Add(time.Second), "web",
		),
	}

	schema := GatherSchema(entries, 20)

	fs := schema["tag"]
	if fs == nil {
		t.Fatal("expected 'tag' in schema")
	}
	if fs.Count != 2 {
		t.Errorf("expected count 2, got %d", fs.Count)
	}
	// Only "important" would be tracked (empty string skipped), but single-value
	// fields are dropped since they're not useful for filtering.
	if fs.Values != nil {
		t.Errorf("expected nil values for single-value field, got %v", fs.Values)
	}
}

func TestGatherSchema_DisableValueTracking(t *testing.T) {
	ts := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	entries := []parser.LogEntry{
		makeJSONEntry(
			map[string]any{"level": "info"},
			`{"level":"info"}`,
			ts, "web",
		),
	}

	schema := GatherSchema(entries, 0)

	fs := schema["level"]
	if fs == nil {
		t.Fatal("expected 'level' in schema")
	}
	if fs.Values != nil {
		t.Errorf("expected nil values when tracking disabled, got %v", fs.Values)
	}
}

func TestGatherSchema_LongStringsSkipped(t *testing.T) {
	ts := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	longVal := strings.Repeat("x", 101)
	entries := []parser.LogEntry{
		makeJSONEntry(
			map[string]any{"token": longVal, "level": "info"},
			`{"token":"...","level":"info"}`,
			ts, "web",
		),
		makeJSONEntry(
			map[string]any{"token": longVal, "level": "error"},
			`{"token":"...","level":"error"}`,
			ts.Add(time.Second), "web",
		),
	}

	schema := GatherSchema(entries, 20)

	// Long strings should not be tracked.
	if schema["token"].Values != nil {
		t.Errorf("expected nil values for long string field, got %v", schema["token"].Values)
	}

	// Short strings should still be tracked.
	if len(schema["level"].Values) != 2 {
		t.Errorf("expected 2 values for level, got %d", len(schema["level"].Values))
	}
}

func TestGatherSchema_SingleValueDropped(t *testing.T) {
	ts := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	entries := []parser.LogEntry{
		makeJSONEntry(
			map[string]any{"env": "prod"},
			`{"env":"prod"}`,
			ts, "web",
		),
		makeJSONEntry(
			map[string]any{"env": "prod"},
			`{"env":"prod"}`,
			ts.Add(time.Second), "web",
		),
	}

	schema := GatherSchema(entries, 20)

	// Single distinct value — not useful for filtering.
	fs := schema["env"]
	if fs == nil {
		t.Fatal("expected 'env' in schema")
	}
	if fs.Count != 2 {
		t.Errorf("expected count 2, got %d", fs.Count)
	}
	if fs.Values != nil {
		t.Errorf("expected nil values for single-value field, got %v", fs.Values)
	}
}

// --- walkPaths tests ---

func TestWalkPaths_FlatObject(t *testing.T) {
	obj := map[string]any{
		"name":  "alice",
		"age":   30.0,
		"email": "alice@example.com",
	}

	schema := make(map[string]*FieldSchema)
	walkPaths(obj, "root", schema, 20)

	expected := []string{"root.name", "root.age", "root.email"}
	for _, p := range expected {
		if schema[p] == nil || schema[p].Count != 1 {
			count := 0
			if schema[p] != nil {
				count = schema[p].Count
			}
			t.Errorf("expected %s count 1, got %d", p, count)
		}
	}

	if len(schema) != len(expected) {
		t.Errorf("expected %d paths, got %d", len(expected), len(schema))
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

	schema := make(map[string]*FieldSchema)
	walkPaths(obj, "data", schema, 20)

	expectedPaths := map[string]int{
		"data.user":              1,
		"data.user.name":         1,
		"data.user.address":      1,
		"data.user.address.city": 1,
		"data.status":            1,
	}

	for path, want := range expectedPaths {
		fs := schema[path]
		got := 0
		if fs != nil {
			got = fs.Count
		}
		if got != want {
			t.Errorf("path %q: expected %d, got %d", path, want, got)
		}
	}

	if len(schema) != len(expectedPaths) {
		t.Errorf("expected %d paths, got %d: %v", len(expectedPaths), len(schema), schema)
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
