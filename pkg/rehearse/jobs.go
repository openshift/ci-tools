package rehearse

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/getlantern/deepcopy"
	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	"k8s.io/api/core/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"

	"k8s.io/client-go/kubernetes/fake"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"

	pjapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	pjclientset "k8s.io/test-infra/prow/client/clientset/versioned"
	pjclientsetfake "k8s.io/test-infra/prow/client/clientset/versioned/fake"
	pj "k8s.io/test-infra/prow/client/clientset/versioned/typed/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/pjutil"

	"github.com/openshift/ci-operator-prowgen/pkg/config"
)

const (
	rehearseLabel                = "ci.openshift.org/rehearse"
	defaultRehearsalRerunCommand = "/test pj-rehearse"
	logRehearsalJob              = "rehearsal-job"
	logCiopConfigFile            = "ciop-config-file"
	logCiopConfigRepo            = "ciop-config-repo"
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
		return c.CoreV1().ConfigMaps(namespace), nil
	}

	cmClient, err := coreclientset.NewForConfig(clusterConfig)
	if err != nil {
		return nil, fmt.Errorf("could not get core client for cluster config: %v", err)
	}

	return cmClient.ConfigMaps(namespace), nil
}

func makeRehearsalPresubmit(source *prowconfig.Presubmit, repo string, prNumber int) (*prowconfig.Presubmit, error) {
	var rehearsal prowconfig.Presubmit
	deepcopy.Copy(&rehearsal, source)

	rehearsal.Name = fmt.Sprintf("rehearse-%d-%s", prNumber, source.Name)

	branch := strings.TrimPrefix(strings.TrimSuffix(source.Branches[0], "$"), "^")
	shortName := strings.TrimPrefix(source.Context, "ci/prow/")
	rehearsal.Context = fmt.Sprintf("ci/rehearse/%s/%s/%s", repo, branch, shortName)
	rehearsal.RerunCommand = defaultRehearsalRerunCommand

	gitrefArg := fmt.Sprintf("--git-ref=%s@%s", repo, branch)
	rehearsal.Spec.Containers[0].Args = append(source.Spec.Containers[0].Args, gitrefArg)
	rehearsal.Optional = true

	if rehearsal.Labels == nil {
		rehearsal.Labels = make(map[string]string, 1)
	}
	rehearsal.Labels[rehearseLabel] = strconv.Itoa(prNumber)

	return &rehearsal, nil
}

func filterJobs(changedPresubmits map[string][]prowconfig.Presubmit, allowVolumes bool, logger logrus.FieldLogger) config.Presubmits {
	ret := config.Presubmits{}
	for repo, jobs := range changedPresubmits {
		for _, job := range jobs {
			jobLogger := logger.WithFields(logrus.Fields{"repo": repo, "job": job.Name})
			if err := filterJob(&job, allowVolumes); err != nil {
				jobLogger.WithError(err).Warn("could not rehearse job")
				continue
			}
			ret.Add(repo, job)
		}
	}
	return ret
}

func filterJob(source *prowconfig.Presubmit, allowVolumes bool) error {
	// there will always be exactly one container.
	container := source.Spec.Containers[0]

	if len(container.Command) != 1 || container.Command[0] != "ci-operator" {
		return fmt.Errorf("cannot rehearse jobs that have Command different from simple 'ci-operator'")
	}

	for _, arg := range container.Args {
		if strings.HasPrefix(arg, "--git-ref") || strings.HasPrefix(arg, "-git-ref") {
			return fmt.Errorf("cannot rehearse jobs that call ci-operator with '--git-ref' arg")
		}
	}
	if len(source.Spec.Volumes) > 0 && !allowVolumes {
		return fmt.Errorf("jobs that need additional volumes mounted are not allowed")
	}

	if len(source.Branches) == 0 {
		return fmt.Errorf("cannot rehearse jobs with no branches")
	}

	if len(source.Branches) != 1 {
		return fmt.Errorf("cannot rehearse jobs that run over multiple branches")
	}
	return nil
}

// inlineCiOpConfig detects whether a job needs a ci-operator config file
// provided by a `ci-operator-configs` ConfigMap and if yes, returns a copy
// of the job where a reference to this ConfigMap is replaced by the content
// of the needed config file passed to the job as a direct value. This needs
// to happen because the rehearsed Prow jobs may depend on these config files
// being also changed by the tested PR.
func inlineCiOpConfig(job *prowconfig.Presubmit, targetRepo string, ciopConfigs config.CompoundCiopConfig, loggers Loggers) (*prowconfig.Presubmit, error) {
	var rehearsal prowconfig.Presubmit
	deepcopy.Copy(&rehearsal, job)
	for _, container := range rehearsal.Spec.Containers {
		for index := range container.Env {
			env := &(container.Env[index])
			if env.ValueFrom == nil {
				continue
			}
			if env.ValueFrom.ConfigMapKeyRef == nil {
				continue
			}
			if config.IsCiopConfigCM(env.ValueFrom.ConfigMapKeyRef.Name) {
				filename := env.ValueFrom.ConfigMapKeyRef.Key

				logFields := logrus.Fields{logCiopConfigFile: filename, logCiopConfigRepo: targetRepo, logRehearsalJob: job.Name}
				loggers.Debug.WithFields(logFields).Debug("Rehearsal job uses ci-operator config ConfigMap, needed content will be inlined")

				ciopConfig, ok := ciopConfigs[filename]
				if !ok {
					return nil, fmt.Errorf("ci-operator config file %s was not found", filename)
				}

				ciOpConfigContent, err := yaml.Marshal(ciopConfig)
				if err != nil {
					loggers.Job.WithError(err).Error("Failed to marshal ci-operator config file")
					return nil, err
				}

				env.Value = string(ciOpConfigContent)
				env.ValueFrom = nil
			}
		}
	}

	return &rehearsal, nil
}

