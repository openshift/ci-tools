package main

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/github"

	"github.com/openshift/ci-tools/pkg/api"
)

type fakeReporterGithubClient struct {
	checkRuns map[string]github.CheckRun
}

func (c *fakeReporterGithubClient) CreateCheckRun(org, repo string, checkRun github.CheckRun) (int64, error) {
	id := int64(len(c.checkRuns) + 1)
	checkRun.ID = id
	c.checkRuns[fmt.Sprintf("%s/%s-%d", org, repo, id)] = checkRun
	return int64(len(c.checkRuns)), nil
}

func (c *fakeReporterGithubClient) UpdateCheckRun(org, repo string, checkRunId int64, checkRun github.CheckRun) error {
	existing := c.checkRuns[fmt.Sprintf("%s/%s-%d", org, repo, checkRunId)]
	existing.Conclusion = checkRun.Conclusion
	existing.Status = checkRun.Status
	c.checkRuns[fmt.Sprintf("%s/%s-%d", org, repo, checkRunId)] = existing
	return nil
}

func TestReportNewProwJob(t *testing.T) {
	testCases := []struct {
		name             string
		jobRun           jobRun
		prowJob          *prowv1.ProwJob
		expectedCheckRun github.CheckRun
		expectedConfig   *Config
		expectedErr      error
	}{
		{
			name: "report a prow job",
			jobRun: jobRun{
				JobMetadata: api.MetadataWithTest{
					Metadata: api.Metadata{
						Org:    "openshift",
						Repo:   "ci-tools",
						Branch: "master",
					},
					Test: "unit",
				},
				OriginPR: github.PullRequest{
					Base: github.PullRequestBranch{
						Repo: github.Repo{
							Owner: github.User{Login: "openshift"},
							Name:  "ci-tools",
						},
					},
					Head: github.PullRequestBranch{
						SHA: "HEAD-SHA",
					},
					Number: 999,
				},
				AdditionalPRs: []github.PullRequest{
					{
						Base: github.PullRequestBranch{
							Repo: github.Repo{
								Owner: github.User{Login: "openshift"},
								Name:  "ci-tools",
							},
						},
						Number: 123,
					},
				},
			},
			prowJob: &prowv1.ProwJob{
				TypeMeta: metav1.TypeMeta{Kind: "ProwJob", APIVersion: "prow.k8s.io/v1"},
				ObjectMeta: metav1.ObjectMeta{
					Name:              "aaaa-bbbbb-cccc",
					Namespace:         "ci",
					CreationTimestamp: metav1.Time{Time: time.Date(2024, 1, 0, 0, 0, 0, 0, time.UTC)},
				},
				Spec: prowv1.ProwJobSpec{Job: "some-prow-job"},
				Status: prowv1.ProwJobStatus{
					State: "triggered",
					URL:   "https://deck.prow.com",
				},
			},
			expectedConfig: &Config{Jobs: []Job{
				{
					ProwJobID: "aaaa-bbbbb-cccc",
					CheckRunDetails: CheckRunDetails{
						ID:    1,
						Title: "some-prow-job",
						Text: `[Job logs and status](https://deck.prow.com)
Included PRs: 
* openshift/ci-tools#123
`,
					},
					Org:       "openshift",
					Repo:      "ci-tools",
					CreatedAt: time.Date(2024, 1, 0, 0, 0, 0, 0, time.UTC),
				},
			}},
			expectedCheckRun: github.CheckRun{
				ID:      1,
				HeadSHA: "HEAD-SHA",
				Status:  "in_progress",
				Output: github.CheckRunOutput{
					Title:   "some-prow-job",
					Summary: "Job Triggered",
					Text: `[Job logs and status](https://deck.prow.com)
Included PRs: 
* openshift/ci-tools#123
`,
				},
				Name: "some-prow-job",
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			jobConfigFile := t.TempDir() + "/prowjob.json"
			if err := os.WriteFile(jobConfigFile, []byte{}, 0666); err != nil {
				t.Fatalf("failed to write job config file to set up test: %v", err)
			}

			kubeClient := fakectrlruntimeclient.NewClientBuilder().WithObjects(tc.prowJob).Build()
			fghc := fakeReporterGithubClient{checkRuns: make(map[string]github.CheckRun)}
			r := reporter{
				kubeClient:    kubeClient,
				ghc:           &fghc,
				namespace:     "ci",
				jobConfigFile: jobConfigFile,
			}

			err := r.reportNewProwJob(tc.prowJob, tc.jobRun, logrus.NewEntry(logrus.StandardLogger()))
			if diff := cmp.Diff(tc.expectedErr, err); diff != "" {
				t.Fatalf("reportNewProwJob returned unexpected error (-want +got): %v", diff)
			}

			config, err := r.getConfig()
			if err != nil {
				t.Fatalf("failed to get config: %v", err)
			}
			if diff := cmp.Diff(config, tc.expectedConfig); diff != "" {
				t.Fatalf("config doesn't match expected (-want +got): %v", diff)
			}

			checkRunKey := fmt.Sprintf("%s/%s-%d", tc.jobRun.OriginPR.Base.Repo.Owner.Login, tc.jobRun.OriginPR.Base.Repo.Name, 1)
			if diff := cmp.Diff(fghc.checkRuns[checkRunKey], tc.expectedCheckRun); diff != "" {
				t.Fatalf("checkRun doesn't match expected for prowJob (-want +got): %v", diff)
			}
		})
	}
}

