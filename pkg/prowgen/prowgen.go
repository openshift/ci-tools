package prowgen

import (
	"fmt"
	"hash/fnv"

	"k8s.io/apimachinery/pkg/util/sets"
	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"

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

	for _, element := range configSpec.Tests {
		g := NewProwJobBaseBuilderForTest(configSpec, info, NewCiOperatorPodSpecGenerator(), element)

		if element.Cron != nil || element.Interval != nil || element.ReleaseController {
			cron := ""
			if element.Cron != nil {
				cron = *element.Cron
			}
			interval := ""
			if element.Interval != nil {
				interval = *element.Interval
			}
			periodic := GeneratePeriodicForTest(g, info, cron, interval, element.ReleaseController, configSpec.CanonicalGoRepository)
			periodics = append(periodics, *periodic)
		} else if element.Postsubmit {
			postsubmit := generatePostsubmitForTest(g, info, element.RunIfChanged, element.SkipIfOnlyChanged)
			postsubmit.MaxConcurrency = 1
			postsubmits[orgrepo] = append(postsubmits[orgrepo], *postsubmit)
		} else {
			presubmit := generatePresubmitForTest(g, element.As, info, element.RunIfChanged, element.SkipIfOnlyChanged, element.Optional)
			v, requestingKVM := configSpec.Resources.RequirementsForStep(element.As).Requests[cioperatorapi.KVMDeviceLabel]
			if requestingKVM {
				presubmit.Labels[cioperatorapi.KVMDeviceLabel] = v
			}
			presubmits[orgrepo] = append(presubmits[orgrepo], *presubmit)
		}
	}

	imageTargets := sets.NewString()
	if configSpec.PromotionConfiguration != nil {
		for additional := range configSpec.PromotionConfiguration.AdditionalImages {
			imageTargets.Insert(configSpec.PromotionConfiguration.AdditionalImages[additional])
		}
	}

	newJobBaseBuilder := func() *prowJobBaseBuilder {
		return NewProwJobBaseBuilder(configSpec, info, NewCiOperatorPodSpecGenerator())
	}

	if len(configSpec.Images) > 0 || imageTargets.Len() > 0 {
		imageTargets.Insert("[images]")
	}

	if len(imageTargets) > 0 {
		// Identify which jobs need to have a release payload explicitly requested
		var presubmitTargets = imageTargets.List()
		if promotion.PromotesOfficialImages(configSpec, promotion.WithOKD) {
			presubmitTargets = append(presubmitTargets, "[release:latest]")
		}
		jobBaseGen := newJobBaseBuilder().TestName("images")
		jobBaseGen.PodSpec.Add(Targets(presubmitTargets...))
		presubmits[orgrepo] = append(presubmits[orgrepo], *generatePresubmitForTest(jobBaseGen, "images", info, "", "", false))

		if configSpec.PromotionConfiguration != nil {
			jobBaseGen = newJobBaseBuilder().TestName("images")
			jobBaseGen.PodSpec.Add(Promotion(), Targets(imageTargets.List()...))
			postsubmit := generatePostsubmitForTest(jobBaseGen, info, "", "")
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
			jobBaseGen := newJobBaseBuilder().TestName(indexName)
			jobBaseGen.PodSpec.Add(Targets(indexName))
			presubmits[orgrepo] = append(presubmits[orgrepo], *generatePresubmitForTest(jobBaseGen, indexName, info, "", "", false))
		}
		if containsUnnamedBundle {
			name := string(api.PipelineImageStreamTagReferenceIndexImage)
			jobBaseGen := newJobBaseBuilder().TestName(name)
			jobBaseGen.PodSpec.Add(Targets(name))
			presubmits[orgrepo] = append(presubmits[orgrepo], *generatePresubmitForTest(jobBaseGen, name, info, "", "", false))
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

func generatePresubmitForTest(jobBaseBuilder *prowJobBaseBuilder, name string, info *ProwgenInfo, runIfChanged, skipIfOnlyChanged string, optional bool) *prowconfig.Presubmit {
	shortName := info.TestName(name)
	base := jobBaseBuilder.Rehearsable(true).Build(jc.PresubmitPrefix)
	return &prowconfig.Presubmit{
		JobBase:   base,
		AlwaysRun: runIfChanged == "" && skipIfOnlyChanged == "",
		Brancher:  prowconfig.Brancher{Branches: sets.NewString(jc.ExactlyBranch(info.Branch), jc.FeatureBranch(info.Branch)).List()},
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

func generatePostsubmitForTest(jobBaseBuilder *prowJobBaseBuilder, info *ProwgenInfo, runIfChanged, skipIfOnlyChanged string) *prowconfig.Postsubmit {
	base := jobBaseBuilder.Build(jc.PostsubmitPrefix)
	alwaysRun := runIfChanged == "" && skipIfOnlyChanged == ""
	return &prowconfig.Postsubmit{
		JobBase:   base,
		AlwaysRun: &alwaysRun,
		RegexpChangeMatcher: prowconfig.RegexpChangeMatcher{
			RunIfChanged:      runIfChanged,
			SkipIfOnlyChanged: skipIfOnlyChanged,
		},
		Brancher: prowconfig.Brancher{Branches: []string{jc.ExactlyBranch(info.Branch)}},
	}
}

// hashDailyCron returns a cron pattern derived from a hash of the job name that
// places the trigger between 22 and 04 UTC
func hashDailyCron(job string) string {
	h := fnv.New32()
	// hash writes never return errors
	_, _ = h.Write([]byte(job))
	jobHash := h.Sum32()
	minute := jobHash % 60
	hour := (22 + (jobHash % 6)) % 24
	return fmt.Sprintf("%d %d * * *", minute, hour)
}

func GeneratePeriodicForTest(jobBaseBuilder *prowJobBaseBuilder, info *ProwgenInfo, cron string, interval string, releaseController bool, pathAlias *string) *prowconfig.Periodic {
	// Periodics are rehearsable
	// We are resetting PathAlias because it will be set on the `ExtraRefs` item
	base := jobBaseBuilder.Rehearsable(true).PathAlias("").Build(jc.PeriodicPrefix)

	if cron == "@daily" {
		cron = hashDailyCron(base.Name)
	}

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
