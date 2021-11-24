package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/PagerDuty/go-pagerduty"
	jiraapi "github.com/andygrunwald/go-jira"
	"github.com/blang/semver"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"

	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/test-infra/pkg/flagutil"
	"k8s.io/test-infra/prow/config/secret"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
	jirautil "k8s.io/test-infra/prow/jira"
	"k8s.io/test-infra/prow/logrusutil"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/jira"
	"github.com/openshift/ci-tools/pkg/pagerdutyutil"
)

type options struct {
	logLevel string

	jiraOptions       prowflagutil.JiraOptions
	kubernetesOptions prowflagutil.KubernetesOptions
	pagerDutyOptions  pagerdutyutil.Options

	slackTokenPath string
}

func (o *options) Validate() error {
	_, err := logrus.ParseLevel(o.logLevel)
	if err != nil {
		return fmt.Errorf("invalid --log-level: %w", err)
	}

	if o.slackTokenPath == "" {
		return fmt.Errorf("--slack-token-path is required")
	}

	for _, group := range []flagutil.OptionGroup{&o.jiraOptions, &o.pagerDutyOptions, &o.kubernetesOptions} {
		if err := group.Validate(false); err != nil {
			return err
		}
	}

	return nil
}

func gatherOptions(fs *flag.FlagSet, args ...string) options {
	o := options{kubernetesOptions: prowflagutil.KubernetesOptions{NOInClusterConfigDefault: true}}
	fs.StringVar(&o.logLevel, "log-level", "info", "Level at which to log output.")

	for _, group := range []flagutil.OptionGroup{&o.jiraOptions, &o.pagerDutyOptions, &o.kubernetesOptions} {
		group.AddFlags(fs)
	}

	fs.StringVar(&o.slackTokenPath, "slack-token-path", "", "Path to the file containing the Slack token to use.")

	if err := fs.Parse(args); err != nil {
		logrus.WithError(err).Fatal("Could not parse args.")
	}
	return o
}

func addSchemes() error {
	if err := configv1.AddToScheme(scheme.Scheme); err != nil {
		return fmt.Errorf("failed to add configv1 to scheme: %w", err)
	}
	return nil
}

