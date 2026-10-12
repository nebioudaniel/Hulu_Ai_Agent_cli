package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAutoRunAllowlist(t *testing.T) {
	allowed := []string{
		"ls",
		"pwd",
		"git status",
		"git diff",
		"git log",
	}
	for _, cmd := range allowed {
		if !isAutoRun(cmd) {
			t.Errorf("expected auto-run: %q", cmd)
		}
		if argv, ok := allowlistEntry(cmd); !ok {
			t.Errorf("expected allowlist entry: %q", cmd)
		} else if strings.Join(argv, " ") != cmd {
			t.Errorf("argv %v does not round-trip %q", argv, cmd)
		}
	}

	notAllowed := []string{
		"ls -la",
		"ls; echo hi",
		"pwd\n",
		"git status; rm -rf /",
		"git diff | less",
		"rm -rf /",
		"echo hello",
		"cat README.md",
	}
	for _, cmd := range notAllowed {
		if isAutoRun(cmd) {
			t.Errorf("expected NOT auto-run: %q", cmd)
		}
	}
}

func TestContainsShellChars(t *testing.T) {
	for _, s := range []string{";", "|", "&", ">", "<", "`", "$", "(", ")", "\n", "ls;", "pwd && whoami"} {
		if !containsShellChars(s) {
			t.Errorf("expected shell chars: %q", s)
		}
	}
	for _, s := range []string{"ls", "pwd", "git status", "git diff"} {
		if containsShellChars(s) {
			t.Errorf("expected no shell chars: %q", s)
		}
	}
}

func TestRunDirectNoShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses ls")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	out, isErr := runDirect(dir, []string{"ls"})
	if isErr {
		t.Fatalf("ls failed: %s", out)
	}
	if !strings.Contains(out, "marker.txt") {
		t.Fatalf("output = %q; command did not run in project dir", out)
	}
}

func TestRunCommandUsesProjectDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("hello-dir"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	out, isErr := runCommand(dir, "cat marker.txt")
	if isErr {
		t.Fatalf("command failed: %s", out)
	}
	if strings.TrimSpace(out) != "hello-dir" {
		t.Fatalf("output = %q; command did not run in project dir", out)
	}
}

func TestRunCommandTruncatesOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /dev/zero")
	}
	dir := t.TempDir()
	out, _ := runCommand(dir, "head -c 40000 /dev/zero | tr '\\0' 'a'")
	if !strings.Contains(out, "output truncated") {
		t.Fatalf("expected truncation marker, got %d bytes", len(out))
	}
}

func TestRunCommandCapturesErrors(t *testing.T) {
	out, isErr := runCommand(t.TempDir(), "exit 3")
	if !isErr {
		t.Fatal("expected a non-zero exit to be an error")
	}
	if !strings.Contains(out, "Error") {
		t.Fatalf("output = %q", out)
	}
}
