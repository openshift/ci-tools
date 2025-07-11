package rehearse

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/getlantern/deepcopy"
	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes/fake"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	coretesting "k8s.io/client-go/testing"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	pjapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/pjutil"

	"github.com/openshift/ci-tools/pkg/api"
	apihelper "github.com/openshift/ci-tools/pkg/api/helper"
	"github.com/openshift/ci-tools/pkg/api/utils"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/diffs"
	"github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/registry"
	"github.com/openshift/ci-tools/pkg/util"
	"github.com/openshift/ci-tools/pkg/util/gzip"
)

const (
	// Label is the label key for the pull request we are rehearsing for
	Label = "ci.openshift.io/rehearse"
	// LabelContext exposes the context the job would have had running normally
	LabelContext = "ci.openshift.io/rehearse.context"

	logRehearsalJob   = "rehearsal-job"
	logCiopConfigFile = "ciop-config-file"

	clusterTypeEnvName = "CLUSTER_TYPE"
	pjRehearse         = "pj-rehearse"
)

// Number of openshift versions
var numVersion = 50

// Global map that contains relevance of known branches
var relevancy = map[string]int{
	"master": numVersion + 1,
	"main":   numVersion + 1,
}

func init() {
	for i := 1; i < numVersion; i++ {
		relevancy[fmt.Sprintf("release-4.%d", i)] = i
		relevancy[fmt.Sprintf("openshift-4.%d", i)] = i
	}
}

// NewProwJobClient creates a ProwJob client with a dry run capability
func NewProwJobClient(clusterConfig *rest.Config, dry bool) (ctrlruntimeclient.Client, error) {
	if dry {
		return fakectrlruntimeclient.NewClientBuilder().Build(), nil
	}
	return ctrlruntimeclient.New(clusterConfig, ctrlruntimeclient.Options{})
}

// NewCMClient creates a configMap client with a dry run capability
func NewCMClient(clusterConfig *rest.Config, namespace string, dry bool) (coreclientset.ConfigMapInterface, error) {
	if dry {
		c := fake.NewSimpleClientset()
		c.PrependReactor("update", "configmaps", func(action coretesting.Action) (bool, runtime.Object, error) {
			cm := action.(coretesting.UpdateAction).GetObject().(*v1.ConfigMap)
			y, err := yaml.Marshal([]*v1.ConfigMap{cm})
			if err != nil {
				return true, nil, fmt.Errorf("failed to convert ConfigMap to YAML: %w", err)
			}
			fmt.Print(string(y))
			return false, nil, nil
		})
		return c.CoreV1().ConfigMaps(namespace), nil
	}

	cmClient, err := coreclientset.NewForConfig(clusterConfig)
	if err != nil {
		return nil, fmt.Errorf("could not get core client for cluster config: %w", err)
	}

	return cmClient.ConfigMaps(namespace), nil
}

// BranchFromRegexes undoes the changes we add to a branch name to make it
// an explicit regular expression. We can simply remove the "^$" pre/suffix
// and we know that `\` is an invalid character in Git branch names, so any
// that exist in the name have been placed there by regexp.QuoteMeta() and
// can simply be removed as well.
// Iterates over all branches and returns an empty string when no branch
// is a simple branch name after the stripping
func BranchFromRegexes(branches []string) string {
	for i := range branches {
		branch := strings.ReplaceAll(strings.TrimPrefix(strings.TrimSuffix(branches[i], "$"), "^"), "\\", "")
		if branch != "" && jobconfig.SimpleBranchRegexp.MatchString(branch) {
			return branch
		}
	}

	return ""
}

func makeRehearsalPresubmit(source *prowconfig.Presubmit, repo string, refs *pjapi.Refs) (*prowconfig.Presubmit, error) {
	var rehearsal prowconfig.Presubmit
	if err := deepcopy.Copy(&rehearsal, source); err != nil {
		return nil, fmt.Errorf("deepCopy failed: %w", err)
	}

	prNumber := refs.Pulls[0].Number
	rehearsal.Name = fmt.Sprintf("rehearse-%d-%s", prNumber, source.Name)

	var branch string
	var ghContext string

	if len(source.Branches) > 0 {
		branch = BranchFromRegexes(source.Branches)
		if len(repo) > 0 {
			orgRepo := strings.Split(repo, "/")
			jobOrg := orgRepo[0]
			jobRepo := orgRepo[1]

			if refs.Org != jobOrg || refs.Repo != jobRepo {
				// we need to add the "original" refs that the job will be testing
				// from the target repo with all the "extra" fields from the job
				// config, like path_alias, then remove them from the config so we
				// don't use them in the future for any other refs
				rehearsal.ExtraRefs = append(rehearsal.ExtraRefs, *pjutil.CompletePrimaryRefs(pjapi.Refs{
					Org:            jobOrg,
					Repo:           jobRepo,
					BaseRef:        branch,
					WorkDir:        true,
					PathAlias:      rehearsal.PathAlias,
					CloneURI:       rehearsal.CloneURI,
					SkipSubmodules: rehearsal.SkipSubmodules,
					CloneDepth:     rehearsal.CloneDepth,
				}, source.JobBase))
				rehearsal.PathAlias = ""
				rehearsal.CloneURI = ""
				rehearsal.SkipSubmodules = false
				rehearsal.CloneDepth = 0
			}
			ghContext += repo + "/"
		}
		ghContext += branch + "/"
	}

	shortName := contextFor(source)
	rehearsal.Context = fmt.Sprintf("ci/rehearse/%s%s", ghContext, shortName)

	rehearsal.RerunCommand = "/pj-rehearse " + source.Name
	rehearsal.Trigger = "/pj-rehearse " + source.Name
	rehearsal.Optional = true
	if rehearsal.Labels == nil {
		rehearsal.Labels = map[string]string{}
	}
	rehearsal.Labels[Label] = strconv.Itoa(prNumber)
	rehearsal.Labels[LabelContext] = shortName
	rehearsal.Labels = utils.SanitizeLabels(rehearsal.Labels)

	// rehearsals should not report anything via Slack etc
	rehearsal.ReporterConfig = nil

	return &rehearsal, nil
}

