package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"pm2logs/internal/filter"
	"pm2logs/internal/merge"
	"pm2logs/internal/parser"
	"pm2logs/internal/scanner"
	"pm2logs/internal/stats"

	"github.com/spf13/cobra"
)

var (
	appFlag       string
	sinceFlag     string
	afterFlag     string
	beforeFlag    string
	dirFlag       string
	statsFlag     bool
	schemaFlag    bool
	schemaMaxFlag int
	version       = "dev"
)

var rootCmd = &cobra.Command{
	Use:     "jlogs [flags]",
	Short:   "Filter and format PM2 JSON log files",
	Version: version,
	Long: `jlogs - Filter and format PM2 JSON log files

Reads PM2 structured JSON log files, filters by app and time range,
unwraps nested JSON messages, and outputs clean JSONL.

Inner JSON fields are flattened to the top level for easy querying.
The original 'message' field is always preserved as-is.
When 'message' is valid JSON, a 'message_json' field is also added with the parsed object.`,
	Example: `  # Last 30 minutes of web app logs
  jlogs --app web --since 30m

  # Absolute time range
  jlogs --app web --after "2026-03-13T12:00:00Z" --before "2026-03-13T12:30:00Z"

  # Glob pattern for app name
  jlogs --app "cc*" --since 1h

  # Discover available apps
  jlogs --since 1h

  # See what fields exist in the data
  jlogs --app web --since 1h --schema

  # Pipe from stdin
  cat ~/.pm2/logs/web-out.log | jlogs --app web --since 30m

Common jq patterns:
  # Pretty-print
  jlogs --app web --since 10m | jq .

  # Filter by level (flattened to top level)
  jlogs --app web --since 30m | jq 'select(.level == "error")'

  # Get browser-originated logs (isFromBrowser is flattened)
  jlogs --app web --since 30m | jq 'select(.isFromBrowser == true)'

  # Extract raw messages only
  jlogs --app web --since 10m | jq -r .message

  # Count by level
  jlogs --app web --since 1h | jq -r '.level // "text"' | sort | uniq -c | sort -rn

  # Access double-nested fields (second level stays in message_json)
  jlogs --app web --since 1h | jq '.message_json?.message_json?.component'

Tip: use --schema to discover what fields are available before writing jq filters.

Setup:
  # Run preflight checks for PM2 in your project
  jlogs init`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          run,
}

func init() {
	rootCmd.Flags().StringVarP(&appFlag, "app", "a", "", `App name filter (supports globs, e.g. "cc*")`)
	rootCmd.Flags().StringVarP(&sinceFlag, "since", "s", "", "Relative time filter (e.g. 10m, 2h, 60s)")
	rootCmd.Flags().StringVar(&afterFlag, "after", "", "Absolute start time (RFC 3339)")
	rootCmd.Flags().StringVar(&beforeFlag, "before", "", "Absolute end time (RFC 3339)")
	rootCmd.Flags().StringVarP(&dirFlag, "dir", "d", "", `Log directory (default "~/.pm2/logs/")`)
	rootCmd.Flags().BoolVar(&statsFlag, "stats", false, "Show summary stats instead of log lines")
	rootCmd.Flags().BoolVar(&schemaFlag, "schema", false, "Output field paths with occurrence counts as JSON")
	rootCmd.Flags().IntVar(&schemaMaxFlag, "schema-values", 20, "Max distinct values to track per field in schema mode (0 to disable)")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func buildTimeFilter() (filter.TimeFilter, error) {
	if sinceFlag != "" {
		d, err := filter.ParseSince(sinceFlag)
		if err != nil {
			return filter.TimeFilter{}, fmt.Errorf("invalid --since value %q: %w", sinceFlag, err)
		}
		return filter.NewTimeFilterSince(d), nil
	}
	if afterFlag != "" || beforeFlag != "" {
		return filter.NewTimeFilterAbsolute(afterFlag, beforeFlag)
	}
	return filter.TimeFilter{}, fmt.Errorf("a time filter is required. Use --since, --after, or --before.\n\nExamples:\n  jlogs --app web --since 30m\n  jlogs --app web --after \"2026-03-13T12:00:00Z\"")
}

func defaultLogDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".pm2/logs"
	}
	return home + "/.pm2/logs"
}

func isStdinPiped() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) == 0
}

func run(cmd *cobra.Command, args []string) error {
	if statsFlag && schemaFlag {
		return fmt.Errorf("--stats and --schema cannot be used together")
	}

	tf, err := buildTimeFilter()
	if err != nil {
		return err
	}

	// Stdin mode — only when --dir is not explicitly set
	if isStdinPiped() && !cmd.Flags().Changed("dir") {
		return runFromReader(os.Stdin, appFlag, tf)
	}

	// Directory mode
	logDir := dirFlag
	if logDir == "" {
		logDir = defaultLogDir()
	}

	scanResult, err := scanner.ScanDirectory(logDir)
	if err != nil {
		return fmt.Errorf("failed to scan %s: %w", logDir, err)
	}

	// Stats mode
	if statsFlag {
		appStats, err := stats.GatherStats(scanResult, appFlag, tf)
		if err != nil {
			return err
		}
		if len(appStats) == 0 {
			return printNoApps(scanResult, logDir)
		}
		fmt.Fprint(os.Stderr, stats.FormatStats(appStats))
		return nil
	}

	// No app specified — list available apps
	if appFlag == "" {
		return printNoAppSpecified(scanResult, logDir)
	}

	selected := scanner.SelectFiles(scanResult, appFlag, tf)
	if len(selected) == 0 {
		// Distinguish: does the app exist but no logs match the time range?
		if appExistsInScan(scanResult, appFlag) {
			if schemaFlag {
				return outputSchema(map[string]*stats.FieldSchema{})
			}
			return printNoLogsInTimeRange(appFlag, tf)
		}
		return printNoApps(scanResult, logDir)
	}

	// Read, parse, filter entries from selected files
	entries, err := readEntries(selected, appFlag, tf)
	if err != nil {
		return err
	}

	// Schema mode
	if schemaFlag {
		schema := stats.GatherSchema(entries, schemaMaxFlag)
		return outputSchema(schema)
	}

	// Merge-sort and output
	sorted := merge.MergeEntries(entries)
	return outputEntries(sorted)
}

