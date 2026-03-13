package parser

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// PM2Line represents a parsed PM2 JSON log line.
type PM2Line struct {
	Message   string
	Timestamp time.Time
	Type      string // "out" or "err"
	ProcessID int
	AppName   string
}

// LogEntry holds an ordered list of key-value pairs.
type LogEntry struct {
	Fields []Field
}

// Field is a single key-value pair in a LogEntry.
type Field struct {
	Key   string
	Value any
}

// ParsePM2Line parses raw JSON into a PM2Line. Returns an error if any of the
// five required fields (message, timestamp, type, process_id, app_name) are
// missing or have the wrong type.
func ParsePM2Line(line []byte) (PM2Line, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		return PM2Line{}, fmt.Errorf("invalid JSON: %w", err)
	}

	required := []string{"message", "timestamp", "type", "process_id", "app_name"}
	for _, k := range required {
		if _, ok := raw[k]; !ok {
			return PM2Line{}, fmt.Errorf("missing required field %q", k)
		}
	}

	var msg string
	if err := json.Unmarshal(raw["message"], &msg); err != nil {
		return PM2Line{}, fmt.Errorf("field message: %w", err)
	}

	var tsStr string
	if err := json.Unmarshal(raw["timestamp"], &tsStr); err != nil {
		return PM2Line{}, fmt.Errorf("field timestamp: %w", err)
	}
	ts, err := time.Parse(time.RFC3339, tsStr)
	if err != nil {
		// Try RFC3339Nano as well.
		ts, err = time.Parse(time.RFC3339Nano, tsStr)
		if err != nil {
			return PM2Line{}, fmt.Errorf("field timestamp: %w", err)
		}
	}

	var typ string
	if err := json.Unmarshal(raw["type"], &typ); err != nil {
		return PM2Line{}, fmt.Errorf("field type: %w", err)
	}

	var pid float64
	if err := json.Unmarshal(raw["process_id"], &pid); err != nil {
		return PM2Line{}, fmt.Errorf("field process_id: %w", err)
	}

	var appName string
	if err := json.Unmarshal(raw["app_name"], &appName); err != nil {
		return PM2Line{}, fmt.Errorf("field app_name: %w", err)
	}

	return PM2Line{
		Message:   msg,
		Timestamp: ts,
		Type:      typ,
		ProcessID: int(pid),
		AppName:   appName,
	}, nil
}

// IsPM2Line performs a quick check whether line is valid JSON containing all
// five PM2 fields, without fully parsing or validating types.
func IsPM2Line(line []byte) bool {
	line = trimBytes(line)
	if len(line) == 0 {
		return false
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		return false
	}
	for _, k := range []string{"message", "timestamp", "type", "process_id", "app_name"} {
		if _, ok := raw[k]; !ok {
			return false
		}
	}
	return true
}

// ProcessLine converts a PM2Line into one or more LogEntry values using the
// flatten + preserve + enrich approach. When the message contains multiple
// newline-separated sub-messages (PM2 batching), each sub-message becomes
// its own LogEntry sharing the same PM2 metadata.
func ProcessLine(pm2 PM2Line) []LogEntry {
	// Split on newlines to handle PM2 batching.
	subMessages := splitBatchedMessage(pm2.Message)

	entries := make([]LogEntry, 0, len(subMessages))
	for _, subMsg := range subMessages {
		entry := processSingleMessage(subMsg, pm2)
		entries = append(entries, entry)
	}
	return entries
}

// splitBatchedMessage splits a message on newlines, filtering out empty
// segments. A single message (with or without a trailing newline) returns
// a single-element slice.
func splitBatchedMessage(msg string) []string {
	parts := strings.Split(msg, "\n")
	var result []string
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		// Preserve the original message even if it's whitespace-only.
		return []string{msg}
	}
	return result
}

