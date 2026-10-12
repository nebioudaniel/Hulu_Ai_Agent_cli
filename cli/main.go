package main

import (
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	// Define command-line flags for flexibility
	serverPtr := flag.String("server", "localhost:8080", "Relay server address")
	idPtr := flag.String("id", "my-macbook", "Unique Agent ID")
	flag.Parse()

	u := url.URL{Scheme: "ws", Host: *serverPtr, Path: "/ws", RawQuery: fmt.Sprintf("role=agent&client_id=%s", *idPtr)}
	
	// Handle graceful shutdown on Ctrl+C
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	// Auto-reconnection loop
	for {
		fmt.Printf("[*] Connecting to relay at %s (ID: %s)...\n", u.String(), *idPtr)
		conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
		if err != nil {
			log.Printf("[!] Connection failed: %v. Retrying in 3 seconds...", err)
			select {
			case <-interrupt:
				fmt.Println("\n[*] Exiting CLI agent.")
				return
			case <-time.After(3 * time.Second):
				continue
			}
		}

		fmt.Println("[+] Connected successfully to Relay Server!")

		// Channel to handle read errors / disconnection
		done := make(chan struct{})

		go func() {
			defer close(done)
			for {
				_, message, err := conn.ReadMessage()
				if err != nil {
					log.Printf("[!] Read error / Disconnected: %v", err)
					return
				}

				cmdStr := string(message)
				fmt.Printf("\n[>] Executing command: %s\n", cmdStr)

				var cmd *exec.Cmd
				if runtime.GOOS == "windows" {
					cmd = exec.Command("cmd", "/C", cmdStr)
				} else {
					cmd = exec.Command("sh", "-c", cmdStr)
				}

				output, err := cmd.CombinedOutput()
				var response string
				if err != nil {
					response = fmt.Sprintf("Error: %v\nOutput: %s", err, string(output))
				} else {
					response = string(output)
				}

				fmt.Printf("[<] Command output sent back (%d bytes)\n", len(response))

				// Send output back to relay
				err = conn.WriteMessage(websocket.TextMessage, []byte(response))
				if err != nil {
					log.Printf("[!] Write error: %v", err)
					return
				}
			}
		}()

		select {
		case <-interrupt:
			fmt.Println("\n[*] Shutting down agent gracefully...")
			conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			return
		case <-done:
			conn.Close()
			fmt.Println("[*] Reconnecting...")
			time.Sleep(2 * time.Second)
		}
	}
}