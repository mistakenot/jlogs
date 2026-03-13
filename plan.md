# jlogs — Implementation Plan

## Key observations from test data

Before diving into the plan, some important things discovered in `testdata/`:

1. **Not all files are PM2 JSON.** `claudedb-out.log` contains raw MongoDB logs (`{"t":{"$date":...}}`), not PM2 wrappers. `logload-error.log` is plain text CLI help output. `cctrace-out.log` starts with plain text (`yarn run v1.22.22`). The file inspection step must detect whether a file contains PM2-formatted lines and skip files that don't.

2. **PM2 lines have a consistent shape:** `{"message":"...","timestamp":"...","type":"out|err","process_id":N,"app_name":"..."}`. The presence of all five fields identifies a PM2 log line.

3. **Messages can contain newlines.** PM2 JSON values include `\n` within strings (e.g. multi-line stack traces in `web-error.log`). But each PM2 JSON *line* is still a single line in the file — the newlines are escaped within the JSON string.

4. **Nested JSON in `message`** occurs in `web-out.log` — the `ClientLogsService` pattern where `message` is a JSON string containing fields like `class`, `level`, `message`, `timestamp`, `isFromBrowser`, etc.

5. **Non-JSON messages are common.** Many lines have plain text in the `message` field (startup output, vite HMR messages, etc.). These pass through with the raw `message` preserved — no data is ever dropped.

## Project structure

```
pm2logs/
├── main.go                  # Entrypoint — cobra root command setup
├── go.mod
├── go.sum
├── cmd/
│   └── root.go              # Cobra command definitions, flag parsing, help text
├── internal/
│   ├── scanner/
│   │   ├── scanner.go       # File discovery and inspection (list, probe, select)
│   │   └── scanner_test.go
│   ├── parser/
│   │   ├── parser.go        # PM2 line parsing, JSON unwrapping
│   │   └── parser_test.go
│   ├── filter/
│   │   ├── filter.go        # Time and app name filtering
│   │   └── filter_test.go
│   ├── merge/
│   │   ├── merge.go         # Merge-sort across multiple file streams
│   │   └── merge_test.go
│   └── stats/
│       ├── stats.go         # Stats mode summary generation
│       └── stats_test.go
├── e2e/
│   └── e2e_test.go          # End-to-end tests that build and run the binary
├── testdata/                 # Real PM2 logs (already present)
│   ├── web-out.log
│   ├── web-error.log
│   └── ...
├── requirements.md
├── usage.md
└── plan.md
```

## Build order (TDD)

Each step follows the same pattern: write failing tests first, then implement until they pass.

### Step 1: Parser — PM2 line parsing and JSON unwrapping

**What:** Parse a single line of text into a structured log entry.

**Package:** `internal/parser`

**Core type:**

```go
type PM2Line struct {
    Message   string
    Timestamp time.Time
    Type      string    // "out" or "err"
    ProcessID int
    AppName   string
}

type LogEntry struct {
    // The output is built as an ordered map. See ProcessLine for field layout.
    Fields map[string]any
}
```

**Functions:**

- `ParsePM2Line(line []byte) (PM2Line, error)` — parse raw JSON into PM2Line. Returns error if not valid PM2 JSON (missing required fields).
- `IsPM2Line(line []byte) bool` — quick check without full parse.
- `ProcessLine(pm2 PM2Line) LogEntry` — always produces a LogEntry. The approach is **flatten + preserve + enrich**:
  - If `message` is a valid JSON object, its fields are **flattened to the top level** (e.g. `class`, `level`, `timestamp` become top-level keys).
  - The original `message` is **always preserved** as-is — the raw string, whether text or JSON.
  - If `message` is a valid JSON object, a `message_json` field is added containing the full parsed object (lossless access to the original structure).
  - If a flattened field would collide with `message`, it is **not** flattened (the raw string wins). It remains accessible via `message_json.message`.
  - `message_json` unwrapping is **recursive**: if `message_json` itself has a `message` field that is a JSON string, that inner object also gets a `message_json` alongside it (up to 2 levels). Only the first level of inner fields is flattened to the top level.
  - PM2 metadata is always injected as `pm2_*` fields.
  - If `message` is plain text, no flattening or `message_json` occurs — just `message` + `pm2_*` fields.
