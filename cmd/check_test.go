package cmd

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dgethings/chunt/internal/features/cisco_ios_jinja2"
)

// configWithDiags mirrors example.ios.j2: one undefined ACL and one duplicate
// hostname, each yielding a diagnostic at a known position. Two leading spaces
// on the interface line put the ACL name at column 19 (1-based).
const configWithDiags = "!\ninterface g0/0\n  ip access-group foo in\n!\nhostname foo\nhostname foo\n"

// resetRoot restores cobra global state (output writers + parsed args) between
// tests so one test's SetOut/SetArgs can't leak into another.
func resetRoot(t *testing.T) {
	t.Helper()
	rootCmd.SetOut(os.Stdout)
	rootCmd.SetErr(os.Stderr)
	rootCmd.SetArgs(nil)
}

// TestCheckOutputFormat locks the stable, machine-parseable
// "file:line:col: message" format produced by `chunt check` (chunt-lto):
// no redundant [chunt] prefix and no structured grammar log line on stdout.
func TestCheckOutputFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "example.ios.j2")
	if err := os.WriteFile(path, []byte(configWithDiags), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"--log-level=info", "check", path})
	defer resetRoot(t)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("check: %v", err)
	}

	out := stdout.String()

	// Fix 2: the LSP Diagnostic.Source must not appear as a [chunt] prefix.
	if strings.Contains(out, "[chunt]") {
		t.Errorf("stdout contains [chunt] prefix:\n%s", out)
	}

	// Fix 1: stdout must carry diagnostics only — no leaked log line.
	if strings.Contains(out, "msg=grammar") || strings.Contains(out, "level=") {
		t.Errorf("stdout leaked a structured log line:\n%s", out)
	}

	// Every line is "<path>:<line>:<col>: <message>" — two diagnostics,
	// order-independent (ref-diagnostic ordering is not contractual).
	got := strings.Split(strings.TrimRight(out, "\n"), "\n")
	want := map[string]bool{
		path + `:3:19: undefined acl "foo"`:                 true,
		path + `:6:10: duplicate hostname definition "foo"`: true,
	}
	seen := map[string]bool{}
	for _, line := range got {
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, path+":") {
			t.Errorf("line does not start with %q: %q", path+":", line)
			continue
		}
		if !want[line] {
			t.Errorf("unexpected line: %q", line)
			continue
		}
		seen[line] = true
	}
	for w := range want {
		if !seen[w] {
			t.Errorf("missing expected line: %q\nfull output:\n%s", w, out)
		}
	}
}

// TestCheckCleanConfig prints "No issues found." and nothing else.
func TestCheckCleanConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clean.ios.j2")
	if err := os.WriteFile(path, []byte("hostname router1\n"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"--log-level=info", "check", path})
	defer resetRoot(t)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("check: %v", err)
	}

	if got := stdout.String(); got != "No issues found.\n" {
		t.Errorf("clean output = %q, want %q", got, "No issues found.\n")
	}
}

// withSlogLevel swaps the process default slog logger for a buffer-backed
// handler at the given level and returns a restore func. The grammar log goes
// through the default logger (feature.New), so this captures it directly
// without the os.Stderr redirect that logger.SetLogger would impose.
func withSlogLevel(t *testing.T, level slog.Level) (*bytes.Buffer, func()) {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: level})))
	return &buf, func() { slog.SetDefault(prev) }
}

// TestNewGrammarLogSuppressedAtInfo verifies Fix 1: the grammar version log is
// emitted at Debug, so at the default --log-level=info it stays silent.
func TestNewGrammarLogSuppressedAtInfo(t *testing.T) {
	buf, restore := withSlogLevel(t, slog.LevelInfo)
	defer restore()

	f := cisco_ios_jinja2.New()
	defer f.Close()

	if strings.Contains(buf.String(), "msg=grammar") {
		t.Errorf("grammar log emitted at info level (should be suppressed):\n%s", buf.String())
	}
}

// TestNewGrammarLogEmittedAtDebug confirms the log was downgraded, not deleted:
// it surfaces at Debug level, so `chunt check --log-level debug` still shows it.
func TestNewGrammarLogEmittedAtDebug(t *testing.T) {
	buf, restore := withSlogLevel(t, slog.LevelDebug)
	defer restore()

	f := cisco_ios_jinja2.New()
	defer f.Close()

	if !strings.Contains(buf.String(), "msg=grammar") {
		t.Errorf("grammar log not emitted at debug level:\n%s", buf.String())
	}
}
