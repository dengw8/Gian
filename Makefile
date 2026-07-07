.PHONY: run test

run:
	go run ./cmd/agent-service

test:
	go test ./...
