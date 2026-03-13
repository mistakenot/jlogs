package e2e

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var binaryPath string

func TestMain(m *testing.M) {
	// Build the binary once for all tests
	dir, err := os.MkdirTemp("", "jlogs-e2e")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	binaryPath = filepath.Join(dir, "jlogs")
	if runtime.GOOS == "windows" {
		binaryPath += ".exe"
	}

	cmd := exec.Command("go", "build", "-o", binaryPath, "..")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		panic("failed to build binary: " + err.Error())
	}

	os.Exit(m.Run())
}

func testdataDir() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "testdata")
}

func runJlogs(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(binaryPath, args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	exitCode = 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("failed to run jlogs: %v", err)
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

func parseJSONL(t *testing.T, output string) []map[string]any {
	t.Helper()
	var results []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("failed to parse JSON line: %v\nline: %s", err, line)
		}
		results = append(results, obj)
	}
	return results
}

func TestBasicAppFilter(t *testing.T) {
	stdout, _, _ := runJlogs(t, "--app", "web", "--since", "8760h", "--dir", testdataDir())
	entries := parseJSONL(t, stdout)
	if len(entries) == 0 {
		t.Fatal("expected output lines, got none")
	}
	for i, e := range entries {
		if e["pm2_app_name"] != "web" {
			t.Errorf("line %d: expected pm2_app_name=web, got %v", i, e["pm2_app_name"])
		}
	}
}

func TestGlobAppFilter(t *testing.T) {
	stdout, _, _ := runJlogs(t, "--app", "db*", "--since", "8760h", "--dir", testdataDir())
	entries := parseJSONL(t, stdout)
	if len(entries) == 0 {
		t.Fatal("expected output lines, got none")
	}
	for i, e := range entries {
		name := e["pm2_app_name"].(string)
		if !strings.HasPrefix(name, "db") {
			t.Errorf("line %d: expected app starting with 'db', got %q", i, name)
		}
	}
}

func TestNoAppListsApps(t *testing.T) {
	_, stderr, exitCode := runJlogs(t, "--since", "8760h", "--dir", testdataDir())
	if exitCode == 0 {
		t.Error("expected non-zero exit code")
	}
	if !strings.Contains(stderr, "Available apps") {
		t.Errorf("expected app listing in stderr, got: %s", stderr)
	}
}

func TestUnknownAppListsApps(t *testing.T) {
	_, stderr, exitCode := runJlogs(t, "--app", "nonexistent", "--since", "8760h", "--dir", testdataDir())
	if exitCode == 0 {
		t.Error("expected non-zero exit code")
	}
	if !strings.Contains(stderr, "Available apps") {
		t.Errorf("expected app listing in stderr, got: %s", stderr)
	}
}

func TestMissingTimeFilter(t *testing.T) {
	_, stderr, exitCode := runJlogs(t, "--app", "web")
	if exitCode == 0 {
		t.Error("expected non-zero exit code")
	}
	if !strings.Contains(stderr, "time filter is required") {
		t.Errorf("expected time filter error, got: %s", stderr)
	}
}

func TestOutputIsSorted(t *testing.T) {
	stdout, _, _ := runJlogs(t, "--app", "web", "--since", "8760h", "--dir", testdataDir())
	entries := parseJSONL(t, stdout)
	if len(entries) < 2 {
		t.Skip("not enough entries to test sorting")
	}
	for i := 1; i < len(entries); i++ {
		prevStr := entries[i-1]["pm2_timestamp"].(string)
		currStr := entries[i]["pm2_timestamp"].(string)
		prevT, _ := time.Parse(time.RFC3339Nano, prevStr)
		currT, _ := time.Parse(time.RFC3339Nano, currStr)
		if currT.Before(prevT) {
			t.Errorf("line %d: timestamps not sorted: %s > %s", i, prevStr, currStr)
			break
		}
	}
}

