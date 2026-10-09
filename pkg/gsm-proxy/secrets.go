package gsmproxy

import "context"

// SecretLister returns the group/field paths of one collection.
// The result is names only. A GSM Secret must not cross this interface:
// its annotations hold free text typed by users.
type SecretLister interface {
	List(ctx context.Context, collection string) ([]string, error)
}
