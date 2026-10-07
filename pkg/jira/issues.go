package jira

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/PagerDuty/go-pagerduty"
	"github.com/andygrunwald/go-jira"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/trivago/tgo/tcontainer"

	jirautil "sigs.k8s.io/prow/pkg/jira"

	slackutil "github.com/openshift/ci-tools/pkg/slack"
)

const (
	ProjectDPTP = "DPTP"

	IssueTypeBug   = "Bug"
	IssueTypeStory = "Story"
	IssueTypeTask  = "Task"

	ResolutionDone   = "Done"
	StatusInProgress = "In Progress"

	// Labels applied to forum / modal support tickets so they are excluded from
	// DPTP intake rotation (sprint-automation) and AI backlog intake/groom.
	LabelReady   = "ready"
	LabelGroomed = "groomed"

	helpdeskQuery     = "DPTP Help Desk"
	activityTypeField = "Activity Type"
	activityTypeValue = "Incidents & Support"
)

// IssueFiler knows how to file an issue in Jira
type IssueFiler interface {
	FileIssue(issueType, title, description, reporter, activityType string, logger *logrus.Entry) (*jira.Issue, error)
	SetIssueStatus(issueKey, status string, logger *logrus.Entry) error
	CloseIssue(issueKey, resolution string, logger *logrus.Entry) (bool, error)
}

type slackClient interface {
	GetUserInfo(user string) (*slack.User, error)
}

// this adapter is needed since none of the upstream types
// are interfaces and they hold mutually ambiguous methods
type jiraAdapter struct {
	delegate *jira.Client
}

func (a *jiraAdapter) CreateIssue(issue *jira.Issue) (*jira.Issue, *jira.Response, error) {
	return a.delegate.Issue.Create(issue)
}

func (a *jiraAdapter) GetTransitions(issueKey string) ([]jira.Transition, *jira.Response, error) {
	return a.delegate.Issue.GetTransitions(issueKey)
}

func (a *jiraAdapter) DoTransitionWithPayload(issueKey string, payload interface{}) (*jira.Response, error) {
	return a.delegate.Issue.DoTransitionWithPayload(issueKey, payload)
}

func (a *jiraAdapter) FindUsers(query string, maxResults int) ([]jira.User, *jira.Response, error) {
	return a.delegate.User.Find(query, jira.WithMaxResults(maxResults))
}

func (a *jiraAdapter) UpdateAssignee(issueKey string, assignee *jira.User) (*jira.Response, error) {
	return a.delegate.Issue.UpdateAssignee(issueKey, assignee)
}

func (a *jiraAdapter) GetCreateMeta(projectKey string) (*jira.CreateMetaInfo, *jira.Response, error) {
	return a.delegate.Issue.GetCreateMeta(projectKey)
}

type jiraClient interface {
	CreateIssue(issue *jira.Issue) (*jira.Issue, *jira.Response, error)
	GetTransitions(issueKey string) ([]jira.Transition, *jira.Response, error)
	DoTransitionWithPayload(issueKey string, payload interface{}) (*jira.Response, error)
	FindUsers(query string, maxResults int) ([]jira.User, *jira.Response, error)
	UpdateAssignee(issueKey string, assignee *jira.User) (*jira.Response, error)
	GetCreateMeta(projectKey string) (*jira.CreateMetaInfo, *jira.Response, error)
}

// filer caches information from Jira to make filing issues easier
type filer struct {
	slackClient     slackClient
	jiraClient      jiraClient
	pagerDutyClient *pagerduty.Client
	// project caches metadata for the Jira project we create
	// issues under - this will never change so we can read it
	// once at startup and reuse it forever
	project jira.Project
	// issueTypesByName caches Jira issue types by their given
	// names - these will never change so we can read them once
	// at startup and reuse them forever
	issueTypesByName map[string]jira.IssueType
	// activityTypeFieldKey is the Jira custom field key for "Activity Type".
	activityTypeFieldKey string
	// activityTypeValueIDs maps Activity Type labels to Jira option IDs.
	activityTypeValueIDs map[string]string
	// botUser caches the bot's Jira user metadata for use as a
	// back-stop when no requester can be found to match the
	// Slack user that is interacting with us
	botUser *jira.User
}

