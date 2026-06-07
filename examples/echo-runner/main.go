package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type request struct {
	Version   string `json:"version"`
	RequestID string `json:"request_id"`
	Text      string `json:"text"`
}

type response struct {
	Version   string   `json:"version"`
	RequestID string   `json:"request_id,omitempty"`
	Actions   []action `json:"actions"`
}

type action struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

func main() {
	var req request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "invalid request: %v\n", err)
		os.Exit(1)
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		text = "empty"
	}
	_ = json.NewEncoder(os.Stdout).Encode(response{
		Version:   "1.0",
		RequestID: req.RequestID,
		Actions: []action{{
			Type: "reply",
			Text: "echo: " + text,
		}},
	})
}
