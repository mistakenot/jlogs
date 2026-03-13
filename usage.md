# jlogs — User Guide

`jlogs` is a fast CLI tool for filtering, unwrapping, and formatting structured JSON log files produced by PM2. It merges multiple log files, unwraps nested JSON messages, and outputs clean JSONL that you can pipe into `jq`, `duckdb`, or any other tool.

## Quick start

Show the last 30 minutes of logs for the `web` app:

```bash
jlogs --app web --since 30m
```

Show the last 60 seconds of logs for all apps starting with `cc`:

```bash
jlogs --app "cc*" --since 60s
```

Show logs between two absolute timestamps:

```bash
jlogs --app db --after "2026-03-13T12:00:00Z" --before "2026-03-13T12:30:00Z"
```

## Installation

Build from source:

```bash
go build -o jlogs .
```

Then move the binary somewhere on your `$PATH`:

```bash
mv jlogs /usr/local/bin/
```

## How jlogs reads log files

When you run `jlogs`, it does the following:

1. **Lists all files** in the log directory (default `~/.pm2/logs/`).
2. **Inspects each file** by reading the first and last JSON lines to determine which app produced it and what time range it covers. Non-JSON lines at the start or end of a file are skipped during this inspection.
3. **Selects files** that match your `--app` filter and overlap with your requested time range.
4. **Streams and filters** the selected files line by line, keeping only lines that match your app and time filters.
5. **Merge-sorts** lines from multiple files by timestamp (ascending), so the output is always in chronological order. Each file is assumed to be internally sorted by timestamp.
6. **Unwraps nested JSON** in the `message` field — flattening inner fields to the top level, preserving the original `message` string, and adding a `message_json` field with the parsed object (up to 2 levels deep) — then outputs clean JSONL.

This approach means `jlogs` never assumes anything about filenames. It doesn't matter if files are named `web-out.log`, `web-out-1.log`, or `web-out__2026-03-13.log` — it reads the actual content to figure out what's inside. Both stdout (`-out`) and stderr (`-error`) files are read and merged into a single stream.

## Discovering available apps

If you run `jlogs` without an `--app` flag, or with an app name that doesn't match anything, it lists the available apps and exits:

```bash
$ jlogs --since 30m
No app specified. Available apps in /home/user/.pm2/logs/:

  caddy            2 files    12:04 - 12:32
  cctrace          2 files    12:05 - 12:31
  cc-trace-viewer  2 files    12:06 - 12:30
  claude-canvas    2 files    12:04 - 12:28
  claudedb         2 files    12:05 - 12:32
  db               2 files    12:04 - 12:28
  fb               2 files    12:05 - 12:20
  neo4j            2 files    12:04 - 12:15
  ngrok            2 files    12:05 - 12:32
  tsindex          2 files    12:06 - 12:30
  web              2 files    12:04 - 12:32

Use --app <name> to filter. Glob patterns are supported (e.g. --app "cc*").
```

If your app name doesn't match:

```bash
$ jlogs --app "foo" --since 30m
No apps matching "foo". Available apps in /home/user/.pm2/logs/:

  caddy
  cctrace
  cc-trace-viewer
  ...

Use --app <name> to filter. Glob patterns are supported (e.g. --app "cc*").
```

## Time filtering

A time filter is always required. You must provide either `--since` (relative) or `--after`/`--before` (absolute).

### Relative time with `--since`

`--since` accepts a duration string. Supported units are `s` (seconds), `m` (minutes), and `h` (hours):

```bash
# Last 60 seconds
jlogs --app web --since 60s

# Last 10 minutes
jlogs --app web --since 10m

# Last 2 hours
jlogs --app web --since 2h
```

### Absolute time with `--after` and `--before`

You can use `--after` and `--before` independently or together. Both accept RFC 3339 timestamps:

```bash
# Everything after a specific time
jlogs --app web --after "2026-03-13T12:00:00Z"

# Everything before a specific time
jlogs --app web --before "2026-03-13T12:30:00Z"

# A specific window
jlogs --app web --after "2026-03-13T12:00:00Z" --before "2026-03-13T12:30:00Z"
```

### Error when no time filter is given

```bash
$ jlogs --app web
Error: a time filter is required. Use --since, --after, or --before.

Examples:
  jlogs --app web --since 30m
  jlogs --app web --after "2026-03-13T12:00:00Z"
```

## App name filtering

The `--app` flag supports exact names and glob patterns:

```bash
# Exact match
jlogs --app web --since 30m

# Glob pattern — matches cctrace, cc-trace-viewer
jlogs --app "cc*" --since 30m

# Another glob — matches claude-canvas, claudedb
jlogs --app "claude*" --since 30m
```

Always quote glob patterns to prevent shell expansion.

