package prowgen

import (
	"fmt"
	"regexp"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	utilpointer "k8s.io/utils/pointer"

	"github.com/openshift/ci-tools/pkg/api"
	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	jc "github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/promotion"
)

const (
	oauthTokenPath              = "/usr/local/github-credentials"
	oauthKey                    = "oauth"
	Generator      jc.Generator = "prowgen"
)

type ProwgenInfo struct {
	cioperatorapi.Metadata
	Config config.Prowgen
}

// GenerateJobs
// Given a ci-operator configuration file and basic information about what
// should be tested, generate a following JobConfig:
//
// - one presubmit for each test defined in config file
// - if the config file has non-empty `images` section, generate an additional
//   presubmit and postsubmit that has `--target=[images]`. This postsubmit
//   will additionally pass `--promote` to ci-operator
//
// All these generated jobs will be labeled as "newly generated". After all
// new jobs are generated with GenerateJobs, the callsite should also use
// Prune() function to remove all stale jobs and label the jobs as simply
// "generated".
func GenerateJobs(configSpec *cioperatorapi.ReleaseBuildConfiguration, info *ProwgenInfo) *prowconfig.JobConfig {
	orgrepo := fmt.Sprintf("%s/%s", info.Org, info.Repo)
	presubmits := map[string][]prowconfig.Presubmit{}
	postsubmits := map[string][]prowconfig.Postsubmit{}
	var periodics []prowconfig.Periodic
	var jobRelease string
	if release, found := configSpec.Releases[cioperatorapi.LatestReleaseName]; found && release.Candidate != nil {
		jobRelease = release.Candidate.Version
	}

	skipCloning := true
	if configSpec.BuildRootImage != nil && configSpec.BuildRootImage.FromRepository {
		skipCloning = false
	}
	podSpecGen := func() CiOperatorPodSpecGenerator {
		g := NewCiOperatorPodSpecGenerator()
		g.Add(Variant(info.Variant))
		if info.Config.Private {
			// We can reuse Prow's volume with the token if ProwJob itself is cloning the code
			g.Add(GitHubToken(!skipCloning))
		}
		return g
	}
	for _, element := range configSpec.Tests {
		g := podSpecGen()
		g.Add(Secrets(element.Secret), Secrets(element.Secrets...))
		g.Add(Targets(element.As))

		if element.ClusterClaim != nil {
			g.Add(Claims())
		}
		if testContainsLease(&element) {
			g.Add(LeaseClient())
		}

		switch {
		case element.MultiStageTestConfigurationLiteral != nil:
			if element.MultiStageTestConfigurationLiteral.ClusterProfile != "" {
				g.Add(ClusterProfile(element.MultiStageTestConfigurationLiteral.ClusterProfile, element.As), LeaseClient())
			}
			if configSpec.Releases != nil {
				g.Add(CIPullSecret())
			}
		case element.MultiStageTestConfiguration != nil:
			if element.MultiStageTestConfiguration.ClusterProfile != "" {
				g.Add(ClusterProfile(element.MultiStageTestConfiguration.ClusterProfile, element.As), LeaseClient())
			}
			if configSpec.Releases != nil {
				g.Add(CIPullSecret())
			}
		case element.OpenshiftAnsibleClusterTestConfiguration != nil:
			g.Add(
				Template("cluster-launch-e2e", element.Commands, "", element.As, element.OpenshiftAnsibleClusterTestConfiguration.ClusterProfile),
				ReleaseRpms(configSpec.ReleaseTagConfiguration.Name, info.Metadata),
			)
		case element.OpenshiftAnsibleCustomClusterTestConfiguration != nil:
			g.Add(
				Template("cluster-launch-e2e-openshift-ansible", element.Commands, "", element.As, element.OpenshiftAnsibleCustomClusterTestConfiguration.ClusterProfile),
				ReleaseRpms(configSpec.ReleaseTagConfiguration.Name, info.Metadata),
			)
		case element.OpenshiftInstallerClusterTestConfiguration != nil:
			if !element.OpenshiftInstallerClusterTestConfiguration.Upgrade {
				g.Add(Template("cluster-launch-installer-e2e", element.Commands, "", element.As, element.OpenshiftInstallerClusterTestConfiguration.ClusterProfile))
			}
			g.Add(ClusterProfile(element.OpenshiftInstallerClusterTestConfiguration.ClusterProfile, element.As))
			g.Add(LeaseClient())
		case element.OpenshiftInstallerUPIClusterTestConfiguration != nil:
			g.Add(
				Template("cluster-launch-installer-upi-e2e", element.Commands, "", element.As, element.OpenshiftInstallerUPIClusterTestConfiguration.ClusterProfile),
				LeaseClient(),
			)
		case element.OpenshiftInstallerCustomTestImageClusterTestConfiguration != nil:
			fromImage := element.OpenshiftInstallerCustomTestImageClusterTestConfiguration.From
			g.Add(
				Template("cluster-launch-installer-custom-test-image", element.Commands, fromImage, element.As, element.OpenshiftInstallerCustomTestImageClusterTestConfiguration.ClusterProfile),
				LeaseClient(),
			)
		}

		if element.Cron != nil || element.Interval != nil || element.ReleaseController {
			cron := ""
			if element.Cron != nil {
				cron = *element.Cron
			}
			interval := ""
			if element.Interval != nil {
				interval = *element.Interval
			}
			periodic := generatePeriodicForTest(element.As, info, g.MustBuild(), true, cron, interval, element.ReleaseController, configSpec.CanonicalGoRepository, jobRelease, skipCloning, element.Timeout)
			if element.Cluster != "" {
				periodic.Labels[cioperatorapi.ClusterLabel] = string(element.Cluster)
			}
			periodics = append(periodics, *periodic)
		} else if element.Postsubmit {
			postsubmit := generatePostsubmitForTest(element.As, info, g.MustBuild(), configSpec.CanonicalGoRepository, jobRelease, skipCloning, element.Timeout)
			postsubmit.MaxConcurrency = 1
			if element.Cluster != "" {
				postsubmit.Labels[cioperatorapi.ClusterLabel] = string(element.Cluster)
			}
			postsubmits[orgrepo] = append(postsubmits[orgrepo], *postsubmit)
		} else {
			presubmit := *generatePresubmitForTest(element.As, info, g.MustBuild(), configSpec.CanonicalGoRepository, jobRelease, skipCloning, element.RunIfChanged, element.SkipIfOnlyChanged, element.Optional, element.Timeout)
			v, requestingKVM := configSpec.Resources.RequirementsForStep(element.As).Requests[cioperatorapi.KVMDeviceLabel]
			if requestingKVM {
				presubmit.Labels[cioperatorapi.KVMDeviceLabel] = v
			}
			if element.Cluster != "" {
				presubmit.Labels[cioperatorapi.ClusterLabel] = string(element.Cluster)
			}
			presubmits[orgrepo] = append(presubmits[orgrepo], presubmit)
		}
	}

	imageTargets := sets.NewString()
	if configSpec.PromotionConfiguration != nil {
		for additional := range configSpec.PromotionConfiguration.AdditionalImages {
			imageTargets.Insert(configSpec.PromotionConfiguration.AdditionalImages[additional])
		}
	}

	if len(configSpec.Images) > 0 || imageTargets.Len() > 0 {
		imageTargets.Insert("[images]")
	}

	if len(imageTargets) > 0 {
		// Identify which jobs need a to have a release payload explicitly requested
		var presubmitTargets = imageTargets.List()
		if promotion.PromotesOfficialImages(configSpec) {
			presubmitTargets = append(presubmitTargets, "[release:latest]")
		}
		podSpec := podSpecGen().Add(Targets(presubmitTargets...)).MustBuild()
		presubmits[orgrepo] = append(presubmits[orgrepo], *generatePresubmitForTest("images", info, podSpec, configSpec.CanonicalGoRepository, jobRelease, skipCloning, "", "", false, nil))

		if configSpec.PromotionConfiguration != nil {
			podSpec := podSpecGen().Add(Promotion(), Targets(imageTargets.List()...)).MustBuild()
			postsubmit := generatePostsubmitForTest("images", info, podSpec, configSpec.CanonicalGoRepository, jobRelease, skipCloning, nil)
			postsubmit.MaxConcurrency = 1
			if postsubmit.Labels == nil {
				postsubmit.Labels = map[string]string{}
			}
			postsubmit.Labels[cioperatorapi.PromotionJobLabelKey] = "true"
			postsubmits[orgrepo] = append(postsubmits[orgrepo], *postsubmit)
		}
	}

	if configSpec.Operator != nil {
		containsUnnamedBundle := false
		for _, bundle := range configSpec.Operator.Bundles {
			if bundle.As == "" {
				containsUnnamedBundle = true
				continue
			}
			indexName := api.IndexName(bundle.As)
			podSpec := podSpecGen().Add(Targets(indexName)).MustBuild()
			presubmits[orgrepo] = append(presubmits[orgrepo], *generatePresubmitForTest(indexName, info, podSpec, configSpec.CanonicalGoRepository, jobRelease, skipCloning, "", "", false, nil))
		}
		if containsUnnamedBundle {
			podSpec := podSpecGen().Add(Targets(string(api.PipelineImageStreamTagReferenceIndexImage))).MustBuild()
			presubmits[orgrepo] = append(presubmits[orgrepo], *generatePresubmitForTest(string(api.PipelineImageStreamTagReferenceIndexImage), info, podSpec, configSpec.CanonicalGoRepository, jobRelease, skipCloning, "", "", false, nil))
		}
	}

	return &prowconfig.JobConfig{
		PresubmitsStatic:  presubmits,
		PostsubmitsStatic: postsubmits,
		Periodics:         periodics,
	}
}