func main() {
	logrusutil.ComponentInit()

	o := gatherOptions(flag.NewFlagSet(os.Args[0], flag.ExitOnError), os.Args[1:]...)
	if err := o.Validate(); err != nil {
		logrus.WithError(err).Fatal("Invalid options")
	}
	level, _ := logrus.ParseLevel(o.logLevel)
	logrus.SetLevel(level)

	if err := secret.Add(o.slackTokenPath); err != nil {
		logrus.WithError(err).Fatal("Error starting secrets agent.")
	}

	var blocks []slack.Block

	slackClient := slack.New(string(secret.GetSecret(o.slackTokenPath)))
	pagerDutyClient, err := o.pagerDutyOptions.Client()
	if err != nil {
		logrus.WithError(err).Fatal("Could not initialize PagerDuty client.")
	}
	userIdsByRole, err := users(pagerDutyClient, slackClient)
	if err != nil {
		msg := "Could not get rotating roles from PagerDuty."
		if len(userIdsByRole) == 0 {
			logrus.WithError(err).Fatal(msg)
		} else {
			logrus.WithError(err).Error(msg)
		}
	}
	blocks = append(blocks, getPagerDutyBlocks(userIdsByRole)...)

	prowJiraClient, err := o.jiraOptions.Client()
	if err != nil {
		logrus.WithError(err).Fatal("Could not initialize Jira client.")
	}
	jiraClient := prowJiraClient.JiraClient()
	if approvalBlocks, err := getIssuesNeedingApproval(jiraClient); err != nil {
		logrus.WithError(err).Fatal("Could not get issues needing approval.")
	} else {
		blocks = append(blocks, approvalBlocks...)
	}

	if err := postBlocks(slackClient, blocks); err != nil {
		logrus.WithError(err).Fatal("Could not post team digest to Slack.")
	}

	if err := sendIntakeDigest(slackClient, jiraClient, userIdsByRole[roleIntake]); err != nil {
		logrus.WithError(err).Fatal("Could not post @dptp-intake digest to Slack.")
	}

	if err := ensureGroupMembership(slackClient, userIdsByRole); err != nil {
		logrus.WithError(err).Fatal("Could not ensure Slack group membership.")
	}

	if err := addSchemes(); err != nil {
		logrus.WithError(err).Fatal("failed to set up scheme")
	}
	kubeConfigs, err := o.kubernetesOptions.LoadClusterConfigs()
	if err != nil {
		logrus.WithError(err).Fatal("could not load kube configs")
	}

	clients := map[api.Cluster]ctrlruntimeclient.Reader{}
	for _, cluster := range []api.Cluster{api.ClusterBuild01, api.ClusterBuild02} {
		clusterName := string(cluster)
		config, ok := kubeConfigs[clusterName]
		if !ok {
			logrus.WithField("context", clusterName).Fatal("failed to find context in kube configs")
		}
		client, err := ctrlruntimeclient.New(&config, ctrlruntimeclient.Options{})
		if err != nil {
			logrus.WithField("clusterName", clusterName).WithError(err).Fatal("could not get client for kube config")
		}
		clients[cluster] = client
	}

	versionInfo, err := upgradeBuild02(context.TODO(), clients[api.ClusterBuild01], clients[api.ClusterBuild02])
	if err != nil {
		logrus.WithError(err).Fatal("could not determine if build02 needs to upgraded")
	}
	if versionInfo != nil {
		logrus.WithField("toVersion", versionInfo.version).Info("Posting @dptp-triage about upgrading build02 to Slack")
		if err := sendTriageBuild02Upgrade(slackClient, versionInfo.version, versionInfo.stableDuration); err != nil {
			logrus.WithError(err).Fatal("Could not post @dptp-triage about upgrading build02 to Slack.")
		}
	}
}

const (
	primaryOnCallQuery     = "DPTP Primary On-Call"
	secondaryUSOnCallQuery = "DPTP Secondary On-Call (US)"
	secondaryEUOnCallQuery = "DPTP Secondary On-Call (EU)"
	roleTriagePrimary      = "@dptp-triage Primary"
	roleTriageSecondaryUS  = "@dptp-triage Secondary (US)"
	roleTriageSecondaryEU  = "@dptp-triage Secondary (EU)"
	roleHelpdesk           = "@dptp-helpdesk"
	roleIntake             = "@dptp-intake"
)

func getPagerDutyBlocks(userIdsByRole map[string]string) []slack.Block {
	var fields []*slack.TextBlockObject
	for _, role := range []string{roleTriagePrimary, roleTriageSecondaryUS, roleTriageSecondaryEU, roleHelpdesk, roleIntake} {
		fields = append(fields, &slack.TextBlockObject{
			Type: slack.PlainTextType,
			Text: role,
		}, &slack.TextBlockObject{
			Type: slack.MarkdownType,
			Text: fmt.Sprintf("<@%s>", userIdsByRole[role]),
		})
	}

	blocks := []slack.Block{
		&slack.HeaderBlock{
			Type: slack.MBTHeader,
			Text: &slack.TextBlockObject{
				Type: slack.PlainTextType,
				Text: "Today's Rotating Positions",
			},
		},
		&slack.SectionBlock{
			Type:   slack.MBTSection,
			Fields: fields,
		},
		&slack.SectionBlock{
			Type: slack.MBTSection,
			Text: &slack.TextBlockObject{
				Type: slack.MarkdownType,
				Text: "Role manuals for: <https://docs.google.com/document/d/1eM2H_q9wMHfaJOqT08tO0fYxhAx3hxZp9_Fj1KsmJhA|triage>, <https://docs.google.com/document/d/1CYRzqE2Y4L-SRdp2DB1hXGnpk0tCd5Tm1SjgBI0ihnY|help-desk>, and <https://docs.google.com/document/d/1-zJGyiXiVqUvFWRQ5IYDwxSYmLQPD_cfJeEFXhfjDLA|intake>.",
			},
		},
		&slack.SectionBlock{
			Type: slack.MBTSection,
			Text: &slack.TextBlockObject{
				Type: slack.MarkdownType,
				Text: "Team definitions for: <https://docs.google.com/document/d/19TRTNxaA3-qC4CM-stBxGTC8RqHs86Qb6mjITBsRpG4|ready>, <https://docs.google.com/document/d/1Qd4qcRHUxk5-eiFIjQm2TTH1TaGQ-zhbphLNXxyvr00|done>.",
			},
		},
	}

	return blocks
}

