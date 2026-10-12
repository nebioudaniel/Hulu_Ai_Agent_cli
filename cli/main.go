package main

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"

	"github.com/gorilla/websocket"
)

func main() {
	u := url.URL{Scheme: "ws", Host: "localhost:8080", Path: "/ws", RawQuery: "role=agent&client_id=my-macbook"}
	fmt.Printf("Connecting to %s...\n", u.String())

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatal("dial error:", err)
	}
	defer conn.Close()

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	// Listen for commands from the relay server
	go func() {
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				log.Println("read error:", err)
				return
			}

			cmdStr := string(message)
			fmt.Printf("Received command from relay: %s\n", cmdStr)

			// Execute shell command based on OS
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

			// Send output back to relay
			err = conn.WriteMessage(websocket.TextMessage, []byte(response))
			if err != nil {
				log.Println("write error:", err)
				return
			}
		}
	}()

	<-interrupt
	fmt.Println("\nDisconnecting agent...")
}