func testContainsLease(test *cioperatorapi.TestStepConfiguration) bool {
	// this is predicated upon the config being fully resolved at this time.
	if test.MultiStageTestConfigurationLiteral == nil {
		return false
	}

	return len(api.LeasesForTest(test.MultiStageTestConfigurationLiteral)) > 0
}

func generatePresubmitForTest(name string, info *ProwgenInfo, podSpec *corev1.PodSpec, pathAlias *string, jobRelease string, skipCloning bool, runIfChanged, skipIfOnlyChanged string, optional bool, timeout *prowv1.Duration) *prowconfig.Presubmit {
	shortName := info.TestName(name)
	base := generateJobBase(name, jc.PresubmitPrefix, info, podSpec, true, pathAlias, jobRelease, skipCloning, timeout)
	return &prowconfig.Presubmit{
		JobBase:   base,
		AlwaysRun: runIfChanged == "" && skipIfOnlyChanged == "",
		Brancher:  prowconfig.Brancher{Branches: sets.NewString(ExactlyBranch(info.Branch), FeatureBranch(info.Branch)).List()},
		Reporter: prowconfig.Reporter{
			Context: fmt.Sprintf("ci/prow/%s", shortName),
		},
		RerunCommand: prowconfig.DefaultRerunCommandFor(shortName),
		Trigger:      prowconfig.DefaultTriggerFor(shortName),
		RegexpChangeMatcher: prowconfig.RegexpChangeMatcher{
			RunIfChanged:      runIfChanged,
			SkipIfOnlyChanged: skipIfOnlyChanged,
		},
		Optional: optional,
	}
}