// contextFor returns the shortest context we can use to identify this job
func contextFor(source *prowconfig.Presubmit) string {
	if source.Context != "" {
		// an informal convention is to add categories to a context with prefixed, slash-delimited tokens
		return source.Context[strings.LastIndex(source.Context, "/")+1:]
	} else {
		// while Prow presubmits *must* have a context set, the rehearsal tool coerces periodics into
		// the presubmit type before processing, so we may not have one
		return source.Name
	}

}

func filterPresubmits(changedPresubmits config.Presubmits, disabledJobs []string, logger logrus.FieldLogger) config.Presubmits {
	presubmits := config.Presubmits{}
	for repo, jobs := range changedPresubmits {
		for _, job := range jobs {
			jobLogger := logger.WithFields(logrus.Fields{"repo": repo, "job-name": job.Name})

			if job.Hidden {
				jobLogger.Debug("hidden jobs are not allowed to be rehearsed")
				continue
			}

			if !hasRehearsableLabel(job.Labels) {
				jobLogger.Debugf("job is not allowed to be rehearsed. Label %s is required", jobconfig.CanBeRehearsedLabel)
				continue
			}

			if len(job.Branches) == 0 {
				jobLogger.Debug("cannot rehearse jobs with no branches")
				continue
			}

			if slices.Contains(disabledJobs, job.Name) {
				jobLogger.Debug("cannot rehearse due to 'restrict_network_access' set to 'false' without appropriate label on PR")
				continue
			}

			presubmits.Add(repo, job, config.GetSourceType(job.Labels))
		}
	}

	return presubmits
}

func filterPeriodics(changedPeriodics config.Periodics, disabledJobs []string, logger logrus.FieldLogger) config.Periodics {
	periodics := config.Periodics{}
	for _, periodic := range changedPeriodics {
		jobLogger := logger.WithField("job-name", periodic.Name)

		if periodic.Hidden {
			jobLogger.Warn("hidden jobs are not allowed to be rehearsed")
			continue
		}

		if !hasRehearsableLabel(periodic.Labels) {
			jobLogger.Debugf("job is not allowed to be rehearsed. Label %s is required", jobconfig.CanBeRehearsedLabel)
			continue
		}

		if slices.Contains(disabledJobs, periodic.Name) {
			jobLogger.Debug("cannot rehearse due to 'restrict_network_access' set to 'false' without appropriate label on PR")
			continue
		}

		periodics.Add(periodic, config.GetSourceType(periodic.Labels))
	}

	return periodics
}

func hasRehearsableLabel(labels map[string]string) bool {
	if value, ok := labels[jobconfig.CanBeRehearsedLabel]; !ok || value != "true" {
		return false
	}
	return true
}

// getResolverConfigForTest returns a resolved ci-operator based on the provided filename and only includes the specified test in the
// `tests` section of the config. If `testname` is empty, the resolved config will contain all items from the original `tests`.
// The ImageStreamTagMap contains all imagestreamtags used within this config and is used to ensure they exist on all target clusters.
func getResolvedConfigForTest(ciopConfig config.DataWithInfo, resolver registry.Resolver, testname string) (string, apihelper.ImageStreamTagMap, error) {
	// make copy so we don't change in-memory config
	ciopCopy := config.DataWithInfo{
		Configuration: ciopConfig.Configuration,
		Info:          ciopConfig.Info,
	}
	// only include the test we need to reduce env var size
	ciopCopy.Configuration.Tests = []api.TestStepConfiguration{}
	for _, test := range ciopConfig.Configuration.Tests {
		if testname == "" || test.As == testname {
			ciopCopy.Configuration.Tests = append(ciopCopy.Configuration.Tests, test)
		}
	}

	ciopConfigResolved, err := registry.ResolveConfig(resolver, ciopCopy.Configuration)
	if err != nil {
		return "", nil, fmt.Errorf("failed resolve ReleaseBuildConfiguration: %w", err)
	}

	ciOpConfigContent, err := yaml.Marshal(ciopConfigResolved)
	if err != nil {
		return "", nil, fmt.Errorf("failed to marshal ci-operator config file: %w", err)
	}

	imageStreamTags, err := apihelper.TestInputImageStreamTagsFromResolvedConfig(ciopConfigResolved, nil)
	if err != nil {
		return "", nil, fmt.Errorf("failed to resolve test imagestreamtags: %w", err)
	}
	return string(ciOpConfigContent), imageStreamTags, nil
}

