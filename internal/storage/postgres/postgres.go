package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vladlim/url-shortener/internal/domain"
)

type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	Database string
	SSLMode  string
}

type Storage struct {
	pool *pgxpool.Pool
}

func New(ctx context.Context, cfg Config) (*Storage, error) {
	u := url.URL{
		Scheme: "postgres",
		Host:   net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Path:   "/" + cfg.Database,
		User:   url.UserPassword(cfg.Username, cfg.Password),
	}

	q := u.Query()
	q.Set("sslmode", cfg.SSLMode)
	q.Set("connect_timeout", "5")
	u.RawQuery = q.Encode()
	poolCfg, err := pgxpool.ParseConfig(u.String())
	if err != nil {
		return nil, errors.New("invalid PostgreSQL connection configuration")
	}

	poolCfg.MaxConns = 10
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create PostgreSQL pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to PostgreSQL: %w", err)
	}

	return &Storage{pool: pool}, nil
}

func (s *Storage) Close() {
	s.pool.Close()
}

func (s *Storage) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func scanLink(row pgx.Row) (domain.Link, error) {
	var link domain.Link
	err := row.Scan(
		&link.Code,
		&link.ClientID,
		&link.ExternalID,
		&link.OriginalURL,
		&link.CreatedAt,
		&link.ExpiresAt,
	)
	return link, err
}

const columns = "code, client_id, external_id, original_url, created_at, expires_at"

func (s *Storage) CreateOrGet(ctx context.Context, candidate domain.Link, ttl time.Duration) (domain.Link, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Link{}, false, err
	}

	defer func() {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()

	// Serialize a pair even when no row exists yet. JSON avoids ambiguous concatenation.
	// Hash collisions only serialize unrelated requests; equality still uses both columns.
	key, _ := json.Marshal([2]string{candidate.ClientID, candidate.ExternalID})
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", string(key)); err != nil {
		return domain.Link{}, false, err
	}

	existing, err := scanLink(tx.QueryRow(
		ctx,
		"SELECT "+columns+" FROM urls WHERE client_id = $1 AND external_id = $2 FOR UPDATE",
		candidate.ClientID,
		candidate.ExternalID,
	))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return domain.Link{}, false, err
	}

	var now time.Time
	// Read time after acquiring locks, not at transaction start.
	if err := tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return domain.Link{}, false, err
	}

	if err == nil && existing.ExpiresAt.After(now) {
		if err := tx.Commit(ctx); err != nil {
			return domain.Link{}, false, err
		}

		return existing, false, nil
	}
	// An expired pair can be reused with any URL; the old code stops resolving.
	_, err = tx.Exec(
		ctx,
		"DELETE FROM urls WHERE client_id = $1 AND external_id = $2",
		candidate.ClientID,
		candidate.ExternalID,
	)
	if err != nil {
		return domain.Link{}, false, err
	}

	candidate.CreatedAt = now
	candidate.ExpiresAt = now.Add(ttl)
	_, err = tx.Exec(
		ctx,
		"INSERT INTO urls ("+columns+") VALUES ($1,$2,$3,$4,$5,$6)",
		candidate.Code,
		candidate.ClientID,
		candidate.ExternalID,
		candidate.OriginalURL,
		candidate.CreatedAt,
		candidate.ExpiresAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "urls_pkey" {
			return domain.Link{}, false, domain.ErrCodeCollision
		}

		return domain.Link{}, false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.Link{}, false, err
	}

	return candidate, true, nil
}

func (s *Storage) FindActive(ctx context.Context, code string) (domain.Link, error) {
	link, err := scanLink(s.pool.QueryRow(
		ctx,
		"SELECT "+columns+" FROM urls WHERE code = $1 AND expires_at > clock_timestamp()",
		code,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Link{}, domain.ErrNotFound
	}

	return link, err
}

func (s *Storage) DeleteExpired(ctx context.Context, batchSize int) (int64, error) {
	result, err := s.pool.Exec(ctx, `
        WITH expired AS (
            SELECT code
            FROM urls
            WHERE expires_at <= statement_timestamp()
            ORDER BY expires_at
            LIMIT $1
            FOR UPDATE SKIP LOCKED
        )
        DELETE FROM urls
        USING expired
        WHERE urls.code = expired.code`, batchSize)
	if err != nil {
		return 0, err
	}

	return result.RowsAffected(), nil
}