func users(client *pagerduty.Client, slackClient *slack.Client) (map[string]string, error) {
	var errors []error
	now := time.Now()
	userIdsByRole := map[string]string{}
	for _, item := range []struct {
		role         string
		query        string
		since, until time.Time
	}{
		{
			role:  roleTriagePrimary,
			query: primaryOnCallQuery,
			since: now.Add(-1 * time.Second),
			until: now,
		},
		{
			role:  roleTriageSecondaryUS,
			query: secondaryUSOnCallQuery,
			since: now.Add(-24 * time.Hour),
			until: now,
		},
		{
			role:  roleTriageSecondaryEU,
			query: secondaryEUOnCallQuery,
			since: now.Add(-24 * time.Hour),
			until: now,
		},
		{
			role:  roleHelpdesk,
			query: primaryOnCallQuery,
			since: time.Now().Add(-7 * 24 * time.Hour).Add(-1 * time.Second),
			until: time.Now().Add(-7 * 24 * time.Hour),
		},
		{
			role:  roleIntake,
			query: primaryOnCallQuery,
			since: time.Now().Add(-2 * 7 * 24 * time.Hour).Add(-1 * time.Second),
			until: time.Now().Add(-2 * 7 * 24 * time.Hour),
		},
	} {
		pagerDutyUser, err := userOnCallDuring(client, item.query, item.since, item.until)
		if err != nil {
			errors = append(errors, fmt.Errorf("could not get PagerDuty user for %s: %w", item.role, err))
			continue
		}
		slackUser, err := slackClient.GetUserByEmail(pagerDutyUser.Email)
		if err != nil {
			errors = append(errors, fmt.Errorf("could not get slack user for %s: %w", pagerDutyUser.Name, err))
			continue
		}
		userIdsByRole[item.role] = slackUser.ID
	}
	return userIdsByRole, kerrors.NewAggregate(errors)
}

