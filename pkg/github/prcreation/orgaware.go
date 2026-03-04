package prcreation

import (
	"strings"

	"sigs.k8s.io/prow/pkg/github"
)

// OrgAwareClient wraps a github.Client so that FindIssues routes through
// FindIssuesWithOrg. Prow's App auth round-tripper requires the org in
// the request context to resolve the installation token, but
// bumper.UpdatePullRequestWithLabels internally calls FindIssues which
// passes an empty org.
//
// When IsAppAuth is true, BotUser() appends "[bot]" to the login so that
// GitHub's search API author: qualifier matches the App's acting identity.
type OrgAwareClient struct {
	github.Client
	Org       string
	IsAppAuth bool
}

func (c *OrgAwareClient) FindIssues(query, sort string, asc bool) ([]github.Issue, error) {
	return c.Client.FindIssuesWithOrg(c.Org, query, sort, asc)
}

// BotUser returns the bot user data. When the client is using GitHub App auth,
// it appends the "[bot]" suffix to the login. GitHub Apps act as "slug[bot]"
// users, but prow's getUserData only stores the bare slug. The search API's
// author: qualifier requires the full "slug[bot]" form to match PRs created
// by the App; using the bare slug results in a 422 because that user does not
// exist on GitHub.
func (c *OrgAwareClient) BotUser() (*github.UserData, error) {
	user, err := c.Client.BotUser()
	if err != nil {
		return nil, err
	}
	if !c.IsAppAuth || strings.HasSuffix(user.Login, "[bot]") {
		return user, nil
	}
	return &github.UserData{
		Name:  user.Name,
		Login: user.Login + "[bot]",
		Email: user.Email,
	}, nil
}