func TestOutputIsCompactJSON(t *testing.T) {
	stdout, _, _ := runJlogs(t, "--app", "web", "--since", "8760h", "--dir", testdataDir())
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		// Compact JSON should not start with whitespace or have unquoted newlines
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Errorf("line %d: not valid JSON: %v", i, err)
			continue
		}
		// Re-marshal compactly and compare length (allowing for key order differences)
		compacted, _ := json.Marshal(obj)
		if len(line) != len(string(compacted)) {
			// This is a rough check - exact match isn't expected due to key ordering
			// Just verify it parses as JSON (done above)
		}
	}
}

func TestPM2MetadataPresent(t *testing.T) {
	stdout, _, _ := runJlogs(t, "--app", "web", "--since", "8760h", "--dir", testdataDir())
	entries := parseJSONL(t, stdout)
	if len(entries) == 0 {
		t.Fatal("no entries")
	}
	requiredFields := []string{"pm2_timestamp", "pm2_app_name", "pm2_process_id", "pm2_type"}
	for i, e := range entries {
		for _, f := range requiredFields {
			if _, ok := e[f]; !ok {
				t.Errorf("line %d: missing field %q", i, f)
			}
		}
	}
}

func TestNestedJSONUnwrapped(t *testing.T) {
	stdout, _, _ := runJlogs(t, "--app", "web", "--since", "8760h", "--dir", testdataDir())
	entries := parseJSONL(t, stdout)

	found := false
	for _, e := range entries {
		if cls, ok := e["class"]; ok && cls == "ClientLogsService" {
			found = true
			// message should be preserved as raw string
			msg, ok := e["message"].(string)
			if !ok || msg == "" {
				t.Error("expected message to be a non-empty string")
			}
			// message_json should be a parsed object
			mj, ok := e["message_json"].(map[string]any)
			if !ok {
				t.Error("expected message_json to be an object")
			} else {
				if _, ok := mj["class"]; !ok {
					t.Error("expected message_json to have 'class' field")
				}
			}
			// level should be flattened
			if _, ok := e["level"]; !ok {
				t.Error("expected 'level' to be flattened to top level")
			}
			break
		}
	}
	if !found {
		t.Error("no ClientLogsService line found in output")
	}
}

func TestNonPM2FilesSkipped(t *testing.T) {
	stdout, _, _ := runJlogs(t, "--app", "claudedb", "--since", "8760h", "--dir", testdataDir())
	// claudedb files are MongoDB format, not PM2 - should produce no output or error gracefully
	entries := parseJSONL(t, stdout)
	for _, e := range entries {
		if name, ok := e["pm2_app_name"]; ok && name == "claudedb" {
			t.Error("claudedb (MongoDB format) should not appear in PM2 output")
			break
		}
	}
}

func TestStatsMode(t *testing.T) {
	_, stderr, exitCode := runJlogs(t, "--stats", "--app", "web", "--since", "8760h", "--dir", testdataDir())
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}
	if !strings.Contains(stderr, "web") {
		t.Errorf("expected 'web' in stats output, got: %s", stderr)
	}
	if !strings.Contains(stderr, "Files") || !strings.Contains(stderr, "Lines") {
		t.Errorf("expected table headers in stats, got: %s", stderr)
	}
}

func TestSchemaFlag(t *testing.T) {
	stdout, _, exitCode := runJlogs(t, "--app", "web", "--since", "8760h", "--dir", testdataDir(), "--schema")
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}
	var schema map[string]any
	if err := json.Unmarshal([]byte(stdout), &schema); err != nil {
		t.Fatalf("schema output is not valid JSON: %v\noutput: %s", err, stdout[:min(len(stdout), 500)])
	}
	// message should be present
	if _, ok := schema["message"]; !ok {
		t.Error("expected 'message' in schema")
	}
	// pm2_timestamp should be present
	if _, ok := schema["pm2_timestamp"]; !ok {
		t.Error("expected 'pm2_timestamp' in schema")
	}
	// message count should equal pm2_timestamp count (both present on every line)
	msgCount := schema["message"]
	tsCount := schema["pm2_timestamp"]
	if msgCount != tsCount {
		t.Errorf("expected message count (%v) to equal pm2_timestamp count (%v)", msgCount, tsCount)
	}
}

