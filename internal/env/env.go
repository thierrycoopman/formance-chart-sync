package env

import (
	"cmp"
	"fmt"
	"os"
	"strings"
)

// Config holds all resolved runtime configuration.
type Config struct {
	ClientID     string
	ClientSecret string
	ServerURL    string
	Ledger       string
	Version      string
	ChartGlob    string
	DryRun       bool
	Force        bool
	Workspace    string
	EventPath    string

	// Git provenance (from GitHub Actions environment)
	Repository string // e.g. "org/repo"
	Branch     string // e.g. "main"
	CommitSHA  string // full 40-char SHA
}

// Load reads the full push-mode configuration from environment variables.
// Requires SERVER_URL. VERSION and LEDGER are optional — when omitted, the
// version and ledger name are extracted from each chart file.
func Load() (*Config, error) {
	cfg := loadBase()
	cfg.Version = os.Getenv("VERSION")
	cfg.ChartGlob = cmp.Or(os.Getenv("CHART_GLOB"), "**/*.chart.yaml")
	cfg.DryRun = strings.EqualFold(os.Getenv("DRY_RUN"), "true")
	cfg.Force = strings.EqualFold(os.Getenv("FORCE"), "true")
	cfg.Workspace = cmp.Or(os.Getenv("GITHUB_WORKSPACE"), ".")
	cfg.EventPath = os.Getenv("GITHUB_EVENT_PATH")
	cfg.Repository = os.Getenv("GITHUB_REPOSITORY")
	cfg.Branch = os.Getenv("GITHUB_REF_NAME")
	cfg.CommitSHA = os.Getenv("GITHUB_SHA")

	if cfg.ServerURL == "" {
		return nil, fmt.Errorf("required environment variables not set: SERVER_URL")
	}

	return cfg, nil
}

// LoadConnection reads only the connection-related configuration from
// environment variables. Used by list/get commands that don't need VERSION
// or GitHub Actions context.
func LoadConnection() (*Config, error) {
	cfg := loadBase()

	var missing []string
	if cfg.ServerURL == "" {
		missing = append(missing, "SERVER_URL")
	}
	if cfg.Ledger == "" {
		missing = append(missing, "LEDGER")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("required environment variables not set: %s", strings.Join(missing, ", "))
	}

	return cfg, nil
}

func loadBase() *Config {
	return &Config{
		ClientID:     os.Getenv("CLIENT_ID"),
		ClientSecret: os.Getenv("CLIENT_SECRET"),
		ServerURL:    os.Getenv("SERVER_URL"),
		Ledger:       os.Getenv("LEDGER"),
	}
}
