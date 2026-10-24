package shortener

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/vladlim/url-shortener/internal/domain"
)

// Repository must atomically deduplicate active links by (ClientID, ExternalID).
type Repository interface {
	CreateOrGet(context.Context, domain.Link, time.Duration) (domain.Link, bool, error)
	FindActive(context.Context, string) (domain.Link, error)
	DeleteExpired(context.Context, int) (int64, error)
}

type Config struct {
	Repository Repository
	DefaultTTL time.Duration
	BaseURL    string
}

type Service struct {
	repository Repository
	ttl        time.Duration
	baseURL    string
}

type CreateInput struct {
	ClientID   string
	ExternalID string
	URL        string
}

type Result struct {
	Link     domain.Link
	ShortURL string
	Created  bool
}

func New(cfg Config) (*Service, error) {
	if cfg.Repository == nil || cfg.DefaultTTL < time.Second {
		return nil, errors.New("repository and TTL of at least one second are required")
	}

	u, err := url.Parse(cfg.BaseURL)
	if err != nil ||
		u.Hostname() == "" ||
		(u.Scheme != "http" && u.Scheme != "https") ||
		u.User != nil ||
		u.RawQuery != "" ||
		u.Fragment != "" ||
		(u.Path != "" && u.Path != "/") {
		return nil, errors.New("base URL must be an absolute HTTP(S) origin without credentials, path, query or fragment")
	}

	return &Service{
		repository: cfg.Repository,
		ttl:        cfg.DefaultTTL,
		baseURL:    strings.TrimRight(cfg.BaseURL, "/"),
	}, nil
}

func (s *Service) Create(ctx context.Context, input CreateInput) (Result, error) {
	if strings.TrimSpace(input.ClientID) == "" ||
		len(input.ClientID) > 256 ||
		strings.TrimSpace(input.ExternalID) == "" ||
		len(input.ExternalID) > 256 {
		return Result{}, fmt.Errorf("%w: client_id and external_id must contain 1–256 bytes", domain.ErrInvalidInput)
	}

	u, err := url.Parse(input.URL)
	if err != nil ||
		len(input.URL) > 8192 ||
		u.Hostname() == "" ||
		(u.Scheme != "http" && u.Scheme != "https") ||
		u.User != nil {
		return Result{}, fmt.Errorf(
			"%w: url must be an absolute HTTP(S) URL without credentials, up to 8192 bytes",
			domain.ErrInvalidInput,
		)
	}

	for attempt := 0; attempt < 5; attempt++ {
		var random [12]byte
		if _, err := rand.Read(random[:]); err != nil {
			return Result{}, fmt.Errorf("generate code: %w", err)
		}

		candidate := domain.Link{
			Code:        base64.RawURLEncoding.EncodeToString(random[:]),
			ClientID:    input.ClientID,
			ExternalID:  input.ExternalID,
			OriginalURL: input.URL,
		}

		link, created, err := s.repository.CreateOrGet(ctx, candidate, s.ttl)
		if errors.Is(err, domain.ErrCodeCollision) {
			continue
		}

		if err != nil {
			return Result{}, fmt.Errorf("create link: %w", err)
		}

		if link.OriginalURL != input.URL {
			return Result{}, domain.ErrConflict
		}

		return Result{
			Link:     link,
			ShortURL: s.baseURL + "/" + link.Code,
			Created:  created,
		}, nil
	}

	return Result{}, errors.New("could not allocate a unique short code")
}

func (s *Service) Resolve(ctx context.Context, code string) (domain.Link, error) {
	if len(code) != 16 {
		return domain.Link{}, domain.ErrNotFound
	}

	for _, ch := range code {
		if !(ch >= 'a' && ch <= 'z' ||
			ch >= 'A' && ch <= 'Z' ||
			ch >= '0' && ch <= '9' ||
			ch == '-' || ch == '_') {
			return domain.Link{}, domain.ErrNotFound
		}
	}

	return s.repository.FindActive(ctx, code)
}

func (s *Service) Cleanup(ctx context.Context, batchSize int) (int64, error) {
	if batchSize <= 0 {
		return 0, errors.New("cleanup batch size must be positive")
	}

	return s.repository.DeleteExpired(ctx, batchSize)
}
