package domain

import (
	"errors"
	"time"
)

var (
	ErrInvalidInput  = errors.New("invalid input")
	ErrNotFound      = errors.New("link not found")
	ErrConflict      = errors.New("identifiers already belong to a different URL")
	ErrCodeCollision = errors.New("short code already exists")
)

type Link struct {
	Code        string
	ClientID    string
	ExternalID  string
	OriginalURL string
	CreatedAt   time.Time
	ExpiresAt   time.Time
}
