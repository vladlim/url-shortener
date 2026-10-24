package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/vladlim/url-shortener/internal/handler"
	"github.com/vladlim/url-shortener/internal/services/shortener"
)

type integrationResponse struct {
	Code        string    `json:"code"`
	ShortURL    string    `json:"short_url"`
	OriginalURL string    `json:"original_url"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func integrationAPI(t *testing.T) (*Storage, *shortener.Service, http.Handler) {
	t.Helper()

	storage := integrationStorage(t)
	service, err := shortener.New(shortener.Config{
		Repository: storage,
		DefaultTTL: time.Hour,
		BaseURL:    "https://short.example",
	})

	if err != nil {
		t.Fatal(err)
	}

	router, err := handler.NewHandler(handler.HandlerConfig{
		Service:        service,
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		Ping:           storage.Ping,
		RequestTimeout: 5 * time.Second,
	})

	if err != nil {
		t.Fatal(err)
	}

	return storage, service, router
}

func createThroughHTTP(router http.Handler, client, external, original string) *httptest.ResponseRecorder {
	// Marshal a fixed struct: its fields are all strings, so marshaling cannot fail.
	body, _ := json.Marshal(struct {
		ClientID   string `json:"client_id"`
		ExternalID string `json:"external_id"`
		URL        string `json:"url"`
	}{
		ClientID:   client,
		ExternalID: external,
		URL:        original,
	})

	request := httptest.NewRequest(http.MethodPost, "/urls", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func decodeLink(t *testing.T, response *httptest.ResponseRecorder, status int) integrationResponse {
	t.Helper()

	if response.Code != status {
		t.Fatalf("expected HTTP %d, got %d: %s", status, response.Code, response.Body.String())
	}

	var link integrationResponse
	if err := json.Unmarshal(response.Body.Bytes(), &link); err != nil {
		t.Fatal(err)
	}

	return link
}

func TestIntegrationHTTPLinkLifecycle(t *testing.T) {
	storage, service, router := integrationAPI(t)

	const original = "https://example.com/article?source=test"
	createdResponse := createThroughHTTP(router, "alice", "event", original)
	created := decodeLink(t, createdResponse, http.StatusCreated)
	repeatedResponse := createThroughHTTP(router, "alice", "event", original)
	repeated := decodeLink(t, repeatedResponse, http.StatusOK)
	if created != repeated {
		t.Fatalf("reuse changed the code or TTL: %+v vs %+v", created, repeated)
	}

	conflict := createThroughHTTP(router, "alice", "event", "https://other.example")
	if conflict.Code != http.StatusConflict {
		t.Fatalf("expected conflict, got %d", conflict.Code)
	}

	independentResponse := createThroughHTTP(router, "bob", "event", original)
	independent := decodeLink(t, independentResponse, http.StatusCreated)
	if independent.Code == created.Code {
		t.Fatal("different clients shared a code")
	}

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(method, "/"+created.Code, nil))
		if response.Code != http.StatusFound || response.Header().Get("Location") != original {
			t.Fatalf("%s: wrong redirect: %d, %v", method, response.Code, response.Header())
		}

		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("redirect can be cached beyond TTL")
		}
	}

	expire(t, storage, created.Code)
	expired := httptest.NewRecorder()
	router.ServeHTTP(expired, httptest.NewRequest(http.MethodGet, "/"+created.Code, nil))
	if expired.Code != http.StatusNotFound {
		t.Fatalf("expired link returned %d", expired.Code)
	}

	replacementResponse := createThroughHTTP(router, "alice", "event", "https://new.example")
	replacement := decodeLink(t, replacementResponse, http.StatusCreated)
	if replacement.Code == created.Code || !replacement.ExpiresAt.After(time.Now()) {
		t.Fatalf("invalid replacement: %+v", replacement)
	}

	old := httptest.NewRecorder()
	router.ServeHTTP(old, httptest.NewRequest(http.MethodGet, "/"+created.Code, nil))
	if old.Code != http.StatusNotFound {
		t.Fatal("replaced code still resolves")
	}

	expire(t, storage, independent.Code)
	deleted, err := service.Cleanup(t.Context(), 10)
	if err != nil || deleted != 1 {
		t.Fatalf("cleanup: deleted=%d, error=%v", deleted, err)
	}

	stillActive := httptest.NewRecorder()
	router.ServeHTTP(stillActive, httptest.NewRequest(http.MethodGet, "/"+replacement.Code, nil))
	if stillActive.Code != http.StatusFound {
		t.Fatal("cleanup removed active link")
	}
}

func TestIntegrationHTTPConcurrentConflictingRequests(t *testing.T) {
	_, _, router := integrationAPI(t)

	const requests = 20
	responses := make(chan *httptest.ResponseRecorder, requests)
	start := make(chan struct{})

	var group sync.WaitGroup

	for i := range requests {
		group.Add(1)
		go func() {
			defer group.Done()

			<-start
			original := fmt.Sprintf("https://example.com/version-%d", i%2)
			responses <- createThroughHTTP(router, "client", "same-event", original)
		}()
	}

	close(start)
	group.Wait()
	close(responses)
	createdCount := 0
	successCount := 0
	conflicts := 0

	var winner integrationResponse

	for response := range responses {
		if response.Code == http.StatusConflict {
			conflicts++
			continue
		}

		if response.Code == http.StatusCreated {
			createdCount++
		} else if response.Code != http.StatusOK {
			t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
		}

		link := decodeLink(t, response, response.Code)
		successCount++
		if winner.Code == "" {
			winner = link
		}

		if winner != link {
			t.Fatal("successful requests returned different links")
		}
	}

	if createdCount != 1 || successCount != requests/2 || conflicts != requests/2 {
		t.Fatalf("created=%d, successful=%d, conflicts=%d", createdCount, successCount, conflicts)
	}
}

func TestPostgresCancellationWhileWaitingForPairLock(t *testing.T) {
	storage := integrationStorage(t)
	tx, err := storage.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	defer func() {

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		_ = tx.Rollback(ctx)
	}()

	key, _ := json.Marshal([2]string{"client", "event"})
	_, err = tx.Exec(
		t.Context(),
		"SELECT pg_advisory_xact_lock(hashtextextended($1, 0))",
		string(key),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	_, _, err = storage.CreateOrGet(ctx, candidate("waiting-code", "event"), time.Hour)
	if err == nil || ctx.Err() == nil {
		t.Fatalf("lock wait ignored cancellation: %v", err)
	}

	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}

	_, created, err := storage.CreateOrGet(t.Context(), candidate("fresh-code", "event"), time.Hour)
	if err != nil || !created {
		t.Fatalf("cancelled transaction leaked lock or row: created=%v, error=%v", created, err)
	}
}
