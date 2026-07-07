package main

import (
	"log"
	"net/http"

	"github.com/dengw8/harness-practice/internal/agent"
	"github.com/dengw8/harness-practice/internal/config"
	"github.com/dengw8/harness-practice/internal/model"
	"github.com/dengw8/harness-practice/internal/server"
	"github.com/dengw8/harness-practice/internal/store/local"
	"github.com/dengw8/harness-practice/internal/tools"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("validate config: %v", err)
	}

	provider, err := buildModelProvider(cfg)
	if err != nil {
		log.Fatalf("build model provider: %v", err)
	}
	registry, err := tools.NewRegistry(
		tools.NewReadFileTool(cfg.Tools.Workspace),
		tools.NewListFilesTool(cfg.Tools.Workspace),
		tools.NewShellTool(cfg.Tools.Workspace),
	)
	if err != nil {
		log.Fatalf("build tool registry: %v", err)
	}
	runner := agent.NewRunner(agent.Config{
		Model:         provider,
		MaxIterations: cfg.Agent.MaxIterations,
		Tools:         registry,
		Middleware: []agent.Middleware{
			agent.NoopMiddleware{NameValue: "bootstrap"},
			agent.SafetyMiddleware{},
			agent.PlanModeMiddleware{},
			agent.CompactionMiddleware{},
			agent.SubAgentMiddleware{},
		},
	})

	runStore := local.New(cfg.Storage.LocalDir)
	handler := server.New(server.Config{
		Runner:        runner,
		Store:         runStore,
		ModelProvider: cfg.Model.Provider,
	})
	log.Printf("agent service listening on %s", cfg.Server.Addr)
	if err := http.ListenAndServe(cfg.Server.Addr, handler.Routes()); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

func buildModelProvider(cfg config.Config) (model.Provider, error) {
	switch cfg.Model.Provider {
	case "fake":
		return model.NewFakeProvider(), nil
	case "openai_compatible":
		return model.NewOpenAICompatibleProvider(model.OpenAICompatibleConfig{
			BaseURL: cfg.Model.OpenAI.BaseURL,
			Model:   cfg.Model.OpenAI.Model,
			APIKey:  cfg.Model.OpenAI.APIKey,
		})
	case "modelhub":
		return model.NewModelHubProvider(model.ModelHubConfig{
			Endpoint:  cfg.Model.ModelHub.Endpoint,
			Model:     cfg.Model.ModelHub.Model,
			MaxTokens: cfg.Model.ModelHub.MaxTokens,
			AK:        cfg.Model.ModelHub.AK,
		})
	default:
		return nil, cfg.Validate()
	}
}
