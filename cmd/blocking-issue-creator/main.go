package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	githubql "github.com/shurcooL/githubv4"
	"github.com/sirupsen/logrus"
	"golang.org/x/oauth2"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/promotion"
)

type options struct {
	promotion.FutureOptions
	username  string
	tokenPath string
}

func (o *options) Validate() error {
	if err := o.FutureOptions.Validate(); err != nil {
		return err
	}
	if o.username == "" {
		return errors.New("--username is required")
	}
	if o.tokenPath == "" {
		return errors.New("--token-path is required")
	}
	return nil
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.username, "username", "", "Username to use when communicating with GitHub.")
	fs.StringVar(&o.tokenPath, "token-path", "", "Path to token to use when communicating with GitHub.")
	o.Bind(fs)
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse input")
	}
	return o
}

func main() {
	o := gatherOptions()
	if err := o.Validate(); err != nil {
		logrus.Fatalf("Invalid options: %v", err)
	}

	rawToken, err := ioutil.ReadFile(o.tokenPath)
	if err != nil {
		logrus.WithError(err).Fatal("Could not read token.")
	}
	client := githubql.NewClient(oauth2.NewClient(context.Background(), oauth2.StaticTokenSource(&oauth2.Token{AccessToken: strings.TrimSpace(string(rawToken))})))

	failed := false
	if err := o.OperateOnCIOperatorConfigDir(o.ConfigDir, func(configuration *api.ReleaseBuildConfiguration, repoInfo *config.Info) error {
		logger := config.LoggerForInfo(*repoInfo)

		branches := sets.NewString()
		for _, futureRelease := range o.FutureReleases.Strings() {
			futureBranch, err := promotion.DetermineReleaseBranch(o.CurrentRelease, futureRelease, repoInfo.Branch)
			if err != nil {
				logger.WithError(err).Error("Failed to determine release branch.")
				failed = true
				return nil
			}
			if futureBranch == repoInfo.Branch {
				logger.Debugf("Skipping branch %s as it is the current development branch.", futureBranch)
				continue
			}

			branches.Insert(futureBranch)
		}

		if len(branches) == 0 {
			return nil
		}

		var branchTokens []string
		body := fmt.Sprintf("The following branches are being fast-forwarded from the current development branch (%s) as placeholders for future releases. No merging is allowed into these release branches until they are unfrozen for production release.\n\n", repoInfo.Branch)
		for _, branch := range branches.List() {
			body += fmt.Sprintf(" - `%s`\n", branch)
			branchTokens = append(branchTokens, fmt.Sprintf("branch:%s", branch))
		}
		body += "\nContact the [Test Platform](https://coreos.slack.com/messages/CBN38N3MW) or [Automated Release](https://coreos.slack.com/messages/CB95J6R4N) teams for more information."
		title := fmt.Sprintf("Future Release Branches Frozen For Merging | %s", strings.Join(branchTokens, " "))

		// check to see if there's a blocker issue we already created so we can just edit it
		var blockerQuery struct {
			Search struct {
				Nodes []struct {
					Issue Issue `graphql:"... on Issue"`
				}
			} `graphql:"search(type: ISSUE, first: 10, query: $query)"`
		}
		vars := map[string]interface{}{
			"query": githubql.String(fmt.Sprintf("is:issue state:open label:\"tide/merge-blocker\" repo:%s/%s author:%s", repoInfo.Org, repoInfo.Repo, o.username)),
		}
		logger.WithField("query", vars["query"]).Debug("Issuing query.")
		if err := client.Query(context.Background(), &blockerQuery, vars); err != nil {
			logger.WithError(err).Error("Failed to search for open issues.")
			failed = true
		}
		var issues []Issue
		var numbers []int
		for _, node := range blockerQuery.Search.Nodes {
			issues = append(issues, node.Issue)
			numbers = append(numbers, int(node.Issue.Number))
		}
		if len(numbers) > 1 {
			logger.Warnf("Found more than one merge blocking issue by the bot: %v", numbers)
			for _, issue := range issues[1:] {
				// we need to close this extra issue
				var closeIssue struct {
					CloseIssue struct {
						Issue struct {
							Number githubql.Int
						}
					} `graphql:"closeIssue(input: $input)"`
				}
				// this needs to be a named type for the library
				type CloseIssueInput struct {
					IssueID githubql.ID `json:"issueId"`
				}
				input := CloseIssueInput{
					IssueID: issue.ID,
				}
				if !o.Confirm {
					logger.Infof("Would close issue %d.", issue.Number)
					return nil
				}
				if err := client.Mutate(context.Background(), &closeIssue, input, nil); err != nil {
					logger.WithError(err).Error("Failed to close issue.")
					failed = true
					return nil
				}
				logger.Infof("Closed extra issue %d.", issue.Number)
			}
		}

		// we have an existing issue that needs to be up to date
		if len(issues) != 0 && issues[0].ID != nil {
			logger = logger.WithField("merge-blocker", numbers[0])
			existing := issues[0]
			needsUpdate := string(existing.Title) != title || string(existing.Body) != body

			if !needsUpdate {
				logger.Info("Current merge-blocker issue is up to date, no update necessary.")
				return nil
			}

			// we need to update the issue
			var updateIssue struct {
				UpdateIssue struct {
					Issue struct {
						Number githubql.Int
					}
				} `graphql:"updateIssue(input: $input)"`
			}
			// this needs to be a named type for the library
			type UpdateIssueInput struct {
				ID    githubql.ID     `json:"id"`
				Title githubql.String `json:"title"`
				Body  githubql.String `json:"body"`
			}
			input := UpdateIssueInput{
				ID:    issues[0].ID,
				Title: githubql.String(title),
				Body:  githubql.String(body),
			}
			if !o.Confirm {
				logger.Info("Would update issue.")
				return nil
			}
			if err := client.Mutate(context.Background(), &updateIssue, input, nil); err != nil {
				logger.WithError(err).Error("Failed to update issue.")
				failed = true
				return nil
			}

			logger.Infof("Updated issue %d", updateIssue.UpdateIssue.Issue.Number)
		} else {
			// we need to create a new issue

			// what is the ID of the blocker label?
			var labelQuery struct {
				Repository struct {
					ID    githubql.ID
					Label struct {
						ID githubql.ID
					} `graphql:"label(name:\"tide/merge-blocker\")"`
				} `graphql:"repository(owner: $owner, name: $name)"`
			}
			vars = map[string]interface{}{
				"owner": githubql.String(repoInfo.Org),
				"name":  githubql.String(repoInfo.Repo),
			}
			if err := client.Query(context.Background(), &labelQuery, vars); err != nil {
				logger.WithError(err).Error("Failed to search for merge blocker labels.")
				failed = true
				return nil
			}
			if labelQuery.Repository.Label.ID == nil {
				logger.Error("Failed to find a merge blocker label.")
				failed = true
				return nil
			}

			var createIssue struct {
				CreateIssue struct {
					Issue struct {
						Number githubql.Int
					}
				} `graphql:"createIssue(input: $input)"`
			}
			// this needs to be a named type for the library
			type CreateIssueInput struct {
				RepositoryID githubql.ID     `json:"repositoryId"`
				Title        githubql.String `json:"title"`
				Body         githubql.String `json:"body"`
				LabelIDs     []githubql.ID   `json:"labelIds"`
			}
			input := CreateIssueInput{
				RepositoryID: labelQuery.Repository.ID,
				Title:        githubql.String(title),
				Body:         githubql.String(body),
				LabelIDs:     []githubql.ID{labelQuery.Repository.Label.ID},
			}

			if !o.Confirm {
				logger.Info("Would create issue.")
				return nil
			}

			if err := client.Mutate(context.Background(), &createIssue, input, nil); err != nil {
				logger.WithError(err).Error("Failed to create merge blocker issue.")
				failed = true
				return nil
			}

			logger.Infof("Created issue %d", createIssue.CreateIssue.Issue.Number)
		}

		return nil
	}); err != nil || failed {
		logrus.WithError(err).Fatal("Could not publish merge blocking issues.")
	}
}

// Issue holds graphql response data about issues
type Issue struct {
	ID         githubql.ID
	Number     githubql.Int
	Title      githubql.String
	Body       githubql.String
	Repository struct {
		Name  githubql.String
		Owner struct {
			Login githubql.String
		}
	}
}