func generatePostsubmitForTest(name string, info *ProwgenInfo, podSpec *corev1.PodSpec, pathAlias *string, jobRelease string, skipCloning bool, timeout *prowv1.Duration) *prowconfig.Postsubmit {
	base := generateJobBase(name, jc.PostsubmitPrefix, info, podSpec, false, pathAlias, jobRelease, skipCloning, timeout)
	return &prowconfig.Postsubmit{
		JobBase:  base,
		Brancher: prowconfig.Brancher{Branches: []string{ExactlyBranch(info.Branch)}},
	}
}

func generatePeriodicForTest(name string, info *ProwgenInfo, podSpec *corev1.PodSpec, rehearsable bool, cron string, interval string, releaseController bool, pathAlias *string, jobRelease string, skipCloning bool, timeout *prowv1.Duration) *prowconfig.Periodic {
	base := generateJobBase(name, jc.PeriodicPrefix, info, podSpec, rehearsable, nil, jobRelease, skipCloning, timeout)
	// periodics are not associated with a repo per se, but we can add in an
	// extra ref so that periodics which want to access the repo tha they are
	// defined for can have that information
	ref := prowv1.Refs{
		Org:     info.Org,
		Repo:    info.Repo,
		BaseRef: info.Branch,
	}
	if pathAlias != nil {
		ref.PathAlias = *pathAlias
	}
	base.ExtraRefs = append([]prowv1.Refs{ref}, base.ExtraRefs...)
	if releaseController {
		interval = ""
		cron = "@yearly"
		base.Labels[jc.ReleaseControllerLabel] = jc.ReleaseControllerValue
	}
	return &prowconfig.Periodic{
		JobBase:  base,
		Cron:     cron,
		Interval: interval,
	}
}

