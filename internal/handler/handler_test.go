package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vladlim/url-shortener/internal/domain"
	"github.com/vladlim/url-shortener/internal/services/shortener"
)

type serviceStub struct {
	create  func(context.Context, shortener.CreateInput) (shortener.Result, error)
	resolve func(context.Context, string) (domain.Link, error)
}

func (s serviceStub) Create(ctx context.Context, input shortener.CreateInput) (shortener.Result, error) {
	return s.create(ctx, input)
}

func (s serviceStub) Resolve(ctx context.Context, code string) (domain.Link, error) {
	return s.resolve(ctx, code)
}

func newRouter(t *testing.T, service serviceStub, ping func(context.Context) error) http.Handler {
	t.Helper()

	if ping == nil {
		ping = func(context.Context) error { return nil }
	}

	router, err := NewHandler(HandlerConfig{
		Service:        service,
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		Ping:           ping,
		RequestTimeout: time.Second,
	})

	if err != nil {
		t.Fatal(err)
	}

	return router
}

func TestCreateHTTP(t *testing.T) {
	for _, created := range []bool{true, false} {
		name := "reused"
		expectedStatus := http.StatusOK
		if created {
			name = "created"
			expectedStatus = http.StatusCreated
		}

		t.Run(name, func(t *testing.T) {
			router := newRouter(t, serviceStub{
				create: func(_ context.Context, input shortener.CreateInput) (shortener.Result, error) {
					if input.ClientID != "client" || input.ExternalID != "event" || input.URL != "https://example.com" {
						t.Fatalf("unexpected input: %+v", input)
					}

					return shortener.Result{
						Link:     domain.Link{Code: "abcdefghijklmnop", OriginalURL: input.URL},
						ShortURL: "https://short.example/abcdefghijklmnop",
						Created:  created,
					}, nil
				},
			}, nil)
			body := `{"client_id":"client","external_id":"event","url":"https://example.com"}`
			request := httptest.NewRequest(http.MethodPost, "/urls", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json; charset=utf-8")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			if recorder.Code != expectedStatus || recorder.Header().Get("Location") != "https://short.example/abcdefghijklmnop" {
				t.Fatalf("unexpected response: %d %s", recorder.Code, recorder.Body.String())
			}

			var response linkResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}

			if response.Code != "abcdefghijklmnop" || response.OriginalURL != "https://example.com" {
				t.Fatalf("unexpected body: %+v", response)
			}
		})
	}
}

func TestCreateRejectsMalformedRequests(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		body        string
		status      int
	}{
		{"wrong media type", "text/plain", `{}`, http.StatusUnsupportedMediaType},
		{"broken JSON", "application/json", `{`, http.StatusBadRequest},
		{"unknown field", "application/json", `{"unknown":1}`, http.StatusBadRequest},
		{"multiple values", "application/json", `{} {}`, http.StatusBadRequest},
		{"oversized", "application/json", `{"url":"` + strings.Repeat("x", 17<<10) + `"}`, http.StatusRequestEntityTooLarge},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := newRouter(t, serviceStub{
				create: func(context.Context, shortener.CreateInput) (shortener.Result, error) {
					t.Fatal("invalid request reached service")
					return shortener.Result{}, nil
				},
			}, nil)
			request := httptest.NewRequest(http.MethodPost, "/urls", strings.NewReader(tc.body))
			request.Header.Set("Content-Type", tc.contentType)
			request.Header.Set("X-Request-Id", "request-123")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			if recorder.Code != tc.status {
				t.Fatalf("expected %d, got %d: %s", tc.status, recorder.Code, recorder.Body.String())
			}

			var response errorResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}

			if response.RequestID != "request-123" || response.Error == "" {
				t.Fatalf("missing error context: %+v", response)
			}
		})
	}
}

func TestServiceErrorsMapToHTTP(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
	}{
		{"invalid input", domain.ErrInvalidInput, http.StatusBadRequest},
		{"conflict", domain.ErrConflict, http.StatusConflict},
		{"not found", domain.ErrNotFound, http.StatusNotFound},
		{"timeout", context.DeadlineExceeded, http.StatusGatewayTimeout},
		{"internal", errors.New("private database detail"), http.StatusInternalServerError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := newRouter(t, serviceStub{
				create: func(context.Context, shortener.CreateInput) (shortener.Result, error) {
					return shortener.Result{}, tc.err
				},
			}, nil)
			request := httptest.NewRequest(http.MethodPost, "/urls", strings.NewReader(`{}`))
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			if recorder.Code != tc.status {
				t.Fatalf("expected %d, got %d", tc.status, recorder.Code)
			}

			if strings.Contains(recorder.Body.String(), "private database detail") {
				t.Fatal("internal error leaked to client")
			}
		})
	}
}

func TestRedirectAndHead(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		t.Run(method, func(t *testing.T) {
			router := newRouter(t, serviceStub{
				resolve: func(_ context.Context, code string) (domain.Link, error) {
					if code != "abcdefghijklmnop" {
						t.Fatalf("unexpected code %q", code)
					}

					return domain.Link{OriginalURL: "https://example.com/article"}, nil
				},
			}, nil)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(method, "/abcdefghijklmnop", nil))
			if recorder.Code != http.StatusFound || recorder.Header().Get("Location") != "https://example.com/article" {
				t.Fatalf("unexpected redirect: %d, %v", recorder.Code, recorder.Header())
			}

			if recorder.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("redirect must not be cached")
			}

			if method == http.MethodHead && recorder.Body.Len() != 0 {
				t.Fatal("HEAD must not return a response body")
			}
		})
	}
}

func TestReadinessFailureDoesNotBreakHealth(t *testing.T) {
	router := newRouter(t, serviceStub{}, func(context.Context) error {
		return errors.New("database unavailable")
	})

	for path, expected := range map[string]int{
		"/health": http.StatusOK,
		"/ready":  http.StatusServiceUnavailable,
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != expected {
			t.Fatalf("%s: expected %d, got %d", path, expected, recorder.Code)
		}
	}
}
