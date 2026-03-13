package parser

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

const validPM2Line = `{"message":"hello world\n","timestamp":"2026-03-13T12:04:54.562Z","type":"out","process_id":8,"app_name":"web"}`

func TestParsePM2Line_Valid(t *testing.T) {
	pm2, err := ParsePM2Line([]byte(validPM2Line))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pm2.Message != "hello world\n" {
		t.Errorf("Message = %q, want %q", pm2.Message, "hello world\n")
	}
	if pm2.Type != "out" {
		t.Errorf("Type = %q, want %q", pm2.Type, "out")
	}
	if pm2.ProcessID != 8 {
		t.Errorf("ProcessID = %d, want 8", pm2.ProcessID)
	}
	if pm2.AppName != "web" {
		t.Errorf("AppName = %q, want %q", pm2.AppName, "web")
	}
	expected := time.Date(2026, 3, 13, 12, 4, 54, 562000000, time.UTC)
	if !pm2.Timestamp.Equal(expected) {
		t.Errorf("Timestamp = %v, want %v", pm2.Timestamp, expected)
	}
}

func TestParsePM2Line_MissingAppName(t *testing.T) {
	line := `{"message":"hi","timestamp":"2026-03-13T12:00:00Z","type":"out","process_id":1}`
	_, err := ParsePM2Line([]byte(line))
	if err == nil {
		t.Fatal("expected error for missing app_name")
	}
	if !strings.Contains(err.Error(), "app_name") {
		t.Errorf("error should mention app_name: %v", err)
	}
}

func TestParsePM2Line_MissingTimestamp(t *testing.T) {
	line := `{"message":"hi","type":"out","process_id":1,"app_name":"web"}`
	_, err := ParsePM2Line([]byte(line))
	if err == nil {
		t.Fatal("expected error for missing timestamp")
	}
	if !strings.Contains(err.Error(), "timestamp") {
		t.Errorf("error should mention timestamp: %v", err)
	}
}

func TestIsPM2Line_MongoDB(t *testing.T) {
	mongo := `{"t":{"$date":"2026-03-13T12:00:00Z"},"s":"I","c":"-","id":123,"ctx":"main","msg":"test"}`
	if IsPM2Line([]byte(mongo)) {
		t.Error("MongoDB JSON should not be detected as PM2 line")
	}
}

func TestIsPM2Line_PlainText(t *testing.T) {
	if IsPM2Line([]byte("Usage: logload [options]")) {
		t.Error("plain text should not be detected as PM2 line")
	}
}

func TestIsPM2Line_Empty(t *testing.T) {
	if IsPM2Line([]byte("")) {
		t.Error("empty line should not be detected as PM2 line")
	}
}

func TestIsPM2Line_Valid(t *testing.T) {
	if !IsPM2Line([]byte(validPM2Line)) {
		t.Error("valid PM2 line should be detected")
	}
}

func TestProcessLine_PlainText(t *testing.T) {
	pm2 := PM2Line{
		Message:   "hello world",
		Timestamp: time.Date(2026, 3, 13, 12, 0, 0, 0, time.UTC),
		Type:      "out",
		ProcessID: 1,
		AppName:   "web",
	}
	entries := ProcessLine(pm2)

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	entry := entries[0]

	// Should have message + 4 pm2_* fields = 5 total.
	if len(entry.Fields) != 5 {
		t.Fatalf("expected 5 fields, got %d: %v", len(entry.Fields), fieldKeys(entry))
	}

	// No message_json for plain text.
	if v := FieldValue(entry, "message_json"); v != nil {
		t.Error("plain text should not have message_json")
	}

	if v := FieldValue(entry, "message"); v != "hello world" {
		t.Errorf("message = %v, want %q", v, "hello world")
	}

	// pm2_* fields present.
	for _, k := range []string{"pm2_timestamp", "pm2_app_name", "pm2_process_id", "pm2_type"} {
		if v := FieldValue(entry, k); v == nil {
			t.Errorf("missing %s", k)
		}
	}
}

