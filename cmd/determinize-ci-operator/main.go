package main

import (
	"flag"
	"fmt"

	"github.com/sirupsen/logrus"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/flagutil"
	utilpointer "k8s.io/utils/pointer"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
)

const (
	openshiftInstallerSRCTemplateName = "openshift_installer_src"
)

var validTemplateMigrations = sets.NewString(openshiftInstallerSRCTemplateName)

type options struct {
	config.ConfirmableOptions
	enabledTemplateMigrations               flagutil.Strings
	templateMigrationCeiling                int
	templateMigrationAllowedBranches        flagutil.Strings
	templateMigrationAllowedOrgs            flagutil.Strings
	templateMigrationAllowedClusterProfiles flagutil.Strings
}

func (o options) validate() error {
	var errs []error
	if err := o.ConfirmableOptions.Validate(); err != nil {
		errs = append(errs, err)
	}
	if diff := sets.NewString(o.enabledTemplateMigrations.Strings()...).Difference(validTemplateMigrations); len(diff) != 0 {
		errs = append(errs, fmt.Errorf("invalid values %v for --enabled-template-migration, valid values: %v", diff.List(), validTemplateMigrations.List()))
	}

	return utilerrors.NewAggregate(errs)
}

func gatherOptions() options {
	o := options{}
	o.Bind(flag.CommandLine)
	flag.Var(&o.enabledTemplateMigrations, "enabled-template-migration", fmt.Sprintf("The enabled template migrations. Can be passed multiple times. Valid values are %v", validTemplateMigrations.List()))
	flag.IntVar(&o.templateMigrationCeiling, "template-migration-ceiling", 10, "The maximum number of templates to migrate")
	flag.Var(&o.templateMigrationAllowedBranches, "template-migration-allowed-branch", "Allowed branches to automigrate templates on. Can be passed multiple times. All branches are allowed if unset.")
	flag.Var(&o.templateMigrationAllowedOrgs, "template-migration-allowed-org", "Allowed orgs to automigrate templates on. Can be passed multiple times. All orgs are allowed if unset.")
	flag.Var(&o.templateMigrationAllowedClusterProfiles, "template-migration-allowed-cluster-profile", "Allowed cluster profiles to automigrate templates on. Can be passed multiple times. All cluster profiles are allowed if unset.")
	flag.Parse()

	return o
}

func main() {
	o := gatherOptions()
	if err := o.validate(); err != nil {
		logrus.Fatalf("Invalid options: %v", err)
	}

	var migratedCount int
	var toCommit []config.DataWithInfo
	if err := o.OperateOnCIOperatorConfigDir(o.ConfigDir, func(configuration *api.ReleaseBuildConfiguration, info *config.Info) error {
		output := config.DataWithInfo{Configuration: *configuration, Info: *info}
		if !o.Confirm {
			output.Logger().Info("Would re-format file.")
			return nil
		}

		if sets.NewString(o.enabledTemplateMigrations.Strings()...).Has(openshiftInstallerSRCTemplateName) && migratedCount <= o.templateMigrationCeiling {
			migratedCount += migrateOpenshiftInstallerSRCTemplates(&output, o.templateMigrationAllowedBranches.StringSet(), o.templateMigrationAllowedOrgs.StringSet(), o.templateMigrationAllowedClusterProfiles.StringSet())
		}

		// we treat the filepath as the ultimate source of truth for this
		// data, but we record it in the configuration files to ensure that
		// it's easy to consume it for downstream tools
		output.Configuration.Metadata = info.Metadata

		// we are walking the config so we need to commit once we're done
		toCommit = append(toCommit, output)

		return nil
	}); err != nil {
		logrus.WithError(err).Fatal("Could not branch configurations.")
	}

	for _, output := range toCommit {
		if err := output.CommitTo(o.ConfigDir); err != nil {
			logrus.WithError(err).Fatal("commitTo failed")
		}
	}
}

func migrateOpenshiftInstallerSRCTemplates(
	configuration *config.DataWithInfo,
	allowedBranches sets.String,
	allowedOrgs sets.String,
	allowedCloudproviders sets.String,
) (migratedCount int) {
	if (len(allowedBranches) != 0 && !allowedBranches.Has(configuration.Info.Branch)) || (len(allowedOrgs) != 0 && !allowedOrgs.Has(configuration.Info.Org)) {
		return 0
	}

	for idx, test := range configuration.Configuration.Tests {
		if test.OpenshiftInstallerSrcClusterTestConfiguration == nil ||
			(len(allowedCloudproviders) != 0 && !allowedCloudproviders.Has(string(test.OpenshiftInstallerSrcClusterTestConfiguration.ClusterProfile))) {
			continue
		}

		clusterProfile := test.OpenshiftInstallerSrcClusterTestConfiguration.ClusterProfile
		test.OpenshiftInstallerSrcClusterTestConfiguration = nil
		test.MultiStageTestConfiguration = &api.MultiStageTestConfiguration{
			ClusterProfile: clusterProfile,
			Test: []api.TestStep{{LiteralTestStep: &api.LiteralTestStep{
				As:       "test",
				From:     string(api.PipelineImageStreamTagReferenceSource),
				Commands: test.Commands,
				Cli:      api.LatestReleaseName,
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "100m"},
				},
			}}},
			Workflow: utilpointer.StringPtr(ipiWorkflowForClusterProfile(clusterProfile)),
		}
		test.Commands = ""

		configuration.Configuration.Tests[idx] = test
		migratedCount++

	}

	return migratedCount
}

func ipiWorkflowForClusterProfile(clusterProfile api.ClusterProfile) string {
	suffix := string(clusterProfile)
	if clusterProfile == api.ClusterProfileAzure4 {
		suffix = "azure"
	}
	return fmt.Sprintf("ipi-%s", suffix)
}