// ConfigureRehearsalJobs filters the jobs that should be rehearsed, then return a list of them re-configured with the
// ci-operator's configuration inlined.
func ConfigureRehearsalJobs(toBeRehearsed config.Presubmits, ciopConfigs config.CompoundCiopConfig, prNumber int, loggers Loggers, allowVolumes bool, templates config.CiTemplates) []*prowconfig.Presubmit {
	rehearsals := []*prowconfig.Presubmit{}

	rehearsalsFiltered := filterJobs(toBeRehearsed, allowVolumes, loggers.Job)
	for repo, jobs := range rehearsalsFiltered {
		for _, job := range jobs {
			jobLogger := loggers.Job.WithFields(logrus.Fields{"target-repo": repo, "target-job": job.Name})
			rehearsal, err := makeRehearsalPresubmit(&job, repo, prNumber)
			if err != nil {
				jobLogger.WithError(err).Warn("Failed to make a rehearsal presubmit")
				continue
			}

			rehearsal, err = inlineCiOpConfig(rehearsal, repo, ciopConfigs, loggers)
			if err != nil {
				jobLogger.WithError(err).Warn("Failed to inline ci-operator-config into rehearsal job")
				continue
			}

			exists, index, templateKey := hasChangedTemplateVolume(rehearsal.Spec.Containers[0].VolumeMounts, rehearsal.Spec.Volumes, templates)
			if exists {
				if templateData, err := config.GetTemplateData(templates[templateKey]); err != nil {
					jobLogger.WithError(err).WithField("template-name", templates[templateKey].Name).Warn("couldn't get template's data. Job won't be rehearsed")
					continue
				} else {
					templateName := config.GetTemplateName(templateKey)
					rehearsal.Spec.Volumes[index].VolumeSource.ConfigMap.Name = config.GetTempCMName(templateName, templateKey, templateData)
				}
			}

			jobLogger.WithField(logRehearsalJob, rehearsal.Name).Info("Created a rehearsal job to be submitted")
			rehearsals = append(rehearsals, rehearsal)
		}
	}

	return rehearsals
}

func hasChangedTemplateVolume(volumeMounts []v1.VolumeMount, volumes []v1.Volume, templates config.CiTemplates) (bool, int, string) {
	var templateKey string
	var volumeName string

	for _, volumeMount := range volumeMounts {
		if _, ok := templates[volumeMount.SubPath]; ok {
			templateKey = volumeMount.SubPath
			volumeName = volumeMount.Name
		}
	}

	for index, volume := range volumes {
		if volume.Name == volumeName {
			return true, index, templateKey
		}
	}

	return false, 0, ""
}

// Executor holds all the information needed for the jobs to be executed.
type Executor struct {
	dryRun     bool
	rehearsals []*prowconfig.Presubmit
	prNumber   int
	prRepo     string
	refs       *pjapi.Refs
	loggers    Loggers
	pjclient   pj.ProwJobInterface
}

// NewExecutor creates an executor. It also confgures the rehearsal jobs as a list of presubmits.
func NewExecutor(rehearsals []*prowconfig.Presubmit, prNumber int, prRepo string, refs *pjapi.Refs,
	dryRun bool, loggers Loggers, pjclient pj.ProwJobInterface) *Executor {
	return &Executor{
		dryRun:     dryRun,
		rehearsals: rehearsals,
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
			pj, ok := event.Object.(*pjapi.ProwJob)
			if !ok {
				return false, fmt.Errorf("received a %T from watch", event.Object)
			}
			fields := pjutil.ProwJobFields(pj)
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
				return success, nil
			}
		}
	}
}

func (e *Executor) submitRehearsals() ([]*pjapi.ProwJob, error) {
	var errors []error
	pjs := []*pjapi.ProwJob{}

	for _, job := range e.rehearsals {
		created, err := e.submitRehearsal(job)
		if err != nil {
			e.loggers.Job.WithError(err).Warn("Failed to execute a rehearsal presubmit")
			errors = append(errors, err)
			continue
		}
		e.loggers.Job.WithFields(pjutil.ProwJobFields(created)).Info("Submitted rehearsal prowjob")
		pjs = append(pjs, created)
	}
	return pjs, kerrors.NewAggregate(errors)
}

func (e *Executor) submitRehearsal(job *prowconfig.Presubmit) (*pjapi.ProwJob, error) {
	labels := make(map[string]string)
	for k, v := range job.Labels {
		labels[k] = v
	}

	prowJob := pjutil.NewProwJob(pjutil.PresubmitSpec(*job, *e.refs), labels)
	e.loggers.Job.WithFields(pjutil.ProwJobFields(&prowJob)).Info("Submitting a new prowjob.")

	return e.pjclient.Create(&prowJob)
}
