package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	serverPtr := flag.String("server", "localhost:8080", "Relay server address")
	idPtr := flag.String("id", "my-macbook", "Unique Agent ID")
	flag.Parse()

	// Launch TUI directly as the main application interface
	p := tea.NewProgram(initialModel(*serverPtr, *idPtr), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error running TUI: %v\n", err)
		os.Exit(1)
	}
}