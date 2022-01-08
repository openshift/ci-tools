package main

import (
	"fmt"
	"strings"

	ldapv3 "github.com/go-ldap/ldap/v3"

	"k8s.io/apimachinery/pkg/util/sets"
)

type ldapGroupResolver struct {
	conn ldapConn
}

type ldapConn interface {
	Search(searchRequest *ldapv3.SearchRequest) (*ldapv3.SearchResult, error)
}

const (
	baseDN = "dc=redhat,dc=com"
)

type notFoundErr struct {
	group string
}

// IsNotFoundError returns true if the error indicates the provided
// object is not found.
func IsNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	_, ok := err.(*notFoundErr)
	return ok
}

func (e *notFoundErr) Error() string {
	return fmt.Sprintf("failed to find group on the ldap server: %s", e.group)
}

func (r *ldapGroupResolver) resolve(name string) (*Group, error) {
	if r.conn == nil {
		return nil, fmt.Errorf("ldapGroupResolver's connection is nil")
	}

	filter := fmt.Sprintf("(&(objectClass=rhatRoverGroup)(cn=%s))", ldapv3.EscapeFilter(name))
	searchReq := ldapv3.NewSearchRequest(baseDN, ldapv3.ScopeWholeSubtree, 0, 0, 0, false, filter, []string{"uniqueMember"}, []ldapv3.Control{})

	result, err := r.conn.Search(searchReq)
	if err != nil {
		return nil, fmt.Errorf("failed to search ldap: %w", err)
	}

	switch l := len(result.Entries); {
	case l == 0:
		return nil, &notFoundErr{group: name}
	case l > 1:
		// this should never happen
		return nil, fmt.Errorf("found %d group with the name: %s", l, name)
	}

	members := sets.NewString()
	entry := result.Entries[0]
	for _, attribute := range entry.Attributes {
		for _, value := range attribute.Values {
			// the value starts with uid=<uid>,ou=users
			i := strings.Index(value, ",")
			if i == -1 {
				return nil, fmt.Errorf("the value does not contain ',': %s", value)
			}
			uidPart := value[:i]
			if !strings.HasPrefix(uidPart, "uid=") {
				return nil, fmt.Errorf("the value does not start with 'uid=': %s", value)
			}
			members.Insert(strings.TrimPrefix(uidPart, "uid="))
		}
	}
	return &Group{
		Name:    name,
		Members: members.List(),
	}, nil
}
