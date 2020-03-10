package rehearse

import (
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/getlantern/deepcopy"
	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	v1 "k8s.io/api/core/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"

	"k8s.io/client-go/kubernetes/fake"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	coretesting "k8s.io/client-go/testing"

	pjapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	pjclientset "k8s.io/test-infra/prow/client/clientset/versioned"
	pjclientsetfake "k8s.io/test-infra/prow/client/clientset/versioned/fake"
	pj "k8s.io/test-infra/prow/client/clientset/versioned/typed/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/pjutil"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
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
func NewProwJobClient(clusterConfig *rest.Config, namespace string, dry bool) (pj.ProwJobInterface, error) {
	if dry {
		pjcset := pjclientsetfake.NewSimpleClientset()
		return pjcset.ProwV1().ProwJobs(namespace), nil
	}
	pjcset, err := pjclientset.NewForConfig(clusterConfig)
	if err != nil {
		return nil, err
	}
	return pjcset.ProwV1().ProwJobs(namespace), nil
}

// NewCMClient creates a configMap client with a dry run capability
func NewCMClient(clusterConfig *rest.Config, namespace string, dry bool) (coreclientset.ConfigMapInterface, error) {
	if dry {
		c := fake.NewSimpleClientset()
		c.PrependReactor("update", "configmaps", func(action coretesting.Action) (bool, runtime.Object, error) {
			cm := action.(coretesting.UpdateAction).GetObject().(*v1.ConfigMap)
			y, err := yaml.Marshal([]*v1.ConfigMap{cm})
			if err != nil {
				return true, nil, fmt.Errorf("failed to convert ConfigMap to YAML: %v", err)
			}
			fmt.Print(string(y))
			return false, nil, nil
		})
		return c.CoreV1().ConfigMaps(namespace), nil
	}

	cmClient, err := coreclientset.NewForConfig(clusterConfig)
	if err != nil {
		return nil, fmt.Errorf("could not get core client for cluster config: %v", err)
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

func getTrimmedBranch(branches []string) string {
	return strings.TrimPrefix(strings.TrimSuffix(branches[0], "$"), "^")

}

func makeRehearsalPresubmit(source *prowconfig.Presubmit, repo string, prNumber int, refs *pjapi.Refs) (*prowconfig.Presubmit, error) {
	var rehearsal prowconfig.Presubmit
	deepcopy.Copy(&rehearsal, source)

	rehearsal.Name = fmt.Sprintf("rehearse-%d-%s", prNumber, source.Name)

	var branch string
	var context string

	if len(source.Branches) > 0 {
		branch = getTrimmedBranch(source.Branches)
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
	rehearsal.SkipReport = false

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

// inlineCiOpConfig detects whether a job needs a ci-operator config file
// provided by a `ci-operator-configs` ConfigMap and if yes, returns a copy
// of the job where a reference to this ConfigMap is replaced by the content
// of the needed config file passed to the job as a direct value. This needs
// to happen because the rehearsed Prow jobs may depend on these config files
// being also changed by the tested PR.
func inlineCiOpConfig(container *v1.Container, ciopConfigs config.ByFilename, resolver registry.Resolver, info config.Info, loggers Loggers) error {
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
		if config.IsCiopConfigCM(env.ValueFrom.ConfigMapKeyRef.Name) {
			filename := env.ValueFrom.ConfigMapKeyRef.Key

			loggers.Debug.WithField(logCiopConfigFile, filename).Debug("Rehearsal job uses ci-operator config ConfigMap, needed content will be inlined")
			ciopConfig, ok := ciopConfigs[filename]
			if !ok {
				return fmt.Errorf("ci-operator config file %s was not found", filename)
			}
			ciopConfigResolved, err := registry.ResolveConfig(resolver, ciopConfig.Configuration)
			if err != nil {
				loggers.Job.WithError(err).Error("Failed resolve ReleaseBuildConfiguration")
				return err
			}

			ciOpConfigContent, err := yaml.Marshal(ciopConfigResolved)
			if err != nil {
				loggers.Job.WithError(err).Error("Failed to marshal ci-operator config file")
				return err
			}

			env.Value = string(ciOpConfigContent)
			env.ValueFrom = nil
		}
	}
	// if CONFIG_SPEC has already been set, do not add new CONFIG_SPEC section
	if configSpecSet {
		return nil
	}
	// inline CONFIG_SPEC for all ci-operator jobs
	if container.Command != nil && container.Command[0] == "ci-operator" {
		filename := info.Basename()
		loggers.Debug.WithField(logCiopConfigFile, filename).Debug("Rehearsal job uses ci-operator config ConfigMap, needed content will be inlined")
		ciopConfig, ok := ciopConfigs[filename]
		if !ok {
			return fmt.Errorf("ci-operator config file %s was not found", filename)
		}
		ciopConfigResolved, err := registry.ResolveConfig(resolver, ciopConfig.Configuration)
		if err != nil {
			loggers.Job.WithError(err).Error("Failed resolve ReleaseBuildConfiguration")
			return err
		}

		ciOpConfigContent, err := yaml.Marshal(ciopConfigResolved)
		if err != nil {
			loggers.Job.WithError(err).Error("Failed to marshal ci-operator config file")
			return err
		}

		envs := container.Env
		env := v1.EnvVar{
			Name:  "CONFIG_SPEC",
			Value: string(ciOpConfigContent),
		}
		envs = append(envs, env)
		container.Env = envs
	}
	return nil
}

// JobConfigurer holds all the information that is needed for the configuration of the jobs.
type JobConfigurer struct {
	ciopConfigs      config.ByFilename
	registryResolver registry.Resolver
	profiles         []config.ConfigMapSource
	prNumber         int
	refs             *pjapi.Refs
	loggers          Loggers
	templateMap      map[string]string
}

// NewJobConfigurer filters the jobs and returns a new JobConfigurer.
func NewJobConfigurer(ciopConfigs config.ByFilename, resolver registry.Resolver, prNumber int, loggers Loggers, templates []config.ConfigMapSource, profiles []config.ConfigMapSource, refs *pjapi.Refs) *JobConfigurer {
	return &JobConfigurer{
		ciopConfigs:      ciopConfigs,
		registryResolver: resolver,
		profiles:         profiles,
		prNumber:         prNumber,
		refs:             refs,
		loggers:          loggers,
		templateMap:      fillTemplateMap(templates),
	}
}

func fillTemplateMap(templates []config.ConfigMapSource) map[string]string {
	templateMap := make(map[string]string, len(templates))
	for _, t := range templates {
		templateMap[filepath.Base(t.Filename)] = t.TempCMName("template")
	}
	return templateMap
}

func variantFromLabels(labels map[string]string) string {
	variant := ""
	if variantLabel, ok := labels[jobconfig.ProwJobLabelVariant]; ok {
		variant = variantLabel
	}
	return variant
}

// ConfigurePeriodicRehearsals adds the required configuration for the periodics to be rehearsed.
func (jc *JobConfigurer) ConfigurePeriodicRehearsals(periodics config.Periodics) []prowconfig.Periodic {
	var rehearsals []prowconfig.Periodic

	filteredPeriodics := filterPeriodics(periodics, jc.loggers.Job)
	for _, job := range filteredPeriodics {
		jobLogger := jc.loggers.Job.WithField("target-job", job.Name)
		info := config.Info{
			Variant: variantFromLabels(job.Labels),
		}
		if len(job.ExtraRefs) != 0 {
			info.Org = job.ExtraRefs[0].Org
			info.Repo = job.ExtraRefs[0].Repo
			info.Branch = job.ExtraRefs[0].BaseRef
		}
		if err := jc.configureJobSpec(job.Spec, info, jc.loggers.Debug.WithField("name", job.Name)); err != nil {
			jobLogger.WithError(err).Warn("Failed to inline ci-operator-config into rehearsal periodic job")
			continue
		}

		jobLogger.WithField(logRehearsalJob, job.Name).Info("Created a rehearsal job to be submitted")
		rehearsals = append(rehearsals, job)
	}

	return rehearsals
}

// ConfigurePresubmitRehearsals adds the required configuration for the presubmits to be rehearsed.
func (jc *JobConfigurer) ConfigurePresubmitRehearsals(presubmits config.Presubmits) []*prowconfig.Presubmit {
	var rehearsals []*prowconfig.Presubmit

	presubmitsFiltered := filterPresubmits(presubmits, jc.loggers.Job)
	for orgrepo, jobs := range presubmitsFiltered {
		for _, job := range jobs {
			jobLogger := jc.loggers.Job.WithFields(logrus.Fields{"target-repo": orgrepo, "target-job": job.Name})
			rehearsal, err := makeRehearsalPresubmit(&job, orgrepo, jc.prNumber, jc.refs)
			if err != nil {
				jobLogger.WithError(err).Warn("Failed to make a rehearsal presubmit")
				continue
			}

			splitOrgRepo := strings.Split(orgrepo, "/")
			if len(splitOrgRepo) != 2 {
				jobLogger.WithError(fmt.Errorf("failed to identify org and repo from string %s", orgrepo)).Warn("Failed to inline ci-operator-config into rehearsal presubmit job")
			}
			info := config.Info{
				Org:     splitOrgRepo[0],
				Repo:    splitOrgRepo[1],
				Branch:  getTrimmedBranch(job.Branches),
				Variant: variantFromLabels(job.Labels),
			}

			if err := jc.configureJobSpec(rehearsal.Spec, info, jc.loggers.Debug.WithField("name", job.Name)); err != nil {
				jobLogger.WithError(err).Warn("Failed to inline ci-operator-config into rehearsal presubmit job")
				continue
			}

			jobLogger.WithField(logRehearsalJob, rehearsal.Name).Info("Created a rehearsal job to be submitted")
			rehearsals = append(rehearsals, rehearsal)
		}
	}
	return rehearsals
}

func (jc *JobConfigurer) configureJobSpec(spec *v1.PodSpec, info config.Info, logger *logrus.Entry) error {
	if err := inlineCiOpConfig(&spec.Containers[0], jc.ciopConfigs, jc.registryResolver, info, jc.loggers); err != nil {
		return err
	}
	// Remove configresolver flags from ci-operator jobs
	if len(spec.Containers[0].Command) > 0 && spec.Containers[0].Command[0] == "ci-operator" {
		spec.Containers[0].Args = removeConfigResolverFlags(spec.Containers[0].Args)
	}

	replaceCMTemplateName(spec.Containers[0].VolumeMounts, spec.Volumes, jc.templateMap)
	replaceClusterProfiles(spec.Volumes, jc.profiles, logger)

	return nil
}

// ConvertPeriodicsToPresubmits converts periodic jobs to presubmits by using the same JobBase and filling up
// the rest of the presubmit's required fields.
func (jc *JobConfigurer) ConvertPeriodicsToPresubmits(periodics []prowconfig.Periodic) []*prowconfig.Presubmit {
	var presubmits []*prowconfig.Presubmit

	for _, periodic := range periodics {
		jobLogger := jc.loggers.Job.WithField("target-job", periodic.Name)
		p, err := makeRehearsalPresubmit(&prowconfig.Presubmit{JobBase: periodic.JobBase}, "", jc.prNumber, jc.refs)
		if err != nil {
			jobLogger.WithError(err).Warn("Failed to make a rehearsal presubmit")
			continue
		}

		if len(p.ExtraRefs) > 0 {
			// we aren't injecting this as we do for presubmits, but we need it to be set
			p.ExtraRefs[0].WorkDir = true
		}

		presubmits = append(presubmits, p)
	}
	return presubmits
}

// AddRandomJobsForChangedTemplates finds jobs from the PR config that are using a specific template with a specific cluster type.
// The job selection is done by iterating in an unspecified order, which avoids picking the same job
// So if a template will be changed, find the jobs that are using a template in combination with the `aws`,`openstack`,`gcs` and `libvirt` cluster types.
func AddRandomJobsForChangedTemplates(templates []config.ConfigMapSource, toBeRehearsed config.Presubmits, prConfigPresubmits map[string][]prowconfig.Presubmit, loggers Loggers, prNumber int) config.Presubmits {
	clusterTypes := getClusterTypes(prConfigPresubmits)
	rehearsals := make(config.Presubmits)

	for _, template := range templates {
		templateFile := filepath.Base(template.Filename)
		for _, clusterType := range clusterTypes {
			if isAlreadyRehearsed(toBeRehearsed, clusterType, templateFile) {
				continue
			}

			if repo, job := pickTemplateJob(prConfigPresubmits, templateFile, clusterType); job != nil {
				jobLogger := loggers.Job.WithFields(logrus.Fields{"target-repo": repo, "target-job": job.Name})
				jobLogger.Info("Picking job to rehearse the template changes")
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

func getPresubmitsForRegistryStep(node registry.Node, configs config.ByFilename, prConfigPresubmits map[string][]prowconfig.Presubmit, addedConfigs []*api.MultiStageTestConfiguration) (map[string][]prowconfig.Presubmit, []*api.MultiStageTestConfiguration, error) {
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

// expandAncestors takes a graph of changed steps and adds all ancestors of
// the existing steps to the changed steps graph
func expandAncestors(changed, graph registry.NodeByName) {
	for _, node := range changed {
		for name := range node.AncestorNames() {
			changed[name] = graph[name]
		}
	}
}

func AddRandomJobsForChangedRegistry(regSteps, graph registry.NodeByName, prConfigPresubmits map[string][]prowconfig.Presubmit, configPath string, loggers Loggers) config.Presubmits {
	configsByFilename, err := config.LoadConfigByFilename(configPath)
	if err != nil {
		loggers.Debug.Errorf("Failed to load config by filename in AddRandomJobsForChangedRegistry: %v", err)
	}
	expandAncestors(regSteps, graph)
	rehearsals := make(config.Presubmits)
	// get sorted list of regSteps keys to make the function deterministic
	var keys []string
	for k := range regSteps {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// make list to store MultiStageTestConfigurations that we've already added to the test list
	addedConfigs := []*api.MultiStageTestConfiguration{}
	for _, key := range keys {
		step := regSteps[key]
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
			if hasClusterType(job, clusterType) && hasTemplateFile(job, templateFile) {
				return true
			}
		}
	}
	return false
}

func replaceCMTemplateName(volumeMounts []v1.VolumeMount, volumes []v1.Volume, mapping map[string]string) {
	for _, volume := range volumes {
		for _, volumeMount := range volumeMounts {
			if name, ok := mapping[volumeMount.SubPath]; ok && volumeMount.Name == volume.Name {
				volume.VolumeSource.ConfigMap.Name = name
			}
		}
	}
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

			if hasClusterType(job, clusterType) && hasTemplateFile(job, templateFile) {
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

func hasTemplateFile(job prowconfig.Presubmit, templateFile string) bool {
	if job.Spec.Containers[0].VolumeMounts != nil {
		for _, volumeMount := range job.Spec.Containers[0].VolumeMounts {
			if volumeMount.SubPath == templateFile {
				return true
			}
		}
	}
	return false
}

func replaceClusterProfiles(volumes []v1.Volume, profiles []config.ConfigMapSource, logger *logrus.Entry) {
	nameMap := make(map[string]string, len(profiles))
	for _, p := range profiles {
		nameMap[p.CMName(config.ClusterProfilePrefix)] = p.TempCMName("cluster-profile")
	}
	replace := func(s *v1.VolumeProjection) {
		if s.ConfigMap == nil {
			return
		}
		tmp, ok := nameMap[s.ConfigMap.Name]
		if !ok {
			return
		}
		fields := logrus.Fields{"profile": s.ConfigMap.Name, "tmp": tmp}
		logger.WithFields(fields).Debug("Rehearsal job uses cluster profile, will be replaced by temporary")
		s.ConfigMap.Name = tmp
	}
	for _, v := range volumes {
		if v.Name != "cluster-profile" || v.Projected == nil {
			continue
		}
		for _, s := range v.Projected.Sources {
			replace(&s)
		}
	}
}

// Executor holds all the information needed for the jobs to be executed.
type Executor struct {
	Metrics *ExecutionMetrics

	dryRun     bool
	presubmits []*prowconfig.Presubmit
	prNumber   int
	prRepo     string
	refs       *pjapi.Refs
	loggers    Loggers
	pjclient   pj.ProwJobInterface
}

// NewExecutor creates an executor. It also confgures the rehearsal jobs as a list of presubmits.
func NewExecutor(presubmits []*prowconfig.Presubmit, prNumber int, prRepo string, refs *pjapi.Refs,
	dryRun bool, loggers Loggers, pjclient pj.ProwJobInterface) *Executor {
	return &Executor{
		Metrics: &ExecutionMetrics{},

		dryRun:     dryRun,
		presubmits: presubmits,
		prNumber:   prNumber,
		prRepo:     prRepo,
		refs:       refs,
		loggers:    loggers,
		pjclient:   pjclient,
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
		printAsYaml(pjs)

		if submitSuccess {
			return true, nil
		}
		return true, fmt.Errorf("failed to submit all rehearsal jobs")
	}

	req, err := labels.NewRequirement(rehearseLabel, selection.Equals, []string{strconv.Itoa(e.prNumber)})
	if err != nil {
		return false, fmt.Errorf("failed to create label selector: %v", err)
	}
	selector := labels.NewSelector().Add(*req).String()

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

func (e *Executor) waitForJobs(jobs sets.String, selector string) (bool, error) {
	if len(jobs) == 0 {
		return true, nil
	}
	success := true
	for {
		w, err := e.pjclient.Watch(metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return false, fmt.Errorf("failed to create watch for ProwJobs: %v", err)
		}
		defer w.Stop()
		for event := range w.ResultChan() {
			prowJob, ok := event.Object.(*pjapi.ProwJob)
			if !ok {
				return false, fmt.Errorf("received a %T from watch", event.Object)
			}
			fields := pjutil.ProwJobFields(prowJob)
			fields["state"] = prowJob.Status.State
			e.loggers.Debug.WithFields(fields).Debug("Processing ProwJob")
			if !jobs.Has(prowJob.Name) {
				continue
			}
			switch prowJob.Status.State {
			case pjapi.FailureState, pjapi.AbortedState, pjapi.ErrorState:
				e.loggers.Job.WithFields(fields).Error("Job failed")
				e.Metrics.FailedRehearsals = append(e.Metrics.FailedRehearsals, prowJob.Spec.Job)
				success = false
			case pjapi.SuccessState:
				e.loggers.Job.WithFields(fields).Info("Job succeeded")
				e.Metrics.PassedRehearsals = append(e.Metrics.FailedRehearsals, prowJob.Spec.Job)
			default:
				continue
			}
			jobs.Delete(prowJob.Name)
			if jobs.Len() == 0 {
				return success, nil
			}
		}
	}
}

func removeConfigResolverFlags(args []string) []string {
	newArgs := []string{}
	ignoreNext := false
	for _, arg := range args {
		if ignoreNext {
			ignoreNext = false
			continue
		}
		ignore := false
		for _, ignoredArg := range []string{"-resolver-address", "-org", "-repo", "-branch", "-variant"} {
			// Handle both single and double dash forms of go flags
			if strings.Contains(arg, ignoredArg) {
				ignore = true
			}
			if !strings.Contains(arg, "=") {
				ignoreNext = true
			}
		}
		if !ignore {
			newArgs = append(newArgs, arg)
		}
	}
	return newArgs
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
		e.Metrics.SubmittedRehearsals = append(e.Metrics.SubmittedRehearsals, created.Spec.Job)
		e.loggers.Job.WithFields(pjutil.ProwJobFields(created)).Info("Submitted rehearsal prowjob")
		pjs = append(pjs, created)
	}

	return pjs, kerrors.NewAggregate(errors)
}

func (e *Executor) submitPresubmit(job *prowconfig.Presubmit) (*pjapi.ProwJob, error) {
	prowJob := pjutil.NewProwJob(pjutil.PresubmitSpec(*job, *e.refs), job.Labels, job.Annotations)
	e.loggers.Job.WithFields(pjutil.ProwJobFields(&prowJob)).Info("Submitting a new prowjob.")
	return e.pjclient.Create(&prowJob)
}