- `MarshalEntry(entry LogEntry) []byte` — serialize to compact JSON for output.
```

**Tests (write first):**

- Parse a valid PM2 JSON line, check all fields.
- Parse a line missing `app_name` — should return error.
- `IsPM2Line` returns false for MongoDB JSON, plain text, empty lines.
- Process a plain text message like `"yarn run v1.22.22\n"` — output has `message` (unchanged) + `pm2_*` fields, no `message_json`, no flattened fields.
- Process a JSON message (e.g. `ClientLogsService` log) — output has flattened fields (`class`, `level`, `timestamp`) at top level + `message` (raw string preserved) + `message_json` (parsed object) + `pm2_*` fields.
- Flattened field collision: inner JSON's `message` field is NOT flattened (raw `message` string wins). Accessible via `message_json.message`.
- Process a double-nested JSON message — first level fields are flattened to top, `message_json.message` is the raw inner string, `message_json.message_json` is the parsed inner object. Second level is NOT flattened to top.
- `pm2_timestamp` is always present.
- `message` in the output is always identical to the original PM2 line's `message` value.
- `MarshalEntry` produces compact JSON (no whitespace).
- Real lines from `testdata/web-out.log` — parse + process end-to-end (mix of JSON and plain text messages in the same file). Verify flattened fields present on JSON lines, absent on plain text lines.
- Real lines from `testdata/web-error.log` — plain text error messages pass through with `message` field intact, no `message_json`, no flattened fields.

### Step 2: Filter — time and app name matching

**What:** Determine whether a log entry matches the user's filters.

**Package:** `internal/filter`

**Core type:**

```go
type TimeFilter struct {
    After  time.Time  // zero value = no lower bound
    Before time.Time  // zero value = no upper bound
}

type Config struct {
    AppPattern string     // glob pattern, e.g. "cc*", "web", "*"
    Time       TimeFilter
}
```

**Functions:**

- `MatchApp(pattern, appName string) bool` — glob match using `filepath.Match` semantics.
- `MatchTime(filter TimeFilter, t time.Time) bool` — check if timestamp is within bounds.
- `ParseSince(s string) (time.Duration, error)` — parse duration strings like `10m`, `2h`, `60s`.
- `NewTimeFilterSince(d time.Duration) TimeFilter` — create a filter for "last N minutes" relative to now.
- `NewTimeFilterAbsolute(after, before string) (TimeFilter, error)` — parse RFC 3339 strings.

**Tests (write first):**

- `MatchApp("web", "web")` → true (exact).
- `MatchApp("cc*", "cctrace")` → true.
- `MatchApp("cc*", "claude-canvas")` → false.
- `MatchApp("*", "anything")` → true.
- `MatchApp("db*", "db")` → true.
- `MatchApp("db*", "db.migrate")` → true.
- `ParseSince("10m")` → 10 minutes.
- `ParseSince("2h")` → 2 hours.
- `ParseSince("60s")` → 60 seconds.
- `ParseSince("abc")` → error.
- `MatchTime` with only `After` set.
- `MatchTime` with only `Before` set.
- `MatchTime` with both set.
- `MatchTime` with neither set (zero values) → always true.

### Step 3: Scanner — file discovery and inspection

**What:** List files in a directory, probe each one to determine app name and time range, select relevant files.

**Package:** `internal/scanner`

**Core types:**

```go
type FileInfo struct {
    Path      string
    AppName   string
    StartTime time.Time
    EndTime   time.Time
    IsPM2     bool
}

