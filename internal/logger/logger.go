package logger

import (
	"errors"
	"log/slog"
	"os"
)

type Config struct {
	Env string
}

func New(cfg *Config) (*slog.Logger, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}

	switch cfg.Env {
	case "local":
		return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level:     slog.LevelDebug,
			AddSource: true,
		})), nil
	case "dev":
		return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level:     slog.LevelDebug,
			AddSource: true,
		})), nil
	case "prod":
		return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level:     slog.LevelError,
			AddSource: true,
		})), nil
	default:
		return nil, errors.New("incorrect env name")
	}
}
