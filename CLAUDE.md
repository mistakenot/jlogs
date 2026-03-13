# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**jlogs** is a Go CLI tool for filtering, unwrapping, and formatting structured JSON logs from PM2 (Node.js process manager). It outputs JSONL for piping to `jq`, `duckdb`, etc. Only dependency beyond stdlib is Cobra.

## Build & Development Commands

```bash
# Format
gofmt -w .

# Build
go build -o jlogs .

# Run all tests
go test ./...

# Unit tests only
go test ./internal/...

# E2E tests (builds binary, tests against testdata/)
go test ./e2e/ -v

# Run a single test
go test ./internal/parser/ -run TestParsePM2Line

# Release build (with version injection and size optimization)
go build -ldflags "-s -w -X pm2logs/cmd.version=v1.0.0" -o jlogs .
```

## Release Process

Follow these steps for every commit after finishing work:

1. `gofmt -w .` — format all Go files
2. `go build -o jlogs .` — compile
3. `go test ./...` — run all tests
4. `git add` and `git commit` with a descriptive message
5. `git push`
6. If releasing: `git tag vX.Y.Z && git push --tags` (semantic versioning)

## Architecture

Pipeline-based design where each stage transforms data flowing through:

```
Scanner → Selection → Parser → Filter → Merge → Output (JSONL)
```

- **`cmd/root.go`** — Cobra CLI setup, flag parsing, orchestrates the pipeline. Three modes: normal (JSONL output), `--stats` (per-app summary), `--schema` (field path discovery). Supports stdin when piped.
- **`internal/scanner/`** — Discovers log files, probes content (not filenames) to detect PM2 format, extracts app names and time ranges.
- **`internal/parser/`** — Parses PM2 JSON lines, unwraps nested JSON messages (up to 2 levels deep), flattens inner fields to top level. Uses ordered field lists (not maps) to preserve JSON output order.
- **`internal/filter/`** — Time range filtering (relative durations like `30m`, absolute RFC 3339) and app name glob matching.
- **`internal/merge/`** — Merge-sorts entries from multiple files by timestamp using `slices.SortStableFunc`.
- **`internal/stats/`** — Gathers per-app statistics and schema (field path occurrence counts).

## Key Design Decisions

- **Content-based detection**: Files identified by actual PM2 JSON content, not filename conventions.
- **Collision handling**: When flattening nested JSON, inner `message` field is not promoted (raw `message` string wins). Access inner message via `message_json.message`.
- **Batching**: PM2 messages containing embedded newlines are split into separate entries.
- **LogEntry ordering**: Uses `[]Field` slice (not map) so JSON field order is deterministic.