func TestProcessLine_JSONMessage(t *testing.T) {
	innerJSON := `{"class":"MyService","level":"info","timestamp":"2026-03-13T12:05:00Z"}`
	pm2 := PM2Line{
		Message:   innerJSON,
		Timestamp: time.Date(2026, 3, 13, 12, 5, 0, 0, time.UTC),
		Type:      "out",
		ProcessID: 2,
		AppName:   "api",
	}
	entries := ProcessLine(pm2)

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	entry := entries[0]

	// Flattened fields: class, level, timestamp (3) + message + message_json + 4 pm2_* = 9
	if len(entry.Fields) != 9 {
		t.Fatalf("expected 9 fields, got %d: %v", len(entry.Fields), fieldKeys(entry))
	}

	// Flattened fields present.
	if v := FieldValue(entry, "class"); v != "MyService" {
		t.Errorf("class = %v, want MyService", v)
	}
	if v := FieldValue(entry, "level"); v != "info" {
		t.Errorf("level = %v, want info", v)
	}

	// message is the raw string.
	if v := FieldValue(entry, "message"); v != innerJSON {
		t.Errorf("message should be raw JSON string")
	}

	// message_json is present.
	msgJSON, ok := FieldValue(entry, "message_json").(map[string]any)
	if !ok {
		t.Fatal("message_json should be map[string]any")
	}
	if msgJSON["class"] != "MyService" {
		t.Errorf("message_json.class = %v", msgJSON["class"])
	}
}

func TestProcessLine_CollisionMessageField(t *testing.T) {
	// Inner JSON has a "message" field — it should NOT be flattened.
	innerJSON := `{"level":"warn","message":"inner msg"}`
	pm2 := PM2Line{
		Message:   innerJSON,
		Timestamp: time.Date(2026, 3, 13, 12, 0, 0, 0, time.UTC),
		Type:      "err",
		ProcessID: 3,
		AppName:   "svc",
	}
	entries := ProcessLine(pm2)

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	entry := entries[0]

	// message at top level should be the raw JSON string, not "inner msg".
	if v := FieldValue(entry, "message"); v != innerJSON {
		t.Errorf("message should be raw JSON string, got %v", v)
	}

	// The inner "message" should still be accessible via message_json.
	msgJSON, ok := FieldValue(entry, "message_json").(map[string]any)
	if !ok {
		t.Fatal("message_json should be map")
	}
	if msgJSON["message"] != "inner msg" {
		t.Errorf("message_json.message = %v, want 'inner msg'", msgJSON["message"])
	}

	// "level" should be flattened.
	if v := FieldValue(entry, "level"); v != "warn" {
		t.Errorf("level = %v, want warn", v)
	}
}

func TestProcessLine_DoubleNestedJSON(t *testing.T) {
	// Build a message where the inner JSON's "message" field is itself a JSON
	// string, triggering recursive unwrapping.
	//
	// Level 2 (innermost): {"detail":"deep","code":42}
	// Level 1: {"level":"error","message":"{\"detail\":\"deep\",\"code\":42}"}
	// PM2 message is level 1 as a string.
	inner2 := `{"detail":"deep","code":42}`
	// Escape inner2 for embedding inside a JSON string value.
	inner2Escaped := strings.ReplaceAll(inner2, `"`, `\"`)
	inner1JSON := `{"level":"error","message":"` + inner2Escaped + `"}`

	pm2 := PM2Line{
		Message:   inner1JSON,
		Timestamp: time.Date(2026, 3, 13, 12, 0, 0, 0, time.UTC),
		Type:      "err",
		ProcessID: 4,
		AppName:   "nested",
	}
	entries := ProcessLine(pm2)

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	entry := entries[0]

	// First-level "level" should be flattened.
	if v := FieldValue(entry, "level"); v != "error" {
		t.Errorf("level = %v, want error", v)
	}

	// message_json should exist at the top level.
	msgJSON, ok := FieldValue(entry, "message_json").(map[string]any)
	if !ok {
		t.Fatal("message_json should be map")
	}

	// The first-level inner object's "message" field is a JSON string, so
	// unwrapMessageJSON should have added message_json inside it.
	if msgJSON["message"] != inner2 {
		t.Errorf("message_json.message = %v, want %q", msgJSON["message"], inner2)
	}

	innerMsgJSON, ok := msgJSON["message_json"].(map[string]any)
	if !ok {
		t.Fatal("message_json.message_json should be map (double-nested unwrap)")
	}
	if innerMsgJSON["detail"] != "deep" {
		t.Errorf("inner detail = %v, want deep", innerMsgJSON["detail"])
	}
	// code is float64 from JSON unmarshal.
	if innerMsgJSON["code"] != float64(42) {
		t.Errorf("inner code = %v, want 42", innerMsgJSON["code"])
	}
}

