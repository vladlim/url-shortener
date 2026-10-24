package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/vladlim/url-shortener/internal/domain"
	"github.com/vladlim/url-shortener/internal/metrics"
	"github.com/vladlim/url-shortener/internal/services/shortener"
)

type Shortener interface {
	Create(context.Context, shortener.CreateInput) (shortener.Result, error)
	Resolve(context.Context, string) (domain.Link, error)
}

type HandlerConfig struct {
	Metrics        *metrics.Metrics
	RequestTimeout time.Duration
	Service        Shortener
	Logger         *slog.Logger
	Ping           func(context.Context) error
	Middlewares    []func(http.Handler) http.Handler
}

type Handler struct {
	service Shortener
	logger  *slog.Logger
	ping    func(context.Context) error
}

func NewHandler(cfg HandlerConfig) (http.Handler, error) {
	if cfg.Service == nil || cfg.Logger == nil || cfg.Ping == nil || cfg.RequestTimeout <= 0 {
		return nil, errors.New("service, logger, database ping and positive request timeout are required")
	}

	if cfg.Metrics == nil {
		cfg.Metrics = metrics.New()
	}

	h := &Handler{
		service: cfg.Service,
		logger:  cfg.Logger,
		ping:    cfg.Ping,
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(h.requestLogger)
	r.Use(cfg.Metrics.Middleware)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(cfg.RequestTimeout))
	r.Use(cfg.Middlewares...)
	r.Handle("/metrics", cfg.Metrics.Handler())

	r.Get("/health", h.health)
	r.Get("/ready", h.ready)
	r.Post("/urls", h.create)
	r.Get("/{code}", h.redirect)
	r.Head("/{code}", h.redirect)
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		h.writeError(w, r, http.StatusNotFound, "not found")
	})

	return r, nil
}

func (h *Handler) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		status := ww.Status()
		if status == 0 {
			status = http.StatusOK
		}

		h.logger.Info("HTTP request",
			"request_id", middleware.GetReqID(r.Context()),
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"duration", time.Since(started),
		)
	})
}