// FileIssue files an issue, closing over a number of Jira-specific API
// quirks like how issue types and projects are provided, as well as
// transforming the Slack reporter ID to a Jira user, when possible.
func (f *filer) FileIssue(issueType, title, description, reporter, activityType string, logger *logrus.Entry) (*jira.Issue, error) {
	suffix := f.requesterSuffix(reporter, logger)
	requester := f.botUser
	description = fmt.Sprintf("%s\n\nThis issue was filed by %s", description, suffix)
	logger.WithFields(logrus.Fields{
		"title": title,
		"type":  issueType,
	}).Debug("Filing Jira issue.")
	fields := &jira.IssueFields{
		Project:     f.project,
		Reporter:    requester,
		Type:        f.issueTypesByName[issueType],
		Summary:     title,
		Description: description,
	}
	if activityType != "" {
		if !IsValidActivityType(activityType) {
			return nil, fmt.Errorf("invalid activity type %q", activityType)
		}
		if err := f.setActivityTypeField(fields, activityType); err != nil {
			logger.WithError(err).WithField("activity_type", activityType).Warn("could not set Jira Activity Type; omitting field from create request")
		}
		// Support / incident tickets are ops work, not backlog grooming candidates.
		if activityType == activityTypeValue {
			fields.Labels = []string{LabelReady, LabelGroomed}
		}
	}
	toCreate := &jira.Issue{Fields: fields}
	issue, response, err := f.jiraClient.CreateIssue(toCreate)
	if err := jirautil.HandleJiraError(response, err); err != nil {
		return nil, err
	}
	if err := f.assignToHelpdeskOnCall(issue); err != nil {
		logger.WithError(err).WithField("issue", issue.Key).Warn("failed to assign support request to current helpdesk on-call")
	}
	return issue, nil
}

// SetIssueStatus transitions a Jira issue to the requested status when possible.
func (f *filer) SetIssueStatus(issueKey, status string, logger *logrus.Entry) error {
	transitions, response, err := f.jiraClient.GetTransitions(issueKey)
	if err := jirautil.HandleJiraError(response, err); err != nil {
		return err
	}

	var transitionID string
	for _, transition := range transitions {
		if transition.To.Name == status || transition.Name == status {
			transitionID = transition.ID
			break
		}
	}
	if transitionID == "" {
		logger.WithFields(logrus.Fields{"issue": issueKey, "status": status}).Info("No matching transition found; issue may already be in target status")
		return nil
	}

	payload := jira.CreateTransitionPayload{
		Transition: jira.TransitionPayload{ID: transitionID},
	}
	response, err = f.jiraClient.DoTransitionWithPayload(issueKey, payload)
	return jirautil.HandleJiraError(response, err)
}

// closingStatusNames are destination statuses that indicate a closing transition.
var closingStatusNames = []string{"done", "closed", "resolved"}
var closingTransitionNames = []string{"close issue", "close", "resolve issue", "resolve"}

// CloseIssue transitions a Jira issue using the first available closing
// transition and sets the provided resolution in the payload.
// Transition selection: first tries an exact (case-insensitive) match on
// transition name or destination status against the resolution string,
// then falls back to well-known closing statuses (Done, Closed, Resolved).
func (f *filer) CloseIssue(issueKey, resolution string, logger *logrus.Entry) (bool, error) {
	transitions, response, err := f.jiraClient.GetTransitions(issueKey)
	if err := jirautil.HandleJiraError(response, err); err != nil {
		return false, err
	}

	transitionID := findClosingTransition(transitions, resolution)
	if transitionID == "" {
		logger.WithField("issue", issueKey).Info("No closing transition found; issue may already be closed")
		return false, nil
	}

	payload := jira.CreateTransitionPayload{
		Transition: jira.TransitionPayload{ID: transitionID},
		Fields: jira.TransitionPayloadFields{
			Resolution: &jira.Resolution{Name: resolution},
		},
	}
	response, err = f.jiraClient.DoTransitionWithPayload(issueKey, payload)
	return true, jirautil.HandleJiraError(response, err)
}

func findClosingTransition(transitions []jira.Transition, resolution string) string {
	const (
		noMatchPriority = 100
	)
	bestPriority := noMatchPriority
	bestTransitionID := ""

	for _, t := range transitions {
		priority := closingTransitionPriority(t, resolution)
		if priority < bestPriority {
			bestPriority = priority
			bestTransitionID = t.ID
		}
	}
	return bestTransitionID
}

func closingTransitionPriority(transition jira.Transition, resolution string) int {
	const (
		priorityExactTransitionName = iota
		priorityExactDestinationStatus
		priorityKnownTransitionName
		priorityKnownDestinationStatus
		priorityNoMatch = 100
	)

	if strings.EqualFold(transition.Name, resolution) {
		return priorityExactTransitionName
	}
	if strings.EqualFold(transition.To.Name, resolution) {
		return priorityExactDestinationStatus
	}

	name := strings.ToLower(transition.Name)
	for _, transitionName := range closingTransitionNames {
		if name == transitionName {
			return priorityKnownTransitionName
		}
	}

	status := strings.ToLower(transition.To.Name)
	for _, closingStatus := range closingStatusNames {
		if status == closingStatus {
			return priorityKnownDestinationStatus
		}
	}
	return priorityNoMatch
}

