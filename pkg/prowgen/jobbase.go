package prowgen

import (
	"time"

	"k8s.io/utils/ptr"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/api/ocplifecycle"
	jc "github.com/openshift/ci-tools/pkg/jobconfig"
)

type prowJobBaseBuilder struct {
	PodSpec CiOperatorPodSpecGenerator
	base    prowconfig.JobBase

	info     *ProwgenInfo
	testName string
}

// isPrivate returns true if the repo is private, checking ci-operator config,
// .config.prowgen, and the org name (openshift-priv is always private).
func isPrivate(configSpec *cioperatorapi.ReleaseBuildConfiguration, info *ProwgenInfo) bool {
	if configSpec.Prowgen != nil && configSpec.Prowgen.Private != nil {
		return *configSpec.Prowgen.Private
	}
	if info.Config.Private {
		return true
	}
	return info.Org == "openshift-priv"
}

// isExposed returns true if jobs should be visible in Deck despite being private,
// checking both ci-operator config and .config.prowgen.
func isExposed(configSpec *cioperatorapi.ReleaseBuildConfiguration, info *ProwgenInfo) bool {
	if configSpec.Prowgen != nil && configSpec.Prowgen.Expose != nil {
		return *configSpec.Prowgen.Expose
	}
	return info.Config.Expose
}

// isSecretsStoreCSIDriverEnabled returns true if CSI Secrets Store should be used,
// checking both ci-operator config and .config.prowgen.
func isSecretsStoreCSIDriverEnabled(configSpec *cioperatorapi.ReleaseBuildConfiguration, info *ProwgenInfo) bool {
	if configSpec.Prowgen != nil && configSpec.Prowgen.EnableSecretsStoreCSIDriver != nil {
		return *configSpec.Prowgen.EnableSecretsStoreCSIDriver
	}
	return info.Config.EnableSecretsStoreCSIDriver
}

// If any included buildRoot uses from_repository we must not skip cloning
func skipCloning(configSpec *cioperatorapi.ReleaseBuildConfiguration) bool {
	buildRoots := configSpec.BuildRootImages
	if buildRoots == nil {
		buildRoots = make(map[string]cioperatorapi.BuildRootImageConfiguration)
	}
	if configSpec.BuildRootImage != nil {
		buildRoots[""] = *configSpec.BuildRootImage
	}
	for _, buildRoot := range buildRoots {
		if buildRoot.FromRepository {
			return false
		}
	}

	return true
}

func hasNoBuilds(c *cioperatorapi.ReleaseBuildConfiguration, info *ProwgenInfo) bool {
	if c == nil {
		return false
	}
	// only consider release jobs ATM
	if info.Org != "openshift" || info.Repo != "release" || (info.Branch != "master" && info.Branch != "main") {
		return false
	}
	if len(c.Images) == 0 && c.BuildRootImage == nil && c.RpmBuildCommands == "" && c.TestBinaryBuildCommands == "" && c.BinaryBuildCommands == "" {
		return true
	}
	return false
}