// inlineCiOpConfig detects whether a Container in a rehearsed job uses
// a ci-operator config file. If so, it uploads the resolved configuration coming
// from the content of the release repository, and modifies the Container so that its
// environment has a CONFIG_SPEC_GCS_URL variable containing the uploaded location.
// This needs to happen because the config files or step registry content they
// refer to may change in the PR that triggered a rehearsal, and the rehearsals
// must use all content changed in this way.
//
// Also returns an ImageStreamTagMap with that contains all imagestreamtags used
// within the inlined config (this is needed to later ensure they exist on all
// target clusters where the rehearsals will execute).
func (jc *JobConfigurer) inlineCiOpConfig(container *v1.Container, ciopConfigs config.DataByFilename, resolver registry.Resolver, metadata api.Metadata, testName, jobName string, logger *logrus.Entry) (apihelper.ImageStreamTagMap, error) {
	allImageStreamTags := apihelper.ImageStreamTagMap{}
	if len(container.Command) < 1 || container.Command[0] != "ci-operator" {
		return allImageStreamTags, nil
	}

	var hasConfigEnv bool
	var ciopConfig config.DataWithInfo
	var envs []v1.EnvVar
	for idx, env := range container.Env {
		switch {
		case env.Name == "CONFIG_SPEC" && env.ValueFrom != nil:
			// job attempts to get CONFIG_SPEC from cluster resource, which is weird,
			// unexpected and we cannot support rehearsals for that
			return nil, fmt.Errorf("CONFIG_SPEC is set from a cluster resource, cannot rehearse such job")
		case env.Name == "UNRESOLVED_CONFIG" && env.ValueFrom != nil:
			// job attempts to get UNRESOLVED_CONFIG from cluster resource, which is weird,
			// unexpected and we cannot support rehearsals for that
			return nil, fmt.Errorf("UNRESOLVED_CONFIG is set from a cluster resource, cannot rehearse such job")
		case env.Name == "CONFIG_SPEC" && env.Value != "":
			// job already has inline CONFIG_SPEC: we should not modify it
			return allImageStreamTags, nil
		case env.Name == "UNRESOLVED_CONFIG" && env.Value != "":
			if err := yaml.Unmarshal([]byte(env.Value), &ciopConfig.Configuration); err != nil {
				return nil, fmt.Errorf("failed to unmarshal UNRESOLVED_CONFIG: %w", err)
			}
			// Annoying hack: UNRESOLVED_CONFIG means this is a handcrafted job, which means
			// `testname` cannot be relied on (it is derived from job name, which is arbitrary
			// in handcrafted jobs). We need the test name to know which `tests` field to
			// resolve, so we try to detect it from `--target` arg, if present.
			//
			// The worst case is that we do not find the matching name. In such case,
			// the inlined config will contain all items from `tests` stanza.
			testName = ""
			for idx, arg := range container.Args {
				if strings.HasPrefix(arg, "--target=") {
					testName = strings.TrimPrefix(arg, "--target=")
					break
				}
				if arg == "--target" {
					if len(container.Args) == (idx + 1) {
						return nil, errors.New("plain '--target' is a last arg, expected to be followed with a value")
					}
					testName = container.Args[idx+1]
					break
				}
			}
			hasConfigEnv = true
		default:
			// Another envvar, we just need to keep it
			envs = append(envs, container.Env[idx])
		}
	}

	if !hasConfigEnv {
		if err := metadata.IsComplete(); err != nil {
			return nil, fmt.Errorf("could not infer which ci-operator config this job uses: %w", err)
		}
		filename := metadata.Basename()
		if _, ok := ciopConfigs[filename]; !ok {
			return nil, fmt.Errorf("ci-operator config file %s was not found", filename)
		}
		ciopConfig = ciopConfigs[filename]
		logger.WithField(logCiopConfigFile, filename).Debug("Rehearsal job would use ci-operator config from registry, its content will be inlined")
	}

	ciOpConfigContent, imageStreamTags, err := getResolvedConfigForTest(ciopConfig, resolver, testName)
	if err != nil {
		logger.WithError(err).Error("Failed to get resolved config for test")
		return nil, err
	}
	apihelper.MergeImageStreamTagMaps(allImageStreamTags, imageStreamTags)
	if !jc.dryRun {
		pull := jc.refs.Pulls[0]
		fileLocation := fmt.Sprintf("%s/configs/%s/%s/%d/%s/%s", pjRehearse, jc.refs.Org, jc.refs.Repo, pull.Number, pull.SHA, jobName)
		location, err := jc.uploader.UploadConfigSpec(context.Background(), fileLocation, ciOpConfigContent)
		if err != nil {
			return nil, fmt.Errorf("failed to save config to GCS bucket: %w", err)
		}
		container.Env = append(envs, v1.EnvVar{Name: "CONFIG_SPEC_GCS_URL", Value: location})
	} else {
		compressedConfig, err := gzip.CompressStringAndBase64(ciOpConfigContent)
		if err != nil {
			return nil, fmt.Errorf("couldn't compress and base64 encode CONFIG_SPEC: %w", err)
		}
		container.Env = append(envs, v1.EnvVar{Name: "CONFIG_SPEC", Value: compressedConfig})
	}

	return allImageStreamTags, nil
}

type configSpecUploader interface {
	UploadConfigSpec(ctx context.Context, location, ciOpConfigContent string) (string, error)
}

