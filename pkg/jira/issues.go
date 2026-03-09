package jira

import (
	"fmt"
	"net/url"

	"github.com/andygrunwald/go-jira"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"

	jirautil "sigs.k8s.io/prow/pkg/jira"

	slackutil "github.com/openshift/ci-tools/pkg/slack"
)

const (
	ProjectDPTP = "DPTP"

	IssueTypeBug   = "Bug"
	IssueTypeStory = "Story"
	IssueTypeTask  = "Task"
)

// IssueFiler knows how to file an issue in Jira
type IssueFiler interface {
	FileIssue(issueType, title, description, reporter string, logger *logrus.Entry) (*jira.Issue, error)
}

// IssueUpdater knows how to update and close Jira issues
type IssueUpdater interface {
	AddComment(issueKey, comment string, logger *logrus.Entry) error
	TransitionIssue(issueKey, transitionName string, logger *logrus.Entry) error
}

type slackClient interface {
	GetUserInfo(user string) (*slack.User, error)
}

// this adapter is needed since none of the upstream types
// are interfaces and they hold mutually ambiguous methods
type jiraAdapter struct {
	delegate *jira.Client
}

func (a *jiraAdapter) FindUser(property string) ([]jira.User, *jira.Response, error) {
	// the JIRA API does not work as documented, and we can therefore not use the jira.Client.User.Find function here.
	// That function supplies a query parameter to the search endpoint which will result in a 400 as you are required to pass the
	// username parameter and this parameter behaves as the query parameter should.
	req, _ := a.delegate.NewRequest("GET", fmt.Sprintf("rest/api/2/user/search?username=%s", url.QueryEscape(property)), nil)
	var users []jira.User
	response, err := a.delegate.Do(req, &users)

	return users, response, err
}

func (a *jiraAdapter) CreateIssue(issue *jira.Issue) (*jira.Issue, *jira.Response, error) {
	return a.delegate.Issue.Create(issue)
}

type jiraClient interface {
	FindUser(property string) ([]jira.User, *jira.Response, error)
	CreateIssue(issue *jira.Issue) (*jira.Issue, *jira.Response, error)
}

// filer caches information from Jira to make filing issues easier
type filer struct {
	slackClient slackClient
	jiraClient  jiraClient
	// delegateClient is the underlying jira.Client for operations not covered by jiraClient interface
	delegateClient *jira.Client
	// project caches metadata for the Jira project we create
	// issues under - this will never change so we can read it
	// once at startup and reuse it forever
	project jira.Project
	// issueTypesByName caches Jira issue types by their given
	// names - these will never change so we can read them once
	// at startup and reuse them forever
	issueTypesByName map[string]jira.IssueType
	// botUser caches the bot's Jira user metadata for use as a
	// back-stop when no requester can be found to match the
	// Slack user that is interacting with us
	botUser *jira.User
}

// FileIssue files an issue, closing over a number of Jira-specific API
// quirks like how issue types and projects are provided, as well as
// transforming the Slack reporter ID to a Jira user, when possible.
func (f *filer) FileIssue(issueType, title, description, reporter string, logger *logrus.Entry) (*jira.Issue, error) {
	suffix, requester := f.resolveRequester(reporter, logger)
	description = fmt.Sprintf("%s\n\nThis issue was filed by %s", description, suffix)
	logger.WithFields(logrus.Fields{
		"title":    title,
		"reporter": requester.Name,
		"type":     issueType,
	}).Debug("Filing Jira issue.")
	toCreate := &jira.Issue{Fields: &jira.IssueFields{
		Project:     f.project,
		Reporter:    requester,
		Type:        f.issueTypesByName[issueType],
		Summary:     title,
		Description: description,
	}}
	issue, response, err := f.jiraClient.CreateIssue(toCreate)
	return issue, jirautil.HandleJiraError(response, err)
}

// AddWatchers adds watchers to a Jira issue.
// watchers is a slice of Jira usernames (account IDs) to add as watchers.
func (f *filer) AddWatchers(issueKey string, watchers []string, logger *logrus.Entry) error {
	if f.delegateClient == nil {
		return fmt.Errorf("delegate client not available")
	}
	for _, watcher := range watchers {
		response, err := f.delegateClient.Issue.AddWatcher(issueKey, watcher)
		if err != nil {
			if err := jirautil.HandleJiraError(response, err); err != nil {
				logger.WithError(err).WithField("watcher", watcher).Warn("failed to add watcher to Jira issue")
				// Continue with other watchers even if one fails
				continue
			}
		}
		logger.WithField("watcher", watcher).Debug("added watcher to Jira issue")
	}
	return nil
}

