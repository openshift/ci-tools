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

	metadata cioperatorapi.Metadata
	extras   *cioperatorapi.ProwgenExtras
	testName string
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

func hasNoBuilds(c *cioperatorapi.ReleaseBuildConfiguration) bool {
	if c == nil {
		return false
	}
	// only consider release jobs ATM
	if c.Metadata.Org != "openshift" || c.Metadata.Repo != "release" || (c.Metadata.Branch != "master" && c.Metadata.Branch != "main") {
		return false
	}
	if len(c.Images.Items) == 0 && c.BuildRootImage == nil && c.RpmBuildCommands == "" && c.TestBinaryBuildCommands == "" && c.BinaryBuildCommands == "" {
		return true
	}
	return false
}

// NewProwJobBaseBuilder returns a new builder instance populated with defaults
// from the given ReleaseBuildConfiguration, Prowgen config. The embedded PodSpec
// is built using an injected CiOperatorPodSpecGenerator, not directly. The embedded
// PodSpec is not built until the Build method is called.
func NewProwJobBaseBuilder(configSpec *cioperatorapi.ReleaseBuildConfiguration, extras *cioperatorapi.ProwgenExtras, podSpecGenerator CiOperatorPodSpecGenerator) *prowJobBaseBuilder {
	b := &prowJobBaseBuilder{
		PodSpec: podSpecGenerator,
		base: prowconfig.JobBase{
			Agent:  string(prowv1.KubernetesAgent),
			Labels: map[string]string{},
			UtilityConfig: prowconfig.UtilityConfig{
				Decorate: ptr.To(true),
			},
		},
		metadata: configSpec.Metadata,
	}
	if extras == nil {
		extras = cioperatorapi.NewProwgenExtras(cioperatorapi.Prowgen{}, configSpec)
	}
	b.extras = extras

	if skipCloning(configSpec) {
		b.base.UtilityConfig.DecorationConfig = &prowv1.DecorationConfig{SkipCloning: ptr.To(true)}
	} else if extras.Prowgen.Private {
		b.base.UtilityConfig.DecorationConfig = &prowv1.DecorationConfig{OauthTokenSecret: &prowv1.OauthTokenSecret{Key: cioperatorapi.OauthTokenSecretKey, Name: cioperatorapi.OauthTokenSecretName}}
	}

	if len(configSpec.Metadata.Variant) > 0 {
		b.base.Labels[jc.ProwJobLabelVariant] = configSpec.Metadata.Variant
	}

	// jobs generated from some configSpec shapes provide relevant CI signal about OCP version stream
	// quality, so we label them as such for downstream tooling like Sippy to recognize them
	if versionStream := ocplifecycle.ProvidesSignalForVersion(configSpec); versionStream != "" {
		b.base.Labels[jc.JobReleaseKey] = versionStream
	}

	if hasNoBuilds(configSpec) {
		b.base.Labels[cioperatorapi.NoBuildsLabel] = cioperatorapi.NoBuildsValue
	}

	b.PodSpec.Add(Variant(configSpec.Metadata.Variant))
	if b.extras.Prowgen.Private {
		// We can reuse Prow's volume with the token if ProwJob itself is cloning the code
		b.PodSpec.Add(GitHubToken(!skipCloning(configSpec)))
	}

	if configSpec.CanonicalGoRepository != nil {
		b.base.UtilityConfig.PathAlias = *configSpec.CanonicalGoRepository
	}

	if b.extras.Prowgen.Private && !b.extras.Prowgen.Expose {
		b.base.Hidden = true
	}

	return b
}

// NewProwJobBaseBuilderForTest creates a new builder populated with defaults
// for the given ci-operator test. The resulting builder is a superset of a
// one built by NewProwJobBaseBuilder, with additional fields set for test
func NewProwJobBaseBuilderForTest(configSpec *cioperatorapi.ReleaseBuildConfiguration, extras *cioperatorapi.ProwgenExtras, podSpecGenerator CiOperatorPodSpecGenerator, test cioperatorapi.TestStepConfiguration) *prowJobBaseBuilder {
	p := NewProwJobBaseBuilder(configSpec, extras, podSpecGenerator)
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

	p.PodSpec.Add(HTTPServer())

	// Note: Slack reporter config is now set in individual job generation functions
	// to support full job name matching in excluded_job_patterns
	switch {
	case test.MultiStageTestConfigurationLiteral != nil:
		p.PodSpec.Add(LeaseClient())
		if clusterProfile := test.MultiStageTestConfigurationLiteral.ClusterProfile; clusterProfile != "" {
			p.WithLabel(cioperatorapi.CloudClusterProfileLabel, string(clusterProfile))
			p.WithLabel(cioperatorapi.CloudLabel, clusterProfile.ClusterType())
		}
		if configSpec.Releases != nil {
			p.PodSpec.Add(CIPullSecret())
		}
		if p.extras.Prowgen.EnableSecretsStoreCSIDriver {
			p.PodSpec.Add(
				GSMConfig(),
			)
		}
	case test.MultiStageTestConfiguration != nil:
		p.PodSpec.Add(LeaseClient())
		if clusterProfile := test.MultiStageTestConfiguration.ClusterProfile; clusterProfile != "" {
			p.WithLabel(cioperatorapi.CloudClusterProfileLabel, string(clusterProfile))
			p.WithLabel(cioperatorapi.CloudLabel, clusterProfile.ClusterType())
		}
		if configSpec.Releases != nil {
			p.PodSpec.Add(CIPullSecret())
		}
		if p.extras.Prowgen.EnableSecretsStoreCSIDriver {
			p.PodSpec.Add(
				GSMConfig(),
			)
		}
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
	p.base.Name = p.metadata.JobName(namePrefix, p.testName)
	p.base.Spec = p.PodSpec.MustBuild()
	return p.base
}