// JobConfigurer holds all the information that is needed for the configuration of the jobs.
type JobConfigurer struct {
	dryRun                bool
	ciopConfigs           config.DataByFilename
	prowConfig            *prowconfig.Config
	registryResolver      registry.Resolver
	clusterProfileCMNames map[string]string
	refs                  *pjapi.Refs
	uploader              configSpecUploader
	logger                *logrus.Entry
}

// NewJobConfigurer filters the jobs and returns a new JobConfigurer.
func NewJobConfigurer(
	dryRun bool,
	ciopConfigs config.DataByFilename,
	prowConfig *prowconfig.Config,
	resolver registry.Resolver,
	logger *logrus.Entry,
	refs *pjapi.Refs,
	uploader configSpecUploader) *JobConfigurer {
	return &JobConfigurer{
		dryRun:           dryRun,
		ciopConfigs:      ciopConfigs,
		prowConfig:       prowConfig,
		registryResolver: resolver,
		refs:             refs,
		uploader:         uploader,
		logger:           logger,
	}
}

func VariantFromLabels(labels map[string]string) string {
	variant := ""
	if variantLabel, ok := labels[jobconfig.ProwJobLabelVariant]; ok {
		variant = variantLabel
	}
	return variant
}

// ConfigurePeriodicRehearsals adds the required configuration for the periodics to be rehearsed.
func (jc *JobConfigurer) ConfigurePeriodicRehearsals(periodics config.Periodics) (apihelper.ImageStreamTagMap, []prowconfig.Periodic, error) {
	var rehearsals []prowconfig.Periodic
	allImageStreamTags := apihelper.ImageStreamTagMap{}

	for _, job := range periodics {
		jobLogger := jc.logger.WithField("target-job", job.Name)
		metadata := api.Metadata{
			Variant: VariantFromLabels(job.Labels),
		}
		if len(job.ExtraRefs) != 0 {
			metadata.Org = job.ExtraRefs[0].Org
			metadata.Repo = job.ExtraRefs[0].Repo
			metadata.Branch = job.ExtraRefs[0].BaseRef
		}
		jc.configureDecorationConfig(&job.JobBase, metadata)
		testname := metadata.TestNameFromJobName(job.Name, jobconfig.PeriodicPrefix)
		imageStreamTags, err := jc.configureJobSpec(job.Spec, metadata, testname, job.Name, jc.logger.WithField("name", job.Name))
		if err != nil {
			jobLogger.WithError(err).Warn("Failed to inline ci-operator-config into rehearsal periodic job")
			return nil, nil, err
		}
		apihelper.MergeImageStreamTagMaps(allImageStreamTags, imageStreamTags)

		jobLogger.Infof("Imagestream tags required for job %s: %s", job.Name, imageStreamTags.String())
		jobLogger.WithField(logRehearsalJob, job.Name).Info("Created a rehearsal job to be submitted")
		rehearsals = append(rehearsals, job)
	}

	return allImageStreamTags, rehearsals, nil
}

// ConfigurePresubmitRehearsals adds the required configuration for the presubmits to be rehearsed.
func (jc *JobConfigurer) ConfigurePresubmitRehearsals(presubmits config.Presubmits) (apihelper.ImageStreamTagMap, []*prowconfig.Presubmit, error) {
	var rehearsals []*prowconfig.Presubmit
	allImageStreamTags := apihelper.ImageStreamTagMap{}

	for orgrepo, jobs := range presubmits {
		splitOrgRepo := strings.Split(orgrepo, "/")
		if len(splitOrgRepo) != 2 {
			jc.logger.WithError(fmt.Errorf("failed to identify org and repo from string %s", orgrepo)).Warn("Failed to inline ci-operator-config into rehearsal presubmit job")
		}

		for _, job := range jobs {
			jobLogger := jc.logger.WithFields(logrus.Fields{"target-repo": orgrepo, "target-job": job.Name})
			branch := BranchFromRegexes(job.Branches)
			if branch == "" {
				jobLogger.Warn("failed to extract a simple branch name for a presubmit")
				continue
			}
			metadata := api.Metadata{
				Org:     splitOrgRepo[0],
				Repo:    splitOrgRepo[1],
				Branch:  branch,
				Variant: VariantFromLabels(job.Labels),
			}
			jc.configureDecorationConfig(&job.JobBase, metadata)

			rehearsal, err := makeRehearsalPresubmit(&job, orgrepo, jc.refs)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to make rehearsal presubmit: %w", err)
			}
			testname := metadata.TestNameFromJobName(job.Name, jobconfig.PresubmitPrefix)

			imageStreamTags, err := jc.configureJobSpec(rehearsal.Spec, metadata, testname, job.Name, jc.logger.WithField("name", job.Name))
			if err != nil {
				jobLogger.WithError(err).Warn("Failed to inline ci-operator-config into rehearsal presubmit job")
				return nil, nil, err
			}
			apihelper.MergeImageStreamTagMaps(allImageStreamTags, imageStreamTags)

			jobLogger.Infof("Imagestream tags required for job %s: %s", job.Name, imageStreamTags.String())
			jobLogger.WithField(logRehearsalJob, rehearsal.Name).Info("Created a rehearsal job to be submitted")
			rehearsals = append(rehearsals, rehearsal)
		}
	}
	return allImageStreamTags, rehearsals, nil
}

