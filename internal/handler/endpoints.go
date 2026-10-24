package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/vladlim/url-shortener/internal/domain"
	"github.com/vladlim/url-shortener/internal/services/shortener"
)

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		h.writeError(w, r, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request createRequest
	if err := decoder.Decode(&request); err != nil {
		h.invalidJSON(w, r, err)
		return
	}

	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		h.invalidJSON(w, r, err)
		return
	}

	result, err := h.service.Create(r.Context(), shortener.CreateInput{
		ClientID:   request.ClientID,
		ExternalID: request.ExternalID,
		URL:        request.URL,
	})
	if err != nil {
		h.serviceError(w, r, err)
		return
	}

	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	// The resource is the short link itself; GET follows it to the original URL.
	w.Header().Set("Location", result.ShortURL)
	h.writeJSON(w, status, linkResponse{
		Code:        result.Link.Code,
		ShortURL:    result.ShortURL,
		OriginalURL: result.Link.OriginalURL,
		CreatedAt:   result.Link.CreatedAt,
		ExpiresAt:   result.Link.ExpiresAt,
	})
}

func (h *Handler) redirect(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	link, err := h.service.Resolve(r.Context(), chi.URLParam(r, "code"))
	if err != nil {
		h.serviceError(w, r, err)
		return
	}

	http.Redirect(w, r, link.OriginalURL, http.StatusFound)
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	h.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := h.ping(ctx); err != nil {
		h.logger.Error("database readiness failed",
			"error", err,
			"request_id", middleware.GetReqID(r.Context()),
		)
		h.writeError(w, r, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (h *Handler) invalidJSON(w http.ResponseWriter, r *http.Request, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		h.writeError(w, r, http.StatusRequestEntityTooLarge, "request body exceeds 16 KiB")
		return
	}

	h.writeError(w, r, http.StatusBadRequest, "body must contain exactly one JSON object with client_id, external_id and url")
}

func (h *Handler) serviceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		h.writeError(w, r, http.StatusGatewayTimeout, "request timed out")
	case errors.Is(err, domain.ErrInvalidInput):
		h.writeError(w, r, http.StatusBadRequest, err.Error())
	case errors.Is(err, domain.ErrConflict):
		h.writeError(w, r, http.StatusConflict, domain.ErrConflict.Error())
	case errors.Is(err, domain.ErrNotFound):
		h.writeError(w, r, http.StatusNotFound, "link not found or expired")
	default:
		h.logger.Error("request failed",
			"error", err,
			"request_id", middleware.GetReqID(r.Context()),
		)
		h.writeError(w, r, http.StatusInternalServerError, "internal server error")
	}
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, status int, message string) {
	h.writeJSON(w, status, errorResponse{
		Error:     message,
		RequestID: middleware.GetReqID(r.Context()),
	})
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		h.logger.Error("write response", "error", err)
	}
}
