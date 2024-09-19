package prowgen

import (
	"time"

	utilpointer "k8s.io/utils/pointer"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	jc "github.com/openshift/ci-tools/pkg/jobconfig"
)

type prowJobBaseBuilder struct {
	PodSpec CiOperatorPodSpecGenerator
	base    prowconfig.JobBase

	info     *ProwgenInfo
	testName string
}

func jobRelease(configSpec *cioperatorapi.ReleaseBuildConfiguration) string {
	if release, found := configSpec.Releases[cioperatorapi.LatestReleaseName]; found && release.Candidate != nil {
		return release.Candidate.Version
	}
	return ""
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
	if info.Org != "openshift" || info.Repo != "release" || info.Branch != "master" {
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
				Decorate: utilpointer.Bool(true),
			},
		},
	}

	if skipCloning(configSpec) {
		b.base.UtilityConfig.DecorationConfig = &prowv1.DecorationConfig{SkipCloning: utilpointer.Bool(true)}
	} else if info.Config.Private {
		b.base.UtilityConfig.DecorationConfig = &prowv1.DecorationConfig{OauthTokenSecret: &prowv1.OauthTokenSecret{Key: cioperatorapi.OauthTokenSecretKey, Name: cioperatorapi.OauthTokenSecretName}}
	}

	if len(info.Variant) > 0 {
		b.base.Labels[jc.ProwJobLabelVariant] = info.Variant
	}

	if release := jobRelease(configSpec); release != "" {
		b.base.Labels[jc.JobReleaseKey] = release
	}

	if hasNoBuilds(configSpec, info) {
		b.base.Labels[cioperatorapi.NoBuildsLabel] = cioperatorapi.NoBuildsValue
	}

	b.PodSpec.Add(Variant(info.Variant))
	if info.Config.Private {
		// We can reuse Prow's volume with the token if ProwJob itself is cloning the code
		b.PodSpec.Add(GitHubToken(!skipCloning(configSpec)))
	}

	if configSpec.CanonicalGoRepository != nil {
		b.base.UtilityConfig.PathAlias = *configSpec.CanonicalGoRepository
	}

	if info.Config.Private && !info.Config.Expose {
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

	maxCustomDuration := time.Hour * 8
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
	if slackReporter := info.Config.GetSlackReporterConfigForTest(test.As, configSpec.Metadata.Variant); slackReporter != nil {
		if p.base.ReporterConfig == nil {
			p.base.ReporterConfig = &prowv1.ReporterConfig{}
		}
		p.base.ReporterConfig.Slack = &prowv1.SlackReporterConfig{
			Channel:           slackReporter.Channel,
			JobStatesToReport: slackReporter.JobStatesToReport,
			ReportTemplate:    slackReporter.ReportTemplate,
		}
	}

	switch {
	case test.MultiStageTestConfigurationLiteral != nil:
		if clusterProfile := test.MultiStageTestConfigurationLiteral.ClusterProfile; clusterProfile != "" {
			p.PodSpec.Add(ClusterProfile(clusterProfile, test.As), LeaseClient())
			p.WithLabel(cioperatorapi.CloudClusterProfileLabel, string(clusterProfile))
			p.WithLabel(cioperatorapi.CloudLabel, clusterProfile.ClusterType())
		}
		if configSpec.Releases != nil {
			p.PodSpec.Add(CIPullSecret())
		}
	case test.MultiStageTestConfiguration != nil:
		if clusterProfile := test.MultiStageTestConfiguration.ClusterProfile; clusterProfile != "" {
			p.PodSpec.Add(ClusterProfile(clusterProfile, test.As), LeaseClient())
			p.WithLabel(cioperatorapi.CloudClusterProfileLabel, string(clusterProfile))
			p.WithLabel(cioperatorapi.CloudLabel, clusterProfile.ClusterType())
		}
		if configSpec.Releases != nil {
			p.PodSpec.Add(CIPullSecret())
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
		p.PodSpec.Add(ClusterProfile(test.OpenshiftInstallerClusterTestConfiguration.ClusterProfile, test.As))
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