func userOnCallDuring(client *pagerduty.Client, query string, since, until time.Time) (*pagerduty.User, error) {
	scheduleResponse, err := client.ListSchedules(pagerduty.ListSchedulesOptions{Query: query})
	if err != nil {
		return nil, fmt.Errorf("could not query PagerDuty for the %s on-call schedule: %w", query, err)
	}
	if len(scheduleResponse.Schedules) != 1 {
		return nil, fmt.Errorf("did not get exactly one schedule when querying PagerDuty for the %s on-call schedule: %v", query, scheduleResponse.Schedules)
	}

	users, err := client.ListOnCallUsers(scheduleResponse.Schedules[0].ID, pagerduty.ListOnCallUsersOptions{
		Since: since.String(),
		Until: until.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("could not query PagerDuty for the %s on-call: %w", query, err)
	}
	if len(users) != 1 {
		return nil, fmt.Errorf("did not get exactly one user when querying PagerDuty for the %s on-call: %v", query, users)
	}
	return &users[0], nil
}

func getIssuesNeedingApproval(jiraClient *jiraapi.Client) ([]slack.Block, error) {
	issues, response, err := jiraClient.Issue.Search(fmt.Sprintf(`project=%s AND status="QE Review"`, jira.ProjectDPTP), nil)
	if err := jirautil.JiraError(response, err); err != nil {
		return nil, fmt.Errorf("could not query for Jira issues: %w", err)
	}

	if len(issues) == 0 {
		return nil, nil
	}

	blocks := []slack.Block{
		&slack.HeaderBlock{
			Type: slack.MBTHeader,
			Text: &slack.TextBlockObject{
				Type: slack.PlainTextType,
				Text: "Cards Awaiting Acceptance",
			},
		},
		&slack.SectionBlock{
			Type: slack.MBTSection,
			Text: &slack.TextBlockObject{
				Type: slack.PlainTextType,
				Text: "The following issues are ready for acceptance on the DPTP board:",
			},
		},
	}
	idByUser := map[string]slack.Block{}
	blocksByUser := map[string][]slack.Block{}
	for _, issue := range issues {
		if _, recorded := idByUser[issue.Fields.Assignee.DisplayName]; !recorded {
			idByUser[issue.Fields.Assignee.DisplayName] = &slack.ContextBlock{
				Type: slack.MBTContext,
				ContextElements: slack.ContextElements{
					Elements: []slack.MixedElement{
						&slack.ImageBlockElement{
							Type:     slack.METImage,
							ImageURL: issue.Fields.Assignee.AvatarUrls.Four8X48,
							AltText:  issue.Fields.Assignee.DisplayName,
						},
						&slack.TextBlockObject{
							Type: slack.MarkdownType,
							Text: issue.Fields.Assignee.DisplayName,
						},
					},
				},
			}
		}
		blocksByUser[issue.Fields.Assignee.DisplayName] = append(blocksByUser[issue.Fields.Assignee.DisplayName], blockForIssue(issue))
	}

	for user, id := range idByUser {
		blocks = append(blocks, id)
		blocks = append(blocks, blocksByUser[user]...)
		blocks = append(blocks, &slack.DividerBlock{
			Type: slack.MBTDivider,
		})
	}
	return blocks, nil
}

const (
	dptpTeamChannel = "team-dp-testplatform"
	dptpOpsChannel  = "ops-testplatform"

	privateChannelType = "private_channel"
	publicChannelType  = "public_channel"
)

func channelID(slackClient *slack.Client, channel, t string) (string, error) {
	var channelID, cursor string
	for {
		conversations, nextCursor, err := slackClient.GetConversations(&slack.GetConversationsParameters{Cursor: cursor, Types: []string{t}})
		if err != nil {
			return "", fmt.Errorf("could not query Slack for channel ID: %w", err)
		}
		for _, conversation := range conversations {
			if conversation.Name == channel {
				channelID = conversation.ID
				break
			}
		}
		if channelID != "" || nextCursor == "" {
			break
		}
		cursor = nextCursor
	}
	if channelID == "" {
		return "", fmt.Errorf("could not find Slack channel %s", channel)
	}
	return channelID, nil
}

func postBlocks(slackClient *slack.Client, blocks []slack.Block) error {
	channelID, err := channelID(slackClient, dptpTeamChannel, privateChannelType)
	if err != nil {
		return fmt.Errorf("failed for get channel ID for %s", dptpTeamChannel)
	}
	responseChannel, responseTimestamp, err := slackClient.PostMessage(channelID, slack.MsgOptionText("Jira card digest.", false), slack.MsgOptionBlocks(blocks...))
	if err != nil {
		return fmt.Errorf("failed to post to channel: %w", err)
	}

	logrus.Infof("Posted team digest in channel %s at %s", responseChannel, responseTimestamp)
	return nil
}

func sendIntakeDigest(slackClient *slack.Client, jiraClient *jiraapi.Client, userId string) error {
	issues, response, err := jiraClient.Issue.Search(fmt.Sprintf(`project=%s AND (labels is EMPTY OR NOT (labels=ready OR labels=no-intake)) AND created >= -30d AND status = "To Do"`, jira.ProjectDPTP), nil)
	if err := jirautil.JiraError(response, err); err != nil {
		return fmt.Errorf("could not query for Jira issues: %w", err)
	}

	if len(issues) == 0 {
		return nil
	}

	blocks := []slack.Block{
		&slack.HeaderBlock{
			Type: slack.MBTHeader,
			Text: &slack.TextBlockObject{
				Type: slack.PlainTextType,
				Text: "Cards Awaiting Intake",
			},
		},
		&slack.SectionBlock{
			Type: slack.MBTSection,
			Text: &slack.TextBlockObject{
				Type: slack.PlainTextType,
				Text: "The following issues need to be reviewed as part of the intake process:",
			},
		},
	}
	for _, issue := range issues {
		blocks = append(blocks, blockForIssue(issue))
	}
	responseChannel, responseTimestamp, err := slackClient.PostMessage(userId, slack.MsgOptionText("Jira card digest.", false), slack.MsgOptionBlocks(blocks...))
	if err != nil {
		return fmt.Errorf("failed to message @dptp-intake: %w", err)
	}

	logrus.Infof("Posted intake digest in channel %s at %s", responseChannel, responseTimestamp)
	return nil
}

type versionInfo struct {
	stable          bool
	stableDuration  string
	version         string
	state           configv1.UpdateState
	semanticVersion semver.Version
}

// newVersionInfo checks if the current version is stable enough.
// A version is stable iff Z-stream (or Y-stream) upgrade has been completed for 1 day (1 week).
// Z-stream upgrade: the current version is upgraded from the same minor version e.g., 4.8.23 <- 4.8.18
// Y-stream upgrade: the current version is upgraded from a smaller minor version e.g., 4.9.6 <- 4.8.18
func newVersionInfo(status configv1.ClusterVersionStatus) (*versionInfo, error) {
	if len(status.History) == 0 {
		return nil, fmt.Errorf("failed to get history of ClusterVersion version")
	}
	current := status.History[0]
	ret := &versionInfo{
		version: current.Version,
		state:   current.State,
		// soak a day after a Z-stream upgrade
		stable:         current.State == configv1.CompletedUpdate && current.CompletionTime != nil && time.Since(current.CompletionTime.Time) > 24*time.Hour,
		stableDuration: "1 day",
	}
	cv, err := semver.Make(current.Version)
	if err != nil {
		return nil, fmt.Errorf("failed to determine semantic version: %s", current.Version)
	}
	ret.semanticVersion = cv
	if ret.stable && len(status.History) > 1 {
		previous := status.History[1]
		pv, err := semver.Make(previous.Version)
		if err != nil {
			return nil, fmt.Errorf("failed to determine semantic version: %s", previous.Version)
		}
		if cv.Minor > pv.Minor {
			// soak a week after a Y-stream upgrade
			ret.stable = time.Since(current.CompletionTime.Time) > 7*24*time.Hour
			ret.stableDuration = "7 days"
		}
	}
	return ret, nil
}

func clusterVersion(ctx context.Context, clusterName string, Client ctrlruntimeclient.Reader) (*versionInfo, error) {
	cv := &configv1.ClusterVersion{}
	if err := Client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: "version"}, cv); err != nil {
		return nil, fmt.Errorf("failed to get ClusterVersion version on %s: %w", clusterName, err)
	}
	return newVersionInfo(cv.Status)
}

