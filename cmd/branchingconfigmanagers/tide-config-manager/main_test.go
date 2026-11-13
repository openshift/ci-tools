package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/afero"

	"k8s.io/apimachinery/pkg/util/sets"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/api/shardprowconfig"
	"github.com/openshift/ci-tools/pkg/config"
)

func prepareProwConfig(repos, labels, branches []string) string {
	return prepareProwConfigWithExcludedBranches(repos, labels, branches, []string{})
}

func prepareProwConfigWithExcludedBranches(repos, labels, branches, excludedBranches []string) string {
	return prepareProwQueries(prowconfig.TideQueries{
		{
			Repos:            repos,
			Labels:           labels,
			IncludedBranches: branches,
			ExcludedBranches: excludedBranches,
		},
	})
}

// prepareProwQueries serializes expected sharded queries without empty config sections.
func prepareProwQueries(queries prowconfig.TideQueries) string {
	config := &shardprowconfig.ProwConfigWithPointers{Tide: &shardprowconfig.TideConfig{Queries: queries}}
	bytes, err := yaml.Marshal(config)
	if err != nil {
		panic("Error marshaling prow config for comparison")
	}
	return string(bytes)
}

// reconcileArgs identifies the lifecycle event and input configuration under test.
type reconcileArgs struct {
	event  string
	config *prowconfig.ProwConfig
}

// reconcileCase describes expected shard contents and optional GA policy fixtures.
type reconcileCase struct {
	name               string
	currentRelease     string
	args               reconcileArgs
	inputQueries       prowconfig.TideQueries
	expectedQueries    prowconfig.TideQueries
	excluded           excludedRepos
	wantErr            bool
	expectedShardFiles map[string]string
}