func TestPM2TimestampAlwaysPresent(t *testing.T) {
	pm2 := PM2Line{
		Message:   "test",
		Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Type:      "out",
		ProcessID: 0,
		AppName:   "app",
	}
	entries := ProcessLine(pm2)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	v := FieldValue(entries[0], "pm2_timestamp")
	if v == nil {
		t.Fatal("pm2_timestamp must always be present")
	}
}

func TestMessagePreserved(t *testing.T) {
	origMsg := `{"key":"value"}`
	pm2 := PM2Line{
		Message:   origMsg,
		Timestamp: time.Now(),
		Type:      "out",
		ProcessID: 0,
		AppName:   "app",
	}
	entries := ProcessLine(pm2)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if v := FieldValue(entries[0], "message"); v != origMsg {
		t.Errorf("message = %v, want %q", v, origMsg)
	}
}

func TestMarshalEntry_Compact(t *testing.T) {
	entry := LogEntry{
		Fields: []Field{
			{Key: "level", Value: "info"},
			{Key: "message", Value: "hello"},
			{Key: "pm2_type", Value: "out"},
		},
	}
	b, err := MarshalEntry(entry)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should be compact JSON with preserved order.
	expected := `{"level":"info","message":"hello","pm2_type":"out"}`
	if string(b) != expected {
		t.Errorf("got %s, want %s", string(b), expected)
	}

	// Verify it's valid JSON.
	var check map[string]any
	if err := json.Unmarshal(b, &check); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
}

func TestGetTimestamp(t *testing.T) {
	ts := time.Date(2026, 3, 13, 12, 0, 0, 0, time.UTC)
	entry := LogEntry{
		Fields: []Field{
			{Key: "message", Value: "hi"},
			{Key: "pm2_timestamp", Value: ts.Format(time.RFC3339Nano)},
		},
	}
	got := GetTimestamp(entry)
	if !got.Equal(ts) {
		t.Errorf("GetTimestamp = %v, want %v", got, ts)
	}
}

func TestTestdata_Line1(t *testing.T) {
	data, err := os.ReadFile("/home/vscode/src/pm2logs/testdata/web-out.log")
	if err != nil {
		t.Skipf("testdata not available: %v", err)
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) < 1 {
		t.Skip("testdata empty")
	}

	line := lines[0]
	if !IsPM2Line([]byte(line)) {
		t.Fatal("line 1 should be valid PM2")
	}
	pm2, err := ParsePM2Line([]byte(line))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if pm2.AppName != "web" {
		t.Errorf("AppName = %q, want web", pm2.AppName)
	}
	entries := ProcessLine(pm2)
	if len(entries) < 1 {
		t.Fatal("expected at least 1 entry")
	}
	entry := entries[0]

	// Line 1 is plain text ("yarn run v1.22.22\n"), so no message_json.
	if v := FieldValue(entry, "message_json"); v != nil {
		t.Error("line 1 should be plain text, no message_json")
	}

	b, err := MarshalEntry(entry)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	var check map[string]any
	if err := json.Unmarshal(b, &check); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
}

func TestTestdata_Line17(t *testing.T) {
	data, err := os.ReadFile("/home/vscode/src/pm2logs/testdata/web-out.log")
	if err != nil {
		t.Skipf("testdata not available: %v", err)
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) < 17 {
		t.Skipf("testdata has only %d lines", len(lines))
	}

	line := lines[16] // 0-indexed
	if !IsPM2Line([]byte(line)) {
		t.Fatal("line 17 should be valid PM2")
	}
	pm2, err := ParsePM2Line([]byte(line))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	entries := ProcessLine(pm2)
	if len(entries) < 1 {
		t.Fatal("expected at least 1 entry")
	}
	entry := entries[0]

	// Line 17 has a JSON message with class, level, message, timestamp.
	if v := FieldValue(entry, "message_json"); v == nil {
		t.Error("line 17 should have message_json (JSON message)")
	}
	if v := FieldValue(entry, "class"); v == nil {
		t.Error("line 17 should have flattened 'class' field")
	}
	if v := FieldValue(entry, "level"); v == nil {
		t.Error("line 17 should have flattened 'level' field")
	}

	// message should be the raw JSON string.
	if v, ok := FieldValue(entry, "message").(string); !ok || !strings.HasPrefix(strings.TrimSpace(v), "{") {
		t.Errorf("message should be raw JSON string, got %v", v)
	}

	b, err := MarshalEntry(entry)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	var check map[string]any
	if err := json.Unmarshal(b, &check); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
}

