package config

import (
	"errors"
	"strings"
	"time"
)

type Config struct {
	Env           string         `yaml:"-" env:"APP_ENV" env-required:"true"`
	Server        ServerConfig   `yaml:"server"`
	Database      DatabaseConfig `yaml:"-"`
	ShortenConfig ShortenConfig  `yaml:"shorten"`
	Cleanup       CleanupConfig  `yaml:"cleanup"`
}

type ServerConfig struct {
	RequestTimeout    time.Duration `yaml:"request_timeout" env-default:"5s"`
	Addr              string        `yaml:"-" env:"HTTP_ADDR" env-default:":8082"`
	ReadTimeout       time.Duration `yaml:"read_timeout" env-default:"5s"`
	ReadHeaderTimeout time.Duration `yaml:"read_header_timeout" env-default:"2s"`
	WriteTimeout      time.Duration `yaml:"write_timeout" env-default:"10s"`
	IdleTimeout       time.Duration `yaml:"idle_timeout" env-default:"60s"`
}

type DatabaseConfig struct {
	Host     string `yaml:"-" env:"DB_HOST" env-required:"true"`
	SSLMode  string `yaml:"-" env:"DB_SSLMODE" env-required:"true"`
	Port     int    `yaml:"-" env:"DB_PORT" env-required:"true"`
	Username string `yaml:"-" env:"DB_USER" env-required:"true"`
	Password string `yaml:"-" env:"DB_PASSWORD" env-required:"true"`
	Database string `yaml:"-" env:"DB_NAME" env-required:"true"`
}

type ShortenConfig struct {
	DefaultTTL time.Duration `yaml:"default_ttl"`
	Host       string        `yaml:"-" env:"SHORTENER_HOST" env-required:"true"`
}

type CleanupConfig struct {
	Interval  time.Duration `yaml:"interval" env-default:"1m"`
	Timeout   time.Duration `yaml:"timeout" env-default:"10s"`
	BatchSize int           `yaml:"batch_size" env-default:"1000"`
}

func (c *Config) Validate() error {
	if c.ShortenConfig.DefaultTTL < time.Second {
		return errors.New("default TTL must be at least one second")
	}

	if c.Cleanup.Interval <= 0 || c.Cleanup.Timeout <= 0 || c.Cleanup.BatchSize <= 0 {
		return errors.New("cleanup interval, timeout and batch size must be positive")
	}

	if strings.TrimSpace(c.Server.Addr) == "" {
		return errors.New("empty server address")
	}

	if c.Server.RequestTimeout <= 0 ||
		c.Server.ReadTimeout <= 0 ||
		c.Server.ReadHeaderTimeout <= 0 ||
		c.Server.WriteTimeout <= 0 ||
		c.Server.IdleTimeout <= 0 {
		return errors.New("server timeouts must be positive")
	}

	if c.Server.WriteTimeout <= c.Server.RequestTimeout {
		return errors.New("write timeout must exceed request timeout")
	}

	if strings.TrimSpace(c.Database.Host) == "" {
		return errors.New("empty db host")
	}

	if strings.TrimSpace(c.Database.Username) == "" {
		return errors.New("empty db username")
	}

	if strings.TrimSpace(c.Database.Password) == "" {
		return errors.New("empty db password")
	}

	if strings.TrimSpace(c.Database.Database) == "" {
		return errors.New("empty db database")
	}

	if c.Database.Port <= 0 || c.Database.Port > 65535 {
		return errors.New("incorrect db port")
	}

	if strings.TrimSpace(c.ShortenConfig.Host) == "" {
		return errors.New("empty shorten host")
	}

	return nil
}
