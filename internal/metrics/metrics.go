package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics owns a registry so application instances and tests do not share collectors.
type Metrics struct {
	registry *prometheus.Registry
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	inFlight prometheus.Gauge
}

func New() *Metrics {
	registry := prometheus.NewRegistry()

	requests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "shortener_http_requests_total",
		Help: "Completed application HTTP requests by method, route template and status.",
	}, []string{"method", "route", "status"})

	duration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "shortener_http_request_duration_seconds",
		Help:    "Application HTTP request duration in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "route"})

	inFlight := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "shortener_http_requests_in_flight",
		Help: "Application HTTP requests currently being processed.",
	})

	registry.MustRegister(
		requests,
		duration,
		inFlight,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	return &Metrics{
		registry: registry,
		requests: requests,
		duration: duration,
		inFlight: inFlight,
	}
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/metrics", "/health", "/ready":
			next.ServeHTTP(w, r)
			return
		}

		started := time.Now()
		response := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		m.inFlight.Inc()
		defer m.inFlight.Dec()

		next.ServeHTTP(response, r)

		// Read after routing: never put short codes, URLs or client IDs in labels.
		route := chi.RouteContext(r.Context()).RoutePattern()
		if route == "" {
			route = "unmatched"
		}

		status := response.Status()
		if status == 0 {
			status = http.StatusOK
		}

		method := boundedMethod(r.Method)
		m.requests.WithLabelValues(method, route, strconv.Itoa(status)).Inc()
		m.duration.WithLabelValues(method, route).Observe(time.Since(started).Seconds())
	})
}

func boundedMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions,
		http.MethodConnect, http.MethodTrace:
		return method
	default:
		return "OTHER"
	}
}
