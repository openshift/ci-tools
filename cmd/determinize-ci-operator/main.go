package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/flagutil"
	utilpointer "k8s.io/utils/pointer"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
)

const (
	openshiftInstallerCustomTestImageTemplateName = "openshift_installer_custom_test_image"
	OpenshiftInstallerUPITemplateName             = "openshift_installer_upi"
	OpenShiftInstallerTemplateName                = "openshift_installer"
)

var validTemplateMigrations = sets.New[string](openshiftInstallerCustomTestImageTemplateName, OpenshiftInstallerUPITemplateName, OpenShiftInstallerTemplateName)

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
	if diff := sets.New[string](o.enabledTemplateMigrations.Strings()...).Difference(validTemplateMigrations); len(diff) != 0 {
		errs = append(errs, fmt.Errorf("invalid values %v for --enabled-template-migration, valid values: %v", sets.List(diff), sets.List(validTemplateMigrations)))
	}

	return utilerrors.NewAggregate(errs)
}

func gatherOptions() options {
	o := options{}
	o.Bind(flag.CommandLine)
	flag.Var(&o.enabledTemplateMigrations, "enabled-template-migration", fmt.Sprintf("The enabled template migrations. Can be passed multiple times. Valid values are %v", sets.List(validTemplateMigrations)))
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

	if err := o.ConfirmableOptions.Complete(); err != nil {
		logrus.Fatalf("Couldn't complete the config options: %v", err)
	}

	var migratedCount int
	var toCommit []config.DataWithInfo
	if err := o.OperateOnCIOperatorConfigDir(o.ConfigDir, func(configuration *api.ReleaseBuildConfiguration, info *config.Info) error {
		output := config.DataWithInfo{Configuration: *configuration, Info: *info}
		if !o.Confirm {
			output.Logger().Info("Would re-format file.")
			return nil
		}

		allowedBranches := o.templateMigrationAllowedBranches.StringSet()
		allowedOrgs := o.templateMigrationAllowedOrgs.StringSet()
		allowedClusterProfiles := o.templateMigrationAllowedClusterProfiles.StringSet()
		if sets.New[string](o.enabledTemplateMigrations.Strings()...).Has(openshiftInstallerCustomTestImageTemplateName) && migratedCount <= o.templateMigrationCeiling {
			migratedCount += migrateOpenshiftInstallerCustomTestImageTemplates(&output, allowedBranches, allowedOrgs, allowedClusterProfiles)
		}
		if o.enabledTemplateMigrations.StringSet().Has(OpenshiftInstallerUPITemplateName) && migratedCount <= o.templateMigrationCeiling {
			migratedCount += migrateOpenshiftOpenshiftInstallerUPIClusterTestConfiguration(&output, allowedBranches, allowedOrgs, allowedClusterProfiles)
		}
		if o.enabledTemplateMigrations.StringSet().Has(OpenShiftInstallerTemplateName) && migratedCount <= o.templateMigrationCeiling {
			migratedCount += migrateOpenShiftInstallerTemplates(&output, allowedBranches, allowedOrgs, allowedClusterProfiles)
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

func upgradeWorkflowForClusterProfile(clusterProfile api.ClusterProfile) string {
	return fmt.Sprintf("openshift-upgrade-%s", clusterProfile)
}

func e2eWorkflowForClusterProfile(clusterProfile api.ClusterProfile) string {
	return fmt.Sprintf("openshift-e2e-%s", clusterProfile)
}

func migrateOpenShiftInstallerTemplates(
	configuration *config.DataWithInfo,
	allowedBranches sets.Set[string],
	allowedOrgs sets.Set[string],
	allowedCloudproviders sets.Set[string],
) (migratedCount int) {
	if (len(allowedBranches) != 0 && !allowedBranches.Has(configuration.Info.Branch)) || (len(allowedOrgs) != 0 && !allowedOrgs.Has(configuration.Info.Org)) {
		return 0
	}

	for idx, test := range configuration.Configuration.Tests {
		if test.OpenshiftInstallerClusterTestConfiguration == nil ||
			(len(allowedCloudproviders) != 0 && !allowedCloudproviders.Has(string(test.OpenshiftInstallerClusterTestConfiguration.ClusterProfile))) {
			continue
		}

		clusterProfile := test.OpenshiftInstallerClusterTestConfiguration.ClusterProfile
		switch {
		case test.OpenshiftInstallerClusterTestConfiguration.Upgrade:
			test.OpenshiftInstallerClusterTestConfiguration = nil
			test.MultiStageTestConfiguration = &api.MultiStageTestConfiguration{
				ClusterProfile: clusterProfile,
				Workflow:       utilpointer.String(upgradeWorkflowForClusterProfile(clusterProfile)),
			}
		case test.Commands == "setup_ssh_bastion; TEST_SUITE=openshift/disruptive run-tests; TEST_SUITE=openshift/conformance/parallel run-tests":
			// TODO(muller): Unfortunately there is no easy way to express this ("run same step twice")
			continue
		default:
			test.OpenshiftInstallerClusterTestConfiguration = nil
			test.MultiStageTestConfiguration = &api.MultiStageTestConfiguration{
				ClusterProfile: clusterProfile,
				Workflow:       utilpointer.String(e2eWorkflowForClusterProfile(clusterProfile)),
			}
		}
		test.Commands = ""
		configuration.Configuration.Tests[idx] = test
		migratedCount++
	}

	return migratedCount
}

func migrateOpenshiftInstallerCustomTestImageTemplates(
	configuration *config.DataWithInfo,
	allowedBranches sets.Set[string],
	allowedOrgs sets.Set[string],
	allowedCloudproviders sets.Set[string],
) (migratedCount int) {
	if (len(allowedBranches) != 0 && !allowedBranches.Has(configuration.Info.Branch)) || (len(allowedOrgs) != 0 && !allowedOrgs.Has(configuration.Info.Org)) {
		return 0
	}

	for idx, test := range configuration.Configuration.Tests {
		if test.OpenshiftInstallerCustomTestImageClusterTestConfiguration == nil ||
			(len(allowedCloudproviders) != 0 && !allowedCloudproviders.Has(string(test.OpenshiftInstallerCustomTestImageClusterTestConfiguration.ClusterProfile))) {
			continue
		}

		clusterProfile := test.OpenshiftInstallerCustomTestImageClusterTestConfiguration.ClusterProfile
		fromImage := test.OpenshiftInstallerCustomTestImageClusterTestConfiguration.From
		test.OpenshiftInstallerCustomTestImageClusterTestConfiguration = nil
		test.MultiStageTestConfiguration = &api.MultiStageTestConfiguration{
			ClusterProfile: clusterProfile,
			Test: []api.TestStep{{LiteralTestStep: &api.LiteralTestStep{
				As:       "test",
				From:     fromImage,
				Commands: test.Commands,
				Cli:      api.LatestReleaseName,
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "100m"},
				},
			}}},
			Workflow: utilpointer.String(ipiWorkflowForClusterProfile(clusterProfile)),
		}
		test.Commands = ""

		configuration.Configuration.Tests[idx] = test
		migratedCount++

	}

	return migratedCount
}

func providerNameForProfile(clusterProfile api.ClusterProfile) string {
	if clusterProfile == api.ClusterProfileAzure4 {
		return "azure"
	}
	return string(clusterProfile)
}

func ipiWorkflowForClusterProfile(clusterProfile api.ClusterProfile) string {
	return fmt.Sprintf("ipi-%s", providerNameForProfile(clusterProfile))
}

func migrateOpenshiftOpenshiftInstallerUPIClusterTestConfiguration(
	configuration *config.DataWithInfo,
	allowedBranches sets.Set[string],
	allowedOrgs sets.Set[string],
	allowedCloudproviders sets.Set[string],
) (migratedCount int) {
	if (len(allowedBranches) != 0 && !allowedBranches.Has(configuration.Info.Branch)) || (len(allowedOrgs) != 0 && !allowedOrgs.Has(configuration.Info.Org)) {
		return 0
	}

	log := logrus.WithField("file", configuration.Info.Filename)

	for idx, test := range configuration.Configuration.Tests {
		if test.OpenshiftInstallerUPIClusterTestConfiguration == nil ||
			(len(allowedCloudproviders) != 0 && !allowedCloudproviders.Has(string(test.OpenshiftInstallerUPIClusterTestConfiguration.ClusterProfile))) {
			continue
		}
		log := log.WithField("field", fmt.Sprintf("tests.%d", idx))

		commandFields := strings.Fields(test.Commands)
		if n := len(commandFields); n != 2 {
			log.Warnf("command %q didn't have exactly two fields, skipping migration of openshift_installer_upi template", test.Commands)
			continue
		}
		equalSignSplit := strings.Split(commandFields[0], "=")
		if n := len(equalSignSplit); n != 2 {
			log.Warnf("splitting first field of command %q by = didn't yield exactly two results, skipping migration of openshift_installer_upi template", test.Commands)
			continue
		}

		var testTypeEnv string
		switch commandFields[1] {
		case "run-tests":
			testTypeEnv = ""
		case "run-upgrade":
			testTypeEnv = "upgrade"
		default:
			log.Warnf("command %q has unrecognized command element %q, known elements: ['run-tests', 'run-upgrade'], skipping migration of openshift_installer_upi template", test.Commands, commandFields[1])
			continue
		}

		clusterProfile := test.OpenshiftInstallerUPIClusterTestConfiguration.ClusterProfile
		test.OpenshiftInstallerUPIClusterTestConfiguration = nil
		test.MultiStageTestConfiguration = &api.MultiStageTestConfiguration{
			ClusterProfile: clusterProfile,
			Environment: api.TestEnvironment{
				// https://github.com/openshift/release/blob/ea3cc4842843c941e9fa1e71ce8a4dc3ce841184/ci-operator/step-registry/openshift/e2e/test/openshift-e2e-test-ref.yaml#L10
				"TEST_SUITE": equalSignSplit[1],
			},
			Workflow: utilpointer.String(fmt.Sprintf("openshift-e2e-%s-upi", providerNameForProfile(clusterProfile))),
		}
		if testTypeEnv != "" {
			// https://github.com/openshift/release/blob/ea3cc4842843c941e9fa1e71ce8a4dc3ce841184/ci-operator/step-registry/openshift/e2e/test/openshift-e2e-test-ref.yaml#L7
			test.MultiStageTestConfiguration.Environment["TEST_TYPE"] = testTypeEnv
		}
		test.Commands = ""

		configuration.Configuration.Tests[idx] = test
		migratedCount++

	}

	return migratedCount
}
