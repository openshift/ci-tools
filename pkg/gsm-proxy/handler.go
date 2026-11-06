package gsmproxy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	ListSecretsPattern    = "GET /api/v1/collections/{collection}/secrets"
	ListSecretsAction     = "list-secrets"
	DescribeSecretPattern = "GET /api/v1/collections/{collection}/secrets/{path...}"
	DescribeSecretAction  = "describe-secret"
)

// Route is a collection endpoint: a ServeMux pattern containing a {collection} path
// parameter, the action name used in audit logs, and the handler serving it.
type Route struct {
	Pattern string
	Action  string
	Handler http.Handler
}

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
	MessageBadSecretPath   = "the secret path must be a valid group/field path"
	MessageSecretNotFound  = "the secret does not exist"
)

// ListSecretsHandler serves the secret names of one collection.
// It must be registered as a Route through NewServeMux.
type ListSecretsHandler struct {
	List func(context.Context, string) ([]string, error)
	Log  *logrus.Entry
}

// DescribeSecretHandler serves the metadata of one secret.
// It must be registered as a Route through NewServeMux.
type DescribeSecretHandler struct {
	Describe func(context.Context, string, string) (SecretDescription, error)
	Log      *logrus.Entry
}

// NewServeMux returns an HTTP handler that passes each request to the matching route's handler.
func NewServeMux(verifier TokenVerifier, authorizer Authorizer, logger *logrus.Entry, routes ...Route) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	for _, route := range routes {
		mux.Handle(route.Pattern, Audit(route.Action, logger, RequireCollectionAccess(verifier, authorizer, logger, route.Handler)))
	}
	return mux
}

// ServeHTTP writes the secret paths for the collection identified by the request.
func (h *ListSecretsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	collection := r.PathValue("collection")
	names, err := h.List(r.Context(), collection)
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

// ServeHTTP writes the metadata for the secret identified by the request's collection and path.
func (h *DescribeSecretHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	description, err := h.Describe(r.Context(), r.PathValue("collection"), r.PathValue("path"))
	if err != nil {
		writeSecretError(w, err, h.Log)
		return
	}
	writeJSON(w, http.StatusOK, description)
}

// writeSecretError writes the fixed error response for an error returned by a secret operation:
// 400 for ErrInvalidSecretPath, 404 for a Google NotFound status, and 500 otherwise.
func writeSecretError(w http.ResponseWriter, err error, log *logrus.Entry) {
	if errors.Is(err, ErrInvalidSecretPath) {
		writeError(w, http.StatusBadRequest, MessageBadSecretPath)
		return
	}
	if status.Code(err) == codes.NotFound {
		writeError(w, http.StatusNotFound, MessageSecretNotFound)
		return
	}
	log.WithField("grpc_code", status.Code(err).String()).Error("failed to complete secret operation")
	writeError(w, http.StatusInternalServerError, MessageInternal)
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
