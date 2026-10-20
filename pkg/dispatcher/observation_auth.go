package dispatcher

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ObservationAudience restricts projected credentials to the dispatcher observation API.
const ObservationAudience = "prowjob-dispatcher-observation"

// TokenReviewer exposes only the API operation needed to validate bound credentials.
type TokenReviewer interface {
	Create(context.Context, *authenticationv1.TokenReview, metav1.CreateOptions) (*authenticationv1.TokenReview, error)
}

// ObservationAuthorizer checks a request without trusting network location or token claims locally.
type ObservationAuthorizer interface {
	Authorize(*http.Request) (int, error)
}

// ServiceAccountAuthorizer accepts only the configured service account and observation audience.
type ServiceAccountAuthorizer struct {
	reviewer TokenReviewer
	username string
}

// NewServiceAccountAuthorizer restricts access to one namespace/name identity.
func NewServiceAccountAuthorizer(reviewer TokenReviewer, namespace, name string) *ServiceAccountAuthorizer {
	return &ServiceAccountAuthorizer{reviewer: reviewer, username: "system:serviceaccount:" + namespace + ":" + name}
}

// Authorize delegates credential validity, including bound-token expiry and revocation, to Kubernetes.
func (a *ServiceAccountAuthorizer) Authorize(request *http.Request) (int, error) {
	headers := request.Header.Values("Authorization")
	if len(headers) != 1 {
		return http.StatusUnauthorized, errors.New("unauthorized")
	}
	fields := strings.Fields(headers[0])
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
		return http.StatusUnauthorized, errors.New("unauthorized")
	}
	if a.reviewer == nil {
		return http.StatusServiceUnavailable, errors.New("credential validation unavailable")
	}
	ctx, cancel := context.WithTimeout(request.Context(), 3*time.Second)
	defer cancel()
	review, err := a.reviewer.Create(ctx, &authenticationv1.TokenReview{Spec: authenticationv1.TokenReviewSpec{Token: fields[1], Audiences: []string{ObservationAudience}}}, metav1.CreateOptions{})
	if err != nil {
		return http.StatusServiceUnavailable, errors.New("credential validation unavailable")
	}
	if review == nil || !review.Status.Authenticated || review.Status.Error != "" {
		return http.StatusUnauthorized, errors.New("unauthorized")
	}
	audienceAccepted := false
	for _, audience := range review.Status.Audiences {
		if audience == ObservationAudience {
			audienceAccepted = true
			break
		}
	}
	if !audienceAccepted {
		return http.StatusUnauthorized, errors.New("unauthorized")
	}
	if review.Status.User.Username != a.username {
		return http.StatusForbidden, errors.New("service account is not allowed")
	}
	return http.StatusOK, nil
}

// authorizeObservation consistently protects API responses and metrics, including unknown paths.
func authorizeObservation(writer http.ResponseWriter, request *http.Request, authorizer ObservationAuthorizer) bool {
	writer.Header().Set("Cache-Control", "no-store")
	if authorizer == nil {
		writeControlError(writer, http.StatusUnauthorized, errors.New("unauthorized"))
		return false
	}
	if status, err := authorizer.Authorize(request); err != nil {
		writeControlError(writer, status, err)
		return false
	}
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeControlError(writer, http.StatusMethodNotAllowed, errors.New("only GET is supported"))
		return false
	}
	return true
}

// observationMetricsHandler applies the same authentication and method policy to metrics.
type observationMetricsHandler struct {
	authorizer ObservationAuthorizer
	metrics    http.Handler
}

// NewObservationMetricsHandler exposes existing metrics only to the observation identity.
func NewObservationMetricsHandler(authorizer ObservationAuthorizer, metrics http.Handler) http.Handler {
	return &observationMetricsHandler{authorizer: authorizer, metrics: metrics}
}

// ServeHTTP authenticates before allowing the metrics handler to inspect a request.
func (h *observationMetricsHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if authorizeObservation(writer, request, h.authorizer) {
		h.metrics.ServeHTTP(writer, request)
	}
}
