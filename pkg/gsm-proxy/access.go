package gsmproxy

import (
	"context"
	"errors"
	"net/http"

	"github.com/sirupsen/logrus"

	gsmvalidation "github.com/openshift/ci-tools/pkg/gsm-validation"
)

// RequireCollectionAccess protects a collection endpoint by validating the collection
// name, verifying the caller's bearer token, and checking access to that collection.
// If any check fails, it writes an error response without calling next. Otherwise,
// it passes the request to next with the verified identity in the request context.
// Register the returned handler on a route with a {collection} path parameter.
func RequireCollectionAccess(verifier TokenVerifier, authorizer Authorizer, logger *logrus.Entry, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		collection := r.PathValue("collection")
		if !gsmvalidation.ValidateCollectionName(collection) {
			writeError(w, http.StatusBadRequest, MessageBadCollection)
			return
		}

		token, err := BearerToken(r.Header.Get("Authorization"))
		if err != nil {
			logger.WithError(err).Debug("rejected a request without a usable bearer token")
			writeError(w, http.StatusUnauthorized, MessageUnauthenticated)
			return
		}

		identity, err := verifier.Verify(r.Context(), token)
		if err != nil {
			logger.WithError(err).Debug("rejected a token")
			writeError(w, http.StatusUnauthorized, MessageUnauthenticated)
			return
		}

		log := logger.WithFields(logrus.Fields{"kerberos": identity.Username, "collection": collection})
		if err := authorizer.Authorize(identity, collection); err != nil {
			if errors.Is(err, ErrForbidden) {
				log.WithError(err).Info("denied collection access")
				writeError(w, http.StatusForbidden, MessageForbidden)
				return
			}
			log.WithError(err).Error("authorization failed")
			writeError(w, http.StatusInternalServerError, MessageInternal)
			return
		}

		ctx := context.WithValue(r.Context(), identityContextKey{}, identity)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
