package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/steps/utils"
	"os"
	"path/filepath"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/util"
	"github.com/sirupsen/logrus"
	"github.com/spf13/afero"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	pjapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	pjclientset "k8s.io/test-infra/prow/client/clientset/versioned"
	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/gcsupload"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/pjutil"
	"k8s.io/test-infra/prow/pod-utils/decorate"
	"k8s.io/test-infra/prow/pod-utils/downwardapi"
)

const (
	bundleImageRefOption       = "bundle-image-ref"
	channelOption              = "channel"
	indexImageRefOption        = "index-image-ref"
	installNamespaceOption     = "install-namespace"
	jobConfigPathOption        = "job-config-path"
	jobNameOption              = "job-name"
	ocpVersionOption           = "ocp-version"
	operatorPackageNameOptions = "operator-package-name"
	outputFilePathOption       = "output-path"
	prowConfigPathOption       = "prow-config-path"
	releaseImageRefOption      = "release-image-ref"
	targetNamespacesOption     = "target-namespaces"
)

type options struct {
	bundleImageRef      string
	channel             string
	indexImageRef       string
	installNamespace    string
	jobConfigPath       string
	jobName             string
	ocpVersion          string
	operatorPackageName string
	outputPath          string
	prowConfigPath      string
	releaseImageRef     string
	targetNamespaces    string
	dryRun              bool
}

