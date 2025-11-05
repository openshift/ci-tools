package prowgen

import (
	"fmt"
	"hash/fnv"

	"k8s.io/apimachinery/pkg/util/sets"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"

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
		shardCount := 1
		if element.ShardCount != nil {
			shardCount = *element.ShardCount
		}

		// Most of the time, this loop will only run once. the exception is if shard_count is set to an integer greater than 1
		for i := 1; i <= shardCount; i++ {
			g := NewProwJobBaseBuilderForTest(configSpec, info, NewCiOperatorPodSpecGenerator(), element)
			name := element.As
			if shardCount > 1 {
				name = fmt.Sprintf("%s-%dof%d", name, i, shardCount)
				g.TestName(name)
				g.PodSpec.Add(ShardArgs(shardCount, i))
			}

			if element.NodeArchitecture != "" {
				g.WithLabel(fmt.Sprintf("capability/%s", element.NodeArchitecture), string(element.NodeArchitecture))
			}

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

				if element.NodeArchitecture != "" && element.NodeArchitecture != cioperatorapi.NodeArchitectureAMD64 {
					injectCapabilities(g.base.Labels, []string{string(element.NodeArchitecture)})
				}

				periodic := GeneratePeriodicForTest(g, info, FromConfigSpec(configSpec), func(options *GeneratePeriodicOptions) {
					options.Cron = cron
					options.Capabilities = element.Capabilities
					options.Interval = interval
					options.MinimumInterval = minimumInterval
					options.ReleaseController = element.ReleaseController
					options.DisableRehearsal = disableRehearsal
					options.Retry = element.Retry
				})
				periodics = append(periodics, *periodic)
				if element.Presubmit {
					handlePresubmit(g, element, info, name, disableRehearsal, configSpec.Resources.RequirementsForStep(element.As).Requests, presubmits, orgrepo)
				}
			} else if element.Postsubmit {
				postsubmit := generatePostsubmitForTest(g, info, func(options *generatePostsubmitOptions) {
					options.runIfChanged = element.RunIfChanged
					options.Capabilities = element.Capabilities
					options.skipIfOnlyChanged = element.SkipIfOnlyChanged
				})
				postsubmit.MaxConcurrency = 1
				postsubmits[orgrepo] = append(postsubmits[orgrepo], *postsubmit)
			} else {
				handlePresubmit(g, element, info, name, disableRehearsal, configSpec.Resources.RequirementsForStep(element.As).Requests, presubmits, orgrepo)
			}
		}
	}

	newJobBaseBuilder := func() *prowJobBaseBuilder {
		return NewProwJobBaseBuilder(configSpec, info, NewCiOperatorPodSpecGenerator())
	}

	imageTargets := cioperatorapi.ImageTargets(configSpec)

	if len(imageTargets) > 0 {
		// Identify which jobs need to have a release payload explicitly requested
		var presubmitTargets = sets.List(imageTargets)
		if cioperatorapi.PromotesOfficialImages(configSpec, cioperatorapi.WithOKD) {
			presubmitTargets = append(presubmitTargets, "[release:latest]")
		}
		imagesTestName := "images"
		jobBaseGen := newJobBaseBuilder().TestName(imagesTestName)
		injectArchitectureLabels(jobBaseGen, configSpec.Images)

		optional := false
		for _, image := range configSpec.Images {
			if image.Optional {
				optional = true
				break
			}
		}

		jobBaseGen.PodSpec.Add(Targets(presubmitTargets...))
		presubmits[orgrepo] = append(presubmits[orgrepo], *generatePresubmitForTest(jobBaseGen, imagesTestName, info, func(options *generatePresubmitOptions) {
			options.optional = optional
		}))

		if configSpec.PromotionConfiguration != nil {
			jobBaseGen = newJobBaseBuilder().TestName(imagesTestName)
			injectArchitectureLabels(jobBaseGen, configSpec.Images)

			jobBaseGen.PodSpec.Add(Promotion(), Targets(imageTargets.UnsortedList()...))
			// Note: Slack reporter config for images postsubmit is now handled in generatePostsubmitForTest
			postsubmit := generatePostsubmitForTest(jobBaseGen, info)
			postsubmit.MaxConcurrency = 1
			if postsubmit.Labels == nil {
				postsubmit.Labels = map[string]string{}
			}
			postsubmit.Labels[cioperatorapi.PromotionJobLabelKey] = "true"
			postsubmits[orgrepo] = append(postsubmits[orgrepo], *postsubmit)
			if configSpec.PromotionConfiguration.Cron != "" {
				periodic := GeneratePeriodicForTest(jobBaseGen, info, func(options *GeneratePeriodicOptions) {
					//not needed in promotion job, the same way postsubmit does not need it
					options.DisableRehearsal = true
					options.Cron = configSpec.PromotionConfiguration.Cron
				})
				periodic.MaxConcurrency = 1
				if periodic.Labels == nil {
					periodic.Labels = map[string]string{}
				}
				periodic.Labels[cioperatorapi.PromotionJobLabelKey] = "true"
				periodics = append(periodics, *periodic)

			}
		}
	}

	if configSpec.Operator != nil && !info.Config.SkipPresubmits(configSpec.Metadata.Branch, configSpec.Metadata.Variant) {
		containsUnnamedBundle := false
		for _, bundle := range configSpec.Operator.Bundles {
			if bundle.As == "" {
				containsUnnamedBundle = true
				continue
			}
			testName := cioperatorapi.IndexName(bundle.As)
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
				options.Capabilities = bundle.Capabilities
			}))
		}
		if containsUnnamedBundle {
			name := string(cioperatorapi.PipelineImageStreamTagReferenceIndexImage)
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

func handlePresubmit(g *prowJobBaseBuilder, element cioperatorapi.TestStepConfiguration, info *ProwgenInfo, name string, disableRehearsal bool, requests cioperatorapi.ResourceList, presubmits map[string][]prowconfig.Presubmit, orgrepo string) {
	presubmit := generatePresubmitForTest(g, name, info, func(options *generatePresubmitOptions) {
		options.pipelineRunIfChanged = element.PipelineRunIfChanged
		options.Capabilities = element.Capabilities
		options.runIfChanged = element.RunIfChanged
		options.skipIfOnlyChanged = element.SkipIfOnlyChanged
		options.defaultDisable = element.AlwaysRun != nil && !*element.AlwaysRun
		options.optional = element.Optional
		options.disableRehearsal = disableRehearsal
	})
	v, requestingKVM := requests[cioperatorapi.KVMDeviceLabel]
	if requestingKVM {
		presubmit.Labels[cioperatorapi.KVMDeviceLabel] = v
	}
	presubmits[orgrepo] = append(presubmits[orgrepo], *presubmit)
}

func testContainsLease(test *cioperatorapi.TestStepConfiguration) bool {
	// this is predicated upon the config being fully resolved at this time.
	if test.MultiStageTestConfigurationLiteral == nil {
		return false
	}

	return len(cioperatorapi.LeasesForTest(test.MultiStageTestConfigurationLiteral)) > 0
}

type generatePresubmitOptions struct {
	pipelineRunIfChanged string
	Capabilities         []string
	runIfChanged         string
	skipIfOnlyChanged    string
	defaultDisable       bool
	optional             bool
	disableRehearsal     bool
}

func (opts *generatePresubmitOptions) shouldAlwaysRun() bool {
	return opts.runIfChanged == "" && opts.skipIfOnlyChanged == "" && !opts.defaultDisable && opts.pipelineRunIfChanged == ""
}

type generatePresubmitOption func(options *generatePresubmitOptions)

// addSlackReporterConfig sets the Slack reporter configuration on a job base if one is found
func addSlackReporterConfig(base *prowconfig.JobBase, jobName, testName string, info *ProwgenInfo) {
	if slackReporter := info.Config.GetSlackReporterConfigForJobName(jobName, testName, info.Metadata.Variant); slackReporter != nil {
		if base.ReporterConfig == nil {
			base.ReporterConfig = &prowv1.ReporterConfig{}
		}
		base.ReporterConfig.Slack = &prowv1.SlackReporterConfig{
			Channel:           slackReporter.Channel,
			JobStatesToReport: slackReporter.JobStatesToReport,
			ReportTemplate:    slackReporter.ReportTemplate,
		}
	}
}

func generatePresubmitForTest(jobBaseBuilder *prowJobBaseBuilder, name string, info *ProwgenInfo, options ...generatePresubmitOption) *prowconfig.Presubmit {
	opts := &generatePresubmitOptions{}
	for _, opt := range options {
		opt(opts)
	}

	shortName := info.TestName(name)
	base := jobBaseBuilder.Rehearsable(!opts.disableRehearsal).Build(jc.PresubmitPrefix)

	// Set slack reporter config using full job name for proper excluded_job_patterns matching
	fullJobName := info.JobName(jc.PresubmitPrefix, name)
	addSlackReporterConfig(&base, fullJobName, name, info)

	pipelineOpt := false
	if opts.pipelineRunIfChanged != "" {
		if base.Annotations == nil {
			base.Annotations = make(map[string]string)
		}
		base.Annotations["pipeline_run_if_changed"] = opts.pipelineRunIfChanged
		pipelineOpt = true
	}
	triggerCommand := prowconfig.DefaultTriggerFor(shortName)
	if opts.defaultDisable && opts.runIfChanged == "" && opts.skipIfOnlyChanged == "" && !opts.optional && !pipelineOpt {
		triggerCommand = fmt.Sprintf(`(?m)^/test( | .* )(%s|%s),?($|\s.*)`, shortName, "remaining-required")
	}
	pj := &prowconfig.Presubmit{
		JobBase:   base,
		AlwaysRun: opts.shouldAlwaysRun(),
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
	injectCapabilities(pj.Labels, opts.Capabilities)
	return pj
}

type generatePostsubmitOptions struct {
	runIfChanged      string
	Capabilities      []string
	skipIfOnlyChanged string
}

type generatePostsubmitOption func(options *generatePostsubmitOptions)

func generatePostsubmitForTest(jobBaseBuilder *prowJobBaseBuilder, info *ProwgenInfo, options ...generatePostsubmitOption) *prowconfig.Postsubmit {
	opts := &generatePostsubmitOptions{}
	for _, opt := range options {
		opt(opts)
	}

	base := jobBaseBuilder.Build(jc.PostsubmitPrefix)

	// Set slack reporter config using full job name for proper excluded_job_patterns matching
	testName := jobBaseBuilder.testName
	fullJobName := info.JobName(jc.PostsubmitPrefix, testName)
	addSlackReporterConfig(&base, fullJobName, testName, info)

	alwaysRun := opts.runIfChanged == "" && opts.skipIfOnlyChanged == ""
	pj := &prowconfig.Postsubmit{
		JobBase:   base,
		AlwaysRun: &alwaysRun,
		RegexpChangeMatcher: prowconfig.RegexpChangeMatcher{
			RunIfChanged:      opts.runIfChanged,
			SkipIfOnlyChanged: opts.skipIfOnlyChanged,
		},
		Brancher: prowconfig.Brancher{Branches: []string{jc.ExactlyBranch(info.Branch)}},
	}
	injectCapabilities(pj.Labels, opts.Capabilities)
	return pj
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
	Capabilities      []string
	Cron              string
	ReleaseController bool
	PathAlias         *string
	DisableRehearsal  bool
	Retry             *prowconfig.Retry
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

	// Set slack reporter config using full job name for proper excluded_job_patterns matching
	testName := jobBaseBuilder.testName
	fullJobName := info.JobName(jc.PeriodicPrefix, testName)
	addSlackReporterConfig(&base, fullJobName, testName, info)

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
	pj := &prowconfig.Periodic{
		JobBase:         base,
		Cron:            cron,
		Interval:        opts.Interval,
		MinimumInterval: opts.MinimumInterval,
		Retry:           opts.Retry,
	}
	injectCapabilities(pj.Labels, opts.Capabilities)
	return pj
}

func injectCapabilities(labels map[string]string, capabilities []string) {
	for _, c := range capabilities {
		labels[fmt.Sprintf("capability/%s", c)] = c
	}
}

func injectArchitectureLabels(g *prowJobBaseBuilder, imagesConfig []cioperatorapi.ProjectDirectoryImageBuildStepConfiguration) {
	for _, imageConfig := range imagesConfig {
		for _, arch := range imageConfig.AdditionalArchitectures {
			g.WithLabel(fmt.Sprintf("capability/%s", arch), arch)
		}
	}
}