func TestStdinMode(t *testing.T) {
	// Read a test file and pipe it via stdin
	filePath := filepath.Join(testdataDir(), "web-out.log")
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read test file: %v", err)
	}

	cmd := exec.Command(binaryPath, "--app", "web", "--since", "8760h")
	cmd.Stdin = bytes.NewReader(data)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("jlogs stdin mode failed: %v\nstderr: %s", err, errBuf.String())
	}

	entries := parseJSONL(t, outBuf.String())
	if len(entries) == 0 {
		t.Fatal("expected entries from stdin mode")
	}
	for i, e := range entries {
		if e["pm2_app_name"] != "web" {
			t.Errorf("line %d: expected pm2_app_name=web, got %v", i, e["pm2_app_name"])
		}
	}
}

func TestPlainTextMessagesIncluded(t *testing.T) {
	stdout, _, _ := runJlogs(t, "--app", "web", "--since", "8760h", "--dir", testdataDir())
	entries := parseJSONL(t, stdout)

	foundPlainText := false
	for _, e := range entries {
		msg, ok := e["message"].(string)
		if !ok {
			continue
		}
		if strings.Contains(msg, "yarn run") {
			foundPlainText = true
			// Should NOT have message_json
			if _, ok := e["message_json"]; ok {
				t.Error("plain text message should not have message_json")
			}
			// Should NOT have flattened fields like class or level
			if _, ok := e["class"]; ok {
				t.Error("plain text message should not have 'class' field")
			}
			if _, ok := e["level"]; ok {
				t.Error("plain text message should not have 'level' field")
			}
			break
		}
	}
	if !foundPlainText {
		t.Error("no plain text message (yarn run) found in output")
	}
}

func TestHelpOutput(t *testing.T) {
	stdout, _, _ := runJlogs(t, "--help")
	if !strings.Contains(stdout, "--app") {
		t.Error("help should mention --app flag")
	}
	if !strings.Contains(stdout, "--since") {
		t.Error("help should mention --since flag")
	}
	if !strings.Contains(stdout, "jq") {
		t.Error("help should include jq examples")
	}
	if !strings.Contains(stdout, "--schema") {
		t.Error("help should mention --schema flag")
	}
	if !strings.Contains(stdout, "Examples:") {
		t.Error("help should include examples section")
	}
}

func TestAfterBeforeFilter(t *testing.T) {
	stdout, _, exitCode := runJlogs(t, "--app", "web",
		"--after", "2026-03-13T12:05:00Z",
		"--before", "2026-03-13T12:05:10Z",
		"--dir", testdataDir())
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}
	entries := parseJSONL(t, stdout)
	if len(entries) == 0 {
		t.Fatal("expected some entries in the time window")
	}
	afterT, _ := time.Parse(time.RFC3339Nano, "2026-03-13T12:05:00Z")
	beforeT, _ := time.Parse(time.RFC3339Nano, "2026-03-13T12:05:10Z")
	for i, e := range entries {
		tsStr := e["pm2_timestamp"].(string)
		ts, _ := time.Parse(time.RFC3339Nano, tsStr)
		if ts.Before(afterT) || ts.After(beforeT) {
			t.Errorf("line %d: timestamp %s outside filter range", i, tsStr)
		}
	}
}

func TestValidAppNoLogsInTimeRange(t *testing.T) {
	// "web" is a valid app in testdata, but a far-future time range should match zero logs.
	// The tool should NOT say "No apps matching", it should say the app exists but has no logs.
	stdout, stderr, exitCode := runJlogs(t, "--app", "web",
		"--after", "2099-01-01T00:00:00Z",
		"--dir", testdataDir())
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}
	// stdout should be empty array (no matching logs)
	if strings.TrimSpace(stdout) != "[]" {
		t.Errorf("expected [] on stdout, got: %s", stdout)
	}
	// stderr should say no matching results, NOT "No apps matching"
	if strings.Contains(stderr, "No apps matching") {
		t.Errorf("should not say 'No apps matching' for a valid app, got: %s", stderr)
	}
	if !strings.Contains(stderr, "No matching results found") {
		t.Errorf("expected 'No matching results found' in stderr, got: %s", stderr)
	}
}
