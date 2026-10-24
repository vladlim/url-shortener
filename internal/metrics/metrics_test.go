package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func TestMiddlewareUsesRouteTemplatesAndRecordsStatus(t *testing.T) {
	metrics := New()
	router := chi.NewRouter()
	router.Use(metrics.Middleware)
	router.Get("/{code}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusFound)
	})
	router.Handle("/metrics", metrics.Handler())

	for _, code := range []string{"first-private-code", "second-private-code"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/"+code, nil))
	}

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	body := response.Body.String()
	if response.Code != http.StatusOK {
		t.Fatalf("metrics returned %d", response.Code)
	}

	if !strings.Contains(body, `shortener_http_requests_total{method="GET",route="/{code}",status="302"} 2`) {
		t.Fatalf("missing aggregated route counter: %s", body)
	}

	for _, forbidden := range []string{"first-private-code", "second-private-code", `route="/metrics"`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("unexpected high-cardinality or internal route label: %s", forbidden)
		}
	}

	for _, expected := range []string{
		`shortener_http_request_duration_seconds_count{method="GET",route="/{code}"} 2`,
		"shortener_http_requests_in_flight 0",
		"go_goroutines ",
		"process_cpu_seconds_total ",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("missing metric %q", expected)
		}
	}
}

func TestMiddlewareRecordsRecoveredPanic(t *testing.T) {
	metrics := New()
	router := chi.NewRouter()
	router.Use(metrics.Middleware)
	router.Use(middleware.Recoverer)
	router.Get("/panic", func(http.ResponseWriter, *http.Request) {
		panic("unexpected handler error")
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/panic", nil))

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("panic returned %d", response.Code)
	}

	scrape := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(scrape, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if !strings.Contains(scrape.Body.String(), `route="/panic",status="500"} 1`) {
		t.Fatal("recovered panic was not counted as HTTP 500")
	}
}

func TestMiddlewareBoundsUnmatchedRoutesAndMethods(t *testing.T) {
	metrics := New()
	router := chi.NewRouter()
	router.Use(metrics.Middleware)
	router.Get("/known", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	for _, method := range []string{"CUSTOM-ONE", "CUSTOM-TWO"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(method, "/unknown/private/path", nil))
	}

	scrape := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(scrape, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	body := scrape.Body.String()
	if !strings.Contains(body, `method="OTHER",route="unmatched",status="405"} 2`) {
		t.Fatalf("unexpected unmatched request metrics: %s", body)
	}
}

func TestRegistriesAreIndependent(t *testing.T) {
	first := New()
	second := New()

	first.requests.WithLabelValues("GET", "/urls", "200").Inc()

	scrape := httptest.NewRecorder()
	second.Handler().ServeHTTP(scrape, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if strings.Contains(scrape.Body.String(), "shortener_http_requests_total{") {
		t.Fatal("metrics leaked between application instances")
	}
}
