package prowjob

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"path/filepath"

	"github.com/openshift/ci-tools/pkg/util"
	"github.com/sirupsen/logrus"
	"github.com/spf13/afero"
	"gopkg.in/yaml.v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	pjapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	pjclientset "k8s.io/test-infra/prow/client/clientset/versioned"
	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/gcsupload"
	"k8s.io/test-infra/prow/pjutil"
	"k8s.io/test-infra/prow/pod-utils/decorate"
	"k8s.io/test-infra/prow/pod-utils/downwardapi"
)

const (
	jobConfigPathOption  = "job-config-path"
	jobNameOption        = "job-name"
	outputFilePathOption = "output-path"
	prowConfigPathOption = "prow-config-path"
)

type ProwJobOptions struct {
	JobConfigPath  string
	JobName        string
	OutputPath     string
	ProwConfigPath string
	DryRun         bool
}

type JobResult interface {
	toJSON() ([]byte, error)
}

type prowjobResult struct {
	Status       pjapi.ProwJobState `json:"status"`
	ArtifactsURL string             `json:"prowjob_artifacts_url"`
	URL          string             `json:"prowjob_url"`
}

func (p *prowjobResult) toJSON() ([]byte, error) {
	return json.MarshalIndent(p, "", "    ")
}

// gatherOptions binds flag entries to entries in the options struct
func (o *ProwJobOptions) AddFlags(fs *flag.FlagSet) {
	fs.StringVar(&o.JobConfigPath, jobConfigPathOption, "", "Path to the Prow job config directory")
	fs.StringVar(&o.JobName, jobNameOption, "", "Name of the Periodic job to manually trigger")
	fs.StringVar(&o.OutputPath, outputFilePathOption, "", "File to store JSON returned from job submission")
	fs.StringVar(&o.ProwConfigPath, prowConfigPathOption, "", "Path to the Prow config file")
	fs.BoolVar(&o.DryRun, "dry-run", false, "Executes a dry-run, displaying the job YAML without submitting the job to Prow")
}

func (o ProwJobOptions) ValidateOptions(fileSystem afero.Fs) error {

	afs := afero.Afero{Fs: fileSystem}
	if o.JobConfigPath == "" {
		return fmt.Errorf("required parameter %s was not provided", jobConfigPathOption)
	}
	exists, _ := afs.Exists(o.JobConfigPath)
	if !exists {
		return fmt.Errorf("validating job config path %s failed, does not exist", o.JobConfigPath)
	}

	if o.JobName == "" {
		return fmt.Errorf("required parameter %s was not provided", jobNameOption)
	}
	if o.ProwConfigPath == "" {
		return fmt.Errorf("required parameter %s was not provided", prowConfigPathOption)
	}
	exists, _ = afs.Exists(o.ProwConfigPath)
	if !exists {
		return fmt.Errorf("validating prow config path %s failed, does not exist", o.ProwConfigPath)
	}

	if !o.DryRun {
		if o.OutputPath != "" {
			exists, _ = afs.Exists(filepath.Dir(o.OutputPath))
			if !exists {
				return fmt.Errorf("validating output file path %s failed, does not exist", o.OutputPath)
			}
		}
	}

	return nil
}

// GetPeriodicJob returns a Prow Job or an error if the provided
// periodic job name is not found
func getPeriodicJob(jobName string, config *prowconfig.Config) (*pjapi.ProwJob, error) {
	var selectedJob *prowconfig.Periodic
	for _, job := range config.AllPeriodics() {
		if job.Name == jobName {
			selectedJob = &job
			break
		}
	}

	if selectedJob == nil {
		return nil, fmt.Errorf("failed to find the job: %s", jobName)
	}

	prowjob := pjutil.NewProwJob(pjutil.PeriodicSpec(*selectedJob), nil, nil)
	return &prowjob, nil
}

// getJobArtifactsURL returns the artifacts URL for the given job
func getJobArtifactsURL(prowJob *pjapi.ProwJob, config *prowconfig.Config) string {
	var identifier string
	if prowJob.Spec.Refs != nil {
		identifier = fmt.Sprintf("%s/%s", prowJob.Spec.Refs.Org, prowJob.Spec.Refs.Repo)
	} else {
		identifier = fmt.Sprintf("%s/%s", prowJob.Spec.ExtraRefs[0].Org, prowJob.Spec.ExtraRefs[0].Repo)
	}
	spec := downwardapi.NewJobSpec(prowJob.Spec, prowJob.Status.BuildID, prowJob.Name)
	jobBasePath, _, _ := gcsupload.PathsForJob(config.Plank.GetDefaultDecorationConfigs(identifier).GCSConfiguration, &spec, "")
	return fmt.Sprintf("%s%s/%s",
		config.Deck.Spyglass.GCSBrowserPrefix,
		config.Plank.GetDefaultDecorationConfigs(identifier).GCSConfiguration.Bucket,
		jobBasePath,
	)
}

