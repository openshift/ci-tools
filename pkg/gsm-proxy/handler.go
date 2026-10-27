package gsmproxy

import (
	"encoding/json"
	"net/http"

	"github.com/sirupsen/logrus"
)

const ListSecretsPattern = "GET /api/v1/collections/{collection}/secrets"

type ListSecretsResponse struct {
	Secrets []string `json:"secrets"`
}

// ErrorResponse carries a fixed message without exposing upstream error details.
type ErrorResponse struct {
	Error string `json:"error"`
}

const (
	MessageUnauthenticated = "a valid identity token is required"
	MessageForbidden       = "you are not a member of a rover group that owns this secret collection"
	MessageBadCollection   = "the secret collection name is malformed"
	MessageInternal        = "the request could not be completed"
)

// ListSecretsHandler serves the secret names of one collection.
// It must be registered behind RequireCollectionAccess.
type ListSecretsHandler struct {
	Lister SecretLister
	Log    *logrus.Entry
}

// NewServeMux registers healthz and, when supplied, the protected list handler.
func NewServeMux(handler http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	if handler != nil {
		mux.Handle(ListSecretsPattern, handler)
	}
	return mux
}

func (h *ListSecretsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	collection := r.PathValue("collection")
	names, err := h.Lister.List(r.Context(), collection)
	if err != nil {
		h.Log.Error("failed to list secrets")
		writeError(w, http.StatusInternalServerError, MessageInternal)
		return
	}
	if names == nil {
		names = []string{}
	}
	writeJSON(w, http.StatusOK, ListSecretsResponse{Secrets: names})
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, ErrorResponse{Error: message})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		logrus.Error("failed to write response")
	}
}