func TestSync(t *testing.T) {
	tenMinutesAgo := time.Now().Add(-10 * time.Minute)
	twentyMinutesAgo := time.Now().Add(-20 * time.Minute)
	thirtyMinutesAgo := time.Now().Add(-30 * time.Minute)
	twoDaysAgo := time.Now().Add(-48 * time.Hour)
	testCases := []struct {
		name              string
		prowJobs          []ctrlruntimeclient.Object
		initialConfig     *Config
		initialCheckRuns  map[string]github.CheckRun
		expectedConfig    *Config
		expectedCheckRuns map[string]github.CheckRun
	}{
		{
			name: "sync existing jobs",
			prowJobs: []ctrlruntimeclient.Object{
				&prowv1.ProwJob{
					TypeMeta: metav1.TypeMeta{Kind: "ProwJob", APIVersion: "prow.k8s.io/v1"},
					ObjectMeta: metav1.ObjectMeta{
						Name:              "aaaa-bbbbb-cccc",
						Namespace:         "ci",
						CreationTimestamp: metav1.Time{Time: tenMinutesAgo},
					},
					Spec: prowv1.ProwJobSpec{Job: "pending-prow-job"},
					Status: prowv1.ProwJobStatus{
						State: "triggered",
						URL:   "https://deck.prow.com",
					},
				},
				&prowv1.ProwJob{
					TypeMeta: metav1.TypeMeta{Kind: "ProwJob", APIVersion: "prow.k8s.io/v1"},
					ObjectMeta: metav1.ObjectMeta{
						Name:              "aaaa-bbbbb-dddd",
						Namespace:         "ci",
						CreationTimestamp: metav1.Time{Time: twentyMinutesAgo},
					},
					Spec: prowv1.ProwJobSpec{Job: "successful-prow-job"},
					Status: prowv1.ProwJobStatus{
						State: "success",
						URL:   "https://deck.prow.com",
					},
				},
				&prowv1.ProwJob{
					TypeMeta: metav1.TypeMeta{Kind: "ProwJob", APIVersion: "prow.k8s.io/v1"},
					ObjectMeta: metav1.ObjectMeta{
						Name:              "aaaa-bbbbb-eeeeeee",
						Namespace:         "ci",
						CreationTimestamp: metav1.Time{Time: thirtyMinutesAgo},
					},
					Spec: prowv1.ProwJobSpec{Job: "failed-prow-job"},
					Status: prowv1.ProwJobStatus{
						State: "failure",
						URL:   "https://deck.prow.com",
					},
				},
			},
			initialConfig: &Config{
				Jobs: []Job{
					{
						ProwJobID: "aaaa-bbbbb-cccc",
						CheckRunDetails: CheckRunDetails{
							ID:    1,
							Title: "pending-prow-job",
							Text: `[Job logs and status](https://deck.prow.com)
Included PRs: 
* openshift/ci-tools#123
`,
						},
						Org:       "openshift",
						Repo:      "ci-tools",
						CreatedAt: tenMinutesAgo,
					},
					{
						ProwJobID: "aaaa-bbbbb-dddd",
						CheckRunDetails: CheckRunDetails{
							ID:    2,
							Title: "successful-prow-job",
							Text: `[Job logs and status](https://deck.prow.com)
Included PRs: 
* openshift/ci-tools#123
`,
						},
						Org:       "openshift",
						Repo:      "ci-tools",
						CreatedAt: twentyMinutesAgo,
					},
					{
						ProwJobID: "aaaa-bbbbb-eeeeeee",
						CheckRunDetails: CheckRunDetails{
							ID:    3,
							Title: "failed-prow-job",
							Text: `[Job logs and status](https://deck.prow.com)
Included PRs: 
* openshift/ci-tools#123
`,
						},
						Org:       "openshift",
						Repo:      "ci-tools",
						CreatedAt: thirtyMinutesAgo,
					},
				},
			},
			expectedConfig: &Config{
				Jobs: []Job{
					{
						ProwJobID: "aaaa-bbbbb-cccc",
						CheckRunDetails: CheckRunDetails{
							ID:    1,
							Title: "pending-prow-job",
							Text: `[Job logs and status](https://deck.prow.com)
Included PRs: 
* openshift/ci-tools#123
`,
						},
						Org:       "openshift",
						Repo:      "ci-tools",
						CreatedAt: tenMinutesAgo,
					},
				},
			},
			initialCheckRuns: map[string]github.CheckRun{
				"openshift/ci-tools-1": {
					ID:      1,
					HeadSHA: "HEAD-SHA",
					Status:  "in_progress",
					Output: github.CheckRunOutput{
						Title:   "pending-prow-job",
						Summary: "Job Triggered",
						Text: `[Job logs and status](https://deck.prow.com)
Included PRs: 
* openshift/ci-tools#123
`,
					},
					Name: "pending-prow-job",
				},
				"openshift/ci-tools-2": {
					ID:      2,
					HeadSHA: "HEAD-SHA",
					Status:  "in_progress",
					Output: github.CheckRunOutput{
						Title:   "successful-prow-job",
						Summary: "Job Triggered",
						Text: `[Job logs and status](https://deck.prow.com)
Included PRs: 
* openshift/ci-tools#123
`,
					},
					Name: "successful-prow-job",
				},
				"openshift/ci-tools-3": {
					ID:      3,
					HeadSHA: "HEAD-SHA",
					Status:  "in_progress",
					Output: github.CheckRunOutput{
						Title:   "failed-prow-job",
						Summary: "Job Triggered",
						Text: `[Job logs and status](https://deck.prow.com)
Included PRs: 
* openshift/ci-tools#123
`,
					},
					Name: "failed-prow-job",
				},
			},
			expectedCheckRuns: map[string]github.CheckRun{
				"openshift/ci-tools-1": {
					ID:      1,
					HeadSHA: "HEAD-SHA",
					Status:  "in_progress",
					Output: github.CheckRunOutput{
						Title:   "pending-prow-job",
						Summary: "Job Triggered",
						Text: `[Job logs and status](https://deck.prow.com)
Included PRs: 
* openshift/ci-tools#123
`,
					},
					Name: "pending-prow-job",
				},
				"openshift/ci-tools-2": {
					ID:         2,
					HeadSHA:    "HEAD-SHA",
					Conclusion: "success",
					Output: github.CheckRunOutput{
						Title:   "successful-prow-job",
						Summary: "Job Triggered",
						Text: `[Job logs and status](https://deck.prow.com)
Included PRs: 
* openshift/ci-tools#123
`,
					},
					Name: "successful-prow-job",
				},
				"openshift/ci-tools-3": {
					ID:         3,
					HeadSHA:    "HEAD-SHA",
					Conclusion: "failure",
					Output: github.CheckRunOutput{
						Title:   "failed-prow-job",
						Summary: "Job Triggered",
						Text: `[Job logs and status](https://deck.prow.com)
Included PRs: 
* openshift/ci-tools#123
`,
					},
					Name: "failed-prow-job",
				},
			},
		},
		{
			name: "old job removed",
			prowJobs: []ctrlruntimeclient.Object{
				&prowv1.ProwJob{
					TypeMeta: metav1.TypeMeta{Kind: "ProwJob", APIVersion: "prow.k8s.io/v1"},
					ObjectMeta: metav1.ObjectMeta{
						Name:              "aaaa-bbbbb-cccc",
						Namespace:         "ci",
						CreationTimestamp: metav1.Time{Time: twoDaysAgo},
					},
					Spec: prowv1.ProwJobSpec{Job: "pending-prow-job"},
					Status: prowv1.ProwJobStatus{
						State: "triggered",
						URL:   "https://deck.prow.com",
					},
				},
			},
			initialConfig: &Config{
				Jobs: []Job{
					{
						ProwJobID: "aaaa-bbbbb-cccc",
						CheckRunDetails: CheckRunDetails{
							ID:    1,
							Title: "pending-prow-job",
							Text: `[Job logs and status](https://deck.prow.com)
Included PRs: 
* openshift/ci-tools#123
`,
						},
						Org:       "openshift",
						Repo:      "ci-tools",
						CreatedAt: twoDaysAgo,
					},
				},
			},
			expectedConfig: &Config{Jobs: []Job{}},
		},
		{
			name:     "not found job removed",
			prowJobs: []ctrlruntimeclient.Object{},
			initialConfig: &Config{
				Jobs: []Job{
					{
						ProwJobID: "aaaa-bbbbb-cccc",
						CheckRunDetails: CheckRunDetails{
							ID:    1,
							Title: "pending-prow-job",
							Text: `[Job logs and status](https://deck.prow.com)
Included PRs: 
* openshift/ci-tools#123
`,
						},
						Org:       "openshift",
						Repo:      "ci-tools",
						CreatedAt: thirtyMinutesAgo,
					},
				},
			},
			expectedConfig: &Config{Jobs: []Job{}},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			jobConfigFile := t.TempDir() + "/prowjob.json"
			kubeClient := fakectrlruntimeclient.NewClientBuilder().WithObjects(tc.prowJobs...).Build()
			fghc := fakeReporterGithubClient{checkRuns: tc.initialCheckRuns}
			r := reporter{
				kubeClient:    kubeClient,
				ghc:           &fghc,
				namespace:     "ci",
				jobConfigFile: jobConfigFile,
			}

			err := r.ensureJobConfigFile()
			if err != nil {
				t.Fatalf("failed to ensure job config file: %v", err)
			}
			logger := logrus.NewEntry(logrus.StandardLogger())
			if err := r.updateConfig(tc.initialConfig, logger); err != nil {
				t.Fatalf("failed to initialize config: %v", err)
			}

			err = r.sync(logger)
			if err != nil {
				t.Fatalf("failed to sync: %v", err)
			}

			config, err := r.getConfig()
			if err != nil {
				t.Fatalf("failed to get config after sync: %v", err)
			}
			if diff := cmp.Diff(tc.expectedConfig, config); diff != "" {
				t.Errorf("resulting config does not match expected (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.expectedCheckRuns, fghc.checkRuns); diff != "" {
				t.Errorf("resulting checkRuns don't match expected (-want +got):\n%s", diff)
			}
		})
	}
}
