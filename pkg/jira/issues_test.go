package jira

import (
	"errors"
	"testing"

	"github.com/andygrunwald/go-jira"
	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
)

type userResponse struct {
	user *slack.User
	err  error
}

type fakeSlackClient struct {
	userBehavior  map[string]userResponse
	unwantedUsers []string
}

func (f *fakeSlackClient) GetUserInfo(user string) (*slack.User, error) {
	response, registered := f.userBehavior[user]
	if !registered {
		f.unwantedUsers = append(f.unwantedUsers, user)
		return nil, errors.New("no such user behavior in fake")
	}
	delete(f.userBehavior, user)
	return response.user, response.err
}

func (f *fakeSlackClient) Validate(t *testing.T) {
	for user := range f.userBehavior {
		t.Errorf("fake info getter did not get user request: %v", user)
	}
	for _, user := range f.unwantedUsers {
		t.Errorf("fake info getter got unwanted user request: %v", user)
	}
}

type searchResponse struct {
	users []jira.User
	err   error
}

type fakeJiraClient struct {
	searchBehavior  map[string]searchResponse
	unwantedSeaches []string
}

func (f *fakeJiraClient) FindUser(property string) ([]jira.User, *jira.Response, error) {
	response, registered := f.searchBehavior[property]
	if !registered {
		f.unwantedSeaches = append(f.unwantedSeaches, property)
		return nil, nil, errors.New("no such user search behavior in fake")
	}
	delete(f.searchBehavior, property)
	return response.users, nil, response.err
}

func (f *fakeJiraClient) CreateIssue(issue *jira.Issue) (*jira.Issue, *jira.Response, error) {
	return nil, nil, errors.New("not implemented")
}

func (f *fakeJiraClient) Validate(t *testing.T) {
	for user := range f.searchBehavior {
		t.Errorf("fake did not get search: %v", user)
	}
	for _, user := range f.unwantedSeaches {
		t.Errorf("fake got unwanted search: %v", user)
	}
}

func TestResolveRequester(t *testing.T) {
	var testCases = []struct {
		name           string
		filer          filer
		reporter       string
		expectedSuffix string
		expectedUser   *jira.User
	}{
		{
			name: "all calls work",
			filer: filer{
				slackClient: &fakeSlackClient{
					userBehavior: map[string]userResponse{
						"skuznets": {user: &slack.User{RealName: "Steve Kuznetsov", ID: "slackIdentifier"}},
					},
					unwantedUsers: []string{},
				},
				jiraClient: &fakeJiraClient{
					searchBehavior: map[string]searchResponse{
						"Steve Kuznetsov": {users: []jira.User{{AccountID: "jiraIdentifier"}}},
					},
					unwantedSeaches: []string{},
				},
			},
			reporter:       "skuznets",
			expectedSuffix: "Slack user [Steve Kuznetsov|https://coreos.slack.com/team/slackIdentifier]",
			expectedUser:   &jira.User{AccountID: "jiraIdentifier"},
		},
		{
			name: "can get Slack user but no Jira search results",
			filer: filer{
				slackClient: &fakeSlackClient{
					userBehavior: map[string]userResponse{
						"skuznets": {user: &slack.User{RealName: "Steve Kuznetsov", ID: "slackIdentifier"}},
					},
					unwantedUsers: []string{},
				},
				jiraClient: &fakeJiraClient{
					searchBehavior: map[string]searchResponse{
						"Steve Kuznetsov": {users: []jira.User{}},
					},
					unwantedSeaches: []string{},
				},
				botUser: &jira.User{AccountID: "jiraBotIdentifier"},
			},
			reporter:       "skuznets",
			expectedSuffix: "Slack user [Steve Kuznetsov|https://coreos.slack.com/team/slackIdentifier]",
			expectedUser:   &jira.User{AccountID: "jiraBotIdentifier"},
		},
		{
			name: "can get Slack user but not Jira search",
			filer: filer{
				slackClient: &fakeSlackClient{
					userBehavior: map[string]userResponse{
						"skuznets": {user: &slack.User{RealName: "Steve Kuznetsov", ID: "slackIdentifier"}},
					},
					unwantedUsers: []string{},
				},
				jiraClient: &fakeJiraClient{
					searchBehavior: map[string]searchResponse{
						"Steve Kuznetsov": {err: errors.New("oops")},
					},
					unwantedSeaches: []string{},
				},
				botUser: &jira.User{AccountID: "jiraBotIdentifier"},
			},
			reporter:       "skuznets",
			expectedSuffix: "Slack user [Steve Kuznetsov|https://coreos.slack.com/team/slackIdentifier]",
			expectedUser:   &jira.User{AccountID: "jiraBotIdentifier"},
		},
		{
			name: "can't get Slack user",
			filer: filer{
				slackClient: &fakeSlackClient{
					userBehavior: map[string]userResponse{
						"skuznets": {err: errors.New("oops")},
					},
					unwantedUsers: []string{},
				},
				botUser: &jira.User{AccountID: "jiraBotIdentifier"},
			},
			reporter:       "skuznets",
			expectedSuffix: "[a Slack user|https://coreos.slack.com/team/skuznets]",
			expectedUser:   &jira.User{AccountID: "jiraBotIdentifier"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			suffix, user := testCase.filer.resolveRequester(testCase.reporter, logrus.WithField("test", testCase.name))
			if diff := cmp.Diff(testCase.expectedSuffix, suffix); diff != "" {
				t.Errorf("%s: did not get correct suffix: %v", testCase.name, diff)
			}
			if diff := cmp.Diff(testCase.expectedUser, user); diff != "" {
				t.Errorf("%s: did not get correct user: %v", testCase.name, diff)
			}
		})
	}
}