// NewProwJobBaseBuilder returns a new builder instance populated with defaults
// from the given ReleaseBuildConfiguration, Prowgen config. The embedded PodSpec
// is built using an injected CiOperatorPodSpecGenerator, not directly. The embedded
// PodSpec is not built until the Build method is called.
func NewProwJobBaseBuilder(configSpec *cioperatorapi.ReleaseBuildConfiguration, info *ProwgenInfo, podSpecGenerator CiOperatorPodSpecGenerator) *prowJobBaseBuilder {
	b := &prowJobBaseBuilder{
		PodSpec: podSpecGenerator,
		base: prowconfig.JobBase{
			Agent:  string(prowv1.KubernetesAgent),
			Labels: map[string]string{},
			UtilityConfig: prowconfig.UtilityConfig{
				Decorate: ptr.To(true),
			},
		},
	}

	private := isPrivate(configSpec, info)
	exposed := isExposed(configSpec, info)

	if skipCloning(configSpec) {
		b.base.UtilityConfig.DecorationConfig = &prowv1.DecorationConfig{SkipCloning: ptr.To(true)}
	} else if private {
		b.base.UtilityConfig.DecorationConfig = &prowv1.DecorationConfig{OauthTokenSecret: &prowv1.OauthTokenSecret{Key: cioperatorapi.OauthTokenSecretKey, Name: cioperatorapi.OauthTokenSecretName}}
	}

	if len(info.Variant) > 0 {
		b.base.Labels[jc.ProwJobLabelVariant] = info.Variant
	}

	// jobs generated from some configSpec shapes provide relevant CI signal about OCP version stream
	// quality, so we label them as such for downstream tooling like Sippy to recognize them
	if versionStream := ocplifecycle.ProvidesSignalForVersion(configSpec); versionStream != "" {
		b.base.Labels[jc.JobReleaseKey] = versionStream
	}

	if hasNoBuilds(configSpec, info) {
		b.base.Labels[cioperatorapi.NoBuildsLabel] = cioperatorapi.NoBuildsValue
	}

	b.PodSpec.Add(Variant(info.Variant))
	if private {
		// We can reuse Prow's volume with the token if ProwJob itself is cloning the code
		b.PodSpec.Add(GitHubToken(!skipCloning(configSpec)))
	}

	if configSpec.CanonicalGoRepository != nil {
		b.base.UtilityConfig.PathAlias = *configSpec.CanonicalGoRepository
	}

	if private && !exposed {
		b.base.Hidden = true
	}

	b.info = info
	return b
}

// NewProwJobBaseBuilderForTest creates a new builder populated with defaults
// for the given ci-operator test. The resulting builder is a superset of a
// one built by NewProwJobBaseBuilder, with additional fields set for test
func NewProwJobBaseBuilderForTest(configSpec *cioperatorapi.ReleaseBuildConfiguration, info *ProwgenInfo, podSpecGenerator CiOperatorPodSpecGenerator, test cioperatorapi.TestStepConfiguration) *prowJobBaseBuilder {
	p := NewProwJobBaseBuilder(configSpec, info, podSpecGenerator)
	if test.Cluster != "" {
		p.Cluster(test.Cluster)
		p.WithLabel(cioperatorapi.ClusterLabel, string(test.Cluster))
	}
	p.testName = test.As

	maxCustomDuration := time.Hour * 72
	if test.Timeout != nil && test.Timeout.Duration <= maxCustomDuration {
		u := &p.base.UtilityConfig
		if u.DecorationConfig == nil {
			u.DecorationConfig = &prowv1.DecorationConfig{}
		}
		u.DecorationConfig.Timeout = test.Timeout
	}

	p.PodSpec.Add(Secrets(test.Secret), Secrets(test.Secrets...))
	p.PodSpec.Add(Targets(test.As))

	if test.ClusterClaim != nil {
		p.PodSpec.Add(Claims())
	}
	if testContainsLease(&test) {
		p.PodSpec.Add(LeaseClient())
	}

	p.PodSpec.Add(HTTPServer())

	// Note: Slack reporter config is now set in individual job generation functions
	// to support full job name matching in excluded_job_patterns
	switch {
	case test.MultiStageTestConfigurationLiteral != nil:
		if clusterProfile := test.MultiStageTestConfigurationLiteral.ClusterProfile; clusterProfile != "" {
			p.PodSpec.Add(LeaseClient())
			p.WithLabel(cioperatorapi.CloudClusterProfileLabel, string(clusterProfile))
			p.WithLabel(cioperatorapi.CloudLabel, clusterProfile.ClusterType())
		}
		if configSpec.Releases != nil {
			p.PodSpec.Add(CIPullSecret())
		}
		if isSecretsStoreCSIDriverEnabled(configSpec, info) {
			p.PodSpec.Add(
				GSMConfig(),
			)
		}
	case test.MultiStageTestConfiguration != nil:
		if clusterProfile := test.MultiStageTestConfiguration.ClusterProfile; clusterProfile != "" {
			p.PodSpec.Add(LeaseClient())
			p.WithLabel(cioperatorapi.CloudClusterProfileLabel, string(clusterProfile))
			p.WithLabel(cioperatorapi.CloudLabel, clusterProfile.ClusterType())
		}
		if configSpec.Releases != nil {
			p.PodSpec.Add(CIPullSecret())
		}
		if isSecretsStoreCSIDriverEnabled(configSpec, info) {
			p.PodSpec.Add(
				GSMConfig(),
			)
		}
	case test.OpenshiftAnsibleClusterTestConfiguration != nil:
		p.PodSpec.Add(
			Template("cluster-launch-e2e", test.Commands, "", test.As, test.OpenshiftAnsibleClusterTestConfiguration.ClusterProfile),
			ReleaseRpms(configSpec.ReleaseTagConfiguration.Name, p.info.Metadata),
		)
	case test.OpenshiftAnsibleCustomClusterTestConfiguration != nil:
		p.PodSpec.Add(
			Template("cluster-launch-e2e-openshift-ansible", test.Commands, "", test.As, test.OpenshiftAnsibleCustomClusterTestConfiguration.ClusterProfile),
			ReleaseRpms(configSpec.ReleaseTagConfiguration.Name, p.info.Metadata),
		)
	case test.OpenshiftInstallerClusterTestConfiguration != nil:
		if !test.OpenshiftInstallerClusterTestConfiguration.Upgrade {
			p.PodSpec.Add(Template("cluster-launch-installer-e2e", test.Commands, "", test.As, test.OpenshiftInstallerClusterTestConfiguration.ClusterProfile))
		}
		p.PodSpec.Add(LeaseClient())
	case test.OpenshiftInstallerUPIClusterTestConfiguration != nil:
		p.PodSpec.Add(
			Template("cluster-launch-installer-upi-e2e", test.Commands, "", test.As, test.OpenshiftInstallerUPIClusterTestConfiguration.ClusterProfile),
			LeaseClient(),
		)
	case test.OpenshiftInstallerCustomTestImageClusterTestConfiguration != nil:
		fromImage := test.OpenshiftInstallerCustomTestImageClusterTestConfiguration.From
		p.PodSpec.Add(
			Template("cluster-launch-installer-custom-test-image", test.Commands, fromImage, test.As, test.OpenshiftInstallerCustomTestImageClusterTestConfiguration.ClusterProfile),
			LeaseClient(),
		)
	}
	return p
}