func upgradeBuild02(ctx context.Context, build01Client, build02Client ctrlruntimeclient.Reader) (*versionInfo, error) {
	build01VI, err := clusterVersion(ctx, "build01", build01Client)
	if err != nil {
		return nil, err
	}
	if !build01VI.stable {
		logrus.WithField("build01Version", build01VI.version).Info("The version on build01 has not been stable enough and hence no need to upgrade build02")
		return nil, nil
	}

	build02VI, err := clusterVersion(ctx, "build02", build02Client)
	if err != nil {
		return nil, err
	}
	if build02VI.state != configv1.CompletedUpdate {
		logrus.WithField("state", build02VI.state).Info("The previous upgrade of build02 has not been completed")
		return nil, nil
	}
	if build02VI.semanticVersion.Equals(build01VI.semanticVersion) {
		logrus.WithField("version", build01VI.version).Info("build01 and build02 have the same version and hence no need to upgrade build02")
		return nil, nil
	}

	if build02VI.semanticVersion.GT(build01VI.semanticVersion) {
		return nil, fmt.Errorf("version of build02 %s is newer than build01 %s", build02VI.version, build01VI.version)
	}
	return build01VI, nil
}

func sendTriageBuild02Upgrade(slackClient *slack.Client, version, stableDuration string) error {
	blocks := []slack.Block{
		&slack.HeaderBlock{
			Type: slack.MBTHeader,
			Text: &slack.TextBlockObject{
				Type: slack.PlainTextType,
				Text: "Upgrade Build02",
			},
		},
		&slack.SectionBlock{
			Type: slack.MBTSection,
			Text: &slack.TextBlockObject{
				Type: slack.PlainTextType,
				// Ideally, we could just run the upgrade command and notify triage via slack
				// In reality we still need some manual checks before upgrading build02
				Text: fmt.Sprintf("@%s Version %s is stable on `build01` for %s. Please upgrade `build02` if `build02` is healthy: `oc --as system:admin --context build02 adm upgrade --to=%s`",
					userGroupTriage, version, stableDuration, version),
			},
		},
	}

	channelID, err := channelID(slackClient, dptpOpsChannel, publicChannelType)
	if err != nil {
		return fmt.Errorf("failed for get channel ID for %s", dptpOpsChannel)
	}
	responseChannel, responseTimestamp, err := slackClient.PostMessage(channelID, slack.MsgOptionBlocks(blocks...))
	if err != nil {
		return fmt.Errorf("failed to message @dptp-triage: %w", err)
	}

	logrus.Infof("Posted message to triage in channel %s at %s", responseChannel, responseTimestamp)
	return nil
}

