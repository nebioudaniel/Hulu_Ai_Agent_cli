package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	serverPtr := flag.String("server", "127.0.0.1:8080", "Relay server address")
	idPtr := flag.String("id", "my-macbook", "Unique Agent ID")
	flag.Parse()

	// Log in (or reuse a saved session) before starting the UI.
	sess, err := ensureSession(*serverPtr)
	if err != nil {
		fmt.Printf("Login failed: %v\n", err)
		os.Exit(1)
	}

	p := tea.NewProgram(initialModel(*serverPtr, *idPtr, sess.Token, sess.DeviceToken), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error running TUI: %v\n", err)
		os.Exit(1)
	}
}
