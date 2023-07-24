package main

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"k8s.io/apimachinery/pkg/util/sets"
	utilpointer "k8s.io/utils/pointer"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
)

func TestMigrateOpenshiftInstallerCustomTestImageTemplates(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name                                                string
		configuration                                       *config.DataWithInfo
		allowedBranches, allowedOrgs, allowedCloudproviders sets.Set[string]
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
			allowedBranches: sets.New[string]("some-branch"),
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
			allowedOrgs: sets.New[string]("some-org"),
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
			allowedCloudproviders: sets.New[string]("gcp"),
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

func TestMigrateOpenshiftOpenshiftInstallerUPIClusterTestConfiguration(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name                                                string
		configuration                                       *config.DataWithInfo
		allowedBranches, allowedOrgs, allowedCloudproviders sets.Set[string]
		expectedConfig                                      *config.DataWithInfo
		expectedMigrationCount                              int
	}{
		{
			name: "Template gets migrated for non-upgrade test",
			configuration: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					Commands: "TEST_SUITE=openshift/conformance/parallel run-tests",
					OpenshiftInstallerUPIClusterTestConfiguration: &api.OpenshiftInstallerUPIClusterTestConfiguration{
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileAWS},
					},
				}},
			}},
			expectedConfig: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					MultiStageTestConfiguration: &api.MultiStageTestConfiguration{
						ClusterProfile: "aws",
						Environment: api.TestEnvironment{
							"TEST_SUITE": "openshift/conformance/parallel",
						},
						Workflow: utilpointer.StringPtr("openshift-e2e-aws-upi"),
					},
				}},
			}},
			expectedMigrationCount: 1,
		},
		{
			name: "Template gets migrated for upgrade test",
			configuration: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					Commands: "TEST_SUITE=openshift/conformance/parallel run-upgrade",
					OpenshiftInstallerUPIClusterTestConfiguration: &api.OpenshiftInstallerUPIClusterTestConfiguration{
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileAWS},
					},
				}},
			}},
			expectedConfig: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					MultiStageTestConfiguration: &api.MultiStageTestConfiguration{
						ClusterProfile: "aws",
						Environment: api.TestEnvironment{
							"TEST_SUITE": "openshift/conformance/parallel",
							"TEST_TYPE":  "upgrade",
						},
						Workflow: utilpointer.StringPtr("openshift-e2e-aws-upi"),
					},
				}},
			}},
			expectedMigrationCount: 1,
		},
		{
			name: "Config excluded via branches, nothing happens",
			configuration: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					Commands: "TEST_SUITE=openshift/conformance/parallel run-tests",
					OpenshiftInstallerUPIClusterTestConfiguration: &api.OpenshiftInstallerUPIClusterTestConfiguration{
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileAWS},
					},
				}},
			}},
			allowedBranches: sets.New[string]("some-branch"),
			expectedConfig: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					Commands: "TEST_SUITE=openshift/conformance/parallel run-tests",
					OpenshiftInstallerUPIClusterTestConfiguration: &api.OpenshiftInstallerUPIClusterTestConfiguration{
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileAWS},
					},
				}},
			}},
		},
		{
			name: "Config excluded via orgs, nothing happens",
			configuration: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					Commands: "TEST_SUITE=openshift/conformance/parallel run-tests",
					OpenshiftInstallerUPIClusterTestConfiguration: &api.OpenshiftInstallerUPIClusterTestConfiguration{
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileAWS},
					},
				}},
			}},
			allowedOrgs: sets.New[string]("some-org"),
			expectedConfig: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					Commands: "TEST_SUITE=openshift/conformance/parallel run-tests",
					OpenshiftInstallerUPIClusterTestConfiguration: &api.OpenshiftInstallerUPIClusterTestConfiguration{
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileAWS},
					},
				}},
			}},
		},
		{
			name: "Config excluded via cloudprovider, nothing happens",
			configuration: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					Commands: "TEST_SUITE=openshift/conformance/parallel run-tests",
					OpenshiftInstallerUPIClusterTestConfiguration: &api.OpenshiftInstallerUPIClusterTestConfiguration{
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileAWS},
					},
				}},
			}},
			allowedCloudproviders: sets.New[string]("gcp"),
			expectedConfig: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					Commands: "TEST_SUITE=openshift/conformance/parallel run-tests",
					OpenshiftInstallerUPIClusterTestConfiguration: &api.OpenshiftInstallerUPIClusterTestConfiguration{
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileAWS},
					},
				}},
			}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualMigrationCount := migrateOpenshiftOpenshiftInstallerUPIClusterTestConfiguration(tc.configuration, tc.allowedOrgs, tc.allowedBranches, tc.allowedCloudproviders)
			if actualMigrationCount != tc.expectedMigrationCount {
				t.Errorf("expected %d migrated tests, got %d", tc.expectedMigrationCount, actualMigrationCount)
			}

			if diff := cmp.Diff(tc.configuration, tc.expectedConfig); diff != "" {
				t.Errorf("Configuration differs from expected configuration: %s", diff)
			}
		})
	}
}

