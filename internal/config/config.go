package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	DBURL                      string  `yaml:"db_url"`
	CerberusBin                string  `yaml:"cerberus_bin"`
	CerberusImage              string  `yaml:"cerberus_image"`
	ServerPort                 int     `yaml:"server_port"`
	MaxConcurrentWorkflows     int     `yaml:"max_concurrent_workflows"`
	DefaultWorkflowBudgetUSD   float64 `yaml:"default_workflow_budget_usd"`
	DefaultPhaseTimeoutSeconds int     `yaml:"default_phase_timeout_seconds"`
	ReviewBaseURL              string  `yaml:"review_base_url"`
	ReviewAPIKey               string  `yaml:"review_api_key"`
	ReviewModel                string  `yaml:"review_model"`
	GitRoot                    string  `yaml:"git_root"`
}

func Load(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()
	var cfg Config
	if err := yaml.NewDecoder(f).Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	setDefaults(&cfg)
	return cfg, nil
}

func setDefaults(c *Config) {
	if c.CerberusBin == "" {
		c.CerberusBin = "cerberus"
	}
	if c.ServerPort == 0 {
		c.ServerPort = 8080
	}
	if c.MaxConcurrentWorkflows == 0 {
		c.MaxConcurrentWorkflows = 1
	}
	if c.DefaultWorkflowBudgetUSD == 0 {
		c.DefaultWorkflowBudgetUSD = 5.0
	}
	if c.DefaultPhaseTimeoutSeconds == 0 {
		c.DefaultPhaseTimeoutSeconds = 1800
	}
	if c.ReviewModel == "" {
		c.ReviewModel = "claude-haiku-4-5"
	}
	if c.ReviewBaseURL == "" {
		c.ReviewBaseURL = "https://api.openai.com/v1"
	}
	if c.GitRoot != "" {
		c.GitRoot = expandHome(c.GitRoot)
	}
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