## Output format

`jlogs` always outputs compact JSONL (one JSON object per line, no extra whitespace). Every valid PM2 log line produces an output line — whether the message is JSON or plain text.

When a query matches zero log entries, `jlogs` outputs `[]` to stdout and prints `No matching results found.` to stderr. This makes it easy for calling tools and AI agents to distinguish "no results" from an error.

### What the raw PM2 log looks like

A typical PM2 JSON log line looks like this:

```json
{
  "message": "{\"class\":\"ClientLogsService\",\"level\":\"info\",\"message\":\"[browser] Token refreshed\",\"timestamp\":\"2026-03-13T12:32:13.636Z\"}",
  "timestamp": "2026-03-13T12:32:14.559Z",
  "type": "out",
  "process_id": 8,
  "app_name": "web"
}
```

The `message` field contains a JSON string, which itself might contain further nested JSON strings.

### What jlogs outputs

`jlogs` **flattens, preserves, and enriches** — inner JSON fields are promoted to the top level for easy querying, the original `message` string is always kept, and a `message_json` field provides lossless access to the parsed structure.

**JSON message — single level of nesting:**

```json
{"class":"ClientLogsService","level":"info","message":"{\"class\":\"ClientLogsService\",\"level\":\"info\",\"message\":\"[browser] Token refreshed\",\"timestamp\":\"2026-03-13T12:32:13.636Z\"}","message_json":{"class":"ClientLogsService","level":"info","message":"[browser] Token refreshed","timestamp":"2026-03-13T12:32:13.636Z"},"timestamp":"2026-03-13T12:32:13.636Z","pm2_timestamp":"2026-03-13T12:32:14.559Z","pm2_app_name":"web","pm2_process_id":8,"pm2_type":"out"}
```

Note: `class`, `level`, and `timestamp` are flattened to the top level. The inner JSON's `message` field (`"[browser] Token refreshed"`) is **not** flattened because it would collide with the preserved raw `message` string — access it via `message_json.message`.

**Plain text message:**

```json
{"message":"yarn run v1.22.22\n","pm2_timestamp":"2026-03-13T12:04:54.562Z","pm2_app_name":"web","pm2_process_id":8,"pm2_type":"out"}
```

The rules for this transformation are:

- If `message` is a valid JSON object, its fields are **flattened to the top level** (e.g. `class`, `level`, `timestamp` become top-level keys).
- `message` is **always preserved exactly as-is** — the original raw string, whether text or JSON. If a flattened field would collide with `message`, the raw string wins (the inner value is accessible via `message_json.message`).
- Inner fields starting with `pm2_` are **not** flattened, since they would collide with the PM2 metadata fields. Access them via `message_json` instead.
- If `message` is a valid JSON object, a `message_json` field is added containing the full parsed object. If `message` is not valid JSON, no `message_json` field is added and no fields are flattened.
- The `message_json` unwrapping is **recursive**: if `message_json` itself has a `message` field that is a JSON string, that inner message also gets a `message_json` field inside it. This recurse applies up to 2 levels deep. Only the first level of inner fields is flattened to the top level.
- PM2 metadata is added with a `pm2_` prefix: `pm2_timestamp`, `pm2_app_name`, `pm2_process_id`, `pm2_type`.
- Every valid PM2 log line produces output — you never lose data.

### When the message is plain text

When the `message` field is not a JSON object, it passes through unchanged — no `message_json` field and no flattened fields. This is common for startup output, error messages, and general application logging:

```json
{"message":"yarn run v1.22.22\n","timestamp":"2026-03-13T12:04:54.562Z","type":"out","process_id":8,"app_name":"web"}
```

becomes:

```json
{"message":"yarn run v1.22.22\n","pm2_timestamp":"2026-03-13T12:04:54.562Z","pm2_app_name":"web","pm2_process_id":8,"pm2_type":"out"}
```

Note that in a single file (e.g. `web-out.log`), some lines will have `message_json` and others won't — this is expected. Downstream tools like `jq` handle this naturally:

```bash
# Show only lines that had structured JSON messages
jlogs --app web --since 30m | jq 'select(.message_json != null)'

# Show only plain text messages
jlogs --app web --since 30m | jq 'select(.message_json == null) | .message'
```

### Two levels of nesting (browser logs)

Some apps forward browser logs, producing two levels of JSON nesting. The first level of inner fields is flattened to the top level. The `message_json` unwrapping is recursive — if a parsed `message_json` object itself contains a `message` field that is a JSON string, that field also gets a `message_json` alongside it. Only the first level is flattened; deeper levels stay nested.

