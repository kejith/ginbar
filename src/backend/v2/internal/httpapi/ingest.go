package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/kejith/ginbar/backend/v2/internal/ingest"
	"github.com/kejith/ginbar/backend/v2/internal/model"
)

const maxURLImportRequestBytes = 8 << 10

type createIngestionResponse struct {
	PostID int64 `json:"postId"`
	JobID  int64 `json:"jobId"`
}

type createURLImportRequest struct {
	URL    *string `json:"url"`
	Filter string  `json:"filter"`
}

func (s *Server) createPostUpload(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed")
		return
	}
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	filter, ok := parseIngestFilter(r.URL.Query().Get("filter"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_filter", "filter must be one of sfw, nsfp, nsfw, or secret")
		return
	}

	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_upload", "request must contain one multipart file field named file")
		return
	}
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "file" {
		writeError(w, http.StatusBadRequest, "invalid_upload", "request must contain one multipart file field named file")
		return
	}

	created, err := s.ingest.CreateUpload(r.Context(), ingest.Actor{UserID: principal.UserID}, filter, ingest.Upload{
		Body:         part,
		OriginalName: part.FileName(),
		DeclaredMIME: part.Header.Get("Content-Type"),
	})
	if err != nil {
		writeIngestError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, createIngestionResponse{PostID: created.PostID, JobID: created.JobID})
}

func (s *Server) createPostFromURL(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed")
		return
	}
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}

	var request createURLImportRequest
	if !decodeURLImportJSON(w, r, &request) {
		return
	}
	filter, ok := parseIngestFilter(request.Filter)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_filter", "filter must be one of sfw, nsfp, nsfw, or secret")
		return
	}
	if request.URL == nil || *request.URL == "" {
		writeError(w, http.StatusBadRequest, "missing_url", "url is required")
		return
	}
	if !validAbsoluteURLSyntax(*request.URL) {
		writeError(w, http.StatusBadRequest, "invalid_url", "url must be an absolute URL")
		return
	}

	created, err := s.ingest.CreateFromURL(r.Context(), ingest.Actor{UserID: principal.UserID}, filter, *request.URL)
	if err != nil {
		writeIngestError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, createIngestionResponse{PostID: created.PostID, JobID: created.JobID})
}

func parseIngestFilter(raw string) (model.ContentFilter, bool) {
	switch raw {
	case "sfw":
		return model.FilterSFW, true
	case "nsfp":
		return model.FilterNSFP, true
	case "nsfw":
		return model.FilterNSFW, true
	case "secret":
		return model.FilterSecret, true
	default:
		return 0, false
	}
}

func validAbsoluteURLSyntax(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.IsAbs() && parsed.Host != ""
}

func decodeURLImportJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxURLImportRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid request body")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid request body")
		return false
	}
	return true
}

func writeIngestError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ingest.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_request", "ingestion input is invalid")
	case errors.Is(err, ingest.ErrEmptySource):
		writeError(w, http.StatusBadRequest, "empty_source", "media source is empty")
	case errors.Is(err, ingest.ErrSourceTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "source_too_large", "media source exceeds the configured size limit")
	case errors.Is(err, ingest.ErrUnsafeURL):
		writeError(w, http.StatusBadRequest, "unsafe_url", "media source URL is not allowed")
	case errors.Is(err, ingest.ErrCommitOutcomeUnknown):
		writeError(w, http.StatusInternalServerError, "ingestion_outcome_unknown", "ingestion outcome is unknown; do not retry automatically")
	default:
		writeServiceError(w, r, err)
	}
}