func (f *filer) assignToHelpdeskOnCall(issue *jira.Issue) error {
	if f.pagerDutyClient == nil || issue == nil {
		return nil
	}
	assigneeEmail, err := f.helpdeskOnCallEmail()
	if err != nil {
		return err
	}
	if strings.TrimSpace(assigneeEmail) == "" {
		return fmt.Errorf("helpdesk on-call has no email for Jira user lookup")
	}
	jiraUsers, response, err := f.jiraClient.FindUsers(url.QueryEscape(assigneeEmail), 20)
	if err := jirautil.HandleJiraError(response, err); err != nil {
		return fmt.Errorf("could not find jira user by helpdesk email: %w", err)
	}
	if len(jiraUsers) != 1 {
		return fmt.Errorf("could not find a single jira user for helpdesk email %s: got %d", assigneeEmail, len(jiraUsers))
	}

	issueID := strings.TrimSpace(issue.ID)
	if issueID == "" {
		issueID = strings.TrimSpace(issue.Key)
	}
	if issueID == "" {
		return fmt.Errorf("issue has neither ID nor key")
	}
	response, err = f.jiraClient.UpdateAssignee(issueID, &jiraUsers[0])
	return jirautil.HandleJiraError(response, err)
}

func (f *filer) helpdeskOnCallEmail() (string, error) {
	now := time.Now().UTC()
	// Keep the same time window used in sprint-automation.
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 8, 0, 1, 0, time.UTC)
	dayEnd := dayStart.Add(13 * time.Hour).Add(-2 * time.Second)
	user, err := userOnCallDuring(f.pagerDutyClient, helpdeskQuery, dayStart, dayEnd)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(user.Email), nil
}

func userOnCallDuring(client *pagerduty.Client, query string, since, until time.Time) (*pagerduty.User, error) {
	scheduleResponse, err := client.ListSchedules(pagerduty.ListSchedulesOptions{Query: query})
	if err != nil {
		return nil, fmt.Errorf("could not query PagerDuty for the %s on-call schedule: %w", query, err)
	}
	if len(scheduleResponse.Schedules) != 1 {
		return nil, fmt.Errorf("did not get exactly one schedule when querying PagerDuty for the '%s' on-call schedule: %d", query, len(scheduleResponse.Schedules))
	}
	schedule := scheduleResponse.Schedules[0]

	users, err := client.ListOnCallUsers(schedule.ID, pagerduty.ListOnCallUsersOptions{
		Since: since.Format(time.RFC3339),
		Until: until.Format(time.RFC3339),
	})
	if err != nil {
		return nil, fmt.Errorf("could not query PagerDuty for the %s on-call: %w", query, err)
	}
	if len(users) == 0 {
		return nil, fmt.Errorf("did not get any users when querying PagerDuty for the '%s' on-call", query)
	}
	if len(users) == 1 {
		return &users[0], nil
	}

	overrides, err := client.ListOverrides(schedule.ID, pagerduty.ListOverridesOptions{
		Since: since.Format(time.RFC3339),
		Until: until.Format(time.RFC3339),
	})
	if err != nil {
		return nil, fmt.Errorf("could not query PagerDuty for the '%s' overrides: %w", query, err)
	}
	if len(overrides.Overrides) != 1 {
		return nil, fmt.Errorf("did not get exactly one override when querying PagerDuty for the '%s' overrides: %d", query, len(overrides.Overrides))
	}
	override := overrides.Overrides[0]
	user, err := client.GetUser(override.User.ID, pagerduty.GetUserOptions{})
	if err != nil {
		return nil, fmt.Errorf("could not get PagerDuty user %s for override: %w", override.User.ID, err)
	}
	return user, nil
}

func (f *filer) setActivityTypeField(fields *jira.IssueFields, activityType string) error {
	if fields == nil {
		return nil
	}
	if f.activityTypeFieldKey == "" {
		return fmt.Errorf("jira activity type field key is empty")
	}
	activityTypeID, found := f.activityTypeValueIDs[activityType]
	if !found || strings.TrimSpace(activityTypeID) == "" {
		return fmt.Errorf("jira activity type %q has no option ID", activityType)
	}
	if fields.Unknowns == nil {
		fields.Unknowns = tcontainer.NewMarshalMap()
	}
	fields.Unknowns[f.activityTypeFieldKey] = tcontainer.MarshalMap{"id": activityTypeID}
	return nil
}

