package main

import (
	"fmt"
	"strings"

	ldapv3 "github.com/go-ldap/ldap/v3"
	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
)

type ldapGroupResolver struct {
	conn ldapConn
}

type ldapConn interface {
	Search(searchRequest *ldapv3.SearchRequest) (*ldapv3.SearchResult, error)
}

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
	searchReq := ldapv3.NewSearchRequest("dc=redhat,dc=com", ldapv3.ScopeWholeSubtree, 0, 0, 0, false, filter, []string{"uniqueMember"}, []ldapv3.Control{})

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

func (r *ldapGroupResolver) getGitHubUserKerberosIDMapping() (map[string]string, error) {
	if r.conn == nil {
		return nil, fmt.Errorf("ldapGroupResolver's connection is nil")
	}

	searchReq := ldapv3.NewSearchRequest("ou=users,dc=redhat,dc=com", ldapv3.ScopeWholeSubtree, 0, 0, 0,
		false, "(rhatSocialURL=GitHub*)", []string{"uid", "rhatSocialURL"}, []ldapv3.Control{})

	result, err := r.conn.Search(searchReq)
	if err != nil {
		return nil, fmt.Errorf("failed to search ldap: %w", err)
	}

	if len(result.Entries) == 0 {
		logrus.Warn("found no users setting up github URL")
		return nil, nil
	}

	ret := map[string]string{}

	for _, entry := range result.Entries {
		kerberosID := entry.GetAttributeValue("uid")
		if kerberosID == "" {
			//should never happen
			return nil, fmt.Errorf("empty uid from LDAP server")
		}
		var gitHubID string
		for _, value := range entry.GetAttributeValues("rhatSocialURL") {
			if parsed := getGitHubID(value); parsed != "" {
				gitHubID = parsed
			}
		}
		if gitHubID == "" {
			logrus.WithField("kerberosID", kerberosID).WithField("entry", entry).Warn("failed to parse GitHub ID")
			continue
		}
		ret[gitHubID] = kerberosID
	}

	return ret, nil
}

// getGitHubID returns the GitHub ID from the value
// which are the values of "rhatSocialURL" fields set up by Red Hatters at Rover.
// A user can have multiple "rhatSocialURL"s for GitHub, Twitter, LinkedIn etc, even multiple for GitHub.
// Example of such values: "Github->https://github.com/tom", "Github->https://github.com/tom/", or "Twitter->https://twitter.com/tom"
func getGitHubID(value string) string {
	if small := strings.ToLower(value); !strings.Contains(small, "github.com/") && !strings.Contains(small, "git.io/") {
		return ""
	}
	if strings.HasPrefix(value, "Github->") {
		slashSplit := strings.Split(value, "/")
		if ret := slashSplit[len(slashSplit)-1]; ret != "" {
			return ret
		} else if ret = slashSplit[len(slashSplit)-2]; ret != "" {
			return ret
		}
	}
	return ""
}
