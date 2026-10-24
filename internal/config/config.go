package config

import (
	"fmt"

	"github.com/ilyakaznacheev/cleanenv"
)

// Load reads application settings from YAML and deployment settings from the environment.
// The application does not load .env files.
func Load(configPath string) (*Config, error) {
	var cfg Config

	if err := cleanenv.ReadConfig(configPath, &cfg); err != nil {
		return nil, fmt.Errorf("load YAML and environment configuration: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("error validating config: %w", err)
	}

	return &cfg, nil
}

// MustLoad loads and validates the configuration and panics on failure.
// It is intended for use during application startup only.
func MustLoad(configPath string) *Config {
	cfg, err := Load(configPath)
	if err != nil {
		panic(fmt.Sprintf("load application config: %v", err))
	}

	return cfg
}
