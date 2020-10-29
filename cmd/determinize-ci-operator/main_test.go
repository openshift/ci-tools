package main

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/util/sets"
	utilpointer "k8s.io/utils/pointer"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
)

func TestMigrateOpenshiftInstallerSRCTemplates(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name                                                string
		configuration                                       *config.DataWithInfo
		allowedBranches, allowedOrgs, allowedCloudproviders sets.String
		expectedConfig                                      *config.DataWithInfo
		expectedMigrationCount                              int
	}{
		{
			name: "Template gets migrated",
			configuration: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					Commands: "echo hello world",
					OpenshiftInstallerSrcClusterTestConfiguration: &api.OpenshiftInstallerSrcClusterTestConfiguration{
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileAWS},
					},
				}},
			}},
			expectedConfig: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					MultiStageTestConfiguration: &api.MultiStageTestConfiguration{
						ClusterProfile: "aws",
						Test: []api.TestStep{{LiteralTestStep: &api.LiteralTestStep{
							As:       "test",
							From:     "src",
							Commands: "echo hello world",
							Cli:      "latest",
							Resources: api.ResourceRequirements{
								Requests: api.ResourceList{"cpu": "100m"},
							},
						}}},
						Workflow: utilpointer.StringPtr("ipi-aws"),
					},
				}},
			}},
			expectedMigrationCount: 1,
		},
		{
			name: "Config excluded via branches, nothing happens",
			configuration: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					Commands: "echo hello world",
					OpenshiftInstallerSrcClusterTestConfiguration: &api.OpenshiftInstallerSrcClusterTestConfiguration{
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileAWS},
					},
				}},
			}},
			allowedBranches: sets.NewString("some-branch"),
			expectedConfig: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					Commands: "echo hello world",
					OpenshiftInstallerSrcClusterTestConfiguration: &api.OpenshiftInstallerSrcClusterTestConfiguration{
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileAWS},
					},
				}},
			}},
		},
		{
			name: "Config excluded via orgs, nothing happens",
			configuration: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					Commands: "echo hello world",
					OpenshiftInstallerSrcClusterTestConfiguration: &api.OpenshiftInstallerSrcClusterTestConfiguration{
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileAWS},
					},
				}},
			}},
			allowedOrgs: sets.NewString("some-org"),
			expectedConfig: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					Commands: "echo hello world",
					OpenshiftInstallerSrcClusterTestConfiguration: &api.OpenshiftInstallerSrcClusterTestConfiguration{
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileAWS},
					},
				}},
			}},
		},
		{
			name: "Config excluded via cloudprovider, nothing happens",
			configuration: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					Commands: "echo hello world",
					OpenshiftInstallerSrcClusterTestConfiguration: &api.OpenshiftInstallerSrcClusterTestConfiguration{
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileAWS},
					},
				}},
			}},
			allowedCloudproviders: sets.NewString("gcp"),
			expectedConfig: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					Commands: "echo hello world",
					OpenshiftInstallerSrcClusterTestConfiguration: &api.OpenshiftInstallerSrcClusterTestConfiguration{
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileAWS},
					},
				}},
			}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualMigrationCount := migrateOpenshiftInstallerSRCTemplates(tc.configuration, tc.allowedOrgs, tc.allowedBranches, tc.allowedCloudproviders)
			if actualMigrationCount != tc.expectedMigrationCount {
				t.Errorf("expected %d migrated tests, got %d", actualMigrationCount, tc.expectedMigrationCount)
			}

			if diff := cmp.Diff(tc.configuration, tc.expectedConfig); diff != "" {
				t.Errorf("Configuration differs from expected configuration: %s", diff)
			}
		})
	}
}

func TestMigrateOpenshiftInstallerCustomTestImageTemplates(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name                                                string
		configuration                                       *config.DataWithInfo
		allowedBranches, allowedOrgs, allowedCloudproviders sets.String
		expectedConfig                                      *config.DataWithInfo
		expectedMigrationCount                              int
	}{
		{
			name: "Template gets migrated",
			configuration: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					Commands: "echo hello world",
					OpenshiftInstallerCustomTestImageClusterTestConfiguration: &api.OpenshiftInstallerCustomTestImageClusterTestConfiguration{
						From:                     "Berlin",
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileAWS},
					},
				}},
			}},
			expectedConfig: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					MultiStageTestConfiguration: &api.MultiStageTestConfiguration{
						ClusterProfile: "aws",
						Test: []api.TestStep{{LiteralTestStep: &api.LiteralTestStep{
							As:       "test",
							From:     "Berlin",
							Commands: "echo hello world",
							Cli:      "latest",
							Resources: api.ResourceRequirements{
								Requests: api.ResourceList{"cpu": "100m"},
							},
						}}},
						Workflow: utilpointer.StringPtr("ipi-aws"),
					},
				}},
			}},
			expectedMigrationCount: 1,
		},
		{
			name: "Config excluded via branches, nothing happens",
			configuration: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					Commands: "echo hello world",
					OpenshiftInstallerCustomTestImageClusterTestConfiguration: &api.OpenshiftInstallerCustomTestImageClusterTestConfiguration{
						From:                     "Berlin",
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileAWS},
					},
				}},
			}},
			allowedBranches: sets.NewString("some-branch"),
			expectedConfig: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					Commands: "echo hello world",
					OpenshiftInstallerCustomTestImageClusterTestConfiguration: &api.OpenshiftInstallerCustomTestImageClusterTestConfiguration{
						From:                     "Berlin",
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileAWS},
					},
				}},
			}},
		},
		{
			name: "Config excluded via orgs, nothing happens",
			configuration: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					Commands: "echo hello world",
					OpenshiftInstallerCustomTestImageClusterTestConfiguration: &api.OpenshiftInstallerCustomTestImageClusterTestConfiguration{
						From:                     "Berlin",
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileAWS},
					},
				}},
			}},
			allowedOrgs: sets.NewString("some-org"),
			expectedConfig: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					Commands: "echo hello world",
					OpenshiftInstallerCustomTestImageClusterTestConfiguration: &api.OpenshiftInstallerCustomTestImageClusterTestConfiguration{
						From:                     "Berlin",
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileAWS},
					},
				}},
			}},
		},
		{
			name: "Config excluded via cloudprovider, nothing happens",
			configuration: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					Commands: "echo hello world",
					OpenshiftInstallerCustomTestImageClusterTestConfiguration: &api.OpenshiftInstallerCustomTestImageClusterTestConfiguration{
						From:                     "Berlin",
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileAWS},
					},
				}},
			}},
			allowedCloudproviders: sets.NewString("gcp"),
			expectedConfig: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					Commands: "echo hello world",
					OpenshiftInstallerCustomTestImageClusterTestConfiguration: &api.OpenshiftInstallerCustomTestImageClusterTestConfiguration{
						From:                     "Berlin",
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileAWS},
					},
				}},
			}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualMigrationCount := migrateOpenshiftInstallerCustomTestImageTemplates(tc.configuration, tc.allowedOrgs, tc.allowedBranches, tc.allowedCloudproviders)
			if actualMigrationCount != tc.expectedMigrationCount {
				t.Errorf("expected %d migrated tests, got %d", tc.expectedMigrationCount, actualMigrationCount)
			}

			if diff := cmp.Diff(tc.configuration, tc.expectedConfig); diff != "" {
				t.Errorf("Configuration differs from expected configuration: %s", diff)
			}
		})
	}
}
