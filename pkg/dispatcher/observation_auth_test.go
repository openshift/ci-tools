package dispatcher

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// observationReviewer records the narrow authentication request without a live API server.
type observationReviewer struct {
	response *authenticationv1.TokenReview
	err      error
	request  *authenticationv1.TokenReview
	bounded  bool
}

// Create captures the audience and checks that validation has a bounded deadline.
func (r *observationReviewer) Create(ctx context.Context, request *authenticationv1.TokenReview, _ metav1.CreateOptions) (*authenticationv1.TokenReview, error) {
	r.request = request
	deadline, ok := ctx.Deadline()
	r.bounded = ok && time.Until(deadline) > 0 && time.Until(deadline) <= 3*time.Second
	return r.response, r.err
}

func TestServiceAccountAuthorizer(t *testing.T) {
	for _, tc := range []struct {
		name          string
		headers       []string
		authenticated bool
		username      string
		audiences     []string
		reviewError   string
		unavailable   bool
		nilResponse   bool
		want          int
		message       string
		reviewed      bool
	}{
		{name: "missing credential", want: 401, message: "unauthorized"},
		{name: "basic authentication", headers: []string{"Basic secret"}, want: 401, message: "unauthorized"},
		{name: "empty bearer", headers: []string{"Bearer"}, want: 401, message: "unauthorized"},
		{name: "extra fields", headers: []string{"Bearer secret extra"}, want: 401, message: "unauthorized"},
		{name: "duplicate headers", headers: []string{"Bearer secret", "Bearer second"}, want: 401, message: "unauthorized"},
		{name: "invalid token", headers: []string{"Bearer secret"}, want: 401, message: "unauthorized", reviewed: true},
		{name: "nil response", headers: []string{"Bearer secret"}, nilResponse: true, want: 401, message: "unauthorized", reviewed: true},
		{name: "review rejected", headers: []string{"Bearer secret"}, authenticated: true, reviewError: "private details", want: 401, message: "unauthorized", reviewed: true},
		{name: "empty audiences", headers: []string{"Bearer secret"}, authenticated: true, username: "system:serviceaccount:ci:dispatcher-dashboard", want: 401, message: "unauthorized", reviewed: true},
		{name: "API audience", headers: []string{"Bearer secret"}, authenticated: true, audiences: []string{"https://kubernetes.default.svc"}, want: 401, message: "unauthorized", reviewed: true},
		{name: "other namespace", headers: []string{"Bearer secret"}, authenticated: true, username: "system:serviceaccount:other:dispatcher-dashboard", audiences: []string{ObservationAudience}, want: 403, message: "service account is not allowed", reviewed: true},
		{name: "other account", headers: []string{"Bearer secret"}, authenticated: true, username: "system:serviceaccount:ci:other", audiences: []string{ObservationAudience}, want: 403, message: "service account is not allowed", reviewed: true},
		{name: "anonymous identity", headers: []string{"Bearer secret"}, authenticated: true, username: "system:anonymous", audiences: []string{ObservationAudience}, want: 403, message: "service account is not allowed", reviewed: true},
		{name: "API unavailable", headers: []string{"Bearer secret"}, unavailable: true, want: 503, message: "credential validation unavailable", reviewed: true},
		{name: "allowed", headers: []string{"Bearer secret"}, authenticated: true, username: "system:serviceaccount:ci:dispatcher-dashboard", audiences: []string{ObservationAudience}, want: 200, reviewed: true},
		{name: "audience intersection", headers: []string{"bearer secret"}, authenticated: true, username: "system:serviceaccount:ci:dispatcher-dashboard", audiences: []string{"other", ObservationAudience}, want: 200, reviewed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reviewer := &observationReviewer{response: &authenticationv1.TokenReview{Status: authenticationv1.TokenReviewStatus{Authenticated: tc.authenticated, Audiences: tc.audiences, Error: tc.reviewError, User: authenticationv1.UserInfo{Username: tc.username}}}}
			if tc.nilResponse {
				reviewer.response = nil
			}
			if tc.unavailable {
				reviewer.err = errors.New("private credential details")
			}
			request := httptest.NewRequest(http.MethodGet, "/observe/v1/state", nil)
			for _, header := range tc.headers {
				request.Header.Add("Authorization", header)
			}
			status, err := NewServiceAccountAuthorizer(reviewer, "ci", "dispatcher-dashboard").Authorize(request)
			message := ""
			if err != nil {
				message = err.Error()
			}
			if status != tc.want || message != tc.message {
				t.Fatalf("got status=%d error=%q, want %d %q", status, message, tc.want, tc.message)
			}
			if (reviewer.request != nil) != tc.reviewed {
				t.Fatalf("unexpected review call: %v", reviewer.request != nil)
			}
			if tc.reviewed {
				want := authenticationv1.TokenReviewSpec{Token: "secret", Audiences: []string{ObservationAudience}}
				if diff := cmp.Diff(want, reviewer.request.Spec); diff != "" {
					t.Errorf("unexpected request (-want +got):\n%s", diff)
				}
				if !reviewer.bounded {
					t.Error("review has no bounded deadline")
				}
			}
		})
	}
}

func TestObservationMetricsAuthentication(t *testing.T) {
	for _, tc := range []struct {
		name, method, token string
		authorizer          ObservationAuthorizer
		want                int
	}{
		{name: "authorized", method: "GET", token: "Bearer read-token", authorizer: observationAuthorization{}, want: 200},
		{name: "missing", method: "GET", authorizer: observationAuthorization{}, want: 401},
		{name: "nil authorizer", method: "GET", want: 401},
		{name: "write rejected", method: "POST", token: "Bearer read-token", authorizer: observationAuthorization{}, want: 405},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, "/metrics", nil)
			request.Header.Set("Authorization", tc.token)
			response := httptest.NewRecorder()
			NewObservationMetricsHandler(tc.authorizer, http.NotFoundHandler()).ServeHTTP(response, request)
			want := tc.want
			if want == 200 {
				want = 404
			}
			if response.Code != want {
				t.Fatalf("got %d want %d", response.Code, want)
			}
			if strings.Contains(response.Body.String(), "read-token") {
				t.Error("credential leaked")
			}
		})
	}
}