func TestReconcile(t *testing.T) {
	path := "openshift/dummy/_prowconfig.yaml"
	repos := []string{"openshift/dummy"}
	type args = reconcileArgs
	tests := []reconcileCase{
		{
			name:           "5.0 shared query transitions only nonexempt repo and preserves org policy",
			currentRelease: "5.0",
			excluded:       excludedRepos{ExcludedAllowList: []string{"openshift/exempt"}},
			inputQueries: prowconfig.TideQueries{
				{Orgs: []string{"openshift"}, Repos: []string{"openshift/dummy", "openshift/exempt"}, Labels: []string{backportRiskAssessed}, IncludedBranches: []string{"release-5.0"}},
				{Repos: []string{"openshift/other"}, ExcludedBranches: []string{"release-4.22"}},
			},
			expectedShardFiles: map[string]string{
				"openshift/_prowconfig.yaml": prepareProwQueries(prowconfig.TideQueries{
					{Orgs: []string{"openshift"}, Labels: []string{backportRiskAssessed}, IncludedBranches: []string{"release-5.0"}},
				}),
				path: prepareProwQueries(prowconfig.TideQueries{
					{Repos: repos, Labels: []string{backportRiskAssessed}, IncludedBranches: []string{"release-5.0"}},
					{Repos: repos, Labels: []string{backportRiskAssessed, staffEngApproved}, IncludedBranches: []string{"release-5.1"}},
				}),
				"openshift/exempt/_prowconfig.yaml": prepareProwQueries(prowconfig.TideQueries{
					{Repos: []string{"openshift/exempt"}, Labels: []string{backportRiskAssessed}, IncludedBranches: []string{"release-5.0"}},
				}),
				"openshift/other/_prowconfig.yaml": prepareProwQueries(prowconfig.TideQueries{
					{Repos: []string{"openshift/other"}, ExcludedBranches: []string{"release-4.22"}},
				}),
			},
		},
		{
			name:           "5.0 public GA directly from branching preserves modern labels main and noFF",
			currentRelease: "5.0",
			inputQueries: prowconfig.TideQueries{
				{Repos: repos, Labels: []string{"approved", backportRiskAssessed, validBug, "jira/valid-reference", "lgtm", verified}, IncludedBranches: []string{"release-4.22", "openshift-4.22"}},
				{Repos: repos, Labels: []string{"approved", backportRiskAssessed, validBug, "jira/valid-reference", "lgtm", verified}, IncludedBranches: []string{"release-5.0", "openshift-5.0"}},
				{Repos: repos, Labels: []string{"approved", "lgtm", verified}, IncludedBranches: []string{"main", "master"}},
				{Repos: repos, Labels: []string{"lgtm"}, IncludedBranches: []string{"noFF"}},
			},
			expectedQueries: prowconfig.TideQueries{
				{Repos: repos, Labels: []string{"approved", backportRiskAssessed, validBug, "jira/valid-reference", "lgtm", verified}, IncludedBranches: []string{"openshift-4.22", "openshift-5.0", "release-4.22", "release-5.0"}},
				{Repos: repos, Labels: []string{"approved", backportRiskAssessed, validBug, "jira/valid-reference", "lgtm", staffEngApproved, verified}, IncludedBranches: []string{"openshift-5.1", "release-5.1"}},
				{Repos: repos, Labels: []string{"approved", "lgtm", verified}, IncludedBranches: []string{"main", "master"}},
				{Repos: repos, Labels: []string{"lgtm"}, IncludedBranches: []string{"noFF"}},
			},
		},
		{
			name:           "5.0 private preGA inherits weaker historical past policy",
			currentRelease: "5.0",
			inputQueries: prowconfig.TideQueries{
				{Repos: repos, Labels: []string{"approved", "lgtm"}, IncludedBranches: []string{"release-4.22"}},
				{Repos: repos, Labels: []string{"approved", backportRiskAssessed, "lgtm", staffEngApproved}, IncludedBranches: []string{"release-5.0"}},
			},
			expectedQueries: prowconfig.TideQueries{
				{Repos: repos, Labels: []string{"approved", "lgtm"}, IncludedBranches: []string{"release-4.22", "release-5.0"}},
				{Repos: repos, Labels: []string{"approved", backportRiskAssessed, "lgtm", staffEngApproved}, IncludedBranches: []string{"release-5.1"}},
			},
		},
		{
			name:           "5.0 multiple approval alternatives remain separate families",
			currentRelease: "5.0",
			inputQueries: prowconfig.TideQueries{
				{Repos: repos, Labels: []string{"approved", "lgtm"}, IncludedBranches: []string{"release-4.22"}},
				{Repos: repos, Labels: []string{"lgtm"}, MissingLabels: []string{"needs-approval"}, IncludedBranches: []string{"release-4.22"}},
				{Repos: repos, Labels: []string{"approved", backportRiskAssessed, "lgtm"}, IncludedBranches: []string{"release-5.0"}},
				{Repos: repos, Labels: []string{backportRiskAssessed, "lgtm"}, MissingLabels: []string{"needs-approval"}, IncludedBranches: []string{"release-5.0"}},
			},
			expectedQueries: prowconfig.TideQueries{
				{Repos: repos, Labels: []string{"approved", "lgtm"}, IncludedBranches: []string{"release-4.22", "release-5.0"}},
				{Repos: repos, Labels: []string{"lgtm"}, MissingLabels: []string{"needs-approval"}, IncludedBranches: []string{"release-4.22", "release-5.0"}},
				{Repos: repos, Labels: []string{"approved", backportRiskAssessed, "lgtm", staffEngApproved}, IncludedBranches: []string{"release-5.1"}},
				{Repos: repos, Labels: []string{backportRiskAssessed, "lgtm", staffEngApproved}, MissingLabels: []string{"needs-approval"}, IncludedBranches: []string{"release-5.1"}},
			},
		},
		{
			name:           "5.0 custom labels author and missing labels prevent unsafe past matching",
			currentRelease: "5.0",
			inputQueries: prowconfig.TideQueries{
				{Repos: repos, Labels: []string{"approved", "custom", "lgtm"}, Author: "other", IncludedBranches: []string{"release-4.22"}},
				{Repos: repos, Labels: []string{"approved", backportRiskAssessed, "custom", "lgtm"}, Author: "bot", MissingLabels: []string{"hold"}, IncludedBranches: []string{"release-5.0"}},
			},
			expectedQueries: prowconfig.TideQueries{
				{Repos: repos, Labels: []string{"approved", "custom", "lgtm"}, Author: "other", IncludedBranches: []string{"release-4.22"}},
				{Repos: repos, Labels: []string{"approved", backportRiskAssessed, "custom", "lgtm"}, Author: "bot", MissingLabels: []string{"hold"}, IncludedBranches: []string{"release-5.0"}},
				{Repos: repos, Labels: []string{"approved", backportRiskAssessed, "custom", "lgtm", staffEngApproved}, Author: "bot", MissingLabels: []string{"hold"}, IncludedBranches: []string{"release-5.1"}},
			},
		},
		{
			name:           "5.0 complement fills alias gaps and retains later exclusions",
			currentRelease: "5.0",
			inputQueries: prowconfig.TideQueries{
				{Repos: repos, Labels: []string{"approved", "lgtm"}, IncludedBranches: []string{"release-4.22"}},
				{Repos: repos, Labels: []string{"approved", backportRiskAssessed, "lgtm"}, IncludedBranches: []string{"release-5.0", "openshift-5.0"}},
				{Repos: repos, Labels: []string{"lgtm"}, ExcludedBranches: []string{"openshift-4.22", "release-4.23", "release-5.1"}},
			},
			expectedQueries: prowconfig.TideQueries{
				{Repos: repos, Labels: []string{"approved", "lgtm"}, IncludedBranches: []string{"openshift-5.0", "release-4.22", "release-5.0"}},
				{Repos: repos, Labels: []string{"approved", backportRiskAssessed, "lgtm", staffEngApproved}, IncludedBranches: []string{"openshift-5.1", "release-5.1"}},
				{Repos: repos, Labels: []string{"lgtm"}, ExcludedBranches: []string{"openshift-4.22", "openshift-5.0", "openshift-5.1", "release-4.23", "release-5.0", "release-5.1"}},
			},
		},
		{
			name:           "5.0 existing future query is strengthened not duplicated",
			currentRelease: "5.0",
			inputQueries: prowconfig.TideQueries{
				{Repos: repos, Labels: []string{"lgtm"}, IncludedBranches: []string{"release-4.22"}},
				{Repos: repos, Labels: []string{backportRiskAssessed, "lgtm"}, IncludedBranches: []string{"release-5.0"}},
				{Repos: repos, Labels: []string{"lgtm", staffEngApproved}, IncludedBranches: []string{"release-5.1"}},
			},
			expectedQueries: prowconfig.TideQueries{
				{Repos: repos, Labels: []string{"lgtm"}, IncludedBranches: []string{"release-4.22", "release-5.0"}},
				{Repos: repos, Labels: []string{backportRiskAssessed, "lgtm", staffEngApproved}, IncludedBranches: []string{"release-5.1"}},
			},
		},
		{
			name:           "5.0 duplicate future aliases are consolidated without a weaker OR path",
			currentRelease: "5.0",
			inputQueries: prowconfig.TideQueries{
				{Repos: repos, Labels: []string{"lgtm"}, IncludedBranches: []string{"release-4.22"}},
				{Repos: repos, Labels: []string{backportRiskAssessed, "lgtm"}, IncludedBranches: []string{"release-5.0"}},
				{Repos: repos, Labels: []string{"lgtm", staffEngApproved}, IncludedBranches: []string{"release-5.1"}},
				{Repos: repos, Labels: []string{"lgtm"}, IncludedBranches: []string{"openshift-5.1", "release-5.1"}},
				{Repos: repos, Labels: []string{"lgtm"}, ExcludedBranches: []string{"release-4.22"}},
			},
			expectedQueries: prowconfig.TideQueries{
				{Repos: repos, Labels: []string{"lgtm"}, IncludedBranches: []string{"release-4.22", "release-5.0"}},
				{Repos: repos, Labels: []string{backportRiskAssessed, "lgtm", staffEngApproved}, IncludedBranches: []string{"openshift-5.1", "release-5.1"}},
				{Repos: repos, Labels: []string{"lgtm"}, ExcludedBranches: []string{"openshift-5.1", "release-4.22", "release-5.0", "release-5.1"}},
			},
		},
		{
			name:           "5.0 main-only complement excludes transitioned branches and preserves selectors",
			currentRelease: "5.0",
			inputQueries: prowconfig.TideQueries{
				{Repos: repos, Labels: []string{"lgtm"}, IncludedBranches: []string{"release-4.22"}},
				{Repos: repos, Labels: []string{backportRiskAssessed, "lgtm"}, IncludedBranches: []string{"release-5.0"}},
				{Repos: repos, Labels: []string{"custom", "lgtm"}, MissingLabels: []string{"hold"}, ExcludedBranches: []string{"main"}},
			},
			expectedQueries: prowconfig.TideQueries{
				{Repos: repos, Labels: []string{"lgtm"}, IncludedBranches: []string{"release-4.22", "release-5.0"}},
				{Repos: repos, Labels: []string{backportRiskAssessed, "lgtm", staffEngApproved}, IncludedBranches: []string{"release-5.1"}},
				{Repos: repos, Labels: []string{"custom", "lgtm"}, MissingLabels: []string{"hold"}, ExcludedBranches: []string{"main", "release-5.0", "release-5.1"}},
			},
		},
		{
			name:           "5.0 older-release complement changes but author-filtered exceptions do not",
			currentRelease: "5.0",
			inputQueries: prowconfig.TideQueries{
				{Repos: repos, Labels: []string{"lgtm"}, IncludedBranches: []string{"release-4.22"}},
				{Repos: repos, Labels: []string{backportRiskAssessed, "lgtm"}, IncludedBranches: []string{"release-5.0"}},
				{Repos: repos, Labels: []string{"lgtm"}, ExcludedBranches: []string{"release-4.21"}},
				{Repos: repos, Labels: []string{"lgtm"}, Author: "bot", ExcludedBranches: []string{"release-4.21"}},
				{Repos: repos, Labels: []string{"lgtm"}, Author: "bot"},
			},
			expectedQueries: prowconfig.TideQueries{
				{Repos: repos, Labels: []string{"lgtm"}, IncludedBranches: []string{"release-4.22", "release-5.0"}},
				{Repos: repos, Labels: []string{backportRiskAssessed, "lgtm", staffEngApproved}, IncludedBranches: []string{"release-5.1"}},
				{Repos: repos, Labels: []string{"lgtm"}, ExcludedBranches: []string{"release-4.21", "release-5.0", "release-5.1"}},
				{Repos: repos, Labels: []string{"lgtm"}, Author: "bot", ExcludedBranches: []string{"release-4.21"}},
				{Repos: repos, Labels: []string{"lgtm"}, Author: "bot"},
			},
		},
		{
			name:           "5.0 mixed staff exception is not a lifecycle source",
			currentRelease: "5.0",
			inputQueries: prowconfig.TideQueries{
				{Repos: repos, Labels: []string{backportRiskAssessed, staffEngApproved}, IncludedBranches: []string{"release-4.22", "release-5.0"}},
			},
			expectedQueries: prowconfig.TideQueries{
				{Repos: repos, Labels: []string{backportRiskAssessed, staffEngApproved}, IncludedBranches: []string{"release-4.22", "release-5.0"}},
			},
		},
		{
			name:           "5.0 already transitioned etcd policy is unchanged",
			currentRelease: "5.0",
			inputQueries: prowconfig.TideQueries{
				{Repos: repos, Labels: []string{"approved", backportRiskAssessed, validBug, "lgtm"}, IncludedBranches: []string{"release-4.22", "release-5.0"}},
				{Repos: repos, Labels: []string{"approved", "lgtm"}, IncludedBranches: []string{"release-5.1"}},
			},
			expectedQueries: prowconfig.TideQueries{
				{Repos: repos, Labels: []string{"approved", backportRiskAssessed, validBug, "lgtm"}, IncludedBranches: []string{"release-4.22", "release-5.0"}},
				{Repos: repos, Labels: []string{"approved", "lgtm"}, IncludedBranches: []string{"release-5.1"}},
			},
		},
		{
			name:           "5.0 explicit branch exclusions prevent canonical classification",
			currentRelease: "5.0",
			inputQueries: prowconfig.TideQueries{
				{Repos: repos, Labels: []string{backportRiskAssessed}, IncludedBranches: []string{"release-5.0"}, ExcludedBranches: []string{"release-5.0"}},
			},
			expectedQueries: prowconfig.TideQueries{
				{Repos: repos, Labels: []string{backportRiskAssessed}, IncludedBranches: []string{"release-5.0"}, ExcludedBranches: []string{"release-5.0"}},
			},
		},
		{
			name:            "5.0 excluded allow list leaves source unchanged",
			currentRelease:  "5.0",
			excluded:        excludedRepos{ExcludedAllowList: repos},
			inputQueries:    prowconfig.TideQueries{{Repos: repos, Labels: []string{backportRiskAssessed}, IncludedBranches: []string{"release-5.0"}}},
			expectedQueries: prowconfig.TideQueries{{Repos: repos, Labels: []string{backportRiskAssessed}, IncludedBranches: []string{"release-5.0"}}},
		},
		{
			name:            "5.0 no XY allow list leaves source unchanged",
			currentRelease:  "5.0",
			excluded:        excludedRepos{NoXYAllowList: repos},
			inputQueries:    prowconfig.TideQueries{{Repos: repos, Labels: []string{backportRiskAssessed}, IncludedBranches: []string{"release-5.0"}}},
			expectedQueries: prowconfig.TideQueries{{Repos: repos, Labels: []string{backportRiskAssessed}, IncludedBranches: []string{"release-5.0"}}},
		},
		{
			name: "acknowledge-critical-fixes-only added during special event: ackCriticalFixes",
			args: args{
				event: ackCriticalFixes,
				config: &prowconfig.ProwConfig{Tide: prowconfig.Tide{TideGitHubConfig: prowconfig.TideGitHubConfig{Queries: prowconfig.TideQueries{
					{
						Repos:            repos,
						Labels:           []string{"lgtm", "approved"},
						IncludedBranches: []string{"main", "master"},
					},
				}}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwConfig(repos, []string{ackCriticalFixes, "approved", "lgtm"}, []string{"main", "master"})},
		},
		{
			name: "acknowledge-critical-fixes-only removed during special event: revertCriticalFixes",
			args: args{
				event: revertCriticalFixes,
				config: &prowconfig.ProwConfig{Tide: prowconfig.Tide{TideGitHubConfig: prowconfig.TideGitHubConfig{Queries: prowconfig.TideQueries{
					{
						Repos:            repos,
						Labels:           []string{"lgtm", "approved", ackCriticalFixes},
						IncludedBranches: []string{"main", "master"},
					},
				}}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwConfig(repos, []string{"approved", "lgtm"}, []string{"main", "master"})},
		},
		{
			name: "staff-eng-approved replaced by backport-risk-assesed during branching",
			args: args{
				event: branching,
				config: &prowconfig.ProwConfig{Tide: prowconfig.Tide{TideGitHubConfig: prowconfig.TideGitHubConfig{Queries: prowconfig.TideQueries{
					{
						Repos:            repos,
						Labels:           []string{staffEngApproved},
						IncludedBranches: []string{"openshift-4.9", "release-4.9"},
					},
				}}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwConfig(repos, []string{backportRiskAssessed}, []string{"openshift-4.9", "release-4.9"})},
		},
		{
			name: "staff-eng-approved replaced by backport-risk-assesed during branching - for release",
			args: args{
				event: branching,
				config: &prowconfig.ProwConfig{Tide: prowconfig.Tide{TideGitHubConfig: prowconfig.TideGitHubConfig{Queries: prowconfig.TideQueries{
					{
						Repos:            repos,
						Labels:           []string{staffEngApproved},
						IncludedBranches: []string{"release-4.9"},
					},
				}}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwConfig(repos, []string{backportRiskAssessed}, []string{"release-4.9"})},
		},
		{
			name: "staff-eng-approved added with backport-risk-assesed prserved pre GA",
			args: args{
				event: preGeneralAvailability,
				config: &prowconfig.ProwConfig{Tide: prowconfig.Tide{TideGitHubConfig: prowconfig.TideGitHubConfig{Queries: prowconfig.TideQueries{
					{
						Repos:            repos,
						Labels:           []string{backportRiskAssessed},
						IncludedBranches: []string{"openshift-4.9", "release-4.9"},
					},
				}}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwConfig(repos, []string{backportRiskAssessed, staffEngApproved}, []string{"openshift-4.9", "release-4.9"})},
		},
		{
			name: "staff-eng-approved added with backport-risk-assesed prserved pre GA - only openshift",
			args: args{
				event: preGeneralAvailability,
				config: &prowconfig.ProwConfig{Tide: prowconfig.Tide{TideGitHubConfig: prowconfig.TideGitHubConfig{Queries: prowconfig.TideQueries{
					{
						Repos:            repos,
						Labels:           []string{backportRiskAssessed},
						IncludedBranches: []string{"openshift-4.9"},
					},
				}}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwConfig(repos, []string{backportRiskAssessed, staffEngApproved}, []string{"openshift-4.9"})},
		},
		{
			name: "GA retains backport risk on future and preserves first GA coverage",
			args: args{
				event: GeneralAvailability,
				config: &prowconfig.ProwConfig{Tide: prowconfig.Tide{TideGitHubConfig: prowconfig.TideGitHubConfig{Queries: prowconfig.TideQueries{
					{
						Repos:            repos,
						Labels:           []string{staffEngApproved, backportRiskAssessed},
						IncludedBranches: []string{"openshift-4.9", "release-4.9"},
					},
				}}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwQueries(prowconfig.TideQueries{
					{Repos: repos, Labels: []string{backportRiskAssessed}, IncludedBranches: []string{"openshift-4.9", "release-4.9"}},
					{Repos: repos, Labels: []string{backportRiskAssessed, staffEngApproved}, IncludedBranches: []string{"openshift-4.10", "release-4.10"}},
				})},
		},
		{
			name: "staff-eng-approved changes current to future during GA",
			args: args{
				event: GeneralAvailability,
				config: &prowconfig.ProwConfig{Tide: prowconfig.Tide{TideGitHubConfig: prowconfig.TideGitHubConfig{Queries: prowconfig.TideQueries{
					{
						Repos:            repos,
						Labels:           []string{staffEngApproved},
						IncludedBranches: []string{"openshift-4.9", "release-4.9"},
					},
				}}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwQueries(prowconfig.TideQueries{
					{Repos: repos, Labels: []string{}, IncludedBranches: []string{"openshift-4.9", "release-4.9"}},
					{Repos: repos, Labels: []string{staffEngApproved}, IncludedBranches: []string{"openshift-4.10", "release-4.10"}},
				})},
		},
		{
			name: "complement without a transitioned source is unchanged during GA",
			args: args{
				event: GeneralAvailability,
				config: &prowconfig.ProwConfig{Tide: prowconfig.Tide{TideGitHubConfig: prowconfig.TideGitHubConfig{Queries: prowconfig.TideQueries{
					{
						Repos:            repos,
						ExcludedBranches: []string{"openshift-4.8", "release-4.8"},
					},
				}}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwConfigWithExcludedBranches(
					repos,
					[]string{},
					[]string{},
					[]string{"openshift-4.8", "release-4.8"})},
		},
		{
			name: "no label change during branching - more than 2 branches",
			args: args{
				event: branching,
				config: &prowconfig.ProwConfig{Tide: prowconfig.Tide{TideGitHubConfig: prowconfig.TideGitHubConfig{Queries: prowconfig.TideQueries{
					{
						Repos:            repos,
						Labels:           []string{staffEngApproved},
						IncludedBranches: []string{"openshift-4.9", "release-4.9", "main"},
					},
				}}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwConfig(repos, []string{staffEngApproved}, []string{"openshift-4.9", "release-4.9", "main"})},
		},
		{
			name: "no label change during branching - non-current version branches",
			args: args{
				event: branching,
				config: &prowconfig.ProwConfig{Tide: prowconfig.Tide{TideGitHubConfig: prowconfig.TideGitHubConfig{Queries: prowconfig.TideQueries{
					{
						Repos:            repos,
						Labels:           []string{staffEngApproved},
						IncludedBranches: []string{"openshift-4.8", "release-4.8"},
					},
				}}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwConfig(repos, []string{staffEngApproved}, []string{"openshift-4.8", "release-4.8"})},
		},
		{
			name: "no label change during branching - empty branches",
			args: args{
				event: branching,
				config: &prowconfig.ProwConfig{Tide: prowconfig.Tide{TideGitHubConfig: prowconfig.TideGitHubConfig{Queries: prowconfig.TideQueries{
					{
						Repos:  repos,
						Labels: []string{staffEngApproved},
					},
				}}}},
			},
			wantErr: false,
			expectedShardFiles: map[string]string{
				path: prepareProwConfig(repos, []string{staffEngApproved}, []string{})},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runReconcileCase(t, tt)
		})
	}
}

// runReconcileCase checks generated shards and repeats GA against reloaded output.
func runReconcileCase(t *testing.T, tt reconcileCase) {
	t.Helper()
	repos := []string{"openshift/dummy"}
	currentRelease := "4.9"
	if tt.currentRelease != "" {
		currentRelease = tt.currentRelease
	}
	if tt.inputQueries != nil {
		tt.args.event = GeneralAvailability
		tt.args.config = &prowconfig.ProwConfig{Tide: prowconfig.Tide{TideGitHubConfig: prowconfig.TideGitHubConfig{Queries: tt.inputQueries}}}
		if tt.expectedQueries != nil {
			tt.expectedShardFiles = map[string]string{"openshift/dummy/_prowconfig.yaml": prepareProwQueries(tt.expectedQueries)}
		}
	}
	target := afero.NewMemMapFs()
	if err := reconcile(currentRelease, tt.args.event, tt.args.config, target, tt.excluded, repos, "", "", ""); (err != nil) != tt.wantErr {
		t.Fatalf("reconcile() error = %v, wantErr %v", err, tt.wantErr)
	}
	shardedConfigFiles := readShardFiles(t, target, "")
	if diff := cmp.Diff(tt.expectedShardFiles, shardedConfigFiles); diff != "" {
		t.Fatalf("actual sharded config differs from expected:\n%s", diff)
	}
	if tt.args.event != GeneralAvailability {
		return
	}
	reloaded := &prowconfig.ProwConfig{}
	for _, data := range shardedConfigFiles {
		var shard prowconfig.ProwConfig
		if err := yaml.Unmarshal([]byte(data), &shard); err != nil {
			t.Fatal(err)
		}
		reloaded.Tide.Queries = append(reloaded.Tide.Queries, shard.Tide.Queries...)
	}
	secondTarget := afero.NewMemMapFs()
	if err := reconcile(currentRelease, GeneralAvailability, reloaded, secondTarget, tt.excluded, repos, "", "", ""); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(shardedConfigFiles, readShardFiles(t, secondTarget, "")); diff != "" {
		t.Errorf("GA is not idempotent (-want +got):\n%s", diff)
	}
}

// readShardFiles collects all files in an in-memory shard tree without a capturing walker.
func readShardFiles(t *testing.T, target afero.Fs, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	entries, err := afero.ReadDir(target, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		path := entry.Name()
		if dir != "" {
			path = dir + "/" + entry.Name()
		}
		if entry.IsDir() {
			for name, data := range readShardFiles(t, target, path) {
				files[name] = data
			}
			continue
		}
		data, err := afero.ReadFile(target, path)
		if err != nil {
			t.Fatal(err)
		}
		files[path] = string(data)
	}
	return files
}

func TestReleaseQueryFamily(t *testing.T) {
	tests := []struct {
		name string
		a, b prowconfig.TideQuery
		want bool
	}{
		{
			name: "lifecycle labels branch names and label ordering ignored",
			a:    prowconfig.TideQuery{Labels: []string{"lgtm", "approved", backportRiskAssessed}, IncludedBranches: []string{"release-5.0"}},
			b:    prowconfig.TideQuery{Labels: []string{"approved", "lgtm", staffEngApproved}, IncludedBranches: []string{"release-4.22"}},
			want: true,
		},
		{name: "approval alternatives differ", a: prowconfig.TideQuery{Labels: []string{"approved"}}, want: false},
		{name: "custom labels differ", a: prowconfig.TideQuery{Labels: []string{"custom"}}, want: false},
		{name: "authors differ", a: prowconfig.TideQuery{Author: "bot"}, want: false},
		{name: "missing labels differ", a: prowconfig.TideQuery{MissingLabels: []string{"hold"}}, want: false},
		{name: "excluded branches differ", a: prowconfig.TideQuery{ExcludedBranches: []string{"main"}}, want: false},
		{name: "excluded repos differ", a: prowconfig.TideQuery{ExcludedRepos: []string{"openshift/other"}}, want: false},
		{name: "repos differ", a: prowconfig.TideQuery{Repos: []string{"openshift/other"}}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, releaseQueryFamily(tt.a, tt.b)); diff != "" {
				t.Errorf("unexpected result (-want +got):\n%s", diff)
			}
		})
	}
}

func TestNewVerifiedEvent(t *testing.T) {
	delegate := newSharedDataDelegate()

	tests := []struct {
		name              string
		optInRepos        []string
		optOutRepos       []string
		ciOperatorConfigs map[string]string
		expectedOptIn     []string
		wantErr           bool
	}{
		{
			name:              "opt-in repos only",
			optInRepos:        []string{"openshift/dummy", "openshift/test"},
			optOutRepos:       []string{},
			ciOperatorConfigs: map[string]string{},
			expectedOptIn:     []string{"openshift/dummy", "openshift/test"},
			wantErr:           false,
		},
		{
			name:        "ci-operator configs with ocp promotion",
			optInRepos:  []string{},
			optOutRepos: []string{},
			ciOperatorConfigs: map[string]string{
				"openshift/dummy": "ocp",
				"openshift/test":  "other-namespace",
			},
			expectedOptIn: []string{"openshift/dummy"},
			wantErr:       false,
		},
		{
			name:        "opt-out excludes repos",
			optInRepos:  []string{"openshift/dummy", "openshift/test"},
			optOutRepos: []string{"openshift/dummy"},
			ciOperatorConfigs: map[string]string{
				"openshift/excluded": "ocp",
			},
			expectedOptIn: []string{"openshift/test", "openshift/excluded"},
			wantErr:       false,
		},
		{
			name:        "opt-out excludes ci-operator detected repos",
			optInRepos:  []string{},
			optOutRepos: []string{"openshift/dummy"},
			ciOperatorConfigs: map[string]string{
				"openshift/dummy": "ocp",
				"openshift/test":  "ocp",
			},
			expectedOptIn: []string{"openshift/test"},
			wantErr:       false,
		},
		{
			name:              "empty files",
			optInRepos:        []string{},
			optOutRepos:       []string{},
			ciOperatorConfigs: map[string]string{},
			expectedOptIn:     []string{},
			wantErr:           false,
		},
		{
			name:              "file reader error",
			optInRepos:        nil,
			optOutRepos:       []string{},
			ciOperatorConfigs: map[string]string{},
			expectedOptIn:     []string{},
			wantErr:           true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fileReader := func(filePath string) ([]string, error) {
				if tt.optInRepos == nil {
					return nil, fmt.Errorf("mock file read error")
				}
				if strings.Contains(filePath, "opt-in") {
					return tt.optInRepos, nil
				}
				if strings.Contains(filePath, "opt-out") {
					return tt.optOutRepos, nil
				}
				return []string{}, nil
			}

			configDirOperator := func(dir string, callback config.ConfigIterFunc) error {
				for orgRepo, namespace := range tt.ciOperatorConfigs {
					parts := strings.Split(orgRepo, "/")
					if len(parts) != 2 {
						continue
					}

					cfg := &api.ReleaseBuildConfiguration{}
					if namespace != "" {
						cfg.PromotionConfiguration = &api.PromotionConfiguration{
							Targets: []api.PromotionTarget{
								{Namespace: namespace},
							},
						}
					}

					info := &config.Info{
						Metadata: api.Metadata{
							Org:  parts[0],
							Repo: parts[1],
						},
					}

					if err := callback(cfg, info); err != nil {
						return err
					}
				}
				return nil
			}

			result, err := newVerifiedEventWithDeps(
				"opt-in-file",
				"opt-out-file",
				"ci-config-dir",
				delegate,
				fileReader,
				configDirOperator,
			)

			if (err != nil) != tt.wantErr {
				t.Errorf("newVerifiedEventWithDeps() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				return
			}

			actualOptIn := result.optInRepos.UnsortedList()
			expectedOptIn := tt.expectedOptIn

			if len(actualOptIn) != len(expectedOptIn) {
				t.Errorf("Expected %d opt-in repos, got %d", len(expectedOptIn), len(actualOptIn))
				t.Errorf("Expected: %v", expectedOptIn)
				t.Errorf("Actual: %v", actualOptIn)
				return
			}

			actualSet := sets.New[string](actualOptIn...)
			expectedSet := sets.New[string](expectedOptIn...)

			if !actualSet.Equal(expectedSet) {
				t.Errorf("Expected opt-in repos: %v, got: %v", expectedOptIn, actualOptIn)
			}
		})
	}
}

func TestVerifiedEventModifyQuery(t *testing.T) {
	delegate := newSharedDataDelegate()

	tests := []struct {
		name           string
		optInRepos     []string
		optOutRepos    []string
		repo           string
		query          *prowconfig.TideQuery
		expectedLabels []string
	}{
		{
			name:        "adds verified label for opt-in repo on main branch",
			optInRepos:  []string{"openshift/dummy"},
			optOutRepos: []string{},
			repo:        "openshift/dummy",
			query: &prowconfig.TideQuery{
				Labels:           []string{"lgtm", "approved"},
				IncludedBranches: []string{"main"},
			},
			expectedLabels: []string{"approved", "lgtm", "verified"},
		},
		{
			name:        "adds verified label for opt-in repo on master branch",
			optInRepos:  []string{"openshift/dummy"},
			optOutRepos: []string{},
			repo:        "openshift/dummy",
			query: &prowconfig.TideQuery{
				Labels:           []string{"lgtm", "approved"},
				IncludedBranches: []string{"master"},
			},
			expectedLabels: []string{"approved", "lgtm", "verified"},
		},
		{
			name:        "does not add verified label for non-main/master branch",
			optInRepos:  []string{"openshift/dummy"},
			optOutRepos: []string{},
			repo:        "openshift/dummy",
			query: &prowconfig.TideQuery{
				Labels:           []string{"lgtm", "approved"},
				IncludedBranches: []string{"feature-branch"},
			},
			expectedLabels: []string{"approved", "lgtm"},
		},
		{
			name:        "does not add verified label for repo not in opt-in",
			optInRepos:  []string{"openshift/other"},
			optOutRepos: []string{},
			repo:        "openshift/dummy",
			query: &prowconfig.TideQuery{
				Labels:           []string{"lgtm", "approved"},
				IncludedBranches: []string{"main"},
			},
			expectedLabels: []string{"approved", "lgtm"},
		},
		{
			name:        "does not add verified label for opt-out repo",
			optInRepos:  []string{"openshift/dummy"},
			optOutRepos: []string{"openshift/dummy"},
			repo:        "openshift/dummy",
			query: &prowconfig.TideQuery{
				Labels:           []string{"lgtm", "approved"},
				IncludedBranches: []string{"main"},
			},
			expectedLabels: []string{"approved", "lgtm"},
		},
		{
			name:        "adds verified label for release-4.x branch",
			optInRepos:  []string{"openshift/dummy"},
			optOutRepos: []string{},
			repo:        "openshift/dummy",
			query: &prowconfig.TideQuery{
				Labels:           []string{"lgtm", "approved"},
				IncludedBranches: []string{"release-4.9"},
			},
			expectedLabels: []string{"approved", "lgtm", "verified"},
		},
		{
			name:        "adds verified label for openshift-4.x branch",
			optInRepos:  []string{"openshift/dummy"},
			optOutRepos: []string{},
			repo:        "openshift/dummy",
			query: &prowconfig.TideQuery{
				Labels:           []string{"lgtm", "approved"},
				IncludedBranches: []string{"openshift-4.10"},
			},
			expectedLabels: []string{"approved", "lgtm", "verified"},
		},
		{
			name:        "adds verified label for multiple versioned branches",
			optInRepos:  []string{"openshift/dummy"},
			optOutRepos: []string{},
			repo:        "openshift/dummy",
			query: &prowconfig.TideQuery{
				Labels:           []string{"lgtm", "approved"},
				IncludedBranches: []string{"release-4.9", "openshift-4.10"},
			},
			expectedLabels: []string{"approved", "lgtm", "verified"},
		},
		{
			name:        "does not add verified label for non-versioned release branch",
			optInRepos:  []string{"openshift/dummy"},
			optOutRepos: []string{},
			repo:        "openshift/dummy",
			query: &prowconfig.TideQuery{
				Labels:           []string{"lgtm", "approved"},
				IncludedBranches: []string{"release-v4.9"},
			},
			expectedLabels: []string{"approved", "lgtm"},
		},
		{
			name:        "does not add verified label for invalid version branch",
			optInRepos:  []string{"openshift/dummy"},
			optOutRepos: []string{},
			repo:        "openshift/dummy",
			query: &prowconfig.TideQuery{
				Labels:           []string{"lgtm", "approved"},
				IncludedBranches: []string{"release-4.abc"},
			},
			expectedLabels: []string{"approved", "lgtm"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ve := &verifiedEvent{
				optInRepos:         sets.New[string](tt.optInRepos...),
				optOutRepos:        sets.New[string](tt.optOutRepos...),
				sharedDataDelegate: delegate,
			}

			ve.ModifyQuery(tt.query, tt.repo)

			actualLabels := tt.query.Labels
			expectedLabels := tt.expectedLabels

			if len(actualLabels) != len(expectedLabels) {
				t.Errorf("Expected %d labels, got %d", len(expectedLabels), len(actualLabels))
				t.Errorf("Expected: %v", expectedLabels)
				t.Errorf("Actual: %v", actualLabels)
				return
			}

			actualSet := sets.New[string](actualLabels...)
			expectedSet := sets.New[string](expectedLabels...)

			if !actualSet.Equal(expectedSet) {
				t.Errorf("Expected labels: %v, got: %v", expectedLabels, actualLabels)
			}
		})
	}
}

func TestIsVersionedBranch(t *testing.T) {
	tests := []struct {
		name     string
		branch   string
		expected bool
	}{
		{
			name:     "valid release-4.x branch",
			branch:   "release-4.9",
			expected: true,
		},
		{
			name:     "valid openshift-4.x branch",
			branch:   "openshift-4.10",
			expected: true,
		},
		{
			name:     "valid release branch with double digit minor",
			branch:   "release-4.15",
			expected: true,
		},
		{
			name:     "valid openshift branch with double digit minor",
			branch:   "openshift-4.15",
			expected: true,
		},
		{
			name:     "valid release-4.0 branch",
			branch:   "release-4.0",
			expected: true,
		},
		{
			name:     "valid openshift-4.0 branch",
			branch:   "openshift-4.0",
			expected: true,
		},
		{
			name:     "invalid release branch with non-numeric version",
			branch:   "release-4.abc",
			expected: false,
		},
		{
			name:     "invalid openshift branch with non-numeric version",
			branch:   "openshift-4.x",
			expected: false,
		},
		{
			name:     "invalid release branch with v prefix",
			branch:   "release-v4.9",
			expected: false,
		},
		{
			name:     "invalid openshift branch with v prefix",
			branch:   "openshift-v4.9",
			expected: false,
		},
		{
			name:     "invalid branch with release prefix but wrong format",
			branch:   "release-3.9",
			expected: false,
		},
		{
			name:     "invalid branch with openshift prefix but wrong format",
			branch:   "openshift-3.9",
			expected: false,
		},
		{
			name:     "main branch",
			branch:   "main",
			expected: false,
		},
		{
			name:     "master branch",
			branch:   "master",
			expected: false,
		},
		{
			name:     "empty branch",
			branch:   "",
			expected: false,
		},
		{
			name:     "release branch without version",
			branch:   "release-4.",
			expected: false,
		},
		{
			name:     "openshift branch without version",
			branch:   "openshift-4.",
			expected: false,
		},
		{
			name:     "valid release-5.0 branch",
			branch:   "release-5.0",
			expected: true,
		},
		{
			name:     "valid openshift-5.0 branch",
			branch:   "openshift-5.0",
			expected: true,
		},
		{
			name:     "valid release-5.1 branch",
			branch:   "release-5.1",
			expected: true,
		},
		{
			name:     "valid openshift-5.1 branch",
			branch:   "openshift-5.1",
			expected: true,
		},
		{
			name:     "valid release-5.10 branch",
			branch:   "release-5.10",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isVersionedBranch(tt.branch)
			if result != tt.expected {
				t.Errorf("isVersionedBranch(%q) = %v, expected %v", tt.branch, result, tt.expected)
			}
		})
	}
}
