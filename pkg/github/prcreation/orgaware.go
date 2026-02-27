package prcreation

import "sigs.k8s.io/prow/pkg/github"

// OrgAwareClient wraps a github.Client so that FindIssues routes through
// FindIssuesWithOrg. Prow's App auth round-tripper requires the org in
// the request context to resolve the installation token, but
// bumper.UpdatePullRequestWithLabels internally calls FindIssues which
// passes an empty org.
type OrgAwareClient struct {
	github.Client
	Org string
}

func (c *OrgAwareClient) FindIssues(query, sort string, asc bool) ([]github.Issue, error) {
	return c.Client.FindIssuesWithOrg(c.Org, query, sort, asc)
}
