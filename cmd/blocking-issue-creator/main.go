package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/config/secret"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/github"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/promotion"
)

type githubClient interface {
	FindIssues(query, sort string, asc bool) ([]github.Issue, error)
	CreateIssue(org, repo, title, body string, milestone int, labels, assignees []string) (int, error)
	EditIssue(org, repo string, number int, issue *github.Issue) (*github.Issue, error)
	CloseIssue(org, repo string, number int) error
}

type options struct {
	promotion.FutureOptions
	github prowflagutil.GitHubOptions

	dryRun bool
}

func (o *options) Validate() error {
	if err := o.FutureOptions.Validate(); err != nil {
		return err
	}
	if err := o.github.Validate(o.dryRun); err != nil {
		return err
	}
	return nil
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.BoolVar(&o.dryRun, "dry-run", true, "Dry run for testing. Uses API tokens but does not mutate.")

	o.github.AddFlags(fs)
	o.FutureOptions.Bind(fs)

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

	if err := secret.Add(o.github.TokenPath); err != nil {
		logrus.WithError(err).Fatal("Error starting secrets agent.")
	}

	client, err := o.github.GitHubClient(o.dryRun)
	if err != nil {
		logrus.WithError(err).Fatal("Error creating github client.")
	}

	botUser, err := client.BotUser()
	if err != nil {
		logrus.WithError(err).Fatal("Error getting bot's user.")
	}

	failed := false
	if err := client.Throttle(300, 300); err != nil {
		logrus.WithError(err).Fatal("failed to throttle")
	}

	if err := o.OperateOnCIOperatorConfigDir(o.ConfigDir, promotion.WithoutOKD, func(configuration *api.ReleaseBuildConfiguration, repoInfo *config.Info) error {
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

		if err := manageIssues(client, botUser.Login, repoInfo, branches, logger); err != nil {
			failed = true
		}

		// sleep to respect Github's API 30 requests per minute limit, absolute minimum is above 2 seconds
		time.Sleep(5 * time.Second)
		return nil
	}); err != nil || failed {
		logrus.WithError(err).Fatal("Could not publish merge blocking issues.")
	}
}

func manageIssues(client githubClient, githubLogin string, repoInfo *config.Info, branches sets.String, logger *logrus.Entry) error {
	var branchTokens []string
	body := fmt.Sprintf("The following branches are being fast-forwarded from the current development branch (%s) as placeholders for future releases. No merging is allowed into these release branches until they are unfrozen for production release.\n\n", repoInfo.Branch)
	for _, branch := range branches.List() {
		body += fmt.Sprintf(" - `%s`\n", branch)
		branchTokens = append(branchTokens, fmt.Sprintf("branch:%s", branch))
	}
	body += "\nContact the [Test Platform](https://coreos.slack.com/messages/CBN38N3MW) or [Automated Release](https://coreos.slack.com/messages/CB95J6R4N) teams for more information."
	title := fmt.Sprintf("Future Release Branches Frozen For Merging | %s", strings.Join(branchTokens, " "))

	query := fmt.Sprintf("is:issue state:open label:\"tide/merge-blocker\" repo:%s/%s author:%s", repoInfo.Org, repoInfo.Repo, githubLogin)
	sort := "updated"
	// We will make sure that the first issue in the list will be with the most recent update.
	ascending := false
	issues, err := client.FindIssues(query, sort, ascending)
	if err != nil {
		logger.WithError(err).Error("Failed to search for open issues.")
		return err
	}
	closeIssues := func(issues []github.Issue, closingLog string) error {
		for _, issue := range issues {
			if err := client.CloseIssue(repoInfo.Org, repoInfo.Repo, issue.Number); err != nil {
				logger.WithError(err).Error("Failed to close issue.")
				return err
			}
			logger.WithField("number", issue.Number).Info(closingLog)
		}
		return nil
	}

	if len(branches) == 0 {
		if len(issues) > 0 {
			logger.Infof("Repository does not have any blocked branches, number of blocking issues to be deleted: %v", len(issues))
		}
		return closeIssues(issues, "Closed issue")
	}

	if len(issues) > 1 {
		logger.Warnf("Found more than one merge blocking issue by the bot: %v", len(issues))
		if err := closeIssues(issues[1:], "Closed extra issue"); err != nil {
			return err
		}
	}

	// we have an existing issue that needs to be up to date
	if len(issues) != 0 {
		logger = logger.WithField("merge-blocker", issues[0])
		existing := issues[0]
		needsUpdate := existing.Title != title || existing.Body != body
		if !needsUpdate {
			logger.Info("Current merge-blocker issue is up to date, no update necessary.")
			return nil
		}

		toBeUpdated := existing
		toBeUpdated.Title = title
		toBeUpdated.Body = body

		if _, err := client.EditIssue(repoInfo.Org, repoInfo.Repo, existing.Number, &toBeUpdated); err != nil {
			logger.WithError(err).Error("Failed to update issue.")
			return err
		}
		logger.WithField("number", toBeUpdated.Number).Info("Updated issue")
	} else {
		// we need to create a new issue
		issueNumber, err := client.CreateIssue(repoInfo.Org, repoInfo.Repo, title, body, 0, []string{"tide/merge-blocker"}, []string{})
		if err != nil {
			logger.WithError(err).Error("Failed to create merge blocker issue.")
			return err
		}
		logger.WithField("number", issueNumber).Info("Created issue")
	}
	return nil
}