type ScanResult struct {
    Files    []FileInfo
    AppNames []string  // unique, sorted
}
```

**Functions:**

- `ScanDirectory(dir string) (ScanResult, error)` — list all files, probe each, return results.
- `ProbeFile(path string) (FileInfo, error)` — read first and last few lines to find valid PM2 JSON lines, extract app name and time range.
- `SelectFiles(result ScanResult, appPattern string, timeFilter filter.TimeFilter) []FileInfo` — filter to files matching app and overlapping time range.

**Implementation notes for `ProbeFile`:**

- Read the first N lines (e.g. 20) looking for the first valid PM2 JSON line.
- Seek to the end of the file and read backward to find the last valid PM2 JSON line.
- If no PM2 lines found, mark `IsPM2 = false`.
- Extract `app_name` from the first valid PM2 line.
- Extract `timestamp` from first and last valid PM2 lines for time bounds.

**Tests (write first):**

- Probe `testdata/web-out.log` — should find app `web`, IsPM2=true, reasonable time bounds.
- Probe `testdata/web-error.log` — should find app `web`, type `err`.
- Probe `testdata/claudedb-out.log` — first line is plain text "claudedb", rest is MongoDB JSON. IsPM2=false.
- Probe `testdata/logload-error.log` — plain text. IsPM2=false.
- Probe `testdata/cctrace-out.log` — starts with plain text but has PM2 JSON lines. IsPM2 depends on whether PM2 lines exist (they don't — it's raw vite output). Need to verify.
- Scan full `testdata/` directory — should discover all PM2 apps.
- `SelectFiles` with app pattern `"web"` — returns only web files.
- `SelectFiles` with app pattern `"cc*"` — returns cctrace and cc-trace-viewer files (if they contain PM2 lines).
- `SelectFiles` with time filter — excludes files entirely outside the window.

### Step 4: Merge — merge-sort across file streams

**What:** Read multiple files concurrently, merge their lines in timestamp order.

**Package:** `internal/merge`

**Functions:**

```go
// Stream represents one file being read line by line.
// Lines are filtered and parsed before entering the merge.
type Stream struct {
    entries []LogEntry  // or a channel/iterator pattern
}

// MergeStreams takes multiple sorted streams and produces a single
// sorted stream of LogEntry values.
func MergeStreams(streams ...Stream) iter.Seq[LogEntry]
```

**Implementation approach:**

- Use a min-heap (priority queue) keyed on `PM2Timestamp`.
- Each stream provides its next entry. The heap picks the earliest.
- This is a standard k-way merge sort.

**Alternative (simpler, good enough for <10MB files):**

- Read all matching lines from all files into one slice.
- Sort by timestamp.
- This is simpler to implement and test. Files are <10MB each and there are only a handful. The merge-sort approach is more elegant but may be premature optimisation. We can start with the simple approach and optimise later if needed.

**Decision: start with the simple approach (read all, sort once).** The k-way merge is a clear optimisation path if performance becomes an issue. Mark it as a future enhancement.

**Tests (write first):**

- Merge two pre-sorted slices — output is sorted.
- Merge three slices with interleaved timestamps.
- Merge with one empty slice.
- Merge with a single slice (passthrough).
- Merge preserves all fields.

### Step 5: Stats and Schema — summary and field discovery

**What:** Produce the `--stats` output (per-app line counts, file counts, time ranges) and the `--schema` output (field path occurrence counts).

**Package:** `internal/stats`

**Functions:**

```go
// --- Stats mode ---

type AppStats struct {
    AppName   string
    FileCount int
    LineCount int
    StartTime time.Time
    EndTime   time.Time
}

func GatherStats(scanResult scanner.ScanResult, appPattern string, timeFilter filter.TimeFilter) ([]AppStats, error)
func FormatStats(stats []AppStats) string

// --- Schema mode ---

// GatherSchema processes the same filtered log lines as a normal query,
// but instead of outputting them, walks every output object and counts
// occurrences of each dot-delimited field path.
// Returns a map like {"message": 4433, "message_json": 3455, "message_json.level": 3455, ...}
func GatherSchema(entries []parser.LogEntry) map[string]int