// --- Multi-line batching tests ---

func TestProcessLine_BatchedJSON_TwoObjects(t *testing.T) {
	// Real line from caddy-error.log: two JSON objects batched in one message.
	pm2 := PM2Line{
		Message:   "{\"level\":\"info\",\"ts\":1773403498.3267288,\"msg\":\"maxprocs: Leaving GOMAXPROCS=5: CPU quota undefined\"}\n{\"level\":\"info\",\"ts\":1773403498.3272717,\"msg\":\"GOMEMLIMIT is updated\",\"package\":\"github.com/KimMachineGun/automemlimit/memlimit\",\"GOMEMLIMIT\":7409605017,\"previous\":9223372036854775807}\n",
		Timestamp: time.Date(2026, 3, 13, 12, 4, 58, 330000000, time.UTC),
		Type:      "err",
		ProcessID: 10,
		AppName:   "caddy",
	}
	entries := ProcessLine(pm2)

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	// Both entries should have pm2 metadata.
	for i, entry := range entries {
		if v := FieldValue(entry, "pm2_app_name"); v != "caddy" {
			t.Errorf("entry[%d] pm2_app_name = %v, want caddy", i, v)
		}
		if v := FieldValue(entry, "pm2_type"); v != "err" {
			t.Errorf("entry[%d] pm2_type = %v, want err", i, v)
		}
		if v := FieldValue(entry, "pm2_timestamp"); v == nil {
			t.Errorf("entry[%d] missing pm2_timestamp", i)
		}
	}

	// First entry should have msg about GOMAXPROCS.
	msgJSON0, ok := FieldValue(entries[0], "message_json").(map[string]any)
	if !ok {
		t.Fatal("entry[0] should have message_json")
	}
	if msgJSON0["msg"] != "maxprocs: Leaving GOMAXPROCS=5: CPU quota undefined" {
		t.Errorf("entry[0] msg = %v", msgJSON0["msg"])
	}

	// Second entry should have msg about GOMEMLIMIT.
	msgJSON1, ok := FieldValue(entries[1], "message_json").(map[string]any)
	if !ok {
		t.Fatal("entry[1] should have message_json")
	}
	if msgJSON1["msg"] != "GOMEMLIMIT is updated" {
		t.Errorf("entry[1] msg = %v", msgJSON1["msg"])
	}
}

func TestProcessLine_BatchedJSON_FiveObjects(t *testing.T) {
	// Real line from caddy-error.log line 14: five JSON objects batched.
	pm2 := PM2Line{
		Message:   "{\"level\":\"info\",\"ts\":1773404885.1951208,\"msg\":\"shutting down\",\"signal\":\"SIGINT\"}\n{\"level\":\"warn\",\"ts\":1773404885.195165,\"msg\":\"exiting; byeee!! 👋\",\"signal\":\"SIGINT\"}\n{\"level\":\"info\",\"ts\":1773404885.1952288,\"logger\":\"http\",\"msg\":\"servers shutting down with eternal grace period\"}\n{\"level\":\"info\",\"ts\":1773404885.1956258,\"logger\":\"admin\",\"msg\":\"stopped previous server\",\"address\":\"localhost:2019\"}\n{\"level\":\"info\",\"ts\":1773404885.1956508,\"msg\":\"shutdown complete\",\"signal\":\"SIGINT\",\"exit_code\":0}\n",
		Timestamp: time.Date(2026, 3, 13, 12, 28, 5, 198000000, time.UTC),
		Type:      "err",
		ProcessID: 10,
		AppName:   "caddy",
	}
	entries := ProcessLine(pm2)

	if len(entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(entries))
	}

	// Verify order is preserved: first is "shutting down", last is "shutdown complete".
	msgJSON0, _ := FieldValue(entries[0], "message_json").(map[string]any)
	if msgJSON0["msg"] != "shutting down" {
		t.Errorf("entry[0] msg = %v, want 'shutting down'", msgJSON0["msg"])
	}

	msgJSON4, _ := FieldValue(entries[4], "message_json").(map[string]any)
	if msgJSON4["msg"] != "shutdown complete" {
		t.Errorf("entry[4] msg = %v, want 'shutdown complete'", msgJSON4["msg"])
	}

	// All should share the same pm2_timestamp.
	ts0 := FieldValue(entries[0], "pm2_timestamp")
	for i := 1; i < 5; i++ {
		if FieldValue(entries[i], "pm2_timestamp") != ts0 {
			t.Errorf("entry[%d] has different pm2_timestamp", i)
		}
	}
}