// configureDecorationConfig sets the DecorationConfig.GCSConfiguration.JobURLPrefix to get the correct Details link on rehearsal gh statuses
func (jc *JobConfigurer) configureDecorationConfig(job *prowconfig.JobBase, metadata api.Metadata) {
	if job.DecorationConfig == nil {
		job.DecorationConfig = &pjapi.DecorationConfig{}
	}
	if job.DecorationConfig.GCSConfiguration == nil {
		job.DecorationConfig.GCSConfiguration = &pjapi.GCSConfiguration{}
	}
	job.DecorationConfig.GCSConfiguration.JobURLPrefix = determineJobURLPrefix(jc.prowConfig.Plank, metadata.Org, metadata.Repo)
}

func (jc *JobConfigurer) configureJobSpec(spec *v1.PodSpec, metadata api.Metadata, testName, jobName string, logger *logrus.Entry) (apihelper.ImageStreamTagMap, error) {
	// Remove configresolver flags from ci-operator jobs
	var metadataFromFlags api.Metadata
	if len(spec.Containers[0].Command) > 0 && spec.Containers[0].Command[0] == "ci-operator" {
		spec.Containers[0].Args, metadataFromFlags = removeConfigResolverFlags(spec.Containers[0].Args)
	}

	// Periodics may not be tied to a specific ci-operator configuration, but
	// we may have inferred ci-op config from ci-operator flags
	if metadata.IsComplete() != nil && metadataFromFlags.IsComplete() == nil {
		metadata = metadataFromFlags
	}

	imageStreamTags, err := jc.inlineCiOpConfig(&spec.Containers[0], jc.ciopConfigs, jc.registryResolver, metadata, testName, jobName, jc.logger)
	if err != nil {
		return nil, err
	}

	replaceConfigMaps(spec.Volumes, jc.clusterProfileCMNames, logger)

	return imageStreamTags, nil
}

// ConvertPeriodicsToPresubmits converts periodic jobs to presubmits by using the same JobBase and filling up
// the rest of the presubmit's required fields.
func (jc *JobConfigurer) ConvertPeriodicsToPresubmits(periodics []prowconfig.Periodic) ([]*prowconfig.Presubmit, error) {
	var presubmits []*prowconfig.Presubmit

	for _, periodic := range periodics {
		p, err := makeRehearsalPresubmit(&prowconfig.Presubmit{JobBase: periodic.JobBase}, "", jc.refs)
		if err != nil {
			return nil, fmt.Errorf("makeRehearsalPresubmit failed: %w", err)
		}

		if len(p.ExtraRefs) > 0 {
			// we aren't injecting this as we do for presubmits, but we need it to be set
			p.ExtraRefs[0].WorkDir = true
		} else if len(p.ExtraRefs) > 1 {
			// converted periodics already have a ref that should be removed to
			// avoid conflicts with clonerefs
			p.ExtraRefs = p.ExtraRefs[1:]
		}

		presubmits = append(presubmits, p)
	}
	return presubmits, nil
}

// Generic Prow periodics are not related to a repo, but in OpenShift CI many of them
// are generated from ci-operator config which are. Code using this type is expected
// to only work with the generated, repo-connected periodics
type periodicsByRepo map[string][]prowconfig.Periodic
type presubmitsByRepo map[string][]prowconfig.Presubmit

type periodicsByName map[string]prowconfig.Periodic
type presubmitsByName map[string]prowconfig.Presubmit

