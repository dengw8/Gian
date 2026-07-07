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
const APIKeyEnv = "AGENT_MODEL_API_KEY"
const ModelHubAKEnv = "AGENT_MODELHUB_AK"

type Config struct {
	Server  ServerConfig  `yaml:"server"`
	Model   ModelConfig   `yaml:"model"`
	Agent   AgentConfig   `yaml:"agent"`
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
	MaxIterations int `yaml:"max_iterations"`
}

type StorageConfig struct {
	LocalDir string `yaml:"local_dir"`
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
	cfg := Config{}
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
	if c.Storage.LocalDir == "" {
		c.Storage.LocalDir = "data"
	}
	if c.Tools.Workspace == "" {
		c.Tools.Workspace = "."
	}
}

func (c Config) Validate() error {
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
