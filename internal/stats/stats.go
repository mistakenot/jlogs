package stats

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"pm2logs/internal/filter"
	"pm2logs/internal/parser"
	"pm2logs/internal/scanner"
)

// AppStats holds summary statistics for a single application.
type AppStats struct {
	AppName   string
	FileCount int
	LineCount int
	StartTime time.Time
	EndTime   time.Time
}

// GatherStats computes per-app statistics from a scan result. For each unique
// app matching appPattern, it counts files and lines (by reading and parsing
// PM2 lines that match the time filter) and determines the time range.
// Results are sorted by app name.
func GatherStats(scanResult scanner.ScanResult, appPattern string, timeFilter filter.TimeFilter) ([]AppStats, error) {
	// Group files by app name, filtering by pattern.
	appFiles := make(map[string][]scanner.FileInfo)
	for _, fi := range scanResult.Files {
		if !fi.IsPM2 {
			continue
		}
		if !filter.MatchApp(appPattern, fi.AppName) {
			continue
		}
		appFiles[fi.AppName] = append(appFiles[fi.AppName], fi)
	}

	var result []AppStats
	for appName, files := range appFiles {
		stats := AppStats{
			AppName:   appName,
			FileCount: len(files),
		}

		for _, fi := range files {
			lineCount, minT, maxT, err := countLines(fi.Path, timeFilter)
			if err != nil {
				return nil, fmt.Errorf("reading %s: %w", fi.Path, err)
			}
			stats.LineCount += lineCount

			if !minT.IsZero() && (stats.StartTime.IsZero() || minT.Before(stats.StartTime)) {
				stats.StartTime = minT
			}
			if !maxT.IsZero() && (stats.EndTime.IsZero() || maxT.After(stats.EndTime)) {
				stats.EndTime = maxT
			}
		}

		result = append(result, stats)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].AppName < result[j].AppName
	})

	return result, nil
}

// countLines reads a file, parses each line as PM2 JSON, applies the time
// filter, and returns the count of matching lines along with the min/max
// timestamps observed.
func countLines(path string, tf filter.TimeFilter) (count int, minTime, maxTime time.Time, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, time.Time{}, time.Time{}, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	// Increase buffer for long lines.
	sc.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	for sc.Scan() {
		line := sc.Bytes()
		pm2, parseErr := parser.ParsePM2Line(line)
		if parseErr != nil {
			continue
		}
		if !filter.MatchTime(tf, pm2.Timestamp) {
			continue
		}
		count++
		if minTime.IsZero() || pm2.Timestamp.Before(minTime) {
			minTime = pm2.Timestamp
		}
		if maxTime.IsZero() || pm2.Timestamp.After(maxTime) {
			maxTime = pm2.Timestamp
		}
	}

	if err := sc.Err(); err != nil {
		return 0, time.Time{}, time.Time{}, err
	}

	return count, minTime, maxTime, nil
}

// FormatStats produces a human-readable table from a slice of AppStats.
func FormatStats(stats []AppStats) string {
	if len(stats) == 0 {
		return ""
	}

	// Determine column widths.
	maxApp := len("App")
	maxFiles := len("Files")
	maxLines := len("Lines")

	type row struct {
		app       string
		files     string
		lines     string
		timeRange string
	}

	rows := make([]row, len(stats))
	for i, s := range stats {
		r := row{
			app:   s.AppName,
			files: fmt.Sprintf("%d", s.FileCount),
			lines: fmt.Sprintf("%d", s.LineCount),
		}

		if !s.StartTime.IsZero() && !s.EndTime.IsZero() {
			r.timeRange = formatTimeRange(s.StartTime, s.EndTime)
		}

		if len(r.app) > maxApp {
			maxApp = len(r.app)
		}
		if len(r.files) > maxFiles {
			maxFiles = len(r.files)
		}
		if len(r.lines) > maxLines {
			maxLines = len(r.lines)
		}

		rows[i] = r
	}

	var buf strings.Builder
	// Header.
	fmt.Fprintf(&buf, "%-*s  %-*s  %-*s  %s\n",
		maxApp, "App",
		maxFiles, "Files",
		maxLines, "Lines",
		"Time Range",
	)

	// Data rows.
	for _, r := range rows {
		fmt.Fprintf(&buf, "%-*s  %-*s  %-*s  %s\n",
			maxApp, r.app,
			maxFiles, r.files,
			maxLines, r.lines,
			r.timeRange,
		)
	}

	return buf.String()
}

// formatTimeRange formats a start/end time pair. If both are on the same day,
// uses time-only format (15:04:05). Otherwise uses full date-time.
func formatTimeRange(start, end time.Time) string {
	sameDay := start.Year() == end.Year() &&
		start.Month() == end.Month() &&
		start.Day() == end.Day()

	if sameDay {
		return fmt.Sprintf("%s - %s",
			start.Format("15:04:05"),
			end.Format("15:04:05"),
		)
	}
	return fmt.Sprintf("%s - %s",
		start.Format("2006-01-02 15:04:05"),
		end.Format("2006-01-02 15:04:05"),
	)
}

// GatherSchema walks every LogEntry's Fields and counts occurrences of each
// dot-delimited field path. For nested values (map[string]any), it recurses
// with a dot-delimited prefix.
func GatherSchema(entries []parser.LogEntry) map[string]int {
	counts := make(map[string]int)

	for _, entry := range entries {
		for _, f := range entry.Fields {
			counts[f.Key]++
			if m, ok := f.Value.(map[string]any); ok {
				walkPaths(m, f.Key, counts)
			}
		}
	}

	return counts
}

// walkPaths recursively walks a map and increments counts for each
// dot-delimited path. Both intermediate and leaf paths are counted.
func walkPaths(obj map[string]any, prefix string, counts map[string]int) {
	for k, v := range obj {
		path := prefix + "." + k
		counts[path]++
		if m, ok := v.(map[string]any); ok {
			walkPaths(m, path, counts)
		}
	}
}
