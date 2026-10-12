package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"time"

	"github.com/gorilla/websocket"
	"github.com/skip2/go-qrcode"
)

// Function to request a pairing code from the relay server
func startPairing(serverAddr string) {
	apiURL := fmt.Sprintf("http://%s/pair/start", serverAddr)

	// For testing, let's see if the server responds to /pair/start
	resp, err := http.Post(apiURL, "application/json", bytes.NewBuffer([]byte("{}")))
	if err != nil {
		fmt.Printf("[!] Pairing request failed: %v\n", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		fmt.Println("[!] Failed to get pairing code from server (Authentication or DB error). Continuing to WebSocket...")
		return
	}

	var result struct {
		PairingCode string `json:"pairing_code"`
		ExpiresAt   string `json:"expires_at"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		fmt.Printf("[!] Failed to decode pairing response: %v\n", err)
		return
	}

	fmt.Println("\n[+] Scan this QR code with your mobile app to pair your Mac:")

	// Generate terminal QR code string
	q, err := qrcode.New(result.PairingCode, qrcode.Medium)
	if err != nil {
		fmt.Printf("[!] Failed to generate QR code: %v\n", err)
		return
	}

	// Print QR code to terminal
	fmt.Println(q.ToString(false))
	fmt.Printf("Pairing Code: %s (Expires at %s)\n\n", result.PairingCode, result.ExpiresAt)
}

func main() {
	serverPtr := flag.String("server", "localhost:8080", "Relay server address")
	idPtr := flag.String("id", "my-macbook", "Unique Agent ID")
	flag.Parse()

	// 1. Request pairing code and print QR before connecting WebSocket
	startPairing(*serverPtr)

	u := url.URL{Scheme: "ws", Host: *serverPtr, Path: "/ws", RawQuery: fmt.Sprintf("role=agent&client_id=%s", *idPtr)}

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

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