type jobResult interface {
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

var fileSystem = afero.NewOsFs()
var fs = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
var o options

//var prowJobArtifactsURL string

// gatherOptions binds flag entries to entries in the options struct
func (o *options) gatherOptions() {
	fs.StringVar(&o.bundleImageRef, bundleImageRefOption, "", "URL for the bundle image")
	fs.StringVar(&o.channel, channelOption, "", "The channel to test")
	fs.StringVar(&o.indexImageRef, indexImageRefOption, "", "URL for the index image")
	fs.StringVar(&o.installNamespace, installNamespaceOption, "", "namespace into which the operator and catalog will be installed. If empty, a new namespace will be created.")
	fs.StringVar(&o.jobConfigPath, jobConfigPathOption, "", "Path to the Prow job config directory")
	fs.StringVar(&o.jobName, jobNameOption, "", "Name of the Periodic job to manually trigger")
	fs.StringVar(&o.ocpVersion, ocpVersionOption, "", "Version of OCP to use. Version must be 4.x or higher")
	fs.StringVar(&o.outputPath, outputFilePathOption, "", "File to store JSON returned from job submission")
	fs.StringVar(&o.operatorPackageName, operatorPackageNameOptions, "", "Operator package name to test")
	fs.StringVar(&o.prowConfigPath, prowConfigPathOption, "", "Path to the Prow config file")
	fs.StringVar(&o.releaseImageRef, releaseImageRefOption, "", "Pull spec of a specific release payload image used for OCP deployment.")
	fs.StringVar(&o.targetNamespaces, targetNamespacesOption, "", "A comma-separated list of namespaces the operator will target. If empty, all namespaces are targeted")
	fs.BoolVar(&o.dryRun, "dry-run", false, "Executes a dry-run, displaying the job YAML without submitting the job to Prow")
}

// validateOptions ensures that all required flag options are
// present and validates any constraints on appropriate values
func (o options) validateOptions() error {
	afs := afero.Afero{Fs: fileSystem}

	if o.bundleImageRef == "" {
		return fmt.Errorf("required parameter %s was not provided", bundleImageRefOption)
	}

	if o.channel == "" {
		return fmt.Errorf("required parameter %s was not provided", channelOption)
	}

	if o.indexImageRef == "" {
		return fmt.Errorf("required parameter %s was not provided", indexImageRefOption)
	}

	if o.jobConfigPath == "" {
		return fmt.Errorf("required parameter %s was not provided", jobConfigPathOption)
	}
	exists, _ := afs.Exists(o.jobConfigPath)
	if !exists {
		return fmt.Errorf("validating job config path %s failed, does not exist", o.jobConfigPath)
	}

	if o.jobName == "" {
		return fmt.Errorf("required parameter %s was not provided", jobNameOption)
	}

	if o.ocpVersion == "" {
		return fmt.Errorf("required parameter %s was not provided", ocpVersionOption)
	}
	if !strings.HasPrefix(o.ocpVersion, "4") {
		return fmt.Errorf("ocp-version must be 4.x or higher")
	}

	if o.operatorPackageName == "" {
		return fmt.Errorf("required parameter %s was not provided", operatorPackageNameOptions)
	}

	if o.prowConfigPath == "" {
		return fmt.Errorf("required parameter %s was not provided", prowConfigPathOption)
	}
	exists, _ = afs.Exists(o.prowConfigPath)
	if !exists {
		return fmt.Errorf("validating prow config path %s failed, does not exist", o.prowConfigPath)
	}

	if !o.dryRun {
		if o.outputPath == "" {
			return fmt.Errorf("required parameter %s was not provided", outputFilePathOption)
		}
		exists, _ = afs.Exists(filepath.Dir(o.outputPath))
		if !exists {
			return fmt.Errorf("validating output file path %s failed, does not exist", o.outputPath)
		}
	}

	return nil
}

// getPeriodicJob returns a Prow Job or an error if the provided
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

func main() {
	o.gatherOptions()
	err := fs.Parse(os.Args[1:])
	if err != nil {
		logrus.WithError(err).Fatal("error parsing flag set")
	}

	err = o.validateOptions()
	if err != nil {
		logrus.WithError(err).Fatal("incorrect options")
	}

	go func() {
		interrupts.WaitForGracefulShutdown()
		os.Exit(1)
	}()

	config, err := prowconfig.Load(o.prowConfigPath, o.jobConfigPath)
	if err != nil {
		logrus.WithError(err).Fatal("failed to read Prow configuration")
	}
	prowjob, err := getPeriodicJob(o.jobName, config)

	if err != nil {
		logrus.WithField("job-name", o.jobName).Fatal(err)
	}

	// Add flag values to inject as ENV var entries in the prowjob configuration
	envVars := map[string]string{
		"BUNDLE_IMAGE":  o.bundleImageRef,
		"OCP_VERSION":   o.ocpVersion,
		"CLUSTER_TYPE":  "aws",
		steps.OOIndex:   o.indexImageRef,
		steps.OOPackage: o.operatorPackageName,
		steps.OOChannel: o.channel,
	}
	if o.releaseImageRef != "" {
		envVars[utils.ReleaseImageEnv(api.LatestStableName)] = o.releaseImageRef
	}
	if o.installNamespace != "" {
		envVars[steps.OOInstallNamespace] = o.installNamespace
	}
	if o.targetNamespaces != "" {
		envVars[steps.OOTargetNamespaces] = o.targetNamespaces
	}
	prowjob.Spec.PodSpec.Containers[0].Env = append(prowjob.Spec.PodSpec.Containers[0].Env, decorate.KubeEnv(envVars)...)

	// If the dry-run flag is provided, we're going to display the job config YAML and exit
	if o.dryRun {
		jobAsYAML, err := yaml.Marshal(prowjob)
		if err != nil {
			logrus.WithError(err).Fatal("failed to marshal the prowjob to YAML")
		}
		fmt.Println(string(jobAsYAML))
		os.Exit(0)
	}

	logrus.Info("getting cluster config")
	clusterConfig, err := util.LoadClusterConfig()
	if err != nil {
		logrus.WithError(err).Fatal("failed to load cluster configuration")
	}

	logrus.WithFields(pjutil.ProwJobFields(prowjob)).Info("submitting a new prowjob")
	pjcset, err := pjclientset.NewForConfig(clusterConfig)
	if err != nil {
		logrus.WithError(err).Fatal("failed to create prowjob clientset")
	}

	pjclient := pjcset.ProwV1().ProwJobs(config.ProwJobNamespace)

	logrus.WithFields(pjutil.ProwJobFields(prowjob)).Info("submitting a new prowjob")
	created, err := pjclient.Create(context.TODO(), prowjob, metav1.CreateOptions{})
	if err != nil {
		logrus.WithError(err).Fatal("failed to submit the prowjob")
	}

	logger := logrus.WithFields(pjutil.ProwJobFields(created))
	logger.Info("submitted the prowjob, waiting for its result")

	selector := fields.SelectorFromSet(map[string]string{"metadata.name": created.Name})

	for {
		w, err := pjclient.Watch(context.TODO(), metav1.ListOptions{FieldSelector: selector.String()})
		if err != nil {
			logrus.WithError(err).Fatal("failed to create watch for ProwJobs")
		}

		for event := range w.ResultChan() {
			prowJob, ok := event.Object.(*pjapi.ProwJob)
			if !ok {
				logrus.WithField("object-type", fmt.Sprintf("%T", event.Object)).Fatal("received an unexpected object from Watch")
			}

			prowJobArtifactsURL := getJobArtifactsURL(prowJob, config)

			switch prowJob.Status.State {
			case pjapi.FailureState, pjapi.AbortedState, pjapi.ErrorState:
				pjr := &prowjobResult{
					Status:       prowJob.Status.State,
					ArtifactsURL: prowJobArtifactsURL,
					URL:          prowJob.Status.URL,
				}
				err = writeResultOutput(pjr, o.outputPath)
				if err != nil {
					logrus.Error("Unable to write prowjob result to file")
				}
				logrus.Fatal("job failed")
			case pjapi.SuccessState:
				pjr := &prowjobResult{
					Status:       prowJob.Status.State,
					ArtifactsURL: prowJobArtifactsURL,
					URL:          prowJob.Status.URL,
				}
				err = writeResultOutput(pjr, o.outputPath)
				if err != nil {
					logrus.Error("Unable to write prowjob result to file")
				}
				logrus.Info("job succeeded")
				os.Exit(0)
			}
		}
	}
}

// returns the artifacts URL for the given job
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
func writeResultOutput(prowjobResult jobResult, outputPath string) error {
	j, err := prowjobResult.toJSON()
	if err != nil {
		logrus.Error("Unable to marshal prowjob result to JSON")
		return err
	}

	afs := afero.Afero{Fs: fileSystem}
	err = afs.WriteFile(outputPath, j, 0755)
	if err != nil {
		logrus.WithField("output path", outputPath).Error("error writing to output file")
		return err
	}

	return nil
}
