package mysql

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dengw8/harness-practice/internal/runtime"
	"github.com/dengw8/harness-practice/internal/store"
	"github.com/dengw8/harness-practice/internal/store/storetest"
)

func TestMetadataRoundTrip(t *testing.T) {
	promptTokens := 11
	cachedTokens := 4
	meta := store.RunMetadata{
		ID:            "run_0123456789abcdef01234567",
		Status:        store.RunStatusCompleted,
		CreatedAt:     time.Date(2026, 7, 13, 1, 2, 3, 4, time.UTC),
		UpdatedAt:     time.Date(2026, 7, 13, 4, 5, 6, 7, time.UTC),
		ModelProvider: "fake",
		PlanMode:      "planning",
		PlanSteps:     2,
		PlanCompleted: 1,
		TokenUsage: &runtime.TokenUsage{
			PromptTokens: &promptTokens,
			CachedTokens: &cachedTokens,
			Raw:          map[string]any{"provider": "test"},
		},
	}

	record, err := runRecordFromMetadata(meta)
	if err != nil {
		t.Fatal(err)
	}
	got, err := metadataFromRunRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != meta.ID || got.Status != meta.Status || got.CreatedAt != meta.CreatedAt || got.UpdatedAt != meta.UpdatedAt {
		t.Fatalf("metadata = %#v, want %#v", got, meta)
	}
	if got.TokenUsage == nil || got.TokenUsage.PromptTokens == nil || *got.TokenUsage.PromptTokens != promptTokens {
		t.Fatalf("token usage = %#v", got.TokenUsage)
	}
	if got.TokenUsage.Raw["provider"] != "test" {
		t.Fatalf("token raw = %#v", got.TokenUsage.Raw)
	}
}

func TestConfigValidation(t *testing.T) {
	valid := Config{
		PSM:      "agent-service",
		Host:     "mysql.example.test",
		Port:     3306,
		Database: "agent",
		Username: "agent_user",
		Password: "password",
	}
	if err := valid.validate(); err != nil {
		t.Fatal(err)
	}

	invalid := valid
	invalid.Port = 0
	if err := invalid.validate(); err == nil {
		t.Fatal("expected invalid port error")
	}
}

func TestRunStoreContractMySQL(t *testing.T) {
	cfg, ok := mysqlIntegrationConfig(t)
	if !ok {
		return
	}

	storetest.RunStoreContract(t, func(t *testing.T) store.RunStore {
		t.Helper()
		runStore, err := New(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		resetIntegrationData(t, runStore)
		t.Cleanup(func() {
			if err := runStore.Close(); err != nil {
				t.Errorf("close store: %v", err)
			}
		})
		return runStore
	})
}

func mysqlIntegrationConfig(t *testing.T) (Config, bool) {
	t.Helper()
	if os.Getenv("AGENT_TEST_MYSQL_ALLOW_CLEANUP") != "1" {
		t.Skip("set AGENT_TEST_MYSQL_ALLOW_CLEANUP=1 to run against a dedicated MySQL test database")
		return Config{}, false
	}

	config := Config{
		PSM:      os.Getenv("AGENT_TEST_MYSQL_PSM"),
		Host:     os.Getenv("AGENT_TEST_MYSQL_HOST"),
		Database: os.Getenv("AGENT_TEST_MYSQL_DATABASE"),
		Username: os.Getenv("AGENT_TEST_MYSQL_USERNAME"),
		Password: os.Getenv("AGENT_TEST_MYSQL_PASSWORD"),
	}
	port, err := strconv.Atoi(os.Getenv("AGENT_TEST_MYSQL_PORT"))
	if err != nil {
		t.Skip("set AGENT_TEST_MYSQL_PORT with a valid port to run MySQL integration tests")
		return Config{}, false
	}
	config.Port = port
	if err := config.validate(); err != nil {
		t.Skipf("set complete AGENT_TEST_MYSQL_* values to run MySQL integration tests: %v", err)
		return Config{}, false
	}
	if !strings.HasSuffix(config.Database, "_test") {
		t.Fatal("MySQL integration tests require a dedicated database ending in _test")
	}
	return config, true
}

func resetIntegrationData(t *testing.T, runStore *Store) {
	t.Helper()
	for _, table := range []string{
		"agent_run_states",
		"agent_model_logs",
		"agent_run_events",
		"agent_run_messages",
		"agent_runs",
	} {
		if err := runStore.db.Exec("DELETE FROM " + table).Error; err != nil {
			t.Fatalf("clear %s: %v", table, err)
		}
	}
}