func readEntries(files []scanner.FileInfo, appPattern string, tf filter.TimeFilter) ([]parser.LogEntry, error) {
	var all []parser.LogEntry

	for _, fi := range files {
		f, err := os.Open(fi.Path)
		if err != nil {
			return nil, fmt.Errorf("opening %s: %w", fi.Path, err)
		}

		entries, err := readFileEntries(f, appPattern, tf)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", fi.Path, err)
		}
		all = append(all, entries...)
	}

	return all, nil
}

func readFileEntries(r io.Reader, appPattern string, tf filter.TimeFilter) ([]parser.LogEntry, error) {
	var entries []parser.LogEntry
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024) // 10MB max line

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}

		pm2, err := parser.ParsePM2Line(line)
		if err != nil {
			continue // skip non-PM2 lines
		}

		if appPattern != "" && !filter.MatchApp(appPattern, pm2.AppName) {
			continue
		}

		if !filter.MatchTime(tf, pm2.Timestamp) {
			continue
		}

		processed := parser.ProcessLine(pm2)
		entries = append(entries, processed...)
	}

	return entries, sc.Err()
}

func runFromReader(r io.Reader, appPattern string, tf filter.TimeFilter) error {
	entries, err := readFileEntries(r, appPattern, tf)
	if err != nil {
		return err
	}

	if schemaFlag {
		schema := stats.GatherSchema(entries, schemaMaxFlag)
		return outputSchema(schema)
	}

	sorted := merge.MergeEntries(entries)
	return outputEntries(sorted)
}

func outputEntries(entries []parser.LogEntry) error {
	if len(entries) == 0 {
		fmt.Fprintln(os.Stderr, "No matching results found.")
		return nil
	}

	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()

	for _, entry := range entries {
		data, err := parser.MarshalEntry(entry)
		if err != nil {
			return err
		}
		w.Write(data)
		w.WriteByte('\n')
	}
	return nil
}

func outputSchema(schema map[string]*stats.FieldSchema) error {
	// Sort keys for deterministic output
	keys := make([]string, 0, len(schema))
	for k := range schema {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Build ordered JSON
	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()

	w.WriteString("{\n")
	for i, k := range keys {
		kb, _ := json.Marshal(k)
		fs := schema[k]
		fmt.Fprintf(w, "  %s: {\"count\": %d", string(kb), fs.Count)
		if fs.Values != nil {
			valBytes, err := json.Marshal(fs.Values)
			if err == nil {
				fmt.Fprintf(w, ", \"values\": %s", valBytes)
			}
		}
		w.WriteByte('}')
		if i < len(keys)-1 {
			w.WriteByte(',')
		}
		w.WriteByte('\n')
	}
	w.WriteString("}\n")
	return nil
}

func printNoAppSpecified(result scanner.ScanResult, logDir string) error {
	if len(result.AppNames) == 0 {
		return fmt.Errorf("no PM2 log files found in %s", logDir)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "No app specified. Available apps in %s:\n\n", logDir)
	for _, name := range result.AppNames {
		fmt.Fprintf(&sb, "  %s\n", name)
	}
	sb.WriteString("\nUse --app <name> to filter. Glob patterns are supported (e.g. --app \"cc*\").\n")
	fmt.Fprint(os.Stderr, sb.String())
	os.Exit(1)
	return nil
}

func appExistsInScan(result scanner.ScanResult, appPattern string) bool {
	for _, fi := range result.Files {
		if fi.IsPM2 && filter.MatchApp(appPattern, fi.AppName) {
			return true
		}
	}
	return false
}

func printNoLogsInTimeRange(appPattern string, tf filter.TimeFilter) error {
	var timeDesc string
	if !tf.After.IsZero() && !tf.Before.IsZero() {
		timeDesc = fmt.Sprintf("between %s and %s", tf.After.Format(time.RFC3339), tf.Before.Format(time.RFC3339))
	} else if !tf.After.IsZero() {
		timeDesc = fmt.Sprintf("after %s", tf.After.Format(time.RFC3339))
	} else if !tf.Before.IsZero() {
		timeDesc = fmt.Sprintf("before %s", tf.Before.Format(time.RFC3339))
	}
	fmt.Fprintf(os.Stderr, "No matching results found. App %q exists but has no log entries %s.\n", appPattern, timeDesc)
	return nil
}

func printNoApps(result scanner.ScanResult, logDir string) error {
	if len(result.AppNames) == 0 {
		return fmt.Errorf("no PM2 log files found in %s", logDir)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "No apps matching %q. Available apps in %s:\n\n", appFlag, logDir)
	for _, name := range result.AppNames {
		fmt.Fprintf(&sb, "  %s\n", name)
	}
	sb.WriteString("\nUse --app <name> to filter. Glob patterns are supported (e.g. --app \"cc*\").\n")
	fmt.Fprint(os.Stderr, sb.String())
	os.Exit(1)
	return nil
}
