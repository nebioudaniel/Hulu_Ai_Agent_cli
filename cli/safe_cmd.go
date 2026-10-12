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

// riskyPatterns make a command require phone approval. Matches are case
// insensitive and apply anywhere in the command string.
var riskyPatterns = []string{
	"rm -rf", "rm -fr",
	"sudo ",
	"kill -9",
	"dd if=",
	"mkfs.",
	"shutdown", "reboot", "halt ",
	":(){",
	"> /dev/sd",
	"chmod -R 777", "chmod 777",
	"chown -R",
	"mv / ",
}

// isRisky reports whether a command should require approval from the phone.
// Downloads piped into a shell are also treated as risky.
func isRisky(cmdStr string) bool {
	lower := strings.ToLower(cmdStr)
	for _, p := range riskyPatterns {
		if strings.Contains(lower, strings.ToLower(p)) {
			return true
		}
	}
	if (strings.Contains(lower, "curl") || strings.Contains(lower, "wget")) && strings.Contains(lower, "|") {
		return true
	}
	return false
}

// runCommand executes a shell command inside the project directory, kills it
// after defaultCmdTimeout, and caps the returned output size.
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
	if len(output) > maxOutputSize {
		output = append(output[:maxOutputSize], []byte("\n...[output truncated]...\n")...)
	}

	if err != nil {
		if ctx.Err() != nil {
			return fmt.Sprintf("Command timed out after %s\n%s", defaultCmdTimeout, string(output)), true
		}
		return fmt.Sprintf("Error: %v\nOutput: %s", err, string(output)), true
	}
	return string(output), false
}
