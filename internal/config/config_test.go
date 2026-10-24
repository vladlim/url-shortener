package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func deploymentEnvironment(t *testing.T) {
	t.Helper()

	values := map[string]string{
		"APP_ENV":        "local",
		"HTTP_ADDR":      ":9090",
		"DB_HOST":        "db.example",
		"DB_PORT":        "5433",
		"DB_USER":        "example",
		"DB_PASSWORD":    "example-password",
		"DB_NAME":        "example",
		"DB_SSLMODE":     "require",
		"SHORTENER_HOST": "https://short.example",
	}

	for name, value := range values {
		t.Setenv(name, value)
	}
}

func TestLoadSeparatesYAMLAndEnvironment(t *testing.T) {
	deploymentEnvironment(t)

	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := []byte(`
env: ignored
server:
  addr: ignored
  request_timeout: 2s
  write_timeout: 5s
db:
  host: ignored
  password: ignored
shorten:
  default_ttl: 2h
  host: ignored
cleanup:
  interval: 30s
  timeout: 3s
  batch_size: 25
`)

	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Env != "local" || cfg.Server.Addr != ":9090" {
		t.Fatalf("deployment settings did not come from environment: env=%s, addr=%s", cfg.Env, cfg.Server.Addr)
	}

	if cfg.Database.Host != "db.example" || cfg.Database.Port != 5433 {
		t.Fatal("database address did not come from environment")
	}

	if cfg.Database.Password != "example-password" || cfg.ShortenConfig.Host != "https://short.example" {
		t.Fatal("YAML overrode environment-only fields")
	}

	if cfg.ShortenConfig.DefaultTTL != 2*time.Hour || cfg.Cleanup.BatchSize != 25 {
		t.Fatal("application settings did not come from YAML")
	}
}

func TestLoadRejectsMissingDeploymentSettings(t *testing.T) {
	deploymentEnvironment(t)
	t.Setenv("DB_PASSWORD", "")

	if _, err := Load(filepath.Join("..", "..", "configs", "config.yaml")); err == nil {
		t.Fatal("missing required password was accepted")
	}
}

func TestLoadRejectsInvalidTiming(t *testing.T) {
	deploymentEnvironment(t)

	for name, contents := range map[string]string{
		"invalid TTL":                  "shorten:\n  default_ttl: -1s\n",
		"invalid cleanup":              "shorten:\n  default_ttl: 1h\ncleanup:\n  batch_size: -1\n",
		"write before request timeout": "shorten:\n  default_ttl: 1h\nserver:\n  request_timeout: 5s\n  write_timeout: 2s\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}

			if _, err := Load(path); err == nil {
				t.Fatal("invalid timing configuration was accepted")
			}
		})
	}
}