// AddComment adds a comment to a Jira issue with private visibility for Red Hat employees only.
func (f *filer) AddComment(issueKey, comment string, logger *logrus.Entry) error {
	if f.delegateClient == nil {
		return fmt.Errorf("delegate client not available")
	}
	jiraComment := &jira.Comment{
		Body: comment,
		Visibility: jira.CommentVisibility{
			Type:  "group",
			Value: "Red Hat Employee",
		},
	}
	_, response, err := f.delegateClient.Issue.AddComment(issueKey, jiraComment)
	if err != nil {
		return jirautil.HandleJiraError(response, err)
	}
	logger.WithField("issue", issueKey).Debug("added private comment to Jira issue")
	return nil
}

// TransitionIssue transitions a Jira issue to a new status.
func (f *filer) TransitionIssue(issueKey, transitionName string, logger *logrus.Entry) error {
	if f.delegateClient == nil {
		return fmt.Errorf("delegate client not available")
	}
	transitions, response, err := f.delegateClient.Issue.GetTransitions(issueKey)
	if err != nil {
		return jirautil.HandleJiraError(response, err)
	}
	var transitionID string
	for _, t := range transitions {
		if t.Name == transitionName {
			transitionID = t.ID
			break
		}
	}
	if transitionID == "" {
		return fmt.Errorf("transition %q not found for issue %s", transitionName, issueKey)
	}
	response, err = f.delegateClient.Issue.DoTransition(issueKey, transitionID)
	if err != nil {
		return jirautil.HandleJiraError(response, err)
	}
	logger.WithFields(logrus.Fields{
		"issue":      issueKey,
		"transition": transitionName,
	}).Debug("transitioned Jira issue")
	return nil
}

// resolveRequester attempts to get more information about the Slack
// user that requested the Jira issue, doing everything best-effort
func (f *filer) resolveRequester(reporter string, logger *logrus.Entry) (string, *jira.User) {
	var suffix string
	var requester *jira.User
	slackUser, err := f.slackClient.GetUserInfo(reporter)
	if err != nil {
		logger.WithError(err).Warn("could not search Slack for requester")
		suffix = fmt.Sprintf("[a Slack user|%s/team/%s]", slackutil.RedHatInternalURL, reporter)
	} else {
		jiraUsers, response, err := f.jiraClient.FindUser(slackUser.RealName)
		if err := jirautil.HandleJiraError(response, err); err != nil {
			logger.WithError(err).Warn("could not search Jira for requester")
		}
		if len(jiraUsers) != 0 {
			requester = &jiraUsers[0]
		}
		suffix = fmt.Sprintf("Slack user [%s|%s/team/%s]", slackUser.RealName, slackutil.RedHatInternalURL, slackUser.ID)
	}

	if requester == nil {
		logger.Infof("Could not find a Jira user for Slack user %q, defaulting to bot user.", reporter)
		requester = f.botUser
	}
	return suffix, requester
}

var _ IssueFiler = (*filer)(nil)

func NewIssueFiler(slackClient *slack.Client, jiraClient *jira.Client) (IssueFiler, error) {
	filer := &filer{
		slackClient:      slackClient,
		jiraClient:       &jiraAdapter{delegate: jiraClient},
		issueTypesByName: map[string]jira.IssueType{},
	}

	project, response, err := jiraClient.Project.Get(ProjectDPTP)
	if err := jirautil.HandleJiraError(response, err); err != nil {
		return nil, fmt.Errorf("could not find Jira project %s: %w", ProjectDPTP, err)
	}
	filer.project = *project
	for _, t := range project.IssueTypes {
		filer.issueTypesByName[t.Name] = t
	}
	for _, name := range []string{IssueTypeStory, IssueTypeBug, IssueTypeTask} {
		if _, found := filer.issueTypesByName[name]; !found {
			return nil, fmt.Errorf("could not find issue type %s in Jira for project %s", name, ProjectDPTP)
		}
	}

	botUser, response, err := jiraClient.User.GetSelf()
	if err := jirautil.HandleJiraError(response, err); err != nil {
		return nil, fmt.Errorf("could not resolve Jira bot user: %w", err)
	}
	filer.botUser = botUser

	return filer, nil
}
