package gsmproxy

import (
	"errors"
	"fmt"
	"slices"
	"sync/atomic"

	"github.com/openshift/ci-tools/pkg/group"
)

// ErrForbidden is returned when the caller may not access a collection.
var ErrForbidden = errors.New("forbidden")

// Authorizer checks whether an authenticated caller may access a collection.
// The caller's Rover groups (Identity.Groups) come from a Dex ID token already validated by TokenVerifier.
// Collection ownership comes from sync-rover-groups/_config.yaml in release repository.
// Access denials must return an error wrapping ErrForbidden.
type Authorizer interface {
	Authorize(id Identity, collection string) error
}

// CollectionAuthorizer uses an immutable configuration snapshot loaded by group.LoadConfig.
// Configuration replacements are atomic, so each authorization uses one complete snapshot.
type CollectionAuthorizer struct {
	config atomic.Pointer[group.Config]
}

var _ Authorizer = (*CollectionAuthorizer)(nil)

// NewCollectionAuthorizer returns an authorizer deciding access from the given
// ownership configuration.
func NewCollectionAuthorizer(config *group.Config) *CollectionAuthorizer {
	authorizer := &CollectionAuthorizer{}
	authorizer.config.Store(config)
	return authorizer
}

// Authorize allows the collection when at least one of the caller's Rover groups
// is a claimed owner of it in the configuration. Groups marked unclaimed never grant
// access, and without a configuration every request is denied, so access fails closed.
// Every denial wraps ErrForbidden.
func (a *CollectionAuthorizer) Authorize(id Identity, collection string) error {
	if config := a.config.Load(); config != nil {
		for _, name := range id.Groups {
			owner, found := config.Groups[name]
			if !found || owner.Unclaimed {
				continue
			}
			if slices.Contains(owner.SecretCollections, collection) {
				return nil
			}
		}
	}
	return fmt.Errorf("collection access denied: %w", ErrForbidden)
}
