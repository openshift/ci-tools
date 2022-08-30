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
//   - one presubmit for each test defined in config file
//   - if the config file has non-empty `images` section, generate an additional
//     presubmit and postsubmit that has `--target=[images]`. This postsubmit
//     will additionally pass `--promote` to ci-operator
//
// All these generated jobs will be labeled as "newly generated". After all
// new jobs are generated with GenerateJobs, the call site should also use
// Prune() function to remove all stale jobs and label the jobs as simply
// "generated".
func GenerateJobs(configSpec *cioperatorapi.ReleaseBuildConfiguration, info *ProwgenInfo) *prowconfig.JobConfig {
	orgrepo := fmt.Sprintf("%s/%s", info.Org, info.Repo)
	presubmits := map[string][]prowconfig.Presubmit{}
	postsubmits := map[string][]prowconfig.Postsubmit{}
	var periodics []prowconfig.Periodic
	disabledRehearsals := sets.NewString(info.Config.DisabledRehearsals...)

	for _, element := range configSpec.Tests {
		g := NewProwJobBaseBuilderForTest(configSpec, info, NewCiOperatorPodSpecGenerator(), element)
		disableRehearsal := info.Config.DisableAllRehearsals || disabledRehearsals.Has(element.As)

		if element.Cron != nil || element.Interval != nil || element.ReleaseController {
			cron := ""
			if element.Cron != nil {
				cron = *element.Cron
			}
			interval := ""
			if element.Interval != nil {
				interval = *element.Interval
			}
			periodic := GeneratePeriodicForTest(g, info, cron, interval, FromConfigSpec(configSpec), func(options *GeneratePeriodicOptions) {
				options.releaseController = element.ReleaseController
				options.disableRehearsal = disableRehearsal
			})
			periodics = append(periodics, *periodic)
		} else if element.Postsubmit {
			postsubmit := generatePostsubmitForTest(g, info, element.RunIfChanged, element.SkipIfOnlyChanged)
			postsubmit.MaxConcurrency = 1
			postsubmits[orgrepo] = append(postsubmits[orgrepo], *postsubmit)
		} else {
			presubmit := generatePresubmitForTest(g, element.As, info, func(options *generatePresubmitOptions) {
				options.runIfChanged = element.RunIfChanged
				options.skipIfOnlyChanged = element.SkipIfOnlyChanged
				options.optional = element.Optional
				options.disableRehearsal = disableRehearsal
			})
			v, requestingKVM := configSpec.Resources.RequirementsForStep(element.As).Requests[cioperatorapi.KVMDeviceLabel]
			if requestingKVM {
				presubmit.Labels[cioperatorapi.KVMDeviceLabel] = v
			}
			presubmits[orgrepo] = append(presubmits[orgrepo], *presubmit)
		}
	}

	newJobBaseBuilder := func() *prowJobBaseBuilder {
		return NewProwJobBaseBuilder(configSpec, info, NewCiOperatorPodSpecGenerator())
	}

	imageTargets := api.ImageTargets(configSpec)

	if len(imageTargets) > 0 {
		// Identify which jobs need to have a release payload explicitly requested
		var presubmitTargets = imageTargets.List()
		if api.PromotesOfficialImages(configSpec, api.WithOKD) {
			presubmitTargets = append(presubmitTargets, "[release:latest]")
		}
		jobBaseGen := newJobBaseBuilder().TestName("images")
		jobBaseGen.PodSpec.Add(Targets(presubmitTargets...))
		presubmits[orgrepo] = append(presubmits[orgrepo], *generatePresubmitForTest(jobBaseGen, "images", info))

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
			presubmits[orgrepo] = append(presubmits[orgrepo], *generatePresubmitForTest(jobBaseGen, indexName, info))
		}
		if containsUnnamedBundle {
			name := string(api.PipelineImageStreamTagReferenceIndexImage)
			jobBaseGen := newJobBaseBuilder().TestName(name)
			jobBaseGen.PodSpec.Add(Targets(name))
			presubmits[orgrepo] = append(presubmits[orgrepo], *generatePresubmitForTest(jobBaseGen, name, info))
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

type generatePresubmitOptions struct {
	runIfChanged      string
	skipIfOnlyChanged string
	optional          bool
	disableRehearsal  bool
}

type generatePresubmitOption func(options *generatePresubmitOptions)

func generatePresubmitForTest(jobBaseBuilder *prowJobBaseBuilder, name string, info *ProwgenInfo, options ...generatePresubmitOption) *prowconfig.Presubmit {
	opts := &generatePresubmitOptions{}
	for _, opt := range options {
		opt(opts)
	}

	shortName := info.TestName(name)
	base := jobBaseBuilder.Rehearsable(!opts.disableRehearsal).Build(jc.PresubmitPrefix)
	return &prowconfig.Presubmit{
		JobBase:   base,
		AlwaysRun: opts.runIfChanged == "" && opts.skipIfOnlyChanged == "",
		Brancher:  prowconfig.Brancher{Branches: sets.NewString(jc.ExactlyBranch(info.Branch), jc.FeatureBranch(info.Branch)).List()},
		Reporter: prowconfig.Reporter{
			Context: fmt.Sprintf("ci/prow/%s", shortName),
		},
		RerunCommand: prowconfig.DefaultRerunCommandFor(shortName),
		Trigger:      prowconfig.DefaultTriggerFor(shortName),
		RegexpChangeMatcher: prowconfig.RegexpChangeMatcher{
			RunIfChanged:      opts.runIfChanged,
			SkipIfOnlyChanged: opts.skipIfOnlyChanged,
		},
		Optional: opts.optional,
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

type GeneratePeriodicOptions struct {
	releaseController bool
	pathAlias         *string
	disableRehearsal  bool
}

type GeneratePeriodicOption func(options *GeneratePeriodicOptions)

func FromConfigSpec(configSpec *cioperatorapi.ReleaseBuildConfiguration) GeneratePeriodicOption {
	return func(options *GeneratePeriodicOptions) {
		options.pathAlias = configSpec.CanonicalGoRepository
	}
}

func GeneratePeriodicForTest(jobBaseBuilder *prowJobBaseBuilder, info *ProwgenInfo, cron string, interval string, options ...GeneratePeriodicOption) *prowconfig.Periodic {
	opts := &GeneratePeriodicOptions{}
	for _, opt := range options {
		opt(opts)
	}

	// We are resetting PathAlias because it will be set on the `ExtraRefs` item
	base := jobBaseBuilder.Rehearsable(!opts.disableRehearsal).PathAlias("").Build(jc.PeriodicPrefix)

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
	if opts.pathAlias != nil {
		ref.PathAlias = *opts.pathAlias
	}
	base.ExtraRefs = append([]prowv1.Refs{ref}, base.ExtraRefs...)
	if opts.releaseController {
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