// PathAlias sets UtilityConfig.PathAlias to the given value, including an empty
// one. This field is defaulted in NewJobBaseBuilder (inferred from ReleaseBuildConfiguration)
// so this method allows to reset it.
func (p *prowJobBaseBuilder) PathAlias(alias string) *prowJobBaseBuilder {
	p.base.UtilityConfig.PathAlias = alias
	return p
}

// Rehearsable sets/unsets the label that makes jobs rehearsable
func (p *prowJobBaseBuilder) Rehearsable(yes bool) *prowJobBaseBuilder {
	if yes {
		p.base.Labels[jc.CanBeRehearsedLabel] = jc.CanBeRehearsedValue
	} else {
		delete(p.base.Labels, jc.CanBeRehearsedLabel)
	}
	return p
}

// TestName sets the base name that specifies the *test* this job will run
func (p *prowJobBaseBuilder) TestName(name string) *prowJobBaseBuilder {
	p.testName = name
	return p
}

// Cluster sets where the job will run when submitted. Note that this is different
// from setting ClusterLabel label which is consumed by sanitize-prow-config when
// dispatching jobs among clusters. Generated configs will usually not set `Cluster`
// at all and will have ClusterLabel when explicitly configured.
// Cluster set by this method is mostly useful for dynamically creating Prowjobs
// to be submitted to the cluster right away.
func (p *prowJobBaseBuilder) Cluster(cluster cioperatorapi.Cluster) *prowJobBaseBuilder {
	p.base.Cluster = string(cluster)
	return p
}

// WithLabel sets a label to the given value
func (p *prowJobBaseBuilder) WithLabel(key, value string) *prowJobBaseBuilder {
	p.base.Labels[key] = value
	return p
}

// Build builds and returns the final JobBase instance
func (p *prowJobBaseBuilder) Build(namePrefix string) prowconfig.JobBase {
	p.base.Name = p.info.JobName(namePrefix, p.testName)
	p.base.Spec = p.PodSpec.MustBuild()
	return p.base
}
