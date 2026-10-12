package main

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	defaultCmdTimeout = 30 * time.Second
	maxOutputSize     = 10 * 1024
)

// allowlistedCommands are the only commands that run without phone approval.
// Each entry is the exact argv: the command runs with explicit arguments,
// never through a shell. Matching is exact after trimming whitespace.
var allowlistedCommands = [][]string{
	{"ls"},
	{"pwd"},
	{"git", "status"},
	{"git", "diff"},
	{"git", "log"},
}

// shellMetaChars are characters that only have meaning inside a shell. If any
// appear in a command it is treated as arbitrary input that needs approval.
const shellMetaChars = ";&|<>`$()\n"

// containsShellChars reports whether a command string contains any shell
// metacharacter. The raw (untrimmed) string is checked so a trailing newline
// alone forces phone approval.
func containsShellChars(cmdStr string) bool {
	return strings.ContainsAny(cmdStr, shellMetaChars)
}

// allowlistEntry returns the exact argv for a command if it is on the
// allowlist. The command must match an entry word-for-word after trimming.
func allowlistEntry(cmdStr string) ([]string, bool) {
	trimmed := strings.TrimSpace(cmdStr)
	for _, argv := range allowlistedCommands {
		if trimmed == strings.Join(argv, " ") {
			return argv, true
		}
	}
	return nil, false
}

// isAutoRun reports whether a command may run without phone approval: it must
// be on the allowlist and contain no shell metacharacters.
func isAutoRun(cmdStr string) bool {
	if containsShellChars(cmdStr) {
		return false
	}
	_, ok := allowlistEntry(cmdStr)
	return ok
}

// capOutput trims command output so a huge result cannot be read in full by
// the caller or written to the audit log.
func capOutput(output []byte) []byte {
	if len(output) > maxOutputSize {
		output = append(output[:maxOutputSize], []byte("\n...[output truncated]...\n")...)
	}
	return output
}

// runDirect executes an allowlisted command with explicit arguments and no
// shell, inside the project directory. It kills the process after
// defaultCmdTimeout and caps the returned output size.
func runDirect(projectDir string, argv []string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultCmdTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = projectDir

	output, err := cmd.CombinedOutput()
	output = capOutput(output)

	if err != nil {
		if ctx.Err() != nil {
			return fmt.Sprintf("Command timed out after %s\n%s", defaultCmdTimeout, string(output)), true
		}
		return fmt.Sprintf("Error: %v\nOutput: %s", err, string(output)), true
	}
	return string(output), false
}

// runCommand executes a shell command inside the project directory, kills it
// after defaultCmdTimeout, and caps the returned output size. Used only for
// commands that the phone has explicitly approved.
func runCommand(projectDir, cmdStr string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultCmdTimeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", cmdStr)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", cmdStr)
	}
	cmd.Dir = projectDir

	output, err := cmd.CombinedOutput()
	output = capOutput(output)

	if err != nil {
		if ctx.Err() != nil {
			return fmt.Sprintf("Command timed out after %s\n%s", defaultCmdTimeout, string(output)), true
		}
		return fmt.Sprintf("Error: %v\nOutput: %s", err, string(output)), true
	}
	return string(output), false
}
