package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vladlim/url-shortener/internal/domain"
)

// Each test owns a separate schema; existing application tables are never touched.
func integrationStorage(t *testing.T) *Storage {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("REQUIRE_INTEGRATION") == "1" {
			t.Fatal("TEST_DATABASE_URL is required for integration tests")
		}

		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}

	schema := pgx.Identifier{fmt.Sprintf("shortener_test_%d", time.Now().UnixNano())}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}

	var pool *pgxpool.Pool
	t.Cleanup(func() {
		if pool != nil {
			pool.Close()
		}

		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("drop isolated test schema: %v", err)
		}

		admin.Close()
	})

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}

	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err = pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}

	applyMigrations(t, ctx, pool)

	return &Storage{pool: pool}
}

func candidate(code, externalID string) domain.Link {
	return domain.Link{
		Code:        code,
		ClientID:    "client",
		ExternalID:  externalID,
		OriginalURL: "https://example.com",
	}
}

func expire(t *testing.T, storage *Storage, code string) {
	t.Helper()

	_, err := storage.pool.Exec(t.Context(), `
        UPDATE urls
        SET created_at = now() - interval '2 hours',
            expires_at = now() - interval '1 hour'
        WHERE code = $1`, code)
	if err != nil {
		t.Fatal(err)
	}
}

func TestPostgresConcurrentDeduplication(t *testing.T) {
	storage := integrationStorage(t)

	const requests = 20
	type result struct {
		link    domain.Link
		created bool
		err     error
	}

	results := make(chan result, requests)
	start := make(chan struct{})

	var group sync.WaitGroup

	for i := range requests {
		group.Add(1)
		go func() {
			defer group.Done()

			<-start
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()

			link, created, err := storage.CreateOrGet(
				ctx,
				candidate(fmt.Sprintf("%016d", i), "same-event"),
				time.Hour,
			)
			results <- result{
				link:    link,
				created: created,
				err:     err,
			}
		}()
	}

	close(start)
	group.Wait()
	close(results)
	createdCount := 0

	var first domain.Link

	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}

		if result.created {
			createdCount++
		}

		if first.Code == "" {
			first = result.link
		}

		if result.link != first {
			t.Fatalf("concurrent requests returned different links: %+v vs %+v", first, result.link)
		}
	}

	if createdCount != 1 {
		t.Fatalf("expected exactly one creation, got %d", createdCount)
	}
}

func TestPostgresExpiredReplacementAndCollisionRollback(t *testing.T) {
	storage := integrationStorage(t)
	old, _, err := storage.CreateOrGet(t.Context(), candidate("old-code", "event"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = storage.CreateOrGet(t.Context(), candidate("occupied-code", "other-event"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	expire(t, storage, old.Code)
	if _, err := storage.FindActive(t.Context(), old.Code); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expired link resolved: %v", err)
	}

	_, _, err = storage.CreateOrGet(t.Context(), candidate("occupied-code", "event"), time.Hour)
	if !errors.Is(err, domain.ErrCodeCollision) {
		t.Fatalf("expected code collision, got %v", err)
	}

	var count int
	err = storage.pool.QueryRow(
		t.Context(),
		"SELECT count(*) FROM urls WHERE code = $1",
		old.Code,
	).Scan(&count)
	if err != nil || count != 1 {
		t.Fatalf("collision must roll back removal of old record: count=%d, error=%v", count, err)
	}

	replacement := candidate("new-code", "event")
	replacement.OriginalURL = "https://new.example"
	link, created, err := storage.CreateOrGet(t.Context(), replacement, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	if !created || link.Code != replacement.Code || link.OriginalURL != replacement.OriginalURL {
		t.Fatalf("unexpected replacement: %+v", link)
	}

	if _, err := storage.FindActive(t.Context(), old.Code); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("old code still resolves: %v", err)
	}

	if _, err := storage.FindActive(t.Context(), link.Code); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresCleanupHonorsBatchAndRetainsActiveLinks(t *testing.T) {
	storage := integrationStorage(t)

	for _, key := range []string{"expired-1", "expired-2", "active"} {
		if _, _, err := storage.CreateOrGet(t.Context(), candidate(key, key), time.Hour); err != nil {
			t.Fatal(err)
		}

		if key != "active" {
			expire(t, storage, key)
		}
	}

	count, err := storage.DeleteExpired(t.Context(), 1)
	if err != nil || count != 1 {
		t.Fatalf("expected one deletion, count=%d, error=%v", count, err)
	}

	count, err = storage.DeleteExpired(t.Context(), 10)
	if err != nil || count != 1 {
		t.Fatalf("expected remaining expired row, count=%d, error=%v", count, err)
	}

	if _, err := storage.FindActive(t.Context(), "active"); err != nil {
		t.Fatalf("active link removed: %v", err)
	}
}

// Test schemas use every versioned SQL migration, sorted by their numeric version components.
// The full-stack CI check also runs the actual Flyway executable and its history checks.
func applyMigrations(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()

	paths, err := filepath.Glob(filepath.Join("..", "..", "..", "migrations", "V*__*.sql"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("find versioned migrations: paths=%v, error=%v", paths, err)
	}

	type migration struct {
		path    string
		version []int
	}

	migrations := make([]migration, 0, len(paths))

	for _, path := range paths {
		name := filepath.Base(path)
		version, _, _ := strings.Cut(strings.TrimPrefix(name, "V"), "__")
		parts := strings.FieldsFunc(version, func(ch rune) bool {
			return ch == '.' || ch == '_'
		})

		entry := migration{path: path}

		for _, part := range parts {
			number, err := strconv.Atoi(part)
			if err != nil || number < 0 {
				t.Fatalf("invalid migration version in %s", name)
			}

			entry.version = append(entry.version, number)
		}

		migrations = append(migrations, entry)
	}

	sort.Slice(migrations, func(i, j int) bool {
		return slices.Compare(migrations[i].version, migrations[j].version) < 0
	})

	for _, migration := range migrations {
		contents, err := os.ReadFile(migration.path)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := pool.Exec(ctx, string(contents)); err != nil {
			t.Fatalf("apply %s: %v", migration.path, err)
		}
	}
}