// selectJobsForRegistryStep returns all jobs affected by the provided registry node.
func selectJobsForRegistryStep(node registry.Node, configs []*config.DataWithInfo, allPresubmits presubmitsByName, allPeriodics periodicsByName, skipJobs sets.Set[string], logger *logrus.Entry) (presubmitsByRepo, periodicsByRepo) {
	selectedPresubmits := make(map[string][]prowconfig.Presubmit)
	selectedPeriodics := make(map[string][]prowconfig.Periodic)

	nodeLogger := logger.WithFields(registry.FieldsForNode(node))
	nodeLogger.Debugf("Searching %d configs for jobs affected by changed node", len(configs))
	for _, cfg := range configs {
		cfgLogger := nodeLogger.WithFields(cfg.Info.LogFields())
		orgRepo := fmt.Sprintf("%s/%s", cfg.Info.Org, cfg.Info.Repo)
		for _, test := range cfg.Configuration.Tests {
			testLogger := cfgLogger.WithField("tests-item", test.As)
			if test.MultiStageTestConfiguration == nil {
				continue
			}
			var selectJob func()
			var jobName string
			switch {
			case test.Postsubmit:
				continue // We do not handle postsubmits
			case test.IsPeriodic():
				jobName = cfg.Info.JobName(jobconfig.PeriodicPrefix, test.As)
				if periodic, ok := allPeriodics[jobName]; ok {
					selectJob = func() {
						testLogger.WithField("job-name", jobName).Trace("Periodic job uses the node: selecting for rehearse")
						selectedPeriodics[orgRepo] = append(selectedPeriodics[orgRepo], periodic)
					}
				} else {
					testLogger.WithField("job-name", jobName).Trace("Could not find a periodic job for test")
					continue
				}
			default: // Everything else is a presubmit
				jobName = cfg.Info.JobName(jobconfig.PresubmitPrefix, test.As)
				if presubmit, ok := allPresubmits[jobName]; ok {
					selectJob = func() {
						testLogger.WithField("job-name", jobName).Trace("Presubmit job uses the node: selecting for rehearse")
						selectedPresubmits[orgRepo] = append(selectedPresubmits[orgRepo], presubmit)
					}
				} else {
					testLogger.WithField("job-name", jobName).Trace("Could not find a presubmit job for test")
					continue
				}
			}

			if skipJobs.Has(jobName) {
				testLogger.WithField("job-name", jobName).Trace("Already saw this job, skipping")
				continue
			}

			// TODO: Handle workflows with overridden logFields.
			// Workflows can have overridden logFields and thus may have overridden the field that made the workflow an ancestor.
			// This should be handled to reduce the number of rehearsals being done, but requires much more information than
			// the graph alone provides.
			if node.Type() == registry.Workflow {
				if test.MultiStageTestConfiguration.Workflow != nil && node.Name() == *test.MultiStageTestConfiguration.Workflow {
					selectJob()
				}
				continue
			}
			if node.Type() == registry.Observer {
				if testUsesObserver(test, node.Name()) {
					selectJob()
				}
				continue
			}
			testSteps := append(test.MultiStageTestConfiguration.Pre, append(test.MultiStageTestConfiguration.Test, test.MultiStageTestConfiguration.Post...)...)
			for _, testStep := range testSteps {
				hasRef := testStep.Reference != nil && node.Type() == registry.Reference && node.Name() == *testStep.Reference
				hasChain := testStep.Chain != nil && node.Type() == registry.Chain && node.Name() == *testStep.Chain
				if hasRef || hasChain {
					selectJob()
				}
			}
		}
	}

	if len(selectedPresubmits) == 0 && len(selectedPeriodics) == 0 {
		logger.WithField("node-name", node.Name()).Debug("Found no jobs using node")
	}
	return selectedPresubmits, selectedPeriodics
}

func testUsesObserver(test api.TestStepConfiguration, o string) bool {
	t := test.MultiStageTestConfiguration
	return t != nil &&
		t.Observers != nil &&
		util.Contains(t.Observers.Enable, o)
}

// getAffectedNodes returns a sorted list of all nodes affected by a seed list
// of changed nodes. Affected node is either a directly changed node or any of
// its ancestors. Each node is present at most once.
func getAffectedNodes(changed []registry.Node) []registry.Node {
	all := changed
	for _, node := range changed {
		all = append(all, node.Ancestors()...)
	}

	var worklist []registry.Node
	seen := sets.New[string]()
	keyFunc := func(node registry.Node) string { return fmt.Sprintf("type=%d name=%s", node.Type(), node.Name()) }
	for _, node := range all {
		key := keyFunc(node)
		if !seen.Has(key) {
			seen.Insert(key)
			worklist = append(worklist, node)
		}
	}
	sort.Slice(worklist, func(i, j int) bool {
		if worklist[i].Name() == worklist[j].Name() {
			return worklist[i].Type() < worklist[j].Type()
		}
		return worklist[i].Name() < worklist[j].Name()
	})
	return worklist
}

func SelectJobsForChangedRegistry(regSteps []registry.Node, allPresubmits presubmitsByRepo, allPeriodics []prowconfig.Periodic, ciopConfigs config.DataByFilename, logger *logrus.Entry) (config.Presubmits, config.Periodics) {
	// We need a sorted index of ci-operator configs for deterministic behavior
	var sortedConfigs []*config.DataWithInfo
	for idx := range ciopConfigs {
		cfg := ciopConfigs[idx]
		sortedConfigs = append(sortedConfigs, &cfg)
	}
	// The order is INTENTIONALLY reversed to cheaply increase the chance of hitting
	// a useful rehearsal (prefer higher OCP versions)
	sort.Slice(sortedConfigs, func(i, j int) bool {
		return moreRelevant(sortedConfigs[i], sortedConfigs[j])
	})

	stepWorklist := getAffectedNodes(regSteps)

	presubmitIndex := presubmitsByName{}
	for _, jobs := range allPresubmits {
		for _, job := range jobs {
			presubmitIndex[job.Name] = job
		}
	}
	periodicsIndex := periodicsByName{}
	for _, job := range allPeriodics {
		periodicsIndex[job.Name] = job
	}

	selectedPresubmits := config.Presubmits{}
	selectedPeriodics := config.Periodics{}
	selectedNames := sets.New[string]()
	for _, step := range stepWorklist {
		presubmits, periodics := selectJobsForRegistryStep(step, sortedConfigs, presubmitIndex, periodicsIndex, selectedNames, logger)
		for repo, jobs := range presubmits {
			for _, job := range jobs {
				selectionFields := logrus.Fields{diffs.LogRepo: repo, diffs.LogJobName: job.Name, diffs.LogReasons: fmt.Sprintf("registry step %s changed", step.Name())}
				logger.WithFields(selectionFields).Info(diffs.ChosenJob)
				selectedPresubmits.Add(repo, job, config.ChangedRegistryContent)
				selectedNames.Insert(job.Name)
			}
		}
		for repo, jobs := range periodics {
			for _, job := range jobs {
				selectionFields := logrus.Fields{diffs.LogRepo: repo, diffs.LogJobName: job.Name, diffs.LogReasons: fmt.Sprintf("registry step %s changed", step.Name())}
				logger.WithFields(selectionFields).Info(diffs.ChosenJob)
				selectedPeriodics.Add(job, config.ChangedRegistryContent)
				selectedNames.Insert(job.Name)
			}
		}
	}
	return selectedPresubmits, selectedPeriodics
}