func blockForIssue(issue jiraapi.Issue) slack.Block {
	// we really don't want these things to line wrap, so truncate the summary
	cutoff := 85
	summary := issue.Fields.Summary
	if len(summary) > cutoff {
		summary = summary[0:cutoff-3] + "..."
	}
	return &slack.ContextBlock{
		Type: slack.MBTContext,
		ContextElements: slack.ContextElements{
			Elements: []slack.MixedElement{
				&slack.TextBlockObject{
					Type: slack.MarkdownType,
					Text: fmt.Sprintf("<https://issues.redhat.com/browse/%s|*%s*>: %s", issue.Key, issue.Key, summary),
				},
			},
		},
	}
}

const (
	userGroupTriage   = "dptp-triage"
	userGroupHelpdesk = "dptp-helpdesk"
)

func ensureGroupMembership(client *slack.Client, userIdsByRole map[string]string) error {
	groups, err := client.GetUserGroups(slack.GetUserGroupsOptionIncludeUsers(true))
	if err != nil {
		return fmt.Errorf("could not query Slack for groups: %w", err)
	}
	groupsByHandle := map[string]slack.UserGroup{}
	for i := range groups {
		groupsByHandle[groups[i].Handle] = groups[i]
	}
	for role, handle := range map[string]string{
		roleTriagePrimary: userGroupTriage,
		roleHelpdesk:      userGroupHelpdesk,
	} {
		group, found := groupsByHandle[handle]
		if !found {
			return fmt.Errorf("could not find user group %s", handle)
		}

		if expected, actual := sets.NewString(userIdsByRole[role]), sets.NewString(group.Users...); !expected.Equal(actual) {
			if _, err := client.UpdateUserGroupMembers(group.ID, strings.Join(expected.List(), ",")); err != nil {
				return fmt.Errorf("failed to update group %s: %w", handle, err)
			}
		}
	}
	return nil
}
