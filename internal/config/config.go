package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	DBURL                      string  `yaml:"db_url"`
	CerberusBin                string  `yaml:"cerberus_bin"`
	CerberusImage              string  `yaml:"cerberus_image"`
	CerberusModel              string  `yaml:"cerberus_model"`
	CerberusProfile            string  `yaml:"cerberus_profile"`
	ServerPort                 int     `yaml:"server_port"`
	UIVerbosity                string  `yaml:"ui_verbosity"`
	MaxConcurrentWorkflows     int     `yaml:"max_concurrent_workflows"`
	DefaultWorkflowBudgetUSD   float64 `yaml:"default_workflow_budget_usd"`
	DefaultPhaseTimeoutSeconds int     `yaml:"default_phase_timeout_seconds"`
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

func RuntimeSettingKeys() map[string]bool {
	return map[string]bool{
		"cerberus_bin":                  true,
		"cerberus_image":                true,
		"cerberus_model":                true,
		"cerberus_profile":              true,
		"ui_verbosity":                  true,
		"max_concurrent_workflows":      true,
		"default_workflow_budget_usd":   true,
		"default_phase_timeout_seconds": true,
		"git_root":                      true,
	}
}

func RuntimeDefaults(c Config) map[string]string {
	return map[string]string{
		"cerberus_bin":                  c.CerberusBin,
		"cerberus_image":                c.CerberusImage,
		"cerberus_model":                c.CerberusModel,
		"cerberus_profile":              c.CerberusProfile,
		"ui_verbosity":                  c.UIVerbosity,
		"max_concurrent_workflows":      strconv.Itoa(c.MaxConcurrentWorkflows),
		"default_workflow_budget_usd":   strconv.FormatFloat(c.DefaultWorkflowBudgetUSD, 'f', -1, 64),
		"default_phase_timeout_seconds": strconv.Itoa(c.DefaultPhaseTimeoutSeconds),
		"git_root":                      c.GitRoot,
	}
}

func ApplyRuntimeSettings(c *Config, values map[string]string) error {
	for k, v := range values {
		switch k {
		case "cerberus_bin":
			c.CerberusBin = v
		case "cerberus_image":
			c.CerberusImage = v
		case "cerberus_model":
			c.CerberusModel = v
		case "cerberus_profile":
			c.CerberusProfile = v
		case "ui_verbosity":
			c.UIVerbosity = v
		case "max_concurrent_workflows":
			n, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("parse %s: %w", k, err)
			}
			c.MaxConcurrentWorkflows = n
		case "default_workflow_budget_usd":
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return fmt.Errorf("parse %s: %w", k, err)
			}
			c.DefaultWorkflowBudgetUSD = f
		case "default_phase_timeout_seconds":
			n, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("parse %s: %w", k, err)
			}
			c.DefaultPhaseTimeoutSeconds = n
		case "git_root":
			c.GitRoot = expandHome(v)
		}
	}
	setDefaults(c)
	return nil
}

func setDefaults(c *Config) {
	if c.CerberusBin == "" {
		c.CerberusBin = "cerberus"
	}
	if c.ServerPort == 0 {
		c.ServerPort = 8080
	}
	if c.UIVerbosity == "" {
		c.UIVerbosity = "normal"
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