// Compare two branches by their relevancy
func moreRelevant(one, two *config.DataWithInfo) bool {
	return relevancy[one.Info.Metadata.Branch] > relevancy[two.Info.Metadata.Branch]
}

func getClusterTypes(jobs map[string][]prowconfig.Presubmit) []string {
	ret := sets.New[string]()
	for _, jobs := range jobs {
		for _, j := range jobs {
			if j.Spec != nil && j.Spec.Containers != nil {
				for _, c := range j.Spec.Containers {
					for _, e := range c.Env {
						if e.Name == clusterTypeEnvName {
							ret.Insert(e.Value)
						}
					}
				}
			}
		}
	}
	if len(ret) == 0 {
		return nil
	}
	return sets.List(ret)
}

func UsesConfigMap(job prowconfig.JobBase, cm string) bool {
	if job.Spec != nil {
		for _, volume := range job.Spec.Volumes {
			switch {
			case volume.Projected != nil:
				for _, source := range volume.Projected.Sources {
					if source.ConfigMap != nil && source.ConfigMap.Name == cm {
						return true
					}
				}
			case volume.ConfigMap != nil && volume.ConfigMap.Name == cm:
				return true
			}
		}
	}

	return false
}

func replaceConfigMaps(volumes []v1.Volume, cms map[string]string, logger *logrus.Entry) {
	replace := func(cm *string) {
		tmp, ok := cms[*cm]
		if !ok {
			return
		}
		fields := logrus.Fields{"profile": *cm, "tmp": tmp}
		logger.WithFields(fields).Debug("Rehearsal job uses a changed ConfigMap, will be replaced by temporary")
		*cm = tmp
	}
	for _, v := range volumes {
		switch {
		case v.Projected != nil:
			for _, s := range v.Projected.Sources {
				if s.ConfigMap == nil {
					continue
				}
				replace(&s.ConfigMap.Name)
			}
		case v.ConfigMap != nil:
			replace(&v.ConfigMap.Name)
		}
	}
}

// Executor holds all the information needed for the jobs to be executed.
type Executor struct {
	dryRun     bool
	presubmits []*prowconfig.Presubmit
	prRepo     string
	refs       *pjapi.Refs
	logger     *logrus.Entry
	pjclient   ctrlruntimeclient.Client
	namespace  string
	// Allow faking this in tests
	pollFunc          func(ctx context.Context, interval, timeout time.Duration, immediate bool, condition wait.ConditionWithContextFunc) error
	prowCfg           *prowconfig.Config
	waitForCompletion bool
}

// NewExecutor creates an executor. It also configures the rehearsal jobs as a list of presubmits.
func NewExecutor(presubmits []*prowconfig.Presubmit, prRepo string, refs *pjapi.Refs,
	dryRun bool, logger *logrus.Entry, pjclient ctrlruntimeclient.Client, namespace string,
	prowCfg *prowconfig.Config, waitForCompletion bool) *Executor {
	return &Executor{
		dryRun:            dryRun,
		presubmits:        presubmits,
		prRepo:            prRepo,
		refs:              refs,
		logger:            logger,
		pjclient:          pjclient,
		namespace:         namespace,
		pollFunc:          wait.PollUntilContextTimeout,
		prowCfg:           prowCfg,
		waitForCompletion: waitForCompletion,
	}
}

func printAsYaml(pjs []*pjapi.ProwJob) error {
	sort.Slice(pjs, func(a, b int) bool { return pjs[a].Spec.Job < pjs[b].Spec.Job })
	jobAsYAML, err := yaml.Marshal(pjs)
	if err == nil {
		fmt.Printf("%s\n", jobAsYAML)
	}

	return err
}

// ExecuteJobs takes configs for a set of jobs which should be "rehearsed", and
// creates the ProwJobs that perform the actual rehearsal. *Rehearsal* means
// a "trial" execution of a Prow job configuration when the *job config* config
// is changed, giving feedback to Prow config authors on how the changes of the
// config would affect the "production" Prow jobs run on the actual target repos
func (e *Executor) ExecuteJobs() (bool, error) {
	submitSuccess := true
	pjs, err := e.submitRehearsals()
	if err != nil {
		submitSuccess = false
	}

	if e.dryRun {
		if err := printAsYaml(pjs); err != nil {
			return false, fmt.Errorf("printing yaml failed: %w", err)
		}

		if submitSuccess {
			return true, nil
		}
		return true, fmt.Errorf("failed to submit all rehearsal jobs")
	}

	prNumber := e.refs.Pulls[0].Number
	selector := ctrlruntimeclient.MatchingLabels{Label: strconv.Itoa(prNumber)}

	names := sets.New[string]()
	for _, job := range pjs {
		names.Insert(job.Name)
	}
	waitSuccess := true //default to true as we don't assume failure if not waiting for completion
	if e.waitForCompletion {
		waitSuccess, err = e.waitForJobs(names, selector)
	}
	if !submitSuccess {
		return waitSuccess, fmt.Errorf("failed to submit all rehearsal jobs")
	}
	return waitSuccess, err
}