func generateJobBase(name, prefix string, info *ProwgenInfo, podSpec *corev1.PodSpec, rehearsable bool, pathAlias *string, jobRelease string, skipCloning bool, timeout *prowv1.Duration) prowconfig.JobBase {
	labels := map[string]string{}
	if rehearsable {
		labels[jc.CanBeRehearsedLabel] = jc.CanBeRehearsedValue
	}

	jobName := info.JobName(prefix, name)
	if len(info.Variant) > 0 {
		labels[jc.ProwJobLabelVariant] = info.Variant
	}
	if jobRelease != "" {
		labels[jc.JobReleaseKey] = jobRelease
	}

	var decorationConfig *prowv1.DecorationConfig
	if skipCloning {
		decorationConfig = &prowv1.DecorationConfig{SkipCloning: utilpointer.BoolPtr(true)}
	} else if !skipCloning && info.Config.Private {
		decorationConfig = &prowv1.DecorationConfig{OauthTokenSecret: &prowv1.OauthTokenSecret{Key: api.OauthTokenSecretKey, Name: api.OauthTokenSecretName}}
	}
	maxCustomDuration := time.Hour * 8
	if timeout != nil && timeout.Duration <= maxCustomDuration {
		decorationConfig.Timeout = timeout
	}
	base := prowconfig.JobBase{
		Agent:  string(prowv1.KubernetesAgent),
		Labels: labels,
		Name:   jobName,
		Spec:   podSpec,
		UtilityConfig: prowconfig.UtilityConfig{
			DecorationConfig: decorationConfig,
			Decorate:         utilpointer.BoolPtr(true),
		},
	}
	if pathAlias != nil {
		base.PathAlias = *pathAlias
	}
	if info.Config.Private && !info.Config.Expose {
		base.Hidden = true
	}
	return base
}

// ExactlyBranch returns a regex string that matches exactly the given branch name: I.e. returns
// '^master$' for 'master'. If the given branch name already looks like a regex, return it unchanged.
func ExactlyBranch(branch string) string {
	if !jc.SimpleBranchRegexp.MatchString(branch) {
		return branch
	}
	return fmt.Sprintf("^%s$", regexp.QuoteMeta(branch))
}

// FeatureBranch returns a regex string that matches feature branch prefixes for the given branch name:
// I.e. returns '^master-' for 'master'. If the given branch name already looks like a regex,
// return it unchanged.
func FeatureBranch(branch string) string {
	if !jc.SimpleBranchRegexp.MatchString(branch) {
		return branch
	}
	return fmt.Sprintf("^%s-", regexp.QuoteMeta(branch))
}
