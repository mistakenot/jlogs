package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Preflight checks for PM2 and project setup",
	Long: `Run preflight checks for PM2 logging setup.

Checks:
  1. PM2 default log directory (~/.pm2/logs/) contains log files
  2. An ecosystem.config.{js,cjs,ts} file exists in the current directory
  3. JSON log mode is enabled in the ecosystem config
  4. Appends jlogs usage hints to AGENTS.md and/or CLAUDE.md if present`,
	RunE: runInit,
}

func init() {
	rootCmd.AddCommand(initCmd)
}

const jlogsSnippet = `
## jlogs — PM2 Log Viewer

This project uses jlogs to filter and query PM2 structured JSON logs.
Run ` + "`jlogs --help`" + ` for full usage, or try:

` + "```bash" + `
jlogs --since 30m              # list available apps
jlogs --app <name> --since 30m # last 30 min of logs as JSONL
jlogs --app <name> --since 1h --schema  # discover fields and filterable values
jlogs --app <name> --since 1h | jq .    # pretty-print with jq
` + "```" + `
`

func runInit(cmd *cobra.Command, args []string) error {
	var hasError bool

	// 1. Check PM2 log directory
	logDir := defaultLogDir()
	entries, err := os.ReadDir(logDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: Cannot read PM2 log directory %s: %v\n", logDir, err)
		hasError = true
	} else if len(entries) == 0 {
		fmt.Fprintf(os.Stderr, "ERROR: PM2 log directory %s is empty — no log files found.\n", logDir)
		hasError = true
	} else {
		fmt.Fprintf(os.Stderr, "OK: PM2 log directory %s contains %d file(s).\n", logDir, len(entries))
	}

	// 2. Check for ecosystem config
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("cannot determine working directory: %w", err)
	}

	ecosystemFile := findEcosystemConfig(cwd)
	if ecosystemFile == "" {
		fmt.Fprintf(os.Stderr, "WARN: No ecosystem.config.{js,cjs,ts} found in %s.\n", cwd)
	} else {
		fmt.Fprintf(os.Stderr, "OK: Found %s\n", filepath.Base(ecosystemFile))

		// 3. Check for JSON log mode
		if !checkJSONMode(ecosystemFile) {
			fmt.Fprintf(os.Stderr, "ERROR: JSON log mode does not appear to be enabled in %s.\n", filepath.Base(ecosystemFile))
			fmt.Fprintf(os.Stderr, "       Add log_type: 'json' to your app config for structured logging.\n")
			hasError = true
		} else {
			fmt.Fprintf(os.Stderr, "OK: JSON log mode appears to be enabled.\n")
		}
	}

	// 4. Append jlogs info to AGENTS.md / CLAUDE.md
	mdFiles := []string{"AGENTS.md", "CLAUDE.md"}
	appendedAny := false
	for _, name := range mdFiles {
		path := filepath.Join(cwd, name)
		if _, err := os.Stat(path); err != nil {
			continue
		}

		content, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARN: Could not read %s: %v\n", name, err)
			continue
		}

		if strings.Contains(string(content), "jlogs") {
			fmt.Fprintf(os.Stderr, "OK: %s already mentions jlogs, skipping.\n", name)
			continue
		}

		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARN: Could not open %s for writing: %v\n", name, err)
			continue
		}
		_, err = f.WriteString(jlogsSnippet)
		f.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARN: Could not write to %s: %v\n", name, err)
			continue
		}
		fmt.Fprintf(os.Stderr, "OK: Appended jlogs info to %s.\n", name)
		appendedAny = true
	}

	if !appendedAny {
		found := false
		for _, name := range mdFiles {
			if _, err := os.Stat(filepath.Join(cwd, name)); err == nil {
				found = true
				break
			}
		}
		if !found {
			fmt.Fprintf(os.Stderr, "INFO: No AGENTS.md or CLAUDE.md found in %s.\n", cwd)
		}
	}

	if hasError {
		return fmt.Errorf("preflight checks failed — see errors above")
	}

	fmt.Fprintf(os.Stderr, "\nAll checks passed. Run `jlogs --help` for usage.\n")
	return nil
}

func findEcosystemConfig(dir string) string {
	candidates := []string{
		"ecosystem.config.js",
		"ecosystem.config.cjs",
		"ecosystem.config.ts",
	}
	for _, name := range candidates {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func checkJSONMode(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	content := string(data)
	// Look for common patterns that enable JSON logging in PM2
	patterns := []string{
		"log_type",
		"\"json\"",
		"'json'",
	}
	for _, p := range patterns {
		if strings.Contains(content, p) {
			return true
		}
	}
	return false
}
