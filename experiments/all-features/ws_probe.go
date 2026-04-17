package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/websocket"
)

type chatResp struct {
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

func main() {
	url := flag.String("url", "ws://127.0.0.1:18095/api/ws", "websocket url")
	sessionID := flag.String("session", "", "session id")
	input := flag.String("input", "", "chat input")
	outPath := flag.String("out", "", "output file")
	apiKey := flag.String("api-key", "", "optional api key for websocket auth")
	flag.Parse()

	if *sessionID == "" || *input == "" || *outPath == "" {
		fmt.Fprintln(os.Stderr, "session, input and out are required")
		os.Exit(2)
	}

	headers := http.Header{}
	if *apiKey != "" {
		headers.Set("X-API-Key", *apiKey)
	}
	conn, _, err := websocket.DefaultDialer.Dial(*url, headers)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial ws: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Minute))

	req := map[string]string{
		"session_id": *sessionID,
		"input":      *input,
		"lane":       "main",
	}
	if err := conn.WriteJSON(req); err != nil {
		fmt.Fprintf(os.Stderr, "write ws: %v\n", err)
		os.Exit(1)
	}

	var combined string
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		combined += string(data)
	}

	payload := map[string]string{
		"url":        *url,
		"session_id": *sessionID,
		"response":   combined,
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*outPath, raw, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write file: %v\n", err)
		os.Exit(1)
	}
}
