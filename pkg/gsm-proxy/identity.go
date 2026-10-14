package gsmproxy

import (
	"context"
	"errors"
	"strings"
)

// ErrUnauthenticated is returned when a bearer token is missing or rejected.
// The collection-access handler maps every such error to one fixed 401 message.
var ErrUnauthenticated = errors.New("unauthenticated")

// Identity is the caller named by a Dex ID token.
// Username is preferred_username, the kerberos id. Groups are the rover groups
// carried in the token.
type Identity struct {
	Username string
	Groups   []string
}

type identityContextKey struct{}

// IdentityFromContext returns the verified caller set by RequireCollectionAccess.
func IdentityFromContext(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(identityContextKey{}).(Identity)
	return identity, ok
}

// TokenVerifier checks a bearer token and returns the caller it identifies.
// A rejection must wrap ErrUnauthenticated.
// Checks:
//   - Dex's signature,
//   - the issuer is https://idp.ci.openshift.org,
//   - the audience is the gsm-cli client id,
//   - the token is not expired.
type TokenVerifier interface {
	Verify(ctx context.Context, rawToken string) (Identity, error)
}

// BearerToken reads the credential from an Authorization header value.
// The token is only accepted from this header, so it cannot land in an access log via a query parameter.
func BearerToken(header string) (string, error) {
	scheme, token, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", errors.New("Authorization header is not a bearer token")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return "", errors.New("bearer token is empty")
	}
	return token, nil
}
