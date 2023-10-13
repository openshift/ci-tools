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
func GenerateJobs(configSpec *cioperatorapi.ReleaseBuildConfiguration, info *ProwgenInfo) (*prowconfig.JobConfig, error) {
	orgrepo := fmt.Sprintf("%s/%s", info.Org, info.Repo)
	presubmits := map[string][]prowconfig.Presubmit{}
	postsubmits := map[string][]prowconfig.Postsubmit{}
	var periodics []prowconfig.Periodic
	rehearsals := info.Config.Rehearsals
	disabledRehearsals := sets.New[string](rehearsals.DisabledRehearsals...)

	for _, element := range configSpec.Tests {
		g := NewProwJobBaseBuilderForTest(configSpec, info, NewCiOperatorPodSpecGenerator(), element)
		disableRehearsal := rehearsals.DisableAll || disabledRehearsals.Has(element.As)

		if element.IsPeriodic() {
			cron := ""
			if element.Cron != nil {
				cron = *element.Cron
			}
			interval := ""
			if element.Interval != nil {
				interval = *element.Interval
			}
			minimumInterval := ""
			if element.MinimumInterval != nil {
				minimumInterval = *element.MinimumInterval
			}
			periodic := GeneratePeriodicForTest(g, info, FromConfigSpec(configSpec), func(options *GeneratePeriodicOptions) {
				options.Cron = cron
				options.Interval = interval
				options.MinimumInterval = minimumInterval
				options.ReleaseController = element.ReleaseController
				options.DisableRehearsal = disableRehearsal
			})
			periodics = append(periodics, *periodic)
		} else if element.Postsubmit {
			postsubmit := generatePostsubmitForTest(g, info, func(options *generatePostsubmitOptions) {
				options.runIfChanged = element.RunIfChanged
				options.skipIfOnlyChanged = element.SkipIfOnlyChanged
			})
			postsubmit.MaxConcurrency = 1
			postsubmits[orgrepo] = append(postsubmits[orgrepo], *postsubmit)
		} else {
			presubmit := generatePresubmitForTest(g, element.As, info, func(options *generatePresubmitOptions) {
				options.runIfChanged = element.RunIfChanged
				options.skipIfOnlyChanged = element.SkipIfOnlyChanged
				options.defaultDisable = element.AlwaysRun != nil && !*element.AlwaysRun
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
	newJobBaseBuilderForPromotion := NewProwJobBaseBuilderForPromotion(configSpec, info, NewCiOperatorPodSpecGenerator())
	imageTargets := api.ImageTargets(configSpec)

	if len(imageTargets) > 0 {
		// Identify which jobs need to have a release payload explicitly requested
		var presubmitTargets = sets.List(imageTargets)
		if api.PromotesOfficialImages(configSpec, api.WithOKD) {
			presubmitTargets = append(presubmitTargets, "[release:latest]")
		}
		jobBaseGen := newJobBaseBuilder().TestName("images")
		jobBaseGen.PodSpec.Add(Targets(presubmitTargets...))
		presubmits[orgrepo] = append(presubmits[orgrepo], *generatePresubmitForTest(jobBaseGen, "images", info))

		if configSpec.PromotionConfiguration != nil {
			postsubmitsForPromotion, err := generatePostsubmitsForPromotion(newJobBaseBuilderForPromotion, info, func(options *generatePostsubmitOptions) {
				options.imageTargets = imageTargets
			})
			if err != nil {
				return nil, fmt.Errorf("error generating postsubmits for promotion: %w", err)
			}
			postsubmits[orgrepo] = append(postsubmits[orgrepo], postsubmitsForPromotion...)
		}
	}

	if configSpec.Operator != nil {
		containsUnnamedBundle := false
		for _, bundle := range configSpec.Operator.Bundles {
			if bundle.As == "" {
				containsUnnamedBundle = true
				continue
			}
			testName := api.IndexName(bundle.As)
			if bundle.SkipBuildingIndex {
				testName = fmt.Sprintf("ci-bundle-%s", bundle.As)
			}
			jobBaseGen := newJobBaseBuilder().TestName(testName)
			if bundle.SkipBuildingIndex {
				jobBaseGen.PodSpec.Add(Targets(bundle.As))
			} else {
				jobBaseGen.PodSpec.Add(Targets(testName))
			}
			presubmits[orgrepo] = append(presubmits[orgrepo], *generatePresubmitForTest(jobBaseGen, testName, info, func(options *generatePresubmitOptions) {
				options.optional = bundle.Optional
			}))
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
	}, nil
}

func NewProwJobBaseBuilderForPromotion(configSpec *cioperatorapi.ReleaseBuildConfiguration,
	info *ProwgenInfo, podSpecGenerator CiOperatorPodSpecGenerator) func() *prowJobBaseBuilder {
	return func() *prowJobBaseBuilder {
		builder := NewProwJobBaseBuilder(configSpec, info, podSpecGenerator)
		if info.Config.MultiArch {
			builder.Cluster(api.ClusterMulti01).WithLabel(api.ClusterLabel, string(api.ClusterMulti01))
		}
		return builder
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
	defaultDisable    bool
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
	triggerCommand := prowconfig.DefaultTriggerFor(shortName)
	if opts.defaultDisable && opts.runIfChanged == "" && opts.skipIfOnlyChanged == "" && !opts.optional {
		triggerCommand = fmt.Sprintf(`(?m)^/test( | .* )(%s|%s),?($|\s.*)`, shortName, "remaining-required")
	}
	return &prowconfig.Presubmit{
		JobBase:   base,
		AlwaysRun: opts.runIfChanged == "" && opts.skipIfOnlyChanged == "" && !opts.defaultDisable,
		Brancher:  prowconfig.Brancher{Branches: sets.List(sets.New[string](jc.ExactlyBranch(info.Branch), jc.FeatureBranch(info.Branch)))},
		Reporter: prowconfig.Reporter{
			Context: fmt.Sprintf("ci/prow/%s", shortName),
		},
		RerunCommand: prowconfig.DefaultRerunCommandFor(shortName),
		Trigger:      triggerCommand,
		RegexpChangeMatcher: prowconfig.RegexpChangeMatcher{
			RunIfChanged:      opts.runIfChanged,
			SkipIfOnlyChanged: opts.skipIfOnlyChanged,
		},
		Optional: opts.optional,
	}
}

type generatePostsubmitOptions struct {
	runIfChanged      string
	skipIfOnlyChanged string
	imageTargets      sets.Set[string]
}

type generatePostsubmitOption func(options *generatePostsubmitOptions)

func generatePostsubmitForTest(jobBaseBuilder *prowJobBaseBuilder, info *ProwgenInfo, options ...generatePostsubmitOption) *prowconfig.Postsubmit {
	opts := &generatePostsubmitOptions{}
	for _, opt := range options {
		opt(opts)
	}

	base := jobBaseBuilder.Build(jc.PostsubmitPrefix)
	alwaysRun := opts.runIfChanged == "" && opts.skipIfOnlyChanged == ""
	return &prowconfig.Postsubmit{
		JobBase:   base,
		AlwaysRun: &alwaysRun,
		RegexpChangeMatcher: prowconfig.RegexpChangeMatcher{
			RunIfChanged:      opts.runIfChanged,
			SkipIfOnlyChanged: opts.skipIfOnlyChanged,
		},
		Brancher: prowconfig.Brancher{Branches: []string{jc.ExactlyBranch(info.Branch)}},
	}
}

func generatePostsubmitsForPromotion(jobBaseBuilderFactory func() *prowJobBaseBuilder, info *ProwgenInfo, options ...generatePostsubmitOption) ([]prowconfig.Postsubmit, error) {
	opts := &generatePostsubmitOptions{}
	for _, opt := range options {
		opt(opts)
	}
	architectures := append([]api.ReleaseArchitecture{api.ReleaseArchitectureAMD64}, info.Config.AdditionalArchitectures...)
	postsubmits := make([]prowconfig.Postsubmit, 0, len(architectures))
	for _, arch := range architectures {
		jobBaseBuilder := jobBaseBuilderFactory()
		var jobBaseGen *prowJobBaseBuilder
		if arch != api.ReleaseArchitectureAMD64 {
			testName := fmt.Sprintf("images-%s", string(arch))
			cluster := arch.GetMappedCluster()
			if cluster == "" {
				return nil, fmt.Errorf("no cluster found for arch %s", string(arch))
			}
			jobBaseGen = jobBaseBuilder.Cluster(cluster).TestName(testName).WithLabel(api.ClusterLabel, string(cluster))
		} else {
			jobBaseGen = jobBaseBuilder.TestName("images")
		}

		jobBaseGen.PodSpec.Add(Promotion(), Targets(sets.List(opts.imageTargets)...))
		postsubmit := generatePostsubmitForTest(jobBaseGen, info)

		postsubmit.MaxConcurrency = 1
		if postsubmit.Labels == nil {
			postsubmit.Labels = map[string]string{}
		}
		postsubmit.Labels[cioperatorapi.PromotionJobLabelKey] = "true"

		postsubmits = append(postsubmits, *postsubmit)
	}

	return postsubmits, nil
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
	Interval          string
	MinimumInterval   string
	Cron              string
	ReleaseController bool
	PathAlias         *string
	DisableRehearsal  bool
}

type GeneratePeriodicOption func(options *GeneratePeriodicOptions)

func FromConfigSpec(configSpec *cioperatorapi.ReleaseBuildConfiguration) GeneratePeriodicOption {
	return func(options *GeneratePeriodicOptions) {
		options.PathAlias = configSpec.CanonicalGoRepository
	}
}

func GeneratePeriodicForTest(jobBaseBuilder *prowJobBaseBuilder, info *ProwgenInfo, options ...GeneratePeriodicOption) *prowconfig.Periodic {
	opts := &GeneratePeriodicOptions{}
	for _, opt := range options {
		opt(opts)
	}

	// We are resetting PathAlias because it will be set on the `ExtraRefs` item
	base := jobBaseBuilder.Rehearsable(!opts.DisableRehearsal).PathAlias("").Build(jc.PeriodicPrefix)

	cron := opts.Cron
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
	if opts.PathAlias != nil {
		ref.PathAlias = *opts.PathAlias
	}
	base.ExtraRefs = append([]prowv1.Refs{ref}, base.ExtraRefs...)
	if opts.ReleaseController {
		opts.Interval = ""
		cron = "@yearly"
		base.Labels[jc.ReleaseControllerLabel] = jc.ReleaseControllerValue
	}
	return &prowconfig.Periodic{
		JobBase:         base,
		Cron:            cron,
		Interval:        opts.Interval,
		MinimumInterval: opts.MinimumInterval,
	}
}