// processSingleMessage builds a LogEntry for a single (non-batched) message
// string with the given PM2 metadata.
func processSingleMessage(msg string, pm2 PM2Line) LogEntry {
	var fields []Field

	// Try to parse message as JSON object.
	msgTrimmed := strings.TrimSpace(msg)
	var innerObj map[string]any
	isJSON := false
	if len(msgTrimmed) > 0 && msgTrimmed[0] == '{' {
		if err := json.Unmarshal([]byte(msgTrimmed), &innerObj); err == nil {
			isJSON = true
		}
	}

	if isJSON {
		// Recursively unwrap nested message JSON.
		innerObj = unwrapMessageJSON(innerObj, 0)

		// Flatten inner fields to top level (sorted), skipping fields that
		// would collide with preserved keys or pm2_ metadata.
		var flatKeys []string
		for k := range innerObj {
			if k == "message" || k == "message_json" {
				continue
			}
			if strings.HasPrefix(k, "pm2_") {
				continue
			}
			flatKeys = append(flatKeys, k)
		}
		sort.Strings(flatKeys)
		for _, k := range flatKeys {
			fields = append(fields, Field{Key: k, Value: innerObj[k]})
		}

		// message — always the raw string.
		fields = append(fields, Field{Key: "message", Value: msg})

		// message_json — the full parsed object.
		fields = append(fields, Field{Key: "message_json", Value: innerObj})
	} else {
		// Plain text: just message.
		fields = append(fields, Field{Key: "message", Value: msg})
	}

	// PM2 metadata.
	fields = append(fields,
		Field{Key: "pm2_timestamp", Value: pm2.Timestamp.Format(time.RFC3339Nano)},
		Field{Key: "pm2_app_name", Value: pm2.AppName},
		Field{Key: "pm2_process_id", Value: pm2.ProcessID},
		Field{Key: "pm2_type", Value: pm2.Type},
	)

	return LogEntry{Fields: fields}
}

// unwrapMessageJSON recursively checks if obj has a "message" field that is a
// string containing a valid JSON object. If so it parses it, stores it in
// "message_json", and recurses up to maxDepth 2.
func unwrapMessageJSON(obj map[string]any, depth int) map[string]any {
	if depth >= 2 {
		return obj
	}
	msgVal, ok := obj["message"]
	if !ok {
		return obj
	}
	msgStr, ok := msgVal.(string)
	if !ok {
		return obj
	}
	msgStr = strings.TrimSpace(msgStr)
	if len(msgStr) == 0 || msgStr[0] != '{' {
		return obj
	}
	var inner map[string]any
	if err := json.Unmarshal([]byte(msgStr), &inner); err != nil {
		return obj
	}
	inner = unwrapMessageJSON(inner, depth+1)
	obj["message_json"] = inner
	return obj
}

// MarshalEntry serializes a LogEntry to compact JSON, preserving field order.
func MarshalEntry(entry LogEntry) ([]byte, error) {
	var buf strings.Builder
	buf.WriteByte('{')
	for i, f := range entry.Fields {
		if i > 0 {
			buf.WriteByte(',')
		}
		// Marshal key.
		keyBytes, err := json.Marshal(f.Key)
		if err != nil {
			return nil, fmt.Errorf("marshal key %q: %w", f.Key, err)
		}
		buf.Write(keyBytes)
		buf.WriteByte(':')
		// Marshal value.
		valBytes, err := json.Marshal(f.Value)
		if err != nil {
			return nil, fmt.Errorf("marshal value for %q: %w", f.Key, err)
		}
		buf.Write(valBytes)
	}
	buf.WriteByte('}')
	return []byte(buf.String()), nil
}

// GetTimestamp extracts the pm2_timestamp from a LogEntry for sorting.
func GetTimestamp(entry LogEntry) time.Time {
	for _, f := range entry.Fields {
		if f.Key == "pm2_timestamp" {
			if s, ok := f.Value.(string); ok {
				if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
					return t
				}
				if t, err := time.Parse(time.RFC3339, s); err == nil {
					return t
				}
			}
		}
	}
	return time.Time{}
}

// trimBytes trims leading/trailing whitespace from a byte slice.
func trimBytes(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}

// fieldValue returns the value for a given key, or nil if not found.
func fieldValue(entry LogEntry, key string) any {
	for _, f := range entry.Fields {
		if f.Key == key {
			return f.Value
		}
	}
	return nil
}

// FieldValue is the exported version of fieldValue.
func FieldValue(entry LogEntry, key string) any {
	return fieldValue(entry, key)
}

// FormatValue formats any value as a string for display purposes.
func FormatValue(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case float64:
		if val == float64(int64(val)) {
			return strconv.FormatInt(int64(val), 10)
		}
		return strconv.FormatFloat(val, 'f', -1, 64)
	case int:
		return strconv.Itoa(val)
	case bool:
		return strconv.FormatBool(val)
	case nil:
		return "null"
	default:
		b, _ := json.Marshal(val)
		return string(b)
	}
}
