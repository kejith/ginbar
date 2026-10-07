package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/kejith/ginbar/backend/v2/internal/privatemessage"
)

const maxPrivateMessageRequestBytes = privatemessage.MaxBodyCharacters*4 + 1024

type sendPrivateMessageRequest struct {
	Body *string `json:"body"`
}

func (s *Server) listPrivateMessageInbox(w http.ResponseWriter, r *http.Request) {
	before, err := optionalPositiveInt64(r.URL.Query().Get("before"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_cursor", err.Error())
		return
	}
	limit, err := optionalPositiveInt(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_limit", err.Error())
		return
	}
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}

	page, err := s.privateMessages.Inbox(r.Context(), privatemessage.InboxQuery{
		ActorUserID: principal.UserID,
		Before:      before,
		Limit:       limit,
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) listPrivateMessages(w http.ResponseWriter, r *http.Request) {
	peerUserID, ok := parseMessagePeerID(w, r)
	if !ok {
		return
	}
	before, err := optionalPositiveInt64(r.URL.Query().Get("before"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_cursor", err.Error())
		return
	}
	limit, err := optionalPositiveInt(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_limit", err.Error())
		return
	}
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}

	page, err := s.privateMessages.List(r.Context(), privatemessage.Query{
		ActorUserID: principal.UserID,
		PeerUserID:  peerUserID,
		Before:      before,
		Limit:       limit,
	})
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, page)
	case errors.Is(err, privatemessage.ErrRecipientUnavailable):
		writeError(w, http.StatusNotFound, "recipient_unavailable", "recipient is unavailable")
	case errors.Is(err, privatemessage.ErrSelfMessage):
		writeError(w, http.StatusConflict, "self_message_forbidden", "messages to self are not allowed")
	default:
		writeServiceError(w, r, err)
	}
}

func (s *Server) sendPrivateMessage(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed")
		return
	}
	peerUserID, ok := parseMessagePeerID(w, r)
	if !ok {
		return
	}
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}

	var request sendPrivateMessageRequest
	if !decodePrivateMessageJSON(w, r, &request) {
		return
	}
	if request.Body == nil {
		writeError(w, http.StatusBadRequest, "invalid_message_body", "message body must contain 1 to 10000 characters")
		return
	}

	created, err := s.privateMessages.Send(r.Context(), privatemessage.SendRequest{
		SenderUserID:    principal.UserID,
		RecipientUserID: peerUserID,
		Body:            *request.Body,
	})
	switch {
	case err == nil:
		writeJSON(w, http.StatusCreated, created)
	case errors.Is(err, privatemessage.ErrInvalidBody):
		writeError(w, http.StatusBadRequest, "invalid_message_body", "message body must contain 1 to 10000 characters")
	case errors.Is(err, privatemessage.ErrRecipientUnavailable):
		writeError(w, http.StatusNotFound, "recipient_unavailable", "recipient is unavailable")
	case errors.Is(err, privatemessage.ErrSelfMessage):
		writeError(w, http.StatusConflict, "self_message_forbidden", "messages to self are not allowed")
	default:
		writeServiceError(w, r, err)
	}
}

func parseMessagePeerID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	peerUserID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || peerUserID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_peer_id", "peer id must be a positive integer")
		return 0, false
	}
	return peerUserID, true
}

func decodePrivateMessageJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxPrivateMessageRequestBytes)
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
