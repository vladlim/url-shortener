package handler

import "time"

type createRequest struct {
	ClientID   string `json:"client_id"`
	ExternalID string `json:"external_id"`
	URL        string `json:"url"`
}

type linkResponse struct {
	Code        string    `json:"code"`
	ShortURL    string    `json:"short_url"`
	OriginalURL string    `json:"original_url"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type errorResponse struct {
	Error     string `json:"error"`
	RequestID string `json:"request_id"`
}
