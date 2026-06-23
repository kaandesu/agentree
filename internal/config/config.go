// Package config loads and persists agentree's user configuration.
//
// Structured config lives in ~/.config/agentree/config.toml. Secrets (API
// keys) are NEVER stored here — they are read from the environment
// (OPENAI_API_KEY, ANTHROPIC_API_KEY) at call time.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	toml "github.com/pelletier/go-toml/v2"
)

// Provider identifies which LLM API powers the "brain".
type Provider string

const (
	ProviderOpenAI    Provider = "openai"
	ProviderAnthropic Provider = "anthropic"
)

// Config is the persisted, non-secret configuration.
type Config struct {
	// Brain selects the triage/scheduling LLM. Default: openai.
	BrainProvider Provider `toml:"brain_provider"`
	BrainModel    string   `toml:"brain_model"`

	// WorktreeRoot is where worktrees are created. Default:
	// ~/.agentree/worktrees. The per-task path is <root>/<project>/<task>.
	WorktreeRoot string `toml:"worktree_root"`

	// DBPath is the SQLite database file. Default:
	// ~/.config/agentree/agentree.db.
	DBPath string `toml:"db_path"`

	// path is where this config was loaded from (not serialized).
	path string `toml:"-"`
}

// Dirs returns the standard agentree directories, creating them if missing.
func configDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "agentree")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func defaultWorktreeRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".agentree", "worktrees"), nil
}

// Load reads the config from disk, applying defaults for any missing fields.
// If no config file exists yet, defaults are returned (and can be saved later).
func Load() (*Config, error) {
	dir, err := configDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "config.toml")

	wtRoot, err := defaultWorktreeRoot()
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		BrainProvider: ProviderOpenAI,
		BrainModel:    "gpt-4o",
		WorktreeRoot:  wtRoot,
		DBPath:        filepath.Join(dir, "agentree.db"),
		path:          path,
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.path = path
	return cfg, nil
}

// Save writes the config back to disk.
func (c *Config) Save() error {
	data, err := toml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(c.path, data, 0o644)
}

// APIKey returns the API key for the configured brain provider from the env.
func (c *Config) APIKey() string {
	switch c.BrainProvider {
	case ProviderAnthropic:
		return os.Getenv("ANTHROPIC_API_KEY")
	default:
		return os.Getenv("OPENAI_API_KEY")
	}
}
