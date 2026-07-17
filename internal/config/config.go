package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

const defaultAddr = ":8080"
const defaultProvider = "fake"
const defaultMaxIterations = 4
const defaultCompactionMaxMessages = 20
const defaultCompactionKeepRecent = 8
const APIKeyEnv = "AGENT_MODEL_API_KEY"
const ModelHubAKEnv = "AGENT_MODELHUB_AK"
const MySQLPasswordEnv = "AGENT_STORAGE_MYSQL_PASSWORD"

type Config struct {
	Server  ServerConfig  `yaml:"server"`
	Model   ModelConfig   `yaml:"model"`
	Agent   AgentConfig   `yaml:"agent"`
	Trace   TraceConfig   `yaml:"trace"`
	Storage StorageConfig `yaml:"storage"`
	Tools   ToolsConfig   `yaml:"tools"`
}

type ServerConfig struct {
	Addr string `yaml:"addr"`
}

type ModelConfig struct {
	Provider string         `yaml:"provider"`
	OpenAI   OpenAIConfig   `yaml:"openai"`
	ModelHub ModelHubConfig `yaml:"modelhub"`
}

type OpenAIConfig struct {
	BaseURL string `yaml:"base_url"`
	Model   string `yaml:"model"`
	APIKey  string `yaml:"-"`
}

type ModelHubConfig struct {
	Endpoint  string `yaml:"endpoint"`
	Model     string `yaml:"model"`
	MaxTokens int    `yaml:"max_tokens"`
	AK        string `yaml:"ak"`
}

type AgentConfig struct {
	MaxIterations         int  `yaml:"max_iterations"`
	CompactionEnabled     bool `yaml:"compaction_enabled"`
	CompactionMaxMessages int  `yaml:"compaction_max_messages"`
	CompactionKeepRecent  int  `yaml:"compaction_keep_recent"`
}

type TraceConfig struct {
	Enabled bool `yaml:"enabled"`
}

type StorageConfig struct {
	Driver   string             `yaml:"driver"`
	LocalDir string             `yaml:"local_dir"`
	MySQL    MySQLStorageConfig `yaml:"mysql"`
}

type MySQLStorageConfig struct {
	PSM      string `yaml:"psm"`
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Database string `yaml:"database"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type ToolsConfig struct {
	Workspace string `yaml:"workspace"`
}

func Load() (Config, error) {
	path := os.Getenv("AGENT_CONFIG")
	if path == "" {
		path = "config.yaml"
	}
	return LoadFile(path)
}

func LoadFile(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && path == "config.yaml" {
			return cfg, nil
		}
		return Config{}, err
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	cfg.applyDefaults()
	return cfg, nil
}

func Default() Config {
	cfg := Config{Trace: TraceConfig{Enabled: true}}
	cfg.applyDefaults()
	return cfg
}

func (c *Config) applyDefaults() {
	if c.Server.Addr == "" {
		c.Server.Addr = defaultAddr
	}
	if c.Model.Provider == "" {
		c.Model.Provider = defaultProvider
	}
	c.Model.Provider = strings.TrimSpace(c.Model.Provider)
	c.Model.OpenAI.APIKey = os.Getenv(APIKeyEnv)
	if ak := os.Getenv(ModelHubAKEnv); ak != "" {
		c.Model.ModelHub.AK = ak
	}
	if c.Agent.MaxIterations <= 0 {
		c.Agent.MaxIterations = defaultMaxIterations
	}
	if c.Agent.CompactionMaxMessages <= 1 {
		c.Agent.CompactionMaxMessages = defaultCompactionMaxMessages
	}
	if c.Agent.CompactionKeepRecent <= 0 {
		c.Agent.CompactionKeepRecent = defaultCompactionKeepRecent
	}
	if c.Agent.CompactionKeepRecent >= c.Agent.CompactionMaxMessages {
		c.Agent.CompactionKeepRecent = c.Agent.CompactionMaxMessages - 1
	}
	if c.Storage.LocalDir == "" {
		c.Storage.LocalDir = "data"
	}
	if c.Storage.Driver == "" {
		c.Storage.Driver = "local"
	}
	c.Storage.Driver = strings.TrimSpace(c.Storage.Driver)
	if password := os.Getenv(MySQLPasswordEnv); password != "" {
		c.Storage.MySQL.Password = password
	}
	if c.Tools.Workspace == "" {
		c.Tools.Workspace = "."
	}
}

func (c Config) Validate() error {
	if err := c.validateModel(); err != nil {
		return err
	}
	return c.validateStorage()
}

func (c Config) validateModel() error {
	switch c.Model.Provider {
	case "fake":
		return nil
	case "openai_compatible":
		if strings.TrimSpace(c.Model.OpenAI.BaseURL) == "" {
			return fmt.Errorf("model.openai.base_url is required for openai_compatible provider")
		}
		if strings.TrimSpace(c.Model.OpenAI.Model) == "" {
			return fmt.Errorf("model.openai.model is required for openai_compatible provider")
		}
		if strings.TrimSpace(c.Model.OpenAI.APIKey) == "" {
			return fmt.Errorf("%s is required for openai_compatible provider", APIKeyEnv)
		}
		return nil
	case "modelhub":
		if strings.TrimSpace(c.Model.ModelHub.Endpoint) == "" {
			return fmt.Errorf("model.modelhub.endpoint is required for modelhub provider")
		}
		if strings.TrimSpace(c.Model.ModelHub.Model) == "" {
			return fmt.Errorf("model.modelhub.model is required for modelhub provider")
		}
		if strings.TrimSpace(c.Model.ModelHub.AK) == "" {
			return fmt.Errorf("%s is required for modelhub provider", ModelHubAKEnv)
		}
		return nil
	default:
		return fmt.Errorf("unsupported model provider %q", c.Model.Provider)
	}
}

func (c Config) validateStorage() error {
	switch c.Storage.Driver {
	case "local":
		return nil
	case "mysql":
		mysql := c.Storage.MySQL
		for name, value := range map[string]string{
			"storage.mysql.psm":      mysql.PSM,
			"storage.mysql.host":     mysql.Host,
			"storage.mysql.database": mysql.Database,
			"storage.mysql.username": mysql.Username,
			"storage.mysql.password": mysql.Password,
		} {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("%s is required for mysql storage", name)
			}
		}
		if mysql.Port < 1 || mysql.Port > 65535 {
			return fmt.Errorf("storage.mysql.port must be between 1 and 65535 for mysql storage")
		}
		return nil
	default:
		return fmt.Errorf("unsupported storage driver %q", c.Storage.Driver)
	}
}
