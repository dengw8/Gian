package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFileAppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("server:\n  addr: ':9090'\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Server.Addr != ":9090" {
		t.Fatalf("addr = %q, want :9090", cfg.Server.Addr)
	}
	if cfg.Model.Provider != "fake" {
		t.Fatalf("provider = %q, want fake", cfg.Model.Provider)
	}
	if cfg.Agent.MaxIterations != defaultMaxIterations {
		t.Fatalf("max iterations = %d, want %d", cfg.Agent.MaxIterations, defaultMaxIterations)
	}
	if cfg.Agent.CompactionEnabled {
		t.Fatal("compaction enabled by default")
	}
	if cfg.Agent.CompactionMaxMessages != defaultCompactionMaxMessages {
		t.Fatalf("compaction max messages = %d, want %d", cfg.Agent.CompactionMaxMessages, defaultCompactionMaxMessages)
	}
	if cfg.Agent.CompactionKeepRecent != defaultCompactionKeepRecent {
		t.Fatalf("compaction keep recent = %d, want %d", cfg.Agent.CompactionKeepRecent, defaultCompactionKeepRecent)
	}
	if cfg.Storage.LocalDir != "data" {
		t.Fatalf("local dir = %q, want data", cfg.Storage.LocalDir)
	}
	if cfg.Tools.Workspace != "." {
		t.Fatalf("workspace = %q, want .", cfg.Tools.Workspace)
	}
}

func TestLoadFileReadsCompactionConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("agent:\n  compaction_enabled: true\n  compaction_max_messages: 12\n  compaction_keep_recent: 4\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Agent.CompactionEnabled {
		t.Fatal("compaction enabled = false, want true")
	}
	if cfg.Agent.CompactionMaxMessages != 12 || cfg.Agent.CompactionKeepRecent != 4 {
		t.Fatalf("compaction config = max %d keep %d", cfg.Agent.CompactionMaxMessages, cfg.Agent.CompactionKeepRecent)
	}
}

func TestLoadFileNormalizesCompactionConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("agent:\n  compaction_max_messages: 3\n  compaction_keep_recent: 9\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.CompactionMaxMessages != 3 || cfg.Agent.CompactionKeepRecent != 2 {
		t.Fatalf("compaction config = max %d keep %d, want max 3 keep 2", cfg.Agent.CompactionMaxMessages, cfg.Agent.CompactionKeepRecent)
	}
}

func TestLoadFileReadsOpenAIAPIKeyFromEnv(t *testing.T) {
	t.Setenv(APIKeyEnv, "test-key")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("model:\n  provider: openai_compatible\n  openai:\n    base_url: http://example.test/v1\n    model: test-model\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model.OpenAI.APIKey != "test-key" {
		t.Fatalf("api key = %q, want test-key", cfg.Model.OpenAI.APIKey)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateOpenAICompatibleRequiresAPIKey(t *testing.T) {
	t.Setenv(APIKeyEnv, "")
	cfg := Default()
	cfg.Model.Provider = "openai_compatible"
	cfg.Model.OpenAI.BaseURL = "http://example.test/v1"
	cfg.Model.OpenAI.Model = "test-model"

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), APIKeyEnv) {
		t.Fatalf("err = %v, want missing api key error", err)
	}
}

func TestLoadFileReadsModelHubAKFromEnv(t *testing.T) {
	t.Setenv(ModelHubAKEnv, "test-ak")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("model:\n  provider: modelhub\n  modelhub:\n    endpoint: http://example.test/crawl\n    model: test-model\n    max_tokens: 500\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model.ModelHub.AK != "test-ak" {
		t.Fatalf("ak = %q, want test-ak", cfg.Model.ModelHub.AK)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadFileReadsModelHubAKFromConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("model:\n  provider: modelhub\n  modelhub:\n    endpoint: http://example.test/crawl\n    model: test-model\n    max_tokens: 500\n    ak: file-ak\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model.ModelHub.AK != "file-ak" {
		t.Fatalf("ak = %q, want file-ak", cfg.Model.ModelHub.AK)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateModelHubRequiresAK(t *testing.T) {
	t.Setenv(ModelHubAKEnv, "")
	cfg := Default()
	cfg.Model.Provider = "modelhub"
	cfg.Model.ModelHub.Endpoint = "http://example.test/crawl"
	cfg.Model.ModelHub.Model = "test-model"

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), ModelHubAKEnv) {
		t.Fatalf("err = %v, want missing ak error", err)
	}
}
