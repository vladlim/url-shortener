package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"
)

type ServerConfig struct {
	Address           string
	ReadTimeout       time.Duration
	ReadHeaderTimeout time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	Handler           http.Handler
}

// NewServer configures a server without starting it.
func NewServer(cfg ServerConfig) (*http.Server, error) {
	if strings.TrimSpace(cfg.Address) == "" {
		return nil, errors.New("empty server address")
	}

	if cfg.Handler == nil {
		return nil, errors.New("HTTP handler is required")
	}

	if cfg.ReadTimeout <= 0 ||
		cfg.ReadHeaderTimeout <= 0 ||
		cfg.WriteTimeout <= 0 ||
		cfg.IdleTimeout <= 0 {
		return nil, errors.New("server timeouts must be positive")
	}

	return &http.Server{
		Addr:              cfg.Address,
		Handler:           cfg.Handler,
		ReadTimeout:       cfg.ReadTimeout,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}, nil
}
