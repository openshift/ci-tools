package rehearse

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/getlantern/deepcopy"
	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"
	"k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/runtime"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"

	"k8s.io/client-go/kubernetes/fake"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	coretesting "k8s.io/client-go/testing"

	pjapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/pjutil"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openshift/ci-tools/pkg/api"
	apihelper "github.com/openshift/ci-tools/pkg/api/helper"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/diffs"
	"github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/registry"
)

const (
	rehearseLabel                = "ci.openshift.org/rehearse"
	defaultRehearsalRerunCommand = "/test pj-rehearse"
	defaultRehearsalTrigger      = `(?m)^/test (?:.*? )?pj-rehearse(?: .*?)?$`
	logRehearsalJob              = "rehearsal-job"
	logCiopConfigFile            = "ciop-config-file"

	clusterTypeEnvName = "CLUSTER_TYPE"
)

// Loggers holds the two loggers that will be used for normal and debug logging respectively.
type Loggers struct {
	Job, Debug logrus.FieldLogger
}

// NewProwJobClient creates a ProwJob client with a dry run capability
func NewProwJobClient(clusterConfig *rest.Config, dry bool) (ctrlruntimeclient.Client, error) {
	if dry {
		return fakectrlruntimeclient.NewFakeClient(), nil
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

// TODO: remove this when we can upgrate test-infra
func CompletePrimaryRefs(refs pjapi.Refs, jb prowconfig.JobBase) *pjapi.Refs {
	if jb.PathAlias != "" {
		refs.PathAlias = jb.PathAlias
	}
	if jb.CloneURI != "" {
		refs.CloneURI = jb.CloneURI
	}
	refs.SkipSubmodules = jb.SkipSubmodules
	refs.CloneDepth = jb.CloneDepth
	return &refs
}

func BranchFromRegexes(branches []string) string {
	return strings.TrimPrefix(strings.TrimSuffix(branches[0], "$"), "^")
}

func makeRehearsalPresubmit(source *prowconfig.Presubmit, repo string, prNumber int, refs *pjapi.Refs) (*prowconfig.Presubmit, error) {
	var rehearsal prowconfig.Presubmit
	if err := deepcopy.Copy(&rehearsal, source); err != nil {
		return nil, fmt.Errorf("deepCopy failed: %w", err)
	}

	rehearsal.Name = fmt.Sprintf("rehearse-%d-%s", prNumber, source.Name)

	var branch string
	var context string

	if len(source.Branches) > 0 {
		branch = BranchFromRegexes(source.Branches)
		if len(repo) > 0 {
			orgRepo := strings.Split(repo, "/")
			jobOrg := orgRepo[0]
			jobRepo := orgRepo[1]

			if refs != nil {
				if refs.Org != jobOrg || refs.Repo != jobRepo {
					// we need to add the "original" refs that the job will be testing
					// from the target repo with all the "extra" fields from the job
					// config, like path_alias, then remove them from the config so we
					// don't use them in the future for any other refs
					rehearsal.ExtraRefs = append(rehearsal.ExtraRefs, *CompletePrimaryRefs(pjapi.Refs{
						Org:     jobOrg,
						Repo:    jobRepo,
						BaseRef: branch,
						WorkDir: true,
					}, source.JobBase))
					rehearsal.PathAlias = ""
					rehearsal.CloneURI = ""
					rehearsal.SkipSubmodules = false
					rehearsal.CloneDepth = 0
				}
			}
			context += repo + "/"
		}
		context += branch + "/"
	}

	shortName := strings.TrimPrefix(source.Context, "ci/prow/")
	if len(shortName) > 0 {
		context += shortName
	} else {
		context += source.Name
	}
	rehearsal.Context = fmt.Sprintf("ci/rehearse/%s", context)

	rehearsal.RerunCommand = defaultRehearsalRerunCommand
	rehearsal.Trigger = defaultRehearsalTrigger
	rehearsal.Optional = true
	if rehearsal.Labels == nil {
		rehearsal.Labels = make(map[string]string, 1)
	}
	rehearsal.Labels[rehearseLabel] = strconv.Itoa(prNumber)

	return &rehearsal, nil
}

func filterPresubmits(changedPresubmits map[string][]prowconfig.Presubmit, logger logrus.FieldLogger) config.Presubmits {
	presubmits := config.Presubmits{}
	for repo, jobs := range changedPresubmits {
		for _, job := range jobs {
			jobLogger := logger.WithFields(logrus.Fields{"repo": repo, "job": job.Name})

			if job.Hidden {
				jobLogger.Warn("hidden jobs are not allowed to be rehearsed")
				continue
			}

			if !hasRehearsableLabel(job.Labels) {
				jobLogger.Warnf("job is not allowed to be rehearsed. Label %s is required", jobconfig.CanBeRehearsedLabel)
				continue
			}

			if len(job.Branches) == 0 {
				jobLogger.Warn("cannot rehearse jobs with no branches")
				continue
			}

			if len(job.Branches) != 1 {
				jobLogger.Warn("cannot rehearse jobs that run over multiple branches")
				continue
			}

			presubmits.Add(repo, job)
		}
	}
	return presubmits
}

func filterPeriodics(changedPeriodics config.Periodics, logger logrus.FieldLogger) []prowconfig.Periodic {
	var periodics []prowconfig.Periodic
	for _, periodic := range changedPeriodics {
		jobLogger := logger.WithField("job", periodic.Name)

		if periodic.Hidden {
			jobLogger.Warn("hidden jobs are not allowed to be rehearsed")
			continue
		}

		if !hasRehearsableLabel(periodic.Labels) {
			jobLogger.Warnf("job is not allowed to be rehearsed. Label %s is required", jobconfig.CanBeRehearsedLabel)
			continue
		}

		periodics = append(periodics, periodic)
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
// `tests` section of the config.
// The ImageStreamTagMap contains all imagestreamtags used within this config and is used to ensure they exist on all target clusters.
func getResolvedConfigForTest(ciopConfigs config.DataByFilename, resolver registry.Resolver, filename, testname string) (string, apihelper.ImageStreamTagMap, error) {
	ciopConfig, ok := ciopConfigs[filename]
	if !ok {
		return "", nil, fmt.Errorf("ci-operator config file %s was not found", filename)
	}
	// make copy so we don't change in-memory config
	ciopCopy := config.DataWithInfo{
		Configuration: ciopConfig.Configuration,
		Info:          ciopConfig.Info,
	}
	// only include the test we need to reduce env var size
	ciopCopy.Configuration.Tests = []api.TestStepConfiguration{}
	for _, test := range ciopConfig.Configuration.Tests {
		if test.As == testname {
			ciopCopy.Configuration.Tests = append(ciopCopy.Configuration.Tests, test)
			break
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

	imageStreamTags, err := apihelper.TestInputImageStreamTagsFromResolvedConfig(ciopConfigResolved)
	if err != nil {
		return "", nil, fmt.Errorf("failed to resolve test imagestreamtags: %w", err)
	}
	return string(ciOpConfigContent), imageStreamTags, nil
}

// inlineCiOpConfig detects whether a job needs a ci-operator config file
// provided by a `ci-operator-configs` ConfigMap and if yes, returns a copy
// of the job where a reference to this ConfigMap is replaced by the content
// of the needed config file passed to the job as a direct value. This needs
// to happen because the rehearsed Prow jobs may depend on these config files
// being also changed by the tested PR.
func inlineCiOpConfig(container *v1.Container, ciopConfigs config.DataByFilename, resolver registry.Resolver, metadata api.Metadata, testname string, loggers Loggers) (apihelper.ImageStreamTagMap, error) {
	allImageStreamTags := apihelper.ImageStreamTagMap{}
	configSpecSet := false
	// replace all ConfigMapKeyRef mounts with inline config maps
	for index := range container.Env {
		env := &(container.Env[index])
		if env.Name == "CONFIG_SPEC" {
			configSpecSet = true
		}
		if env.ValueFrom == nil {
			continue
		}
		if env.ValueFrom.ConfigMapKeyRef == nil {
			continue
		}
		if api.IsCiopConfigCM(env.ValueFrom.ConfigMapKeyRef.Name) {
			filename := env.ValueFrom.ConfigMapKeyRef.Key

			loggers.Debug.WithField(logCiopConfigFile, filename).Debug("Rehearsal job uses ci-operator config ConfigMap, needed content will be inlined")
			ciOpConfigContent, imageStreamTags, err := getResolvedConfigForTest(ciopConfigs, resolver, filename, testname)
			if err != nil {
				loggers.Job.WithError(err).Error("Failed to get resolved config for test")
				return nil, err
			}
			apihelper.MergeImageStreamTagMaps(allImageStreamTags, imageStreamTags)

			env.Value = ciOpConfigContent
			env.ValueFrom = nil
		}
	}
	// if CONFIG_SPEC has already been set, do not add new CONFIG_SPEC section
	if configSpecSet {
		return allImageStreamTags, nil
	}
	// inline CONFIG_SPEC for all ci-operator jobs
	if container.Command != nil && container.Command[0] == "ci-operator" {
		if err := metadata.IsComplete(); err != nil {
			return nil, fmt.Errorf("could not infer which ci-operator config this job uses: %w", err)
		}
		filename := metadata.Basename()
		loggers.Debug.WithField(logCiopConfigFile, filename).Debug("Rehearsal job uses ci-operator config ConfigMap, needed content will be inlined")
		ciOpConfigContent, imageStreamTags, err := getResolvedConfigForTest(ciopConfigs, resolver, filename, testname)
		if err != nil {
			loggers.Job.WithError(err).Error("Failed to get resolved config for test")
			return nil, err
		}
		apihelper.MergeImageStreamTagMaps(allImageStreamTags, imageStreamTags)

		envs := container.Env
		env := v1.EnvVar{
			Name:  "CONFIG_SPEC",
			Value: ciOpConfigContent,
		}
		envs = append(envs, env)
		container.Env = envs
	}
	return allImageStreamTags, nil
}

// JobConfigurer holds all the information that is needed for the configuration of the jobs.
type JobConfigurer struct {
	ciopConfigs           config.DataByFilename
	registryResolver      registry.Resolver
	clusterProfileCMNames map[string]string
	templateCMNames       map[string]string
	prNumber              int
	refs                  *pjapi.Refs
	loggers               Loggers
}

// NewJobConfigurer filters the jobs and returns a new JobConfigurer.
func NewJobConfigurer(ciopConfigs config.DataByFilename, resolver registry.Resolver, prNumber int, loggers Loggers, templates, profiles map[string]string, refs *pjapi.Refs) *JobConfigurer {
	return &JobConfigurer{
		ciopConfigs:           ciopConfigs,
		registryResolver:      resolver,
		clusterProfileCMNames: profiles,
		templateCMNames:       templates,
		prNumber:              prNumber,
		refs:                  refs,
		loggers:               loggers,
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

	filteredPeriodics := filterPeriodics(periodics, jc.loggers.Job)
	for _, job := range filteredPeriodics {
		jobLogger := jc.loggers.Job.WithField("target-job", job.Name)
		metadata := api.Metadata{
			Variant: VariantFromLabels(job.Labels),
		}
		if len(job.ExtraRefs) != 0 {
			metadata.Org = job.ExtraRefs[0].Org
			metadata.Repo = job.ExtraRefs[0].Repo
			metadata.Branch = job.ExtraRefs[0].BaseRef
		}
		testname := metadata.TestNameFromJobName(job.Name, jobconfig.PeriodicPrefix)
		imageStreamTags, err := jc.configureJobSpec(job.Spec, metadata, testname, jc.loggers.Debug.WithField("name", job.Name))
		if err != nil {
			jobLogger.WithError(err).Warn("Failed to inline ci-operator-config into rehearsal periodic job")
			return nil, nil, err
		}
		apihelper.MergeImageStreamTagMaps(allImageStreamTags, imageStreamTags)

		jobLogger.WithField(logRehearsalJob, job.Name).Info("Created a rehearsal job to be submitted")
		rehearsals = append(rehearsals, job)
	}

	return allImageStreamTags, rehearsals, nil
}

// ConfigurePresubmitRehearsals adds the required configuration for the presubmits to be rehearsed.
func (jc *JobConfigurer) ConfigurePresubmitRehearsals(presubmits config.Presubmits) (apihelper.ImageStreamTagMap, []*prowconfig.Presubmit, error) {
	var rehearsals []*prowconfig.Presubmit
	allImageStreamTags := apihelper.ImageStreamTagMap{}

	presubmitsFiltered := filterPresubmits(presubmits, jc.loggers.Job)
	for orgrepo, jobs := range presubmitsFiltered {
		for _, job := range jobs {
			jobLogger := jc.loggers.Job.WithFields(logrus.Fields{"target-repo": orgrepo, "target-job": job.Name})
			rehearsal, err := makeRehearsalPresubmit(&job, orgrepo, jc.prNumber, jc.refs)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to make rehearsal presubmit: %w", err)
			}

			splitOrgRepo := strings.Split(orgrepo, "/")
			if len(splitOrgRepo) != 2 {
				jobLogger.WithError(fmt.Errorf("failed to identify org and repo from string %s", orgrepo)).Warn("Failed to inline ci-operator-config into rehearsal presubmit job")
			}
			metadata := api.Metadata{
				Org:     splitOrgRepo[0],
				Repo:    splitOrgRepo[1],
				Branch:  BranchFromRegexes(job.Branches),
				Variant: VariantFromLabels(job.Labels),
			}
			testname := metadata.TestNameFromJobName(job.Name, jobconfig.PresubmitPrefix)

			imageStreamTags, err := jc.configureJobSpec(rehearsal.Spec, metadata, testname, jc.loggers.Debug.WithField("name", job.Name))
			if err != nil {
				jobLogger.WithError(err).Warn("Failed to inline ci-operator-config into rehearsal presubmit job")
				return nil, nil, err
			}
			apihelper.MergeImageStreamTagMaps(allImageStreamTags, imageStreamTags)

			jobLogger.WithField(logRehearsalJob, rehearsal.Name).Info("Created a rehearsal job to be submitted")
			rehearsals = append(rehearsals, rehearsal)
		}
	}
	return allImageStreamTags, rehearsals, nil
}

func (jc *JobConfigurer) configureJobSpec(spec *v1.PodSpec, metadata api.Metadata, testName string, logger *logrus.Entry) (apihelper.ImageStreamTagMap, error) {
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

	imageStreamTags, err := inlineCiOpConfig(&spec.Containers[0], jc.ciopConfigs, jc.registryResolver, metadata, testName, jc.loggers)
	if err != nil {
		return nil, err
	}

	replaceConfigMaps(spec.Volumes, jc.templateCMNames, logger)
	replaceConfigMaps(spec.Volumes, jc.clusterProfileCMNames, logger)

	return imageStreamTags, nil
}

// ConvertPeriodicsToPresubmits converts periodic jobs to presubmits by using the same JobBase and filling up
// the rest of the presubmit's required fields.
func (jc *JobConfigurer) ConvertPeriodicsToPresubmits(periodics []prowconfig.Periodic) ([]*prowconfig.Presubmit, error) {
	var presubmits []*prowconfig.Presubmit

	for _, periodic := range periodics {
		p, err := makeRehearsalPresubmit(&prowconfig.Presubmit{JobBase: periodic.JobBase}, "", jc.prNumber, jc.refs)
		if err != nil {
			return nil, fmt.Errorf("makeRehearsalPresubmit failed: %w", err)
		}

		if len(p.ExtraRefs) > 0 {
			// we aren't injecting this as we do for presubmits, but we need it to be set
			p.ExtraRefs[0].WorkDir = true
		}

		presubmits = append(presubmits, p)
	}
	return presubmits, nil
}

// AddRandomJobsForChangedTemplates finds jobs from the PR config that are using a specific template with a specific cluster type.
// The job selection is done by iterating in an unspecified order, which avoids picking the same job
// So if a template will be changed, find the jobs that are using a template in combination with the `aws`,`openstack`,`gcs` and `libvirt` cluster types.
func AddRandomJobsForChangedTemplates(templates sets.String, toBeRehearsed config.Presubmits, prConfigPresubmits map[string][]prowconfig.Presubmit, loggers Loggers) config.Presubmits {
	clusterTypes := getClusterTypes(prConfigPresubmits)
	rehearsals := make(config.Presubmits)

	for template := range templates {
		for _, clusterType := range clusterTypes {
			if isAlreadyRehearsed(toBeRehearsed, clusterType, template) {
				continue
			}

			if repo, job := pickTemplateJob(prConfigPresubmits, template, clusterType); job != nil {
				selectionFields := logrus.Fields{diffs.LogRepo: repo, diffs.LogJobName: job.Name, diffs.LogReasons: fmt.Sprintf("template %s changed", template)}
				loggers.Job.WithFields(selectionFields).Info(diffs.ChosenJob)
				rehearsals[repo] = append(rehearsals[repo], *job)
			}
		}
	}
	return rehearsals
}

func getPresubmitByJobName(presubmits []prowconfig.Presubmit, name string) (prowconfig.Presubmit, error) {
	for _, presubmit := range presubmits {
		if presubmit.Name == name {
			return presubmit, nil
		}
	}
	return prowconfig.Presubmit{}, fmt.Errorf("could not find presubmit with name: %s", name)
}

func getPresubmitsForRegistryStep(node registry.Node, configs config.DataByFilename, prConfigPresubmits map[string][]prowconfig.Presubmit, addedConfigs []*api.MultiStageTestConfiguration) (map[string][]prowconfig.Presubmit, []*api.MultiStageTestConfiguration, error) {
	toTest := make(map[string][]prowconfig.Presubmit)
	// get sorted list of configs keys to make the function deterministic
	var keys []string
	for k := range configs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		ciopConfig := configs[key]
		tests := ciopConfig.Configuration.Tests
		orgRepo := fmt.Sprintf("%s/%s", ciopConfig.Info.Org, ciopConfig.Info.Repo)
		repoPresubmits := prConfigPresubmits[orgRepo]
		for _, test := range tests {
			if test.MultiStageTestConfiguration == nil {
				continue
			}
			skip := false
			for _, added := range addedConfigs {
				if reflect.DeepEqual(test.MultiStageTestConfiguration, added) {
					skip = true
					break
				}
			}
			if skip {
				continue
			}
			jobName := ciopConfig.Info.JobName(jobconfig.PresubmitPrefix, test.As)
			// TODO: Handle workflows with overridden fields.
			// Workflows can have overridden fields and thus may have overridden the field that made the workflow an ancestor.
			// This should be handled to reduce the number of rehearsals being done, but requires much more information than
			// the graph alone provides.
			if test.MultiStageTestConfiguration.Workflow != nil && node.Type() == registry.Workflow && node.Name() == *test.MultiStageTestConfiguration.Workflow {
				presubmit, err := getPresubmitByJobName(repoPresubmits, jobName)
				if err != nil {
					return toTest, addedConfigs, err
				}
				addedConfigs = append(addedConfigs, test.MultiStageTestConfiguration)
				toTest[orgRepo] = append(toTest[orgRepo], presubmit)
				// continue to check other tests
				continue
			}
			testSteps := append(test.MultiStageTestConfiguration.Pre, append(test.MultiStageTestConfiguration.Test, test.MultiStageTestConfiguration.Post...)...)
			for _, testStep := range testSteps {
				if testStep.Reference != nil && node.Type() == registry.Reference && node.Name() == *testStep.Reference {
					presubmit, err := getPresubmitByJobName(repoPresubmits, jobName)
					if err != nil {
						return toTest, addedConfigs, err
					}
					addedConfigs = append(addedConfigs, test.MultiStageTestConfiguration)
					toTest[orgRepo] = append(toTest[orgRepo], presubmit)
					// found step; break
					break
				}
				if testStep.Chain != nil && node.Type() == registry.Chain && node.Name() == *testStep.Chain {
					presubmit, err := getPresubmitByJobName(repoPresubmits, jobName)
					if err != nil {
						return toTest, addedConfigs, err
					}
					addedConfigs = append(addedConfigs, test.MultiStageTestConfiguration)
					toTest[orgRepo] = append(toTest[orgRepo], presubmit)
					// found step; break
					break
				}
			}
		}
	}
	return toTest, addedConfigs, nil
}

// sorting functions for []registry.Node
type nodeArr []registry.Node

func (s nodeArr) Len() int {
	return len(s)
}
func (s nodeArr) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}
func (s nodeArr) Less(i, j int) bool {
	if s[i].Name() == s[j].Name() {
		return s[i].Type() < s[j].Type()
	}
	return s[i].Name() < s[j].Name()
}

// getAllAncestors takes a graph of changed steps and finds all ancestors of
// the existing steps in changed
func getAllAncestors(changed []registry.Node) []registry.Node {
	var ancestors []registry.Node
	for _, node := range changed {
		ancestors = append(ancestors, node.Ancestors()...)
	}
	return ancestors
}

func AddRandomJobsForChangedRegistry(regSteps []registry.Node, prConfigPresubmits map[string][]prowconfig.Presubmit, configPath string, loggers Loggers) config.Presubmits {
	configsByFilename, err := config.LoadDataByFilename(configPath)
	if err != nil {
		loggers.Debug.Errorf("Failed to load config by filename in AddRandomJobsForChangedRegistry: %v", err)
	}
	regSteps = append(regSteps, getAllAncestors(regSteps)...)
	rehearsals := make(config.Presubmits)
	// sort steps to make it deterministic
	sort.Sort(nodeArr(regSteps))
	// make list to store MultiStageTestConfigurations that we've already added to the test list
	addedConfigs := []*api.MultiStageTestConfiguration{}
	for _, step := range regSteps {
		var presubmitsMap map[string][]prowconfig.Presubmit
		presubmitsMap, addedConfigs, err = getPresubmitsForRegistryStep(step, configsByFilename, prConfigPresubmits, addedConfigs)
		if err != nil {
			loggers.Debug.Errorf("Error getting presubmits in AddRandomJobsForChangedRegistry: %v", err)
		}
		if len(presubmitsMap) == 0 {
			// if the code reaches this point, then no config contains the step or the step has already been tested
			loggers.Debug.Warnf("No config found containing step: %+v", step)
		}
		for repo, presubmits := range presubmitsMap {
			for _, job := range presubmits {
				selectionFields := logrus.Fields{diffs.LogRepo: repo, diffs.LogJobName: job.Name, diffs.LogReasons: fmt.Sprintf("registry step %s changed", step.Name())}
				loggers.Job.WithFields(selectionFields).Info(diffs.ChosenJob)
			}
			rehearsals[repo] = append(rehearsals[repo], presubmits...)
			continue
		}
	}
	return rehearsals
}

func getClusterTypes(jobs map[string][]prowconfig.Presubmit) []string {
	ret := sets.NewString()
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
	return ret.List()
}

func isAlreadyRehearsed(toBeRehearsed config.Presubmits, clusterType, templateFile string) bool {
	for _, jobs := range toBeRehearsed {
		for _, job := range jobs {
			if hasClusterType(job, clusterType) && UsesConfigMap(job.JobBase, templateFile) {
				return true
			}
		}
	}
	return false
}

func pickTemplateJob(presubmits map[string][]prowconfig.Presubmit, templateFile, clusterType string) (string, *prowconfig.Presubmit) {
	var keys []string
	for k := range presubmits {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, repo := range keys {
		for _, job := range presubmits[repo] {
			if job.Agent != string(pjapi.KubernetesAgent) || job.Hidden || !hasRehearsableLabel(job.Labels) {
				continue
			}

			if hasClusterType(job, clusterType) && UsesConfigMap(job.JobBase, templateFile) {
				return repo, &job
			}
		}
	}
	return "", nil
}

func hasClusterType(job prowconfig.Presubmit, clusterType string) bool {
	for _, env := range job.Spec.Containers[0].Env {
		if env.Name == clusterTypeEnvName && env.Value == clusterType {
			return true
		}
	}
	return false
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
	prNumber   int
	prRepo     string
	refs       *pjapi.Refs
	loggers    Loggers
	pjclient   ctrlruntimeclient.Client
	namespace  string
	// Allow faking this in tests
	pollFunc func(interval, timeout time.Duration, condition wait.ConditionFunc) error
}

// NewExecutor creates an executor. It also confgures the rehearsal jobs as a list of presubmits.
func NewExecutor(presubmits []*prowconfig.Presubmit, prNumber int, prRepo string, refs *pjapi.Refs,
	dryRun bool, loggers Loggers, pjclient ctrlruntimeclient.Client, namespace string) *Executor {
	return &Executor{
		dryRun:     dryRun,
		presubmits: presubmits,
		prNumber:   prNumber,
		prRepo:     prRepo,
		refs:       refs,
		loggers:    loggers,
		pjclient:   pjclient,
		namespace:  namespace,
		pollFunc:   wait.Poll,
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

	selector := ctrlruntimeclient.MatchingLabels{rehearseLabel: strconv.Itoa(e.prNumber)}

	names := sets.NewString()
	for _, job := range pjs {
		names.Insert(job.Name)
	}
	waitSuccess, err := e.waitForJobs(names, selector)
	if !submitSuccess {
		return waitSuccess, fmt.Errorf("failed to submit all rehearsal jobs")
	}
	return waitSuccess, err
}

func (e *Executor) waitForJobs(jobs sets.String, selector ctrlruntimeclient.ListOption) (bool, error) {
	if len(jobs) == 0 {
		return true, nil
	}
	success := true
	var listErrors []error
	if err := e.pollFunc(10*time.Second, 4*time.Hour, func() (bool, error) {
		result := &pjapi.ProwJobList{}
		// Don't bail out just because one LIST failed
		if err := e.pjclient.List(context.Background(), result, selector, ctrlruntimeclient.InNamespace(e.namespace)); err != nil {
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
			e.loggers.Debug.WithFields(fields).Debug("Processing ProwJob")
			if !jobs.Has(pj.Name) {
				continue
			}

			switch pj.Status.State {
			case pjapi.FailureState, pjapi.AbortedState, pjapi.ErrorState:
				e.loggers.Job.WithFields(fields).Error("Job failed")
				success = false
			case pjapi.SuccessState:
				e.loggers.Job.WithFields(fields).Info("Job succeeded")
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
	var errors []error
	var pjs []*pjapi.ProwJob

	for _, job := range e.presubmits {
		created, err := e.submitPresubmit(job)
		if err != nil {
			e.loggers.Job.WithError(err).Warn("Failed to execute a rehearsal presubmit")
			errors = append(errors, err)
			continue
		}
		pjs = append(pjs, created)
	}

	return pjs, kerrors.NewAggregate(errors)
}

func (e *Executor) submitPresubmit(job *prowconfig.Presubmit) (*pjapi.ProwJob, error) {
	prowJob := pjutil.NewProwJob(pjutil.PresubmitSpec(*job, *e.refs), job.Labels, job.Annotations)
	prowJob.Namespace = e.namespace
	e.loggers.Job.WithFields(pjutil.ProwJobFields(&prowJob)).Info("Submitting a new prowjob.")
	return &prowJob, e.pjclient.Create(context.Background(), &prowJob)
}
