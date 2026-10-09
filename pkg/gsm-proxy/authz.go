package gsmproxy

import "errors"

// ErrForbidden is returned when the caller may not list a collection.
var ErrForbidden = errors.New("forbidden")

// Authorizer decides whether the caller may list a collection.
// Membership is Identity.Groups from the token. Ownership is pkg/group.Config,
// the rover groups and secret_collections in _config.yaml.
// A denial must wrap ErrForbidden.
type Authorizer interface {
	Authorize(id Identity, collection string) error
}
