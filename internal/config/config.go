package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type LLMConfig struct {
	Provider       string  `json:"provider"`        // openai | anthropic | ollama | codex | codex-cli | codex-oauth | clicliproxy | custom
	Model          string  `json:"model"`
	APIKey         string  `json:"api_key"`
	BaseURL        string  `json:"base_url"`
	RateLimit      float64 `json:"rate_limit"`      // requests per second
	MaxConcurrency int     `json:"max_concurrency"` // parallel LLM calls
	MaxTokens      int     `json:"max_tokens"`
	UseCodexCLI    bool    `json:"use_codex_cli"`   // try local `codex` binary first
	CodexBin       string  `json:"codex_bin"`       // path to codex binary (default: look in PATH)
	ProxyPort      int     `json:"proxy_port"`      // for clicliproxy provider (default 8080)
}

type GraphConfig struct {
	ChunkSize        int    `json:"chunk_size"`
	ChunkOverlap     int    `json:"chunk_overlap"`
	CommunityMinSize int    `json:"community_min_size"`
	ExtractionMode   string `json:"extraction_mode"` // "local" | "llm" | "hybrid"
	BatchSize        int    `json:"batch_size"`       // chunks per LLM call (default 3)
}

type SimConfig struct {
	DefaultRounds int `json:"default_rounds"`
	MaxAgents     int `json:"max_agents"`
}

type Config struct {
	Project string      `json:"project"`
	DBPath  string      `json:"db_path"`
	LLM     LLMConfig   `json:"llm"`
	Graph   GraphConfig `json:"graph"`
	Sim     SimConfig   `json:"sim"`
}

func Default() *Config {
	return &Config{
		DBPath: ".fishnet/fishnet.db",
		LLM: LLMConfig{
			// provider: openai | anthropic | ollama | codex | codex-cli
			// codex uses models like "o4-mini" or "gpt-4o" via the OpenAI API
			// codex-cli tries the local `codex` binary first, then falls back to OpenAI API
			Provider:       "openai",
			Model:          "gpt-4o-mini",
			BaseURL:        "https://api.openai.com/v1",
			RateLimit:      10,
			MaxConcurrency: 5,
			MaxTokens:      4096,
		},
		Graph: GraphConfig{
			ChunkSize:        600,
			ChunkOverlap:     80,
			CommunityMinSize: 2,
			ExtractionMode:   "local",
			BatchSize:        3,
		},
		Sim: SimConfig{
			DefaultRounds: 3,
			MaxAgents:     30,
		},
	}
}

// Load reads config from .fishnet/config.json (local) or ~/.fishnet/config.json (global)
func Load() (*Config, error) {
	cfg := Default()

	// Try local config first
	paths := []string{
		".fishnet/config.json",
	}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".fishnet", "config.json"))
	}

	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse %s: %w", p, err)
		}
		return cfg, nil
	}

	return cfg, fmt.Errorf("no config found; run: fishnet init <name>")
}

func Save(cfg *Config) error {
	if err := os.MkdirAll(".fishnet", 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(".fishnet/config.json", data, 0644)
}

// ProviderBaseURL returns default base URLs for known providers
func ProviderBaseURL(provider string) string {
	switch provider {
	case "anthropic":
		return "https://api.anthropic.com/v1"
	case "ollama":
		return "http://localhost:11434/v1"
	case "codex", "codex-cli":
		return "https://api.openai.com/v1" // same endpoint, different model (e.g. o4-mini)
	case "clicliproxy":
		return "http://localhost:8080/v1"
	case "codex-oauth":
		return "" // uses Codex Responses API with OAuth tokens, no OpenAI-compat base URL
	default:
		return "https://api.openai.com/v1"
	}
}
