package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// terminalEcho toggles the tty echo flag with `stty`. The original settings are
// saved with `stty -g` so they can be restored exactly on every exit path,
// including ctrl+c. Using stty keeps fakephone on the standard library only.
type terminalEcho struct {
	mu       sync.Mutex
	saved    string
	disabled bool
}

var echo = &terminalEcho{}

func stty(args ...string) (string, error) {
	cmd := exec.Command("stty", args...)
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// disable turns echo off and remembers the previous terminal state.
func (t *terminalEcho) disable() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.disabled {
		return nil
	}

	saved, err := stty("-g")
	if err != nil {
		return err
	}
	if _, err := stty("-echo"); err != nil {
		return err
	}

	t.saved = saved
	t.disabled = true
	return nil
}

// restore puts the terminal back the way it was. It is safe to call repeatedly
// and from a signal handler.
func (t *terminalEcho) restore() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.disabled {
		return
	}

	if t.saved != "" {
		_, _ = stty(t.saved)
	} else {
		_, _ = stty("echo")
	}
	t.disabled = false
}

// readPassword prompts on stderr and reads one line with echo disabled. Echo is
// restored even if the read fails. When stdin is not a tty (e.g. piped input)
// it falls back to a normal read.
func readPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)

	if err := echo.disable(); err != nil {
		line, readErr := bufio.NewReader(os.Stdin).ReadString('\n')
		fmt.Fprintln(os.Stderr)
		if readErr != nil {
			return "", readErr
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	defer echo.restore()
	defer fmt.Fprintln(os.Stderr)

	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