Input:
```json
{"message":"{\"class\":\"ClientLogsService\",\"message\":\"{\\\"component\\\":\\\"Auth\\\",\\\"action\\\":\\\"refresh\\\"}\",\"timestamp\":\"2026-03-13T12:32:13.636Z\"}","timestamp":"2026-03-13T12:32:14.559Z","type":"out","process_id":8,"app_name":"web"}
```

Output (formatted for readability):
```json
{
  "class": "ClientLogsService",
  "message": "{\"class\":\"ClientLogsService\",\"message\":\"{\\\"component\\\":\\\"Auth\\\",\\\"action\\\":\\\"refresh\\\"}\",\"timestamp\":\"2026-03-13T12:32:13.636Z\"}",
  "message_json": {
    "class": "ClientLogsService",
    "message": "{\"component\":\"Auth\",\"action\":\"refresh\"}",
    "message_json": {
      "component": "Auth",
      "action": "refresh"
    },
    "timestamp": "2026-03-13T12:32:13.636Z"
  },
  "timestamp": "2026-03-13T12:32:13.636Z",
  "pm2_timestamp": "2026-03-13T12:32:14.559Z",
  "pm2_app_name": "web",
  "pm2_process_id": 8,
  "pm2_type": "out"
}
```

Top-level `class` and `timestamp` are flattened from the first level of inner JSON. The second level (`component`, `action`) stays nested inside `message_json.message_json`. The original `message` strings at every level are preserved — you can always access the raw original via `message`, or the structured version via `message_json`.

## Stats mode

Use `--stats` to get a summary of what's in the log directory without outputting actual log lines:

```bash
$ jlogs --stats --since 2h
App                Files  Lines   Time Range
caddy              2      142     12:04:52 - 12:32:31
cctrace            2      4556    12:05:01 - 12:31:58
cc-trace-viewer    2      12      12:06:22 - 12:30:45
claude-canvas      2      9       12:04:50 - 12:28:10
claudedb           2      1011    12:05:03 - 12:32:28
db                 2      49      12:04:57 - 12:28:08
fb                 2      23      12:05:12 - 12:20:44
neo4j              2      8       12:04:55 - 12:15:30
ngrok              2      15      12:05:08 - 12:32:01
tsindex            2      29      12:06:15 - 12:30:22
web                2      4998    12:04:54 - 12:32:31
```

You can combine `--stats` with `--app` to filter:

```bash
$ jlogs --stats --app "cc*" --since 1h
App                Files  Lines   Time Range
cctrace            2      4556    12:05:01 - 12:31:58
cc-trace-viewer    2      12      12:06:22 - 12:30:45
```

Stats output goes to stderr so it doesn't interfere with piping. The stats output is the one exception where `jlogs` outputs human-readable text instead of JSON.

## Reading from stdin

`jlogs` can read from stdin instead of files. Pipe PM2 JSON logs into it and the same filtering and unwrapping applies:

```bash
cat /var/log/pm2/web-out.log | jlogs --app web --since 30m
```

```bash
# Combine with other tools
ssh prod-server "cat ~/.pm2/logs/web-out.log" | jlogs --app web --since 1h
```

```bash
# Tail and filter in real time
tail -f ~/.pm2/logs/web-out.log | jlogs --app web --since 24h
```

When reading from stdin, all the same flags work: `--app`, `--since`, `--after`, `--before`. If `--dir` is explicitly provided, directory mode is used even when stdin is piped — this prevents jlogs from hanging when invoked inside scripts or pipelines where stdin happens to be redirected.

## Custom log directory

By default, `jlogs` reads from `~/.pm2/logs/`. Override this with `--dir`:

```bash
jlogs --app web --since 30m --dir /var/log/pm2/

jlogs --app web --since 1h --dir ./testdata/
```

## Piping to other tools

`jlogs` outputs JSONL, so it works naturally with standard tools:

### jq

```bash
# Pretty-print output
jlogs --app web --since 10m | jq .

# Filter to error-level logs (level is flattened to top level)
jlogs --app web --since 30m | jq 'select(.level == "error")'

# Extract just the raw messages
jlogs --app web --since 10m | jq -r .message

# Count logs by level (plain text lines have no level field)
jlogs --app web --since 1h | jq -r '.level // "plain_text"' | sort | uniq -c | sort -rn
```

### DuckDB

```bash
# Query logs with SQL (level is a top-level field)
jlogs --app web --since 1h > /tmp/logs.jsonl
duckdb -c "SELECT level, count(*) FROM read_json_auto('/tmp/logs.jsonl') GROUP BY level"

# Or pipe directly
jlogs --app web --since 1h | duckdb -c "SELECT * FROM read_json_auto('/dev/stdin') WHERE level = 'error'"
```

### grep

```bash
# Quick text search across unwrapped JSON
jlogs --app web --since 30m | grep "Token refreshed"
```

## Schema command