// requesterSuffix builds a human-readable suffix for the issue description.
// Jira user resolution is intentionally not attempted.
func (f *filer) requesterSuffix(reporter string, logger *logrus.Entry) string {
	slackUser, err := f.slackClient.GetUserInfo(reporter)
	if err != nil {
		logger.WithError(err).Warn("could not search Slack for requester")
		return fmt.Sprintf("[a Slack user|%s/team/%s]", slackutil.RedHatInternalURL, reporter)
	}

	displayName := strings.TrimSpace(slackUser.RealName)
	if displayName == "" {
		displayName = reporter
	}
	return fmt.Sprintf("Slack user [%s|%s/team/%s]", displayName, slackutil.RedHatInternalURL, slackUser.ID)
}

func NewIssueFiler(slackClient *slack.Client, jiraClient *jira.Client, pagerDutyClient *pagerduty.Client) (IssueFiler, error) {
	filer := &filer{
		slackClient:          slackClient,
		jiraClient:           &jiraAdapter{delegate: jiraClient},
		pagerDutyClient:      pagerDutyClient,
		issueTypesByName:     map[string]jira.IssueType{},
		activityTypeValueIDs: map[string]string{},
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
	createMeta, response, err := jiraClient.Issue.GetCreateMeta(ProjectDPTP)
	if metaErr := jirautil.HandleJiraError(response, err); metaErr != nil {
		logrus.WithError(metaErr).Warnf("Could not load Jira create metadata for %s; Activity Type field will be best-effort", ProjectDPTP)
	} else if createMeta == nil {
		logrus.Warnf("Jira create metadata for %s is empty; Activity Type field will be best-effort", ProjectDPTP)
	} else if metaProject := createMeta.GetProjectWithKey(ProjectDPTP); metaProject == nil {
		logrus.Warnf("Jira create metadata missing project %s; Activity Type field will be best-effort", ProjectDPTP)
	} else if metaIssueType := metaProject.GetIssueTypeWithName(IssueTypeTask); metaIssueType == nil {
		logrus.Warnf("Jira create metadata missing issue type %s for %s; Activity Type field will be best-effort", IssueTypeTask, ProjectDPTP)
	} else {
		allFields, fieldsErr := metaIssueType.GetAllFields()
		if fieldsErr != nil {
			logrus.WithError(fieldsErr).Warnf("Could not read Jira create fields for %s/%s; Activity Type field will be best-effort", ProjectDPTP, IssueTypeTask)
		} else if activityFieldKey, found := allFields[activityTypeField]; !found || strings.TrimSpace(activityFieldKey) == "" {
			logrus.Warnf("Could not find Jira field %q for %s/%s; Activity Type field will be best-effort", activityTypeField, ProjectDPTP, IssueTypeTask)
		} else {
			filer.activityTypeFieldKey = activityFieldKey
			for _, value := range AllowedActivityTypes {
				activityTypeValueID, valueErr := activityTypeOptionID(metaIssueType.Fields, activityFieldKey, value)
				if valueErr != nil {
					continue
				}
				filer.activityTypeValueIDs[value] = activityTypeValueID
			}
			if _, found := filer.activityTypeValueIDs[activityTypeValue]; !found {
				logrus.Warnf("Could not find Jira option %q for field %q; Activity Type field will be best-effort", activityTypeValue, activityTypeField)
			}
		}
	}

	botUser, response, err := jiraClient.User.GetSelf()
	if err := jirautil.HandleJiraError(response, err); err != nil {
		return nil, fmt.Errorf("could not resolve Jira bot user: %w", err)
	}
	filer.botUser = botUser

	return filer, nil
}

func activityTypeOptionID(fields tcontainer.MarshalMap, fieldKey, desiredValue string) (string, error) {
	allowedValues, err := fields.Array(fieldKey + "/allowedValues")
	if err != nil {
		return "", fmt.Errorf("could not read allowed values for Jira field %q: %w", activityTypeField, err)
	}
	for _, rawValue := range allowedValues {
		value, err := tcontainer.ConvertToMarshalMap(rawValue, nil)
		if err != nil {
			continue
		}
		optionValue, err := value.String("value")
		if err != nil || optionValue != desiredValue {
			continue
		}
		optionID, err := value.String("id")
		if err != nil || strings.TrimSpace(optionID) == "" {
			return "", fmt.Errorf("jira field %q value %q has no option ID", activityTypeField, desiredValue)
		}
		return optionID, nil
	}
	return "", fmt.Errorf("could not find Jira option %q for field %q", desiredValue, activityTypeField)
}
