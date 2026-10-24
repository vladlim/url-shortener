package shortener

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vladlim/url-shortener/internal/domain"
)

type repositoryStub struct {
	create func(context.Context, domain.Link, time.Duration) (domain.Link, bool, error)
	find   func(context.Context, string) (domain.Link, error)
}

func (r repositoryStub) CreateOrGet(ctx context.Context, link domain.Link, ttl time.Duration) (domain.Link, bool, error) {
	return r.create(ctx, link, ttl)
}

func (r repositoryStub) FindActive(ctx context.Context, code string) (domain.Link, error) {
	return r.find(ctx, code)
}

func (r repositoryStub) DeleteExpired(context.Context, int) (int64, error) {
	return 0, nil
}

func newService(t *testing.T, repository Repository) *Service {
	t.Helper()

	service, err := New(Config{
		Repository: repository,
		DefaultTTL: time.Hour,
		BaseURL:    "https://short.example/",
	})

	if err != nil {
		t.Fatal(err)
	}

	return service
}

func TestCreateRejectsInvalidInputBeforeStorage(t *testing.T) {
	cases := []struct {
		name  string
		input CreateInput
	}{
		{"empty client", CreateInput{"", "event", "https://example.com"}},
		{"blank external", CreateInput{"client", "  ", "https://example.com"}},
		{"long client", CreateInput{strings.Repeat("a", 257), "event", "https://example.com"}},
		{"relative URL", CreateInput{"client", "event", "/article"}},
		{"unsupported scheme", CreateInput{"client", "event", "javascript:alert(1)"}},
		{"credentials", CreateInput{"client", "event", "https://user:pass@example.com"}},
		{"malformed URL", CreateInput{"client", "event", "https://%zz"}},
		{"long URL", CreateInput{"client", "event", "https://example.com/" + strings.Repeat("a", 8192)}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := newService(t, repositoryStub{
				create: func(context.Context, domain.Link, time.Duration) (domain.Link, bool, error) {
					t.Fatal("storage must not be called for invalid input")
					return domain.Link{}, false, nil
				},
			})

			_, err := service.Create(t.Context(), tc.input)
			if !errors.Is(err, domain.ErrInvalidInput) {
				t.Fatalf("expected invalid input, got %v", err)
			}
		})
	}
}

func TestCreateRetriesCodeCollision(t *testing.T) {
	calls := 0
	service := newService(t, repositoryStub{
		create: func(_ context.Context, link domain.Link, ttl time.Duration) (domain.Link, bool, error) {
			calls++
			if ttl != time.Hour || link.ClientID != "client" || link.ExternalID != "event" || len(link.Code) != 16 {
				t.Fatalf("unexpected storage input: %+v, ttl=%v", link, ttl)
			}

			if calls == 1 {
				return domain.Link{}, false, domain.ErrCodeCollision
			}

			return link, true, nil
		},
	})

	result, err := service.Create(t.Context(), CreateInput{"client", "event", "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}

	if calls != 2 || !result.Created || result.ShortURL != "https://short.example/"+result.Link.Code {
		t.Fatalf("unexpected result: %+v, calls=%d", result, calls)
	}
}

func TestCreateReusesLinkWithoutChangingExpiration(t *testing.T) {
	existing := domain.Link{
		Code:        "abcdefghijklmnop",
		OriginalURL: "https://example.com",
		ExpiresAt:   time.Now().Add(time.Minute).UTC(),
	}

	service := newService(t, repositoryStub{
		create: func(context.Context, domain.Link, time.Duration) (domain.Link, bool, error) {
			return existing, false, nil
		},
	})

	result, err := service.Create(t.Context(), CreateInput{"client", "event", existing.OriginalURL})
	if err != nil {
		t.Fatal(err)
	}

	if result.Created || result.Link != existing {
		t.Fatalf("existing link changed: %+v", result)
	}
}

func TestCreateConflictsOnDifferentURL(t *testing.T) {
	service := newService(t, repositoryStub{
		create: func(context.Context, domain.Link, time.Duration) (domain.Link, bool, error) {
			return domain.Link{OriginalURL: "https://old.example"}, false, nil
		},
	})

	_, err := service.Create(t.Context(), CreateInput{"client", "event", "https://new.example"})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
}

func TestCreateBoundsCollisionRetries(t *testing.T) {
	calls := 0
	service := newService(t, repositoryStub{
		create: func(context.Context, domain.Link, time.Duration) (domain.Link, bool, error) {
			calls++
			return domain.Link{}, false, domain.ErrCodeCollision
		},
	})

	_, err := service.Create(t.Context(), CreateInput{"client", "event", "https://example.com"})
	if err == nil || calls != 5 {
		t.Fatalf("expected failure after five attempts: calls=%d, error=%v", calls, err)
	}
}

func TestResolveValidatesCodeAndPreservesStorageError(t *testing.T) {
	calls := 0
	service := newService(t, repositoryStub{
		find: func(context.Context, string) (domain.Link, error) {
			calls++
			return domain.Link{}, domain.ErrNotFound
		},
	})

	for _, code := range []string{"short", "abcdefghijklmno!", "abcdefghijklmnop"} {
		_, err := service.Resolve(t.Context(), code)
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("code %q: expected not found, got %v", code, err)
		}
	}

	if calls != 1 {
		t.Fatalf("storage should only receive the valid code, calls=%d", calls)
	}
}