func TestMigrateOpenshiftInstallerTemplates(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name                                                string
		configuration                                       *config.DataWithInfo
		allowedBranches, allowedOrgs, allowedCloudproviders sets.Set[string]
		expectedConfig                                      *config.DataWithInfo
		expectedMigrationCount                              int
	}{
		{
			name: "Template gets migrated for non-upgrade test",
			configuration: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					OpenshiftInstallerClusterTestConfiguration: &api.OpenshiftInstallerClusterTestConfiguration{
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileAWS},
					},
				}},
			}},
			expectedConfig: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					MultiStageTestConfiguration: &api.MultiStageTestConfiguration{
						ClusterProfile: "aws",
						Workflow:       utilpointer.StringPtr("openshift-e2e-aws"),
					},
				}},
			}},
			expectedMigrationCount: 1,
		},
		{
			name: "Template gets migrated for upgrade test",
			configuration: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					OpenshiftInstallerClusterTestConfiguration: &api.OpenshiftInstallerClusterTestConfiguration{
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileAWS},
						Upgrade:                  true,
					},
				}},
			}},
			expectedConfig: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					MultiStageTestConfiguration: &api.MultiStageTestConfiguration{
						ClusterProfile: "aws",
						Workflow:       utilpointer.StringPtr("openshift-upgrade-aws"),
					},
				}},
			}},
			expectedMigrationCount: 1,
		},
		{
			name: "Special case for disruptive tests, nothing happens",
			configuration: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					Commands: "setup_ssh_bastion; TEST_SUITE=openshift/disruptive run-tests; TEST_SUITE=openshift/conformance/parallel run-tests",
					OpenshiftInstallerClusterTestConfiguration: &api.OpenshiftInstallerClusterTestConfiguration{
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileAWS},
					},
				}},
			}},
			expectedConfig: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					Commands: "setup_ssh_bastion; TEST_SUITE=openshift/disruptive run-tests; TEST_SUITE=openshift/conformance/parallel run-tests",
					OpenshiftInstallerClusterTestConfiguration: &api.OpenshiftInstallerClusterTestConfiguration{
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileAWS},
					},
				}},
			}},
		},
		{
			name: "Config excluded via branches, nothing happens",
			configuration: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					OpenshiftInstallerClusterTestConfiguration: &api.OpenshiftInstallerClusterTestConfiguration{
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileAWS},
						Upgrade:                  true,
					},
				}},
			}},
			allowedBranches: sets.New[string]("some-branch"),
			expectedConfig: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					OpenshiftInstallerClusterTestConfiguration: &api.OpenshiftInstallerClusterTestConfiguration{
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileAWS},
						Upgrade:                  true,
					},
				}},
			}},
		},
		{
			name: "Config excluded via orgs, nothing happens",
			configuration: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					OpenshiftInstallerClusterTestConfiguration: &api.OpenshiftInstallerClusterTestConfiguration{
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileAWS},
						Upgrade:                  true,
					},
				}},
			}},
			allowedOrgs: sets.New[string]("some-org"),
			expectedConfig: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					OpenshiftInstallerClusterTestConfiguration: &api.OpenshiftInstallerClusterTestConfiguration{
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileAWS},
						Upgrade:                  true,
					},
				}},
			}},
		},
		{
			name: "Config excluded via cloudprovider, nothing happens",
			configuration: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					OpenshiftInstallerClusterTestConfiguration: &api.OpenshiftInstallerClusterTestConfiguration{
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileAWS},
						Upgrade:                  true,
					},
				}},
			}},
			allowedCloudproviders: sets.New[string]("gcp"),
			expectedConfig: &config.DataWithInfo{Configuration: api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					OpenshiftInstallerClusterTestConfiguration: &api.OpenshiftInstallerClusterTestConfiguration{
						ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: api.ClusterProfileAWS},
						Upgrade:                  true,
					},
				}},
			}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualMigrationCount := migrateOpenShiftInstallerTemplates(tc.configuration, tc.allowedOrgs, tc.allowedBranches, tc.allowedCloudproviders)
			if actualMigrationCount != tc.expectedMigrationCount {
				t.Errorf("expected %d migrated tests, got %d", tc.expectedMigrationCount, actualMigrationCount)
			}

			if diff := cmp.Diff(tc.configuration, tc.expectedConfig); diff != "" {
				t.Errorf("Configuration differs from expected configuration: %s", diff)
			}
		})
	}
}
