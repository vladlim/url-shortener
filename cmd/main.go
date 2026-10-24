package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vladlim/url-shortener/internal/config"
	"github.com/vladlim/url-shortener/internal/handler"
	"github.com/vladlim/url-shortener/internal/logger"
	"github.com/vladlim/url-shortener/internal/metrics"
	"github.com/vladlim/url-shortener/internal/services/shortener"
	"github.com/vladlim/url-shortener/internal/storage/postgres"
	"github.com/vladlim/url-shortener/internal/worker"
)

func main() {
	configPath := flag.String("config_path", "configs/config.yaml", "path to service config")
	flag.Parse()
	if err := run(*configPath); err != nil {
		slog.Error("application stopped", "error", err)
		os.Exit(1)
	}
}

func run(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	log, err := logger.New(&logger.Config{Env: cfg.Env})
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	startupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	storage, err := postgres.New(startupCtx, postgres.Config{
		Host:     cfg.Database.Host,
		Port:     cfg.Database.Port,
		Username: cfg.Database.Username,
		Password: cfg.Database.Password,
		Database: cfg.Database.Database,
		SSLMode:  cfg.Database.SSLMode,
	})
	cancel()
	if err != nil {
		return err
	}

	defer storage.Close()

	service, err := shortener.New(shortener.Config{
		Repository: storage,
		DefaultTTL: cfg.ShortenConfig.DefaultTTL,
		BaseURL:    cfg.ShortenConfig.Host,
	})
	if err != nil {
		return err
	}

	router, err := handler.NewHandler(handler.HandlerConfig{
		Metrics:        metrics.New(),
		RequestTimeout: cfg.Server.RequestTimeout,
		Service:        service,
		Logger:         log,
		Ping:           storage.Ping,
	})
	if err != nil {
		return err
	}

	srv, err := handler.NewServer(handler.ServerConfig{
		Address:           cfg.Server.Addr,
		ReadTimeout:       cfg.Server.ReadTimeout,
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
		WriteTimeout:      cfg.Server.WriteTimeout,
		IdleTimeout:       cfg.Server.IdleTimeout,
		Handler:           router,
	})
	if err != nil {
		return err
	}

	workerCtx, stopWorker := context.WithCancel(ctx)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		worker.Run(workerCtx, service, log, worker.Config{
			Interval:  cfg.Cleanup.Interval,
			Timeout:   cfg.Cleanup.Timeout,
			BatchSize: cfg.Cleanup.BatchSize,
		})
	}()
	// Join the worker before closing its database pool, including on startup failure.
	defer func() {
		stopWorker()
		<-workerDone
	}()

	serverErr := make(chan error, 1)
	go func() {
		log.Info("HTTP server listening", "address", srv.Addr)
		serverErr <- srv.ListenAndServe()
	}()
	select {
	case err := <-serverErr:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}

		return nil
	case <-ctx.Done():
		log.Info("shutting down")
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		_ = srv.Close()
		return fmt.Errorf("shutdown HTTP: %w", err)
	}

	if err := <-serverErr; !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}