// Calls toJSON method on a jobResult type and writes it to the output path
func writeResultOutput(prowjobResult JobResult, outputPath string, fileSystem afero.Fs) error {
	j, err := prowjobResult.toJSON()
	if err != nil {
		return fmt.Errorf("unable to marshal prowjob result to JSON: %w", err)
	}

	afs := afero.Afero{Fs: fileSystem}
	if outputPath != "" {
		err = afs.WriteFile(outputPath, j, 0755)
		if err != nil {
			logrus.WithField("output path", outputPath).Error("error writing to output file")
			return err
		}
	} else {
		logrus.Info(string(j))
	}

	return nil
}

func (o ProwJobOptions) SubmitJobAndWatchResults(envVars map[string]string, fileSystem afero.Fs) error {

	config, err := prowconfig.Load(o.ProwConfigPath, o.JobConfigPath)
	if err != nil {
		return fmt.Errorf("failed to read Prow configuration: %w", err)
	}
	prowjob, err := getPeriodicJob(o.JobName, config)

	if err != nil {
		return fmt.Errorf("failed to find job job-name %s: %w", o.JobName, err)
	}

	if envVars != nil {
		prowjob.Spec.PodSpec.Containers[0].Env = append(prowjob.Spec.PodSpec.Containers[0].Env, decorate.KubeEnv(envVars)...)
	}

	// If the dry-run flag is provided, we're going to display the job config YAML and exit
	if o.DryRun {
		jobAsYAML, err := yaml.Marshal(prowjob)
		if err != nil {
			return fmt.Errorf("failed to marshal the prowjob to YAML: %w", err)
		}
		fmt.Println(string(jobAsYAML))
		return nil
	}
	logrus.Info("getting cluster config")
	clusterConfig, err := util.LoadClusterConfig()
	if err != nil {
		return fmt.Errorf("failed to load cluster configuration: %w", err)
	}

	pjcset, err := pjclientset.NewForConfig(clusterConfig)
	if err != nil {
		return fmt.Errorf("failed to create prowjob clientset: %w", err)
	}
	pjclient := pjcset.ProwV1().ProwJobs(config.ProwJobNamespace)

	logrus.WithFields(pjutil.ProwJobFields(prowjob)).Info("submitting a new prowjob")
	created, err := pjclient.Create(context.TODO(), prowjob, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to submit the prowjob: %w", err)
	}

	logger := logrus.WithFields(pjutil.ProwJobFields(created))
	logger.Info("submitted the prowjob, waiting for its result")

	selector := fields.SelectorFromSet(map[string]string{"metadata.name": created.Name})

	for {
		w, err := pjclient.Watch(context.TODO(), metav1.ListOptions{FieldSelector: selector.String()})
		if err != nil {
			return fmt.Errorf("failed to create watch for ProwJobs: %w", err)
		}

		for event := range w.ResultChan() {
			prowJob, ok := event.Object.(*pjapi.ProwJob)
			if !ok {
				return fmt.Errorf("received an unexpected object from Watch: object-type %s", fmt.Sprintf("%T", event.Object))
			}

			prowJobArtifactsURL := getJobArtifactsURL(prowJob, config)

			switch prowJob.Status.State {
			case pjapi.FailureState, pjapi.AbortedState, pjapi.ErrorState:
				pjr := &prowjobResult{
					Status:       prowJob.Status.State,
					ArtifactsURL: prowJobArtifactsURL,
					URL:          prowJob.Status.URL,
				}
				err = writeResultOutput(pjr, o.OutputPath, fileSystem)
				if err != nil {
					logrus.Error("Unable to write prowjob result to file")
				}
				logrus.Warn("job failed")
				return nil
			case pjapi.SuccessState:
				pjr := &prowjobResult{
					Status:       prowJob.Status.State,
					ArtifactsURL: prowJobArtifactsURL,
					URL:          prowJob.Status.URL,
				}
				err = writeResultOutput(pjr, o.OutputPath, fileSystem)
				if err != nil {
					logrus.Error("Unable to write prowjob result to file")
				}
				logrus.Info("job succeeded")
				return nil
			}
		}
	}
}
