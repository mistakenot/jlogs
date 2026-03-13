package scanner

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"pm2logs/internal/filter"
)

func testdataDir() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..", "testdata")
}

func TestProbeFile_WebOut(t *testing.T) {
	path := filepath.Join(testdataDir(), "web-out.log")
	fi, err := ProbeFile(path)
	if err != nil {
		t.Fatalf("ProbeFile(%q): %v", path, err)
	}
	if !fi.IsPM2 {
		t.Error("expected IsPM2=true for web-out.log")
	}
	if fi.AppName != "web" {
		t.Errorf("expected AppName=%q, got %q", "web", fi.AppName)
	}
	if fi.StartTime.IsZero() {
		t.Error("expected non-zero StartTime")
	}
	if fi.EndTime.IsZero() {
		t.Error("expected non-zero EndTime")
	}
	if fi.EndTime.Before(fi.StartTime) {
		t.Errorf("EndTime %v is before StartTime %v", fi.EndTime, fi.StartTime)
	}
}

func TestProbeFile_WebError(t *testing.T) {
	path := filepath.Join(testdataDir(), "web-error.log")
	fi, err := ProbeFile(path)
	if err != nil {
		t.Fatalf("ProbeFile(%q): %v", path, err)
	}
	if !fi.IsPM2 {
		t.Error("expected IsPM2=true for web-error.log")
	}
	if fi.AppName != "web" {
		t.Errorf("expected AppName=%q, got %q", "web", fi.AppName)
	}
}

func TestProbeFile_ClaudeDB(t *testing.T) {
	path := filepath.Join(testdataDir(), "claudedb-out.log")
	fi, err := ProbeFile(path)
	if err != nil {
		t.Fatalf("ProbeFile(%q): %v", path, err)
	}
	if fi.IsPM2 {
		t.Error("expected IsPM2=false for claudedb-out.log (MongoDB JSON, not PM2)")
	}
}

func TestProbeFile_LogloadError(t *testing.T) {
	path := filepath.Join(testdataDir(), "logload-error.log")
	fi, err := ProbeFile(path)
	if err != nil {
		t.Fatalf("ProbeFile(%q): %v", path, err)
	}
	if fi.IsPM2 {
		t.Error("expected IsPM2=false for logload-error.log (plain text)")
	}
}

func TestProbeFile_CctraceOut(t *testing.T) {
	path := filepath.Join(testdataDir(), "cctrace-out.log")
	fi, err := ProbeFile(path)
	if err != nil {
		t.Fatalf("ProbeFile(%q): %v", path, err)
	}
	// cctrace-out.log starts with "yarn run v1.22.22" and has no PM2 lines.
	if fi.IsPM2 {
		t.Error("expected IsPM2=false for cctrace-out.log (plain text, no PM2 lines)")
	}
}

func TestProbeFile_EmptyFile(t *testing.T) {
	// Create a temporary empty file.
	tmp, err := os.CreateTemp(t.TempDir(), "empty-*.log")
	if err != nil {
		t.Fatal(err)
	}
	tmp.Close()

	fi, err := ProbeFile(tmp.Name())
	if err != nil {
		t.Fatalf("ProbeFile(empty): %v", err)
	}
	if fi.IsPM2 {
		t.Error("expected IsPM2=false for empty file")
	}
}

func TestScanDirectory(t *testing.T) {
	result, err := ScanDirectory(testdataDir())
	if err != nil {
		t.Fatalf("ScanDirectory: %v", err)
	}

	if len(result.Files) == 0 {
		t.Fatal("expected at least one file in ScanResult")
	}

	if len(result.AppNames) == 0 {
		t.Fatal("expected at least one app name")
	}

	// Check that app names are sorted.
	for i := 1; i < len(result.AppNames); i++ {
		if result.AppNames[i] < result.AppNames[i-1] {
			t.Errorf("AppNames not sorted: %v", result.AppNames)
			break
		}
	}

	// Check that "web" is among the discovered apps.
	found := false
	for _, name := range result.AppNames {
		if name == "web" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'web' in AppNames, got %v", result.AppNames)
	}

	// Should have multiple distinct apps.
	if len(result.AppNames) < 2 {
		t.Errorf("expected multiple app names, got %v", result.AppNames)
	}
}

func TestSelectFiles_AppWeb(t *testing.T) {
	result, err := ScanDirectory(testdataDir())
	if err != nil {
		t.Fatalf("ScanDirectory: %v", err)
	}

	selected := SelectFiles(result, "web", filter.TimeFilter{})
	if len(selected) == 0 {
		t.Fatal("expected at least one file for app=web")
	}
	for _, fi := range selected {
		if fi.AppName != "web" {
			t.Errorf("expected AppName=web, got %q", fi.AppName)
		}
	}
}

func TestSelectFiles_GlobPattern(t *testing.T) {
	result, err := ScanDirectory(testdataDir())
	if err != nil {
		t.Fatalf("ScanDirectory: %v", err)
	}

	// "c*" should match caddy, cloud-sql-proxy, etc. but not web, db, fb.
	selected := SelectFiles(result, "c*", filter.TimeFilter{})
	if len(selected) == 0 {
		t.Fatal("expected at least one file for app=c*")
	}
	for _, fi := range selected {
		if fi.AppName[0] != 'c' {
			t.Errorf("expected app starting with 'c', got %q", fi.AppName)
		}
	}
}

func TestSelectFiles_TimeFilter(t *testing.T) {
	result, err := ScanDirectory(testdataDir())
	if err != nil {
		t.Fatalf("ScanDirectory: %v", err)
	}

	// Use a time filter far in the future — no files should match.
	futureFilter := filter.TimeFilter{
		After: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	selected := SelectFiles(result, "", futureFilter)
	if len(selected) != 0 {
		t.Errorf("expected no files with future time filter, got %d", len(selected))
	}

	// Use a time filter far in the past — no files should match.
	pastFilter := filter.TimeFilter{
		Before: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	selected = SelectFiles(result, "", pastFilter)
	if len(selected) != 0 {
		t.Errorf("expected no files with past time filter, got %d", len(selected))
	}

	// Use a very wide time window — all PM2 files should match.
	wideFilter := filter.TimeFilter{
		After:  time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
		Before: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	selected = SelectFiles(result, "", wideFilter)
	pm2Count := 0
	for _, fi := range result.Files {
		if fi.IsPM2 {
			pm2Count++
		}
	}
	if len(selected) != pm2Count {
		t.Errorf("expected %d PM2 files with wide filter, got %d", pm2Count, len(selected))
	}
}
