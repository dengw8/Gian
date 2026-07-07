.PHONY: run test

run:
	AGENT_CONFIG=config.example.yaml go run ./cmd/agent-service

test:
	go test ./...
