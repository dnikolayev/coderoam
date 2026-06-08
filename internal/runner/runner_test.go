package runner

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/dnikolayev/coderoam/internal/config"
)

func TestProcessOnceJSONRunner(t *testing.T) {
	r := NewProcessRunner(config.RunnerConfig{
		Mode:    "process-once-json",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcess", "--", "json"},
		Env:     map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
	}, 5)
	result, err := r.Invoke(t.Context(), Request{
		Version:   ProtocolVersion,
		RequestID: "req_test",
		Text:      "ping",
	})
	if err != nil {
		t.Fatalf("Invoke returned error: %v", err)
	}
	if got := result.Response.Actions[0].Text; got != "pong: ping" {
		t.Fatalf("reply text = %q", got)
	}
}

func TestProcessOnceTextRunner(t *testing.T) {
	r := NewProcessRunner(config.RunnerConfig{
		Mode:    "process-once-text",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcess", "--", "text"},
		Env:     map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
	}, 5)
	result, err := r.Invoke(t.Context(), Request{RequestID: "req_test", Text: "hello"})
	if err != nil {
		t.Fatalf("Invoke returned error: %v", err)
	}
	if got := result.Response.Actions[0].Text; got != "echo: hello" {
		t.Fatalf("reply text = %q", got)
	}
}

func TestProcessJSONLRunnerReusesPersistentProcess(t *testing.T) {
	r := NewProcessRunner(config.RunnerConfig{
		Mode:    "process-jsonl",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcess", "--", "jsonl"},
		Env:     map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
	}, 5)
	t.Cleanup(func() {
		_ = r.Stop(t.Context())
	})
	first, err := r.Invoke(t.Context(), Request{
		Version:   ProtocolVersion,
		RequestID: "req_one",
		Text:      "one",
	})
	if err != nil {
		t.Fatalf("first Invoke returned error: %v", err)
	}
	second, err := r.Invoke(t.Context(), Request{
		Version:   ProtocolVersion,
		RequestID: "req_two",
		Text:      "two",
	})
	if err != nil {
		t.Fatalf("second Invoke returned error: %v", err)
	}
	if got := first.Response.Actions[0].Text; got != "jsonl 1: one" {
		t.Fatalf("first reply = %q", got)
	}
	if got := second.Response.Actions[0].Text; got != "jsonl 2: two" {
		t.Fatalf("second reply = %q", got)
	}
}

func TestProcessJSONLRunnerAcceptsReplyEventEnvelope(t *testing.T) {
	r := NewProcessRunner(config.RunnerConfig{
		Mode:    "process-jsonl",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcess", "--", "jsonl-event"},
		Env:     map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
	}, 5)
	t.Cleanup(func() {
		_ = r.Stop(t.Context())
	})
	result, err := r.Invoke(t.Context(), Request{
		Version:   ProtocolVersion,
		RequestID: "req_event",
		Text:      "hello",
	})
	if err != nil {
		t.Fatalf("Invoke returned error: %v", err)
	}
	if got := result.Response.Actions[0].Text; got != "event: hello" {
		t.Fatalf("reply = %q", got)
	}
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	mode := os.Args[len(os.Args)-1]
	switch mode {
	case "json":
		body, _ := io.ReadAll(os.Stdin)
		var req Request
		_ = json.Unmarshal(body, &req)
		_ = json.NewEncoder(os.Stdout).Encode(Response{
			Version:   ProtocolVersion,
			RequestID: req.RequestID,
			Actions:   []Action{{Type: "reply", Text: "pong: " + req.Text}},
		})
	case "text":
		body, _ := io.ReadAll(os.Stdin)
		os.Stdout.WriteString("echo: " + string(body))
	case "jsonl":
		decoder := json.NewDecoder(os.Stdin)
		encoder := json.NewEncoder(os.Stdout)
		count := 0
		for {
			var req Request
			if err := decoder.Decode(&req); err != nil {
				os.Exit(0)
			}
			count++
			_ = encoder.Encode(Response{
				Version:   ProtocolVersion,
				RequestID: req.RequestID,
				Actions:   []Action{{Type: "reply", Text: fmt.Sprintf("jsonl %d: %s", count, req.Text)}},
			})
		}
	case "jsonl-event":
		decoder := json.NewDecoder(os.Stdin)
		encoder := json.NewEncoder(os.Stdout)
		for {
			var req Request
			if err := decoder.Decode(&req); err != nil {
				os.Exit(0)
			}
			_ = encoder.Encode(map[string]string{
				"type":       "reply",
				"request_id": req.RequestID,
				"text":       "event: " + req.Text,
			})
		}
	default:
		os.Exit(2)
	}
	os.Exit(0)
}
