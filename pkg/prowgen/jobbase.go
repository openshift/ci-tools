package prowgen

import (
	"time"

	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	utilpointer "k8s.io/utils/pointer"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	jc "github.com/openshift/ci-tools/pkg/jobconfig"
)

type prowJobBaseBuilder struct {
	PodSpec CiOperatorPodSpecGenerator

	configSpec *cioperatorapi.ReleaseBuildConfiguration
	info       *ProwgenInfo

	labels  map[string]string
	timeout *prowv1.Duration
	name    string
}

func (p *prowJobBaseBuilder) jobRelease() string {
	if release, found := p.configSpec.Releases[cioperatorapi.LatestReleaseName]; found && release.Candidate != nil {
		return release.Candidate.Version
	}
	return ""
}

func (p *prowJobBaseBuilder) skipCloning() bool {
	return p.configSpec.BuildRootImage == nil || !p.configSpec.BuildRootImage.FromRepository
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

func NewProwJobBaseBuilder(configSpec *cioperatorapi.ReleaseBuildConfiguration, info *ProwgenInfo, podSpecGenerator CiOperatorPodSpecGenerator) *prowJobBaseBuilder {
	b := &prowJobBaseBuilder{
		PodSpec: podSpecGenerator,

		labels:     map[string]string{},
		configSpec: configSpec,
		info:       info,
	}

	if len(info.Variant) > 0 {
		b.labels[jc.ProwJobLabelVariant] = info.Variant
	}

	if release := b.jobRelease(); release != "" {
		b.labels[jc.JobReleaseKey] = release
	}

	if hasNoBuilds(configSpec, info) {
		b.labels[cioperatorapi.NoBuildsLabel] = cioperatorapi.NoBuildsValue
	}

	b.PodSpec.Add(Variant(info.Variant))
	if info.Config.Private {
		// We can reuse Prow's volume with the token if ProwJob itself is cloning the code
		b.PodSpec.Add(GitHubToken(!b.skipCloning()))
	}

	return b
}

func NewProwJobBaseBuilderForTest(configSpec *cioperatorapi.ReleaseBuildConfiguration, info *ProwgenInfo, podSpecGenerator CiOperatorPodSpecGenerator, test cioperatorapi.TestStepConfiguration) *prowJobBaseBuilder {
	p := NewProwJobBaseBuilder(configSpec, info, podSpecGenerator)
	p.name = test.As
	p.timeout = test.Timeout

	p.PodSpec.Add(Secrets(test.Secret), Secrets(test.Secrets...))
	p.PodSpec.Add(Targets(test.As))

	if test.ClusterClaim != nil {
		p.PodSpec.Add(Claims())
	}
	if testContainsLease(&test) {
		p.PodSpec.Add(LeaseClient())
	}

	switch {
	case test.MultiStageTestConfigurationLiteral != nil:
		if test.MultiStageTestConfigurationLiteral.ClusterProfile != "" {
			p.PodSpec.Add(ClusterProfile(test.MultiStageTestConfigurationLiteral.ClusterProfile, test.As), LeaseClient())
		}
		if p.configSpec.Releases != nil {
			p.PodSpec.Add(CIPullSecret())
		}
	case test.MultiStageTestConfiguration != nil:
		if test.MultiStageTestConfiguration.ClusterProfile != "" {
			p.PodSpec.Add(ClusterProfile(test.MultiStageTestConfiguration.ClusterProfile, test.As), LeaseClient())
		}
		if p.configSpec.Releases != nil {
			p.PodSpec.Add(CIPullSecret())
		}
	case test.OpenshiftAnsibleClusterTestConfiguration != nil:
		p.PodSpec.Add(
			Template("cluster-launch-e2e", test.Commands, "", test.As, test.OpenshiftAnsibleClusterTestConfiguration.ClusterProfile),
			ReleaseRpms(p.configSpec.ReleaseTagConfiguration.Name, p.info.Metadata),
		)
	case test.OpenshiftAnsibleCustomClusterTestConfiguration != nil:
		p.PodSpec.Add(
			Template("cluster-launch-e2e-openshift-ansible", test.Commands, "", test.As, test.OpenshiftAnsibleCustomClusterTestConfiguration.ClusterProfile),
			ReleaseRpms(p.configSpec.ReleaseTagConfiguration.Name, p.info.Metadata),
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

func (p *prowJobBaseBuilder) Rehearsable(yes bool) *prowJobBaseBuilder {
	if yes {
		p.labels[jc.CanBeRehearsedLabel] = jc.CanBeRehearsedValue
	} else {
		delete(p.labels, jc.CanBeRehearsedLabel)
	}
	return p
}

func (p *prowJobBaseBuilder) Name(name string) *prowJobBaseBuilder {
	p.name = name
	return p
}

func (p *prowJobBaseBuilder) Build(namePrefix string) prowconfig.JobBase {
	jobName := p.info.JobName(namePrefix, p.name)

	var decorationConfig *prowv1.DecorationConfig
	if p.skipCloning() {
		decorationConfig = &prowv1.DecorationConfig{SkipCloning: utilpointer.BoolPtr(true)}
	} else if p.info.Config.Private {
		decorationConfig = &prowv1.DecorationConfig{OauthTokenSecret: &prowv1.OauthTokenSecret{Key: cioperatorapi.OauthTokenSecretKey, Name: cioperatorapi.OauthTokenSecretName}}
	}
	maxCustomDuration := time.Hour * 8
	if p.timeout != nil && p.timeout.Duration <= maxCustomDuration {
		decorationConfig.Timeout = p.timeout
	}
	base := prowconfig.JobBase{
		Agent:  string(prowv1.KubernetesAgent),
		Labels: p.labels,
		Name:   jobName,
		Spec:   p.PodSpec.MustBuild(),
		UtilityConfig: prowconfig.UtilityConfig{
			DecorationConfig: decorationConfig,
			Decorate:         utilpointer.BoolPtr(true),
		},
	}
	if p.configSpec.CanonicalGoRepository != nil {
		base.PathAlias = *p.configSpec.CanonicalGoRepository
	}
	if p.info.Config.Private && !p.info.Config.Expose {
		base.Hidden = true
	}
	return base
}