// walkPaths recursively walks a map[string]any and yields all dot-paths.
// Used internally by GatherSchema.
func walkPaths(obj map[string]any, prefix string, counts map[string]int)
```

**Tests (write first):**

- Gather stats from `testdata/` for all apps.
- Gather stats filtered to `"web"`.
- FormatStats produces readable table with aligned columns.
- GatherSchema with a mix of plain text and JSON messages — `message` count equals total lines, `message_json` count equals JSON-message lines.
- GatherSchema with nested JSON — `message_json.message_json.component` path appears with correct count.
- GatherSchema output is sorted alphabetically by path.
- walkPaths on a flat object — one path per key.
- walkPaths on a nested object — produces dot-delimited paths for all leaves and intermediate objects.

### Step 6: CLI — cobra commands, flag parsing, help text

**What:** Wire everything together with cobra.

**Package:** `cmd/`

**Root command behaviour:**

1. Parse flags: `--app`, `--since`, `--after`, `--before`, `--dir`, `--stats`, `--schema`.
2. Validate: time filter is required always (including `--stats` and `--schema`).
3. Detect stdin: if stdin is piped, read from stdin instead of directory.
4. If `--schema`: scan + parse + filter logs (same pipeline as normal), then walk all output entries to count field paths, output as JSON, and exit.
5. If `--stats`: run stats mode and exit.
6. Scan directory → select files → read + parse + filter → merge-sort → output JSONL.
7. If no app matches: list available apps and exit with non-zero status.

**Help text:** detailed, with examples section and jq patterns, as described in `usage.md`. The help text should give an AI agent everything it needs to use the tool effectively without reading any external docs. Follow clig.dev principles.

**No tests in this step** — the CLI integration is covered by e2e tests.

### Step 7: main.go — entrypoint

**What:** Minimal `main.go` that calls `cmd.Execute()`.

### Step 8: End-to-end tests

**What:** Build the binary, run it against `testdata/`, assert on output.

**Package:** `e2e/`

**Approach:** Use `os/exec` to build and run the binary. Parse stdout as JSONL. Assert on content.

**Tests:**

- `TestBasicAppFilter` — run `jlogs --app web --since 24h --dir testdata/`, verify all output lines have `pm2_app_name == "web"`.
- `TestGlobAppFilter` — run with `--app "cc*"`, verify matching apps only.
- `TestNoAppListsApps` — run without `--app`, verify stderr lists available apps, exit code non-zero.
- `TestUnknownAppListsApps` — run with `--app "nonexistent"`, verify stderr lists apps.
- `TestSinceFilter` — run with a `--since` that covers part of the data, verify timestamps are within range.
- `TestAfterBeforeFilter` — run with absolute timestamps, verify bounds.
- `TestMissingTimeFilter` — run without any time flag, verify error message.
- `TestOutputIsSorted` — run a query, verify timestamps are monotonically increasing.
- `TestOutputIsCompactJSON` — verify each line parses as JSON and contains no unnecessary whitespace.
- `TestPM2MetadataPresent` — verify `pm2_timestamp`, `pm2_app_name`, `pm2_process_id`, `pm2_type` are present on every output line.
- `TestNestedJSONUnwrapped` — query `web` logs, find a `ClientLogsService` line, verify: `class` and `level` are flattened to top level, `message` is preserved as raw string, AND `message_json` contains full parsed object.
- `TestNonPM2FilesSkipped` — `claudedb` logs are not PM2 format, should not appear in output or cause errors.
- `TestStatsMode` — run with `--stats`, verify human-readable output to stderr.
- `TestSchemaFlag` — run with `--app web --since 24h --schema`, verify output is a JSON object where keys are dot-delimited field paths and values are integers. Verify `message` count equals total lines, `message_json` count is less than `message`, `pm2_timestamp` count equals total lines.
- `TestSchemaNestedPaths` — run schema on `web` logs, verify flattened fields (`class`, `level`, `isFromBrowser`) and nested paths (`message_json.class`, `message_json.level`) both appear with matching counts.
- `TestStdinMode` — pipe a file into `jlogs` via stdin, verify output matches file-based query.
- `TestPlainTextMessagesIncluded` — query `web` logs, verify lines like `"yarn run v1.22.22"` ARE present in output with `message` field but NO `message_json` field and NO flattened fields like `class` or `level`.
- `TestHelpOutput` — run with `--help`, verify it contains examples, flag descriptions, and jq patterns.

## Dependencies

- `github.com/spf13/cobra` — CLI framework.
- Standard library only for everything else (`encoding/json`, `path/filepath`, `container/heap`, `os`, `bufio`, `time`, `io`, `sort`).

## What NOT to build

- No colour output (always JSON, not a human-interactive tool).
- No config files.
- No subcommands — single root command.
- No `--verbose` or `--debug` flags.
- No log rotation awareness by filename — all intelligence comes from reading file content.
- No watching/tailing mode (out of scope — `tail -f | jlogs` covers this).

## Build & test commands

```bash
# Run all unit tests
go test ./internal/...

# Run e2e tests
go test ./e2e/ -v

# Run all tests
go test ./...

# Build
go build -o jlogs .
```
