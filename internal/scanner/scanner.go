package scanner

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"pm2logs/internal/filter"
	"pm2logs/internal/parser"
)

// FileInfo holds metadata about a single log file discovered during scanning.
type FileInfo struct {
	Path      string
	AppName   string
	StartTime time.Time
	EndTime   time.Time
	IsPM2     bool
}

// ScanResult holds the outcome of scanning a directory of log files.
type ScanResult struct {
	Files    []FileInfo
	AppNames []string // unique, sorted
}

// maxProbeLines is the number of lines read from the start of a file
// when probing for PM2 content. Set high enough to handle files with
// long non-PM2 header sections (e.g., startup output).
const maxProbeLines = 50

// tailReadSize is the number of bytes read from the end of a file
// when searching for the last PM2 line.
const tailReadSize = 8192

// ScanDirectory lists all files in dir (non-recursive), probes each one,
// and returns a ScanResult with file metadata and unique sorted app names.
func ScanDirectory(dir string) (ScanResult, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ScanResult{}, err
	}

	var files []FileInfo
	appSet := make(map[string]struct{})

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		fi, err := ProbeFile(path)
		if err != nil {
			// Skip files we cannot probe.
			continue
		}
		files = append(files, fi)
		if fi.IsPM2 && fi.AppName != "" {
			appSet[fi.AppName] = struct{}{}
		}
	}

	var appNames []string
	for name := range appSet {
		appNames = append(appNames, name)
	}
	sort.Strings(appNames)

	return ScanResult{
		Files:    files,
		AppNames: appNames,
	}, nil
}

// ProbeFile reads the beginning and end of a file to determine whether it
// contains PM2 JSON log lines. It extracts the app name from the first valid
// PM2 line and timestamps from the first and last valid PM2 lines.
func ProbeFile(path string) (FileInfo, error) {
	fi := FileInfo{Path: path}

	f, err := os.Open(path)
	if err != nil {
		return fi, err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return fi, err
	}

	// Empty file — not PM2.
	if stat.Size() == 0 {
		return fi, nil
	}

	// Read first lines to find first PM2 line.
	firstPM2 := findFirstPM2Line(f)

	if firstPM2 == nil {
		// No PM2 lines in first maxProbeLines lines.
		return fi, nil
	}

	fi.IsPM2 = true

	// Parse the first PM2 line for app name and start time.
	pm2First, err := parser.ParsePM2Line(firstPM2)
	if err != nil {
		return fi, nil
	}
	fi.AppName = pm2First.AppName
	fi.StartTime = pm2First.Timestamp

	// Find last PM2 line by reading tail of file.
	lastPM2 := findLastPM2Line(f, stat.Size())
	if lastPM2 != nil {
		pm2Last, err := parser.ParsePM2Line(lastPM2)
		if err == nil {
			fi.EndTime = pm2Last.Timestamp
		} else {
			fi.EndTime = fi.StartTime
		}
	} else {
		fi.EndTime = fi.StartTime
	}

	return fi, nil
}

// findFirstPM2Line reads up to maxProbeLines lines from the current position
// of r and returns the first line that passes parser.IsPM2Line.
func findFirstPM2Line(r io.Reader) []byte {
	buf := make([]byte, 0, 64*1024)
	tmp := make([]byte, 4096)
	linesFound := 0

	for linesFound < maxProbeLines {
		n, err := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}

		// Process complete lines in buf.
		for linesFound < maxProbeLines {
			idx := indexByte(buf, '\n')
			if idx < 0 {
				break
			}
			line := buf[:idx]
			buf = buf[idx+1:]
			linesFound++

			trimmed := []byte(strings.TrimSpace(string(line)))
			if len(trimmed) > 0 && parser.IsPM2Line(trimmed) {
				return trimmed
			}
		}

		if err != nil {
			break
		}
	}

	// Check remaining buffer as last line (no trailing newline).
	if linesFound < maxProbeLines && len(buf) > 0 {
		trimmed := []byte(strings.TrimSpace(string(buf)))
		if len(trimmed) > 0 && parser.IsPM2Line(trimmed) {
			return trimmed
		}
	}

	return nil
}

// findLastPM2Line reads the last tailReadSize bytes of a file and searches
// backward through the lines for the last valid PM2 line.
func findLastPM2Line(f *os.File, fileSize int64) []byte {
	readSize := int64(tailReadSize)
	if readSize > fileSize {
		readSize = fileSize
	}

	offset := fileSize - readSize
	buf := make([]byte, readSize)

	_, err := f.ReadAt(buf, offset)
	if err != nil && err != io.EOF {
		return nil
	}

	lines := strings.Split(string(buf), "\n")

	// Iterate backward.
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if len(trimmed) == 0 {
			continue
		}
		lineBytes := []byte(trimmed)
		if parser.IsPM2Line(lineBytes) {
			return lineBytes
		}
	}

	return nil
}

// SelectFiles filters a ScanResult to files where IsPM2 is true, the app name
// matches appPattern, and the file's time range overlaps with the given time
// filter.
func SelectFiles(result ScanResult, appPattern string, timeFilter filter.TimeFilter) []FileInfo {
	var selected []FileInfo

	for _, fi := range result.Files {
		if !fi.IsPM2 {
			continue
		}

		if !filter.MatchApp(appPattern, fi.AppName) {
			continue
		}

		if !timeRangeOverlaps(fi, timeFilter) {
			continue
		}

		selected = append(selected, fi)
	}

	return selected
}

// timeRangeOverlaps checks whether a file's time range overlaps with the
// given time filter. A zero filter bound means no constraint on that side.
func timeRangeOverlaps(fi FileInfo, tf filter.TimeFilter) bool {
	// file.EndTime >= filter.After (or filter.After is zero)
	if !tf.After.IsZero() && fi.EndTime.Before(tf.After) {
		return false
	}
	// file.StartTime <= filter.Before (or filter.Before is zero)
	if !tf.Before.IsZero() && fi.StartTime.After(tf.Before) {
		return false
	}
	return true
}

// indexByte returns the index of the first occurrence of b in buf, or -1.
func indexByte(buf []byte, b byte) int {
	for i, c := range buf {
		if c == b {
			return i
		}
	}
	return -1
}
