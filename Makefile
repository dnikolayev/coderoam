# Override BIN_DIR to build somewhere other than bin/, e.g. when a daemon
# is running from bin/coderoam: make build BIN_DIR=/tmp/coderoam-build
BIN_DIR ?= bin

.PHONY: build test test-race lint fmt vet ci

build:
	go build -o $(BIN_DIR)/coderoam ./cmd/coderoam
	go build -o $(BIN_DIR)/coderoam-transcribe ./cmd/coderoam-transcribe
	go build -o $(BIN_DIR)/agent-runner ./examples/agent-runner
	go build -o $(BIN_DIR)/codex-runner ./examples/codex-runner
	go build -o $(BIN_DIR)/claude-runner ./examples/claude-runner
	go build -o $(BIN_DIR)/echo-runner ./examples/echo-runner

test:
	go test ./...

test-race:
	go test -race ./...

lint:
	golangci-lint run

fmt:
	gofmt -w cmd examples internal

vet:
	go vet ./...

ci: vet lint test-race build
