# jlogs

A fast CLI tool for filtering, unwrapping, and formatting structured JSON log files produced by [PM2](https://pm2.keymetrics.io/). It merges multiple log files, unwraps nested JSON messages, and outputs clean JSONL that you can pipe into `jq`, `duckdb`, or any other tool.

Built for AI coding agents and developers who need to quickly explore logs from locally running PM2 systems.

## Installation

Download the latest binary for your platform from [GitHub Releases](https://github.com/mistakenot/jlogs/releases/latest).

### Mac (Apple Silicon)

```bash
mkdir -p ~/.local/bin \
  && curl -fsSL https://github.com/mistakenot/jlogs/releases/latest/download/jlogs-darwin-arm64 -o ~/.local/bin/jlogs \
  && chmod +x ~/.local/bin/jlogs \
  && jlogs --help
```

### Mac (Intel)

```bash
mkdir -p ~/.local/bin \
  && curl -fsSL https://github.com/mistakenot/jlogs/releases/latest/download/jlogs-darwin-amd64 -o ~/.local/bin/jlogs \
  && chmod +x ~/.local/bin/jlogs \
  && jlogs --help
```

> If `~/.local/bin` isn't on your `$PATH`: `echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc && source ~/.zshrc`

### Linux (x86_64)

```bash
sudo curl -fsSL https://github.com/mistakenot/jlogs/releases/latest/download/jlogs-linux-amd64 -o /usr/local/bin/jlogs \
  && sudo chmod +x /usr/local/bin/jlogs \
  && jlogs --help
```

### Build from source

Requires Go 1.25+:

```bash
go build -o jlogs .
mv jlogs /usr/local/bin/
```

## Quick start

```bash
# Last 30 minutes of logs for the "web" app
jlogs --app web --since 30m

# Last 60 seconds, glob pattern matching
jlogs --app "cc*" --since 60s

# Absolute time range
jlogs --app db --after "2025-01-01T12:00:00Z" --before "2025-01-01T12:30:00Z"

# Discover available apps
jlogs --since 1h

# See what fields exist in the data
jlogs --app web --since 1h --schema

# Summary stats
jlogs --stats --since 2h
```

## How it works

1. **Lists all files** in the log directory (default `~/.pm2/logs/`).
2. **Inspects each file** by reading the first and last JSON lines to determine which app produced it and what time range it covers. Non-JSON lines are skipped during inspection.
3. **Selects files** that match the `--app` filter and overlap with the requested time range.
4. **Streams and filters** selected files line by line, keeping only lines that match the app and time filters.
5. **Merge-sorts** lines from multiple files by timestamp (ascending), producing chronologically ordered output.
6. **Unwraps nested JSON** in the `message` field — flattening inner fields to the top level, preserving the original `message` string, and adding a `message_json` field with the parsed object (up to 2 levels deep) — then outputs clean JSONL.

File names are irrelevant — `jlogs` reads actual content to determine what's inside each file. Both stdout (`-out`) and stderr (`-error`) PM2 log files are read and merged into a single stream.

## Flags

| Flag | Short | Description |
|---|---|---|
| `--app` | `-a` | App name filter. Supports glob patterns (e.g. `"cc*"`). |
| `--since` | `-s` | Relative time filter (e.g. `10m`, `2h`, `60s`). |
| `--after` | | Absolute start time (RFC 3339). |
| `--before` | | Absolute end time (RFC 3339). |
| `--dir` | `-d` | Log directory path. Default: `~/.pm2/logs/`. |
| `--stats` | | Show summary stats instead of log lines. Cannot be combined with `--schema`. |
| `--schema` | | Output a JSON object mapping every field path to its occurrence count. Cannot be combined with `--stats`. |
| `--help` | `-h` | Show help with usage examples. |
| `--version` | `-v` | Print version and exit. |

A time filter (`--since`, `--after`, or `--before`) is always required.

## Time filtering

### Relative time with `--since`

Accepts a duration string. Supported units: `s` (seconds), `m` (minutes), `h` (hours).

```bash
jlogs --app web --since 60s
jlogs --app web --since 10m
jlogs --app web --since 2h
```

### Absolute time with `--after` and `--before`

Both accept RFC 3339 timestamps and can be used independently or together:

```bash
jlogs --app web --after "2025-01-01T12:00:00Z"
jlogs --app web --before "2025-01-01T12:30:00Z"
jlogs --app web --after "2025-01-01T12:00:00Z" --before "2025-01-01T12:30:00Z"
```

## App discovery

Running without `--app`, or with an app name that doesn't match anything, lists the available apps:

```
$ jlogs --since 30m
No app specified. Available apps in /home/user/.pm2/logs/:

  caddy
  cctrace
  cc-trace-viewer
  web
  ...

Use --app <name> to filter. Glob patterns are supported (e.g. --app "cc*").
```

## Output format

`jlogs` always outputs compact JSONL (one JSON object per line). Every valid PM2 log line produces an output line.

When a query matches zero log entries, `jlogs` outputs `[]` to stdout and prints `No matching results found.` to stderr.

### JSON message unwrapping

A typical PM2 log line has a `message` field containing a JSON string:

```json
{"message":"{\"class\":\"ClientLogsService\",\"level\":\"info\",\"message\":\"Token refreshed\"}","timestamp":"2025-01-01T12:32:14Z","type":"out","process_id":8,"app_name":"web"}
```

`jlogs` **flattens, preserves, and enriches**:

- Inner JSON fields (`class`, `level`, etc.) are **flattened to the top level** for easy querying.
- The original `message` string is **always preserved as-is**.
- A `message_json` field is added with the full parsed object.
- If `message_json` itself has a `message` field that is a JSON string, it is recursively unwrapped (up to 2 levels deep).
- PM2 metadata is added with a `pm2_` prefix: `pm2_timestamp`, `pm2_app_name`, `pm2_process_id`, `pm2_type`.
- Inner fields starting with `pm2_` are not flattened (to avoid collisions with PM2 metadata).
- If a flattened field would collide with `message`, the raw string wins — access the inner value via `message_json.message`.

**Plain text messages** pass through unchanged — just `message` + `pm2_*` fields, no `message_json`, no flattened fields.

## Stats mode

Get a per-app summary without outputting log lines:

```bash
$ jlogs --stats --since 2h
App                Files  Lines   Time Range
caddy              2      142     12:04:52 - 12:32:31
web                2      4998    12:04:54 - 12:32:31

$ jlogs --stats --app "cc*" --since 1h
App                Files  Lines   Time Range
cctrace            2      4556    12:05:01 - 12:31:58
cc-trace-viewer    2      12      12:06:22 - 12:30:45
```

Stats output goes to stderr so it doesn't interfere with piping.

## Schema mode

Scan log data and output a JSON object showing every unique field path and how many log lines contain it:

```bash
$ jlogs --app web --since 1h --schema
{
  "class": 3200,
  "level": 3455,
  "message": 4433,
  "message_json": 3455,
  "message_json.class": 3200,
  "message_json.level": 3455,
  "pm2_app_name": 4433,
  "pm2_timestamp": 4433,
  "timestamp": 3455
}
```

Uses the same `--app` and time filters as a normal query, so the schema reflects exactly the data you'd be querying.

## Reading from stdin

Pipe PM2 JSON logs into `jlogs` and the same filtering and unwrapping applies:

```bash
cat /var/log/pm2/web-out.log | jlogs --app web --since 30m
ssh prod "cat ~/.pm2/logs/web-out.log" | jlogs --app web --since 1h
tail -f ~/.pm2/logs/web-out.log | jlogs --app web --since 24h
```

All flags work with stdin. If `--dir` is explicitly provided, directory mode is used even when stdin is piped.

## Custom log directory

```bash
jlogs --app web --since 30m --dir /var/log/pm2/
jlogs --app web --since 1h --dir ./testdata/
```

## Piping to other tools

### jq

```bash
# Pretty-print
jlogs --app web --since 10m | jq .

# Filter to error-level logs
jlogs --app web --since 30m | jq 'select(.level == "error")'

# Extract raw messages
jlogs --app web --since 10m | jq -r .message

# Count logs by level
jlogs --app web --since 1h | jq -r '.level // "plain_text"' | sort | uniq -c | sort -rn

# Browser-originated errors
jlogs --app web --since 30m | jq 'select(.isFromBrowser == true and .level == "error")'

# Access double-nested fields
jlogs --app web --since 1h | jq '.message_json?.message_json?.component'
```

### DuckDB

```bash
jlogs --app web --since 1h > /tmp/logs.jsonl
duckdb -c "SELECT level, count(*) FROM read_json_auto('/tmp/logs.jsonl') GROUP BY level"

# Or pipe directly
jlogs --app web --since 1h | duckdb -c "SELECT * FROM read_json_auto('/dev/stdin') WHERE level = 'error'"
```

### grep

```bash
jlogs --app web --since 30m | grep "connection refused"
```

## Project structure

```
├── main.go                  # Entrypoint
├── cmd/
│   └── root.go              # Cobra command, flag parsing, help text
├── internal/
│   ├── scanner/             # File discovery and inspection
│   ├── parser/              # PM2 line parsing, JSON unwrapping
│   ├── filter/              # Time and app name filtering
│   ├── merge/               # Merge-sort across file streams
│   └── stats/               # Stats and schema modes
└── e2e/                     # End-to-end tests (builds and runs the binary)
```

## Development

```bash
# Run all unit tests
go test ./internal/...

# Run e2e tests (builds the binary and runs it against testdata/)
go test ./e2e/ -v

# Run all tests
go test ./...

# Build
go build -o jlogs .
```
