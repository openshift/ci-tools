package gsmproxy

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/sirupsen/logrus"

	gsmvalidation "github.com/openshift/ci-tools/pkg/gsm-validation"
)

const ListSecretsPattern = "GET /api/v1/collections/{collection}/secrets"

type ListSecretsResponse struct {
	Secrets []string `json:"secrets"`
}

// ErrorResponse carries a fixed message. Upstream errors are logged, never written back.
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
// Verifier, Authorizer, and Lister are the three steps still to be filled in.
type ListSecretsHandler struct {
	Verifier   TokenVerifier
	Authorizer Authorizer
	Lister     SecretLister
	Log        *logrus.Entry
}

// NewServeMux registers healthz, and the list route when h is non-nil.
// The list route stays off until the three dependencies exist.
func NewServeMux(handler *ListSecretsHandler) *http.ServeMux {
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
	if !gsmvalidation.ValidateCollectionName(collection) {
		writeError(w, http.StatusBadRequest, MessageBadCollection)
		return
	}

	token, err := BearerToken(r.Header.Get("Authorization"))
	if err != nil {
		h.Log.WithError(err).Debug("rejected a request without a usable bearer token")
		writeError(w, http.StatusUnauthorized, MessageUnauthenticated)
		return
	}

	identity, err := h.Verifier.Verify(r.Context(), token)
	if err != nil {
		h.Log.WithError(err).Debug("rejected a token")
		writeError(w, http.StatusUnauthorized, MessageUnauthenticated)
		return
	}

	log := h.Log.WithFields(logrus.Fields{"kerberos": identity.Username, "collection": collection})
	if err := h.Authorizer.Authorize(identity, collection); err != nil {
		if errors.Is(err, ErrForbidden) {
			log.WithError(err).Info("denied collection access")
			writeError(w, http.StatusForbidden, MessageForbidden)
			return
		}
		log.WithError(err).Error("authorization failed")
		writeError(w, http.StatusInternalServerError, MessageInternal)
		return
	}

	names, err := h.Lister.List(r.Context(), collection)
	if err != nil {
		log.WithError(err).Error("failed to list secrets")
		writeError(w, http.StatusInternalServerError, MessageInternal)
		return
	}
	if names == nil {
		names = []string{}
	}
	log.Infof("listed secrets in collection %q", collection)
	writeJSON(w, http.StatusOK, ListSecretsResponse{Secrets: names})
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, ErrorResponse{Error: message})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		logrus.WithError(err).Error("failed to write response")
	}
}
