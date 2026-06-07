package runner

import (
	"encoding/json"
	"io"
	"os"
	"testing"

	"github.com/endurantdevs/codex-whatsapp/internal/config"
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

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	mode := os.Args[len(os.Args)-1]
	body, _ := io.ReadAll(os.Stdin)
	switch mode {
	case "json":
		var req Request
		_ = json.Unmarshal(body, &req)
		_ = json.NewEncoder(os.Stdout).Encode(Response{
			Version:   ProtocolVersion,
			RequestID: req.RequestID,
			Actions:   []Action{{Type: "reply", Text: "pong: " + req.Text}},
		})
	case "text":
		os.Stdout.WriteString("echo: " + string(body))
	default:
		os.Exit(2)
	}
	os.Exit(0)
}