func (e *Executor) waitForJobs(jobs sets.Set[string], selector ctrlruntimeclient.ListOption) (bool, error) {
	if len(jobs) == 0 {
		return true, nil
	}
	success := true
	var listErrors []error
	if err := e.pollFunc(context.Background(), 10*time.Second, 4*time.Hour, false, func(ctx context.Context) (bool, error) {
		result := &pjapi.ProwJobList{}
		// Don't bail out just because one LIST failed
		if err := e.pjclient.List(ctx, result, selector, ctrlruntimeclient.InNamespace(e.namespace)); err != nil {
			if len(listErrors) > 2 {
				return false, utilerrors.NewAggregate(append(listErrors, err, errors.New("encountered three subsequent errors trying to list")))
			}
			listErrors = append(listErrors, err)
			return false, nil
		}
		// Reset the errors after a successful list
		listErrors = nil

		for _, pj := range result.Items {
			fields := pjutil.ProwJobFields(&pj)
			fields["state"] = pj.Status.State
			e.logger.WithFields(fields).Debug("Processing ProwJob")
			if !jobs.Has(pj.Name) {
				continue
			}

			switch pj.Status.State {
			case pjapi.FailureState, pjapi.AbortedState, pjapi.ErrorState:
				e.logger.WithFields(fields).Info("Job failed")
				success = false
			case pjapi.SuccessState:
				e.logger.WithFields(fields).Info("Job succeeded")
			default:
				continue
			}
			jobs.Delete(pj.Name)
			if jobs.Len() == 0 {
				return true, nil
			}
		}

		return false, nil
	}); err != nil {
		return false, fmt.Errorf("failed waiting for prowjobs to finish: %w", err)
	}

	return success, nil
}

func removeConfigResolverFlags(args []string) ([]string, api.Metadata) {
	var newArgs []string
	var usedConfig api.Metadata
	toConfig := map[string]*string{
		"org":     &usedConfig.Org,
		"repo":    &usedConfig.Repo,
		"branch":  &usedConfig.Branch,
		"variant": &usedConfig.Variant,
	}

	// Behave like a parser: consume items from the front of the slice until the
	// slice is empty. Keep all items that are not config resolver flags. When an
	// option that takes a parameter is encountered, but not in a `--param=value`
	// form, two items need to be consumed instead of one.
	consumeOne := func() string {
		item := args[0]
		args = args[1:]
		return item
	}
	for len(args) > 0 {
		current := consumeOne()
		keep := true

		for _, ignored := range []string{"resolver-address", "org", "repo", "branch", "variant"} {
			for _, form := range []string{"-%s=", "--%s=", "-%s", "--%s"} {
				containsValue := strings.HasSuffix(form, "=")
				flag := fmt.Sprintf(form, ignored)
				if (containsValue && strings.HasPrefix(current, flag)) || (!containsValue && current == flag) {
					var value string
					if containsValue {
						// If this is a --%s= form, grab the value from the arg itself
						value = strings.TrimPrefix(current, flag)
					} else if len(args) > 0 {
						// If this is not a --%s= form, consume next item, if possible
						value = consumeOne()
					}
					// Fill the config.Info with the value
					if store := toConfig[ignored]; store != nil {
						*store = value
					}
					keep = false
					// break prevents us from reaching the --%s form when --%s= one matched
					break
				}
			}
			// If we already matched something to ignore, we do not need to process
			// the remaining options
			if !keep {
				break
			}
		}

		if keep {
			newArgs = append(newArgs, current)
		}
	}
	return newArgs, usedConfig
}

func (e *Executor) submitRehearsals() ([]*pjapi.ProwJob, error) {
	var errs []error
	var pjs []*pjapi.ProwJob

	for _, job := range e.presubmits {
		created, err := e.submitPresubmit(job)
		if err != nil {
			e.logger.WithError(err).Warn("Failed to execute a rehearsal presubmit")
			errs = append(errs, err)
			continue
		}
		pjs = append(pjs, created)
	}

	return pjs, utilerrors.NewAggregate(errs)
}

func (e *Executor) submitPresubmit(job *prowconfig.Presubmit) (*pjapi.ProwJob, error) {
	prowJob := pjutil.NewProwJob(pjutil.PresubmitSpec(*job, *e.refs), job.Labels, job.Annotations, pjutil.RequireScheduling(e.prowCfg.Scheduler.Enabled))
	prowJob.Namespace = e.namespace
	e.logger.WithFields(pjutil.ProwJobFields(&prowJob)).Info("Submitting a new prowjob.")
	return &prowJob, e.pjclient.Create(context.Background(), &prowJob)
}

func determineJobURLPrefix(plank prowconfig.Plank, org string, repo string) string {
	jobURLPrefix := plank.JobURLPrefixConfig["*"]
	if plank.JobURLPrefixConfig[org] != "" {
		jobURLPrefix = plank.JobURLPrefixConfig[org]
	}
	if plank.JobURLPrefixConfig[fmt.Sprintf("%s/%s", org, repo)] != "" {
		jobURLPrefix = plank.JobURLPrefixConfig[fmt.Sprintf("%s/%s", org, repo)]
	}

	return jobURLPrefix
}
