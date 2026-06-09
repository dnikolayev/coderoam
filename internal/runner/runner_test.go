package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/dnikolayev/coderoam/internal/config"
)

// stopRunnerCleanup registers a cleanup that stops the runner with a live
// context: t.Context() is already canceled inside Cleanup, which would let
// Stop return before the subprocess exit is confirmed.
func stopRunnerCleanup(t *testing.T, r *ProcessRunner) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := r.Stop(ctx); err != nil {
			t.Errorf("runner stop: %v", err)
		}
	})
}

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
	stopRunnerCleanup(t, r)
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
	stopRunnerCleanup(t, r)
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

func TestProcessJSONLRunnerStopConfirmsExitAndAllowsRestart(t *testing.T) {
	r := NewProcessRunner(config.RunnerConfig{
		Mode:    "process-jsonl",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcess", "--", "jsonl"},
		Env:     map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
	}, 5)
	stopRunnerCleanup(t, r)
	first, err := r.Invoke(t.Context(), Request{RequestID: "req_pre_stop", Text: "alpha"})
	if err != nil {
		t.Fatalf("Invoke before Stop: %v", err)
	}
	if got := first.Response.Actions[0].Text; got != "jsonl 1: alpha" {
		t.Fatalf("first reply = %q", got)
	}
	if err := r.Stop(t.Context()); err != nil {
		t.Fatalf("Stop did not confirm process exit: %v", err)
	}
	// A fresh process must be started transparently; its counter resets,
	// proving the old one was fully reaped rather than reused.
	second, err := r.Invoke(t.Context(), Request{RequestID: "req_post_stop", Text: "beta"})
	if err != nil {
		t.Fatalf("Invoke after Stop: %v", err)
	}
	if got := second.Response.Actions[0].Text; got != "jsonl 1: beta" {
		t.Fatalf("reply after restart = %q", got)
	}
}

func TestProcessJSONLRunnerConcurrentInvokeAndStop(t *testing.T) {
	// Regression test for the Stop vs waiter-goroutine data race: Stop used
	// to read cmd.ProcessState while the goroutine running cmd.Wait wrote
	// it. Run with -race to keep that hazard covered.
	r := NewProcessRunner(config.RunnerConfig{
		Mode:    "process-jsonl",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcess", "--", "jsonl"},
		Env:     map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
	}, 5)
	stopRunnerCleanup(t, r)
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 6; i++ {
				// Errors are expected when Stop kills the process
				// mid-request; the race detector is the assertion here.
				_, _ = r.Invoke(t.Context(), Request{
					RequestID: fmt.Sprintf("req_%d_%d", worker, i),
					Text:      "stress",
				})
			}
		}(worker)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 12; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = r.Stop(ctx)
			cancel()
		}
	}()
	wg.Wait()
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