func TestProcessLine_BatchedPlainText(t *testing.T) {
	// Real line from db-error.log: two plain text lines batched.
	pm2 := PM2Line{
		Message:   "2026-03-13 12:04:57.793 UTC [1] LOG:  listening on IPv4 address \"0.0.0.0\", port 5432\n2026-03-13 12:04:57.793 UTC [1] LOG:  listening on IPv6 address \"::\", port 5432\n",
		Timestamp: time.Date(2026, 3, 13, 12, 4, 57, 794000000, time.UTC),
		Type:      "err",
		ProcessID: 9,
		AppName:   "db",
	}
	entries := ProcessLine(pm2)

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	// Plain text — no message_json.
	for i, entry := range entries {
		if v := FieldValue(entry, "message_json"); v != nil {
			t.Errorf("entry[%d] should not have message_json (plain text)", i)
		}
		if v := FieldValue(entry, "pm2_app_name"); v != "db" {
			t.Errorf("entry[%d] pm2_app_name = %v, want db", i, v)
		}
	}

	// Check message content and order.
	msg0, _ := FieldValue(entries[0], "message").(string)
	if !strings.Contains(msg0, "IPv4") {
		t.Errorf("entry[0] message should contain IPv4, got %q", msg0)
	}
	msg1, _ := FieldValue(entries[1], "message").(string)
	if !strings.Contains(msg1, "IPv6") {
		t.Errorf("entry[1] message should contain IPv6, got %q", msg1)
	}
}

func TestProcessLine_SingleLineUnchanged(t *testing.T) {
	// A normal single-line message should still return exactly 1 entry.
	pm2 := PM2Line{
		Message:   "hello world",
		Timestamp: time.Date(2026, 3, 13, 12, 0, 0, 0, time.UTC),
		Type:      "out",
		ProcessID: 1,
		AppName:   "web",
	}
	entries := ProcessLine(pm2)

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if v := FieldValue(entries[0], "message"); v != "hello world" {
		t.Errorf("message = %v, want 'hello world'", v)
	}
}

func TestProcessLine_TrailingNewlineNotExtraSplit(t *testing.T) {
	// A single message with just a trailing newline should NOT produce an extra empty entry.
	pm2 := PM2Line{
		Message:   "hello world\n",
		Timestamp: time.Date(2026, 3, 13, 12, 0, 0, 0, time.UTC),
		Type:      "out",
		ProcessID: 1,
		AppName:   "web",
	}
	entries := ProcessLine(pm2)

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (trailing newline should not create extra), got %d", len(entries))
	}
}

func TestProcessLine_BatchedMarshalValid(t *testing.T) {
	// Every entry from a batched split should produce valid compact JSON.
	pm2 := PM2Line{
		Message:   "{\"level\":\"info\",\"msg\":\"one\"}\n{\"level\":\"warn\",\"msg\":\"two\"}\n",
		Timestamp: time.Date(2026, 3, 13, 12, 0, 0, 0, time.UTC),
		Type:      "err",
		ProcessID: 1,
		AppName:   "test",
	}
	entries := ProcessLine(pm2)

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	for i, entry := range entries {
		b, err := MarshalEntry(entry)
		if err != nil {
			t.Fatalf("entry[%d] marshal error: %v", i, err)
		}
		var check map[string]any
		if err := json.Unmarshal(b, &check); err != nil {
			t.Fatalf("entry[%d] output is not valid JSON: %v", i, err)
		}
	}
}

// fieldKeys is a test helper to list field keys.
func fieldKeys(e LogEntry) []string {
	keys := make([]string, len(e.Fields))
	for i, f := range e.Fields {
		keys[i] = f.Key
	}
	return keys
}
