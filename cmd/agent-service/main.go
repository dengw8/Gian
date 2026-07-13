package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/dengw8/Gian/internal/agent"
	"github.com/dengw8/Gian/internal/config"
	"github.com/dengw8/Gian/internal/model"
	"github.com/dengw8/Gian/internal/server"
	"github.com/dengw8/Gian/internal/store"
	"github.com/dengw8/Gian/internal/store/local"
	mysqlstore "github.com/dengw8/Gian/internal/store/mysql"
	"github.com/dengw8/Gian/internal/tools"
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
			agent.CompactionMiddleware{
				Enabled:     cfg.Agent.CompactionEnabled,
				MaxMessages: cfg.Agent.CompactionMaxMessages,
				KeepRecent:  cfg.Agent.CompactionKeepRecent,
			},
			agent.SubAgentMiddleware{},
		},
	})

	runStore, closeStore, err := buildRunStore(context.Background(), cfg)
	if err != nil {
		log.Fatalf("build run store: %v", err)
	}
	defer func() {
		if err := closeStore(); err != nil {
			log.Printf("close run store: %v", err)
		}
	}()
	handler := server.New(server.Config{
		Runner:        runner,
		Store:         runStore,
		ModelProvider: cfg.Model.Provider,
	})
	log.Printf("agent service listening on %s", cfg.Server.Addr)
	if err := http.ListenAndServe(cfg.Server.Addr, handler.Routes()); err != nil {
		log.Printf("serve: %v", err)
	}
}

func buildRunStore(ctx context.Context, cfg config.Config) (store.RunStore, func() error, error) {
	switch cfg.Storage.Driver {
	case "local":
		return local.New(cfg.Storage.LocalDir), func() error { return nil }, nil
	case "mysql":
		runStore, err := mysqlstore.New(ctx, mysqlstore.Config{
			PSM:      cfg.Storage.MySQL.PSM,
			Host:     cfg.Storage.MySQL.Host,
			Port:     cfg.Storage.MySQL.Port,
			Database: cfg.Storage.MySQL.Database,
			Username: cfg.Storage.MySQL.Username,
			Password: cfg.Storage.MySQL.Password,
		})
		if err != nil {
			return nil, nil, err
		}
		return runStore, runStore.Close, nil
	default:
		return nil, nil, fmt.Errorf("unsupported storage driver %q", cfg.Storage.Driver)
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