Use `--schema` to scan the actual log data and output a JSON object showing every unique field path and how many log lines contain that path. This uses the same `--app` and time filters as a normal query, so you see the schema for exactly the data you'd be querying.

```bash
$ jlogs --app web --since 1h --schema
{
  "class": 3200,
  "extra": 265,
  "isFromBrowser": 265,
  "level": 3455,
  "message": 4433,
  "message_json": 3455,
  "message_json.class": 3200,
  "message_json.extra": 265,
  "message_json.isFromBrowser": 265,
  "message_json.level": 3455,
  "message_json.message": 3455,
  "message_json.message_json": 120,
  "message_json.message_json.action": 120,
  "message_json.message_json.component": 120,
  "message_json.stacks": 265,
  "message_json.timestamp": 3455,
  "message_json.userAgent": 265,
  "pm2_app_name": 4433,
  "pm2_process_id": 4433,
  "pm2_timestamp": 4433,
  "pm2_type": 4433,
  "stacks": 265,
  "timestamp": 3455,
  "userAgent": 265
}
```

This tells an AI agent at a glance: "there are 4433 total lines, 3455 have structured JSON messages, 265 are from the browser, 120 have double-nested JSON" — all without reading a single log line. Flattened fields like `level`, `class`, `timestamp` appear at the top level alongside their `message_json.*` counterparts.

You can use `--schema` across all apps to get a broad view:

```bash
$ jlogs --app "*" --since 1h --schema
```

Or narrow it to a specific app to see what fields that app produces:

```bash
$ jlogs --app cctrace --since 30m --schema
```

## Full flag reference

| Flag | Short | Description |
|---|---|---|
| `--app` | `-a` | App name filter. Supports glob patterns (e.g. `"cc*"`). |
| `--since` | `-s` | Relative time filter (e.g. `10m`, `2h`, `60s`). |
| `--after` | | Absolute start time (RFC 3339). |
| `--before` | | Absolute end time (RFC 3339). |
| `--dir` | `-d` | Log directory path. Default: `~/.pm2/logs/`. |
| `--stats` | | Show summary stats instead of log lines. Cannot be combined with `--schema`. |
| `--schema` | | Scan logs and output a JSON object mapping every field path to its occurrence count. Cannot be combined with `--stats`. |
| `--help` | `-h` | Show help with usage examples. |
| `--version` | `-v` | Print version and exit. |

## Help output

The `--help` output is designed to be self-documenting for both humans and AI agents. It includes a description, flag reference, usage examples, jq patterns, and a note about `--schema`. Here is what `jlogs --help` outputs:

```
jlogs - Filter and format PM2 JSON log files

Reads PM2 structured JSON log files, filters by app and time range,
unwraps nested JSON messages, and outputs clean JSONL.

Inner JSON fields are flattened to the top level for easy querying.
The original 'message' field is always preserved as-is.
When 'message' is valid JSON, a 'message_json' field is also added with the parsed object.

Usage:
  jlogs [flags]

Examples:
  # Last 30 minutes of web app logs
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

Flags:
  -a, --app string      App name filter (supports globs, e.g. "cc*")
  -s, --since string    Relative time filter (e.g. 10m, 2h, 60s)
      --after string    Absolute start time (RFC 3339)
      --before string   Absolute end time (RFC 3339)
  -d, --dir string      Log directory (default "~/.pm2/logs/")
      --stats           Show summary stats instead of log lines
      --schema          Output field paths with occurrence counts as JSON
  -h, --help            Help for jlogs
  -v, --version         Version for jlogs

Tip: use --schema to discover what fields are available before writing jq filters.
```

## Examples for common tasks

**"The web app is throwing errors, show me the last 5 minutes"**

```bash
jlogs --app web --since 5m | jq 'select(.level == "error")'
```

**"What apps are running and producing logs?"**

```bash
jlogs --stats --since 1h
```

**"Show me all database-related logs from the last hour"**

```bash
jlogs --app "db*" --since 1h
```

**"Show me browser-originated errors from the web app"**

```bash
jlogs --app web --since 30m | jq 'select(.isFromBrowser == true and .level == "error")'
```

**"Export the last 2 hours of cctrace logs for analysis"**

```bash
jlogs --app cctrace --since 2h > /tmp/cctrace.jsonl
```

**"How many log lines per app in the last hour?"**

```bash
jlogs --stats --since 1h
```

**"Search all apps for a specific error message"**

```bash
jlogs --app "*" --since 1h | grep "connection refused"
```

**"Get logs from a remote server"**

```bash
ssh prod "jlogs --app web --since 10m"
```

**"Pipe logs from a non-standard directory on a remote machine"**

```bash
ssh prod "cat /var/log/pm2/*.log" | jlogs --app web --since 30m
```
