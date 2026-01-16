package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"
	"github.com/spf13/afero"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	pjapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	pjclientset "sigs.k8s.io/prow/pkg/client/clientset/versioned"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	configflagutil "sigs.k8s.io/prow/pkg/flagutil/config"
	"sigs.k8s.io/prow/pkg/gcsupload"
	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/pjutil"
	"sigs.k8s.io/prow/pkg/pod-utils/decorate"
	"sigs.k8s.io/prow/pkg/pod-utils/downwardapi"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/steps/utils"
	"github.com/openshift/ci-tools/pkg/util"
)

const (
	bundleImageRefOption          = "bundle-image-ref"
	channelOption                 = "channel"
	indexImageRefOption           = "index-image-ref"
	installNamespaceOption        = "install-namespace"
	jobConfigPathOption           = "job-config-path"
	jobNameOption                 = "job-name"
	ocpVersionOption              = "ocp-version"
	operatorPackageNameOptions    = "operator-package-name"
	outputFilePathOption          = "output-path"
	prowConfigPathOption          = "prow-config-path"
	releaseImageRefOption         = "release-image-ref"
	targetNamespacesOption        = "target-namespaces"
	customScorecardTestcaseOption = "custom-scorecard-testcase"
	enableHybridOverlayOption     = "enable-hybrid-overlay"

	BundleImage             = "BUNDLE_IMAGE"
	Channel                 = "OO_CHANNEL"
	IndexImage              = "OO_INDEX"
	InstallNamespace        = "OO_INSTALL_NAMESPACE"
	Package                 = "OO_PACKAGE"
	TargetNamespaces        = "OO_TARGET_NAMESPACES"
	CustomScorecardTestcase = "CUSTOM_SCORECARD_TESTCASE"
	PyxisUrl                = "PYXIS_URL"
	EnableHybridOverlay     = "ENABLE_HYBRID_OVERLAY"
)

type options struct {
	bundleImageRef          string
	channel                 string
	indexImageRef           string
	installNamespace        string
	jobName                 string
	prowconfig              configflagutil.ConfigOptions
	ocpVersion              string
	operatorPackageName     string
	outputPath              string
	releaseImageRef         string
	targetNamespaces        string
	pyxisUrl                string
	customScorecardTestcase string
	enableHybridOverlay     string
	dryRun                  bool
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

// var prowJobArtifactsURL string

// gatherOptions binds flag entries to entries in the options struct
func (o *options) gatherOptions() {
	o.prowconfig.ConfigPathFlagName = prowConfigPathOption
	o.prowconfig.JobConfigPathFlagName = jobConfigPathOption
	o.prowconfig.AddFlags(fs)
	fs.StringVar(&o.bundleImageRef, bundleImageRefOption, "", "URL for the bundle image")
	fs.StringVar(&o.channel, channelOption, "", "The channel to test")
	fs.StringVar(&o.indexImageRef, indexImageRefOption, "", "URL for the index image")
	fs.StringVar(&o.installNamespace, installNamespaceOption, "", "namespace into which the operator and catalog will be installed. If empty, a new namespace will be created.")
	fs.StringVar(&o.jobName, jobNameOption, "", "Name of the Periodic job to manually trigger")
	fs.StringVar(&o.ocpVersion, ocpVersionOption, "", "Version of OCP to use. Version must be 4.x or higher")
	fs.StringVar(&o.outputPath, outputFilePathOption, "", "File to store JSON returned from job submission")
	fs.StringVar(&o.operatorPackageName, operatorPackageNameOptions, "", "Operator package name to test")
	fs.StringVar(&o.releaseImageRef, releaseImageRefOption, "", "Pull spec of a specific release payload image used for OCP deployment.")
	fs.StringVar(&o.targetNamespaces, targetNamespacesOption, "", "A comma-separated list of namespaces the operator will target. If empty, all namespaces are targeted")
	fs.StringVar(&o.pyxisUrl, PyxisUrl, "", "URL that contains specific cvp product package name for specific ISV with unique pid")
	fs.StringVar(&o.customScorecardTestcase, customScorecardTestcaseOption, "", "Name of custom scorecard testcase that needs to be executed")
	fs.StringVar(&o.enableHybridOverlay, enableHybridOverlayOption, "false", "Enables the hybrid overlay feature on a running cluster")
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
	if err := o.prowconfig.Validate(false); err != nil {
		return err
	}

	exists, _ := afs.Exists(o.prowconfig.JobConfigPath)
	if !exists {
		return fmt.Errorf("validating job config path %s failed, does not exist", o.prowconfig.JobConfigPath)
	}

	if o.jobName == "" {
		return fmt.Errorf("required parameter %s was not provided", jobNameOption)
	}

	if o.ocpVersion == "" {
		return fmt.Errorf("required parameter %s was not provided", ocpVersionOption)
	}
	// Validate ocp-version format (must be X.Y where X >= 4)
	if !isValidOCPVersion(o.ocpVersion) {
		return fmt.Errorf("ocp-version must be in format X.Y where X >= 4 (e.g., 4.15, 5.0)")
	}

	if o.operatorPackageName == "" {
		return fmt.Errorf("required parameter %s was not provided", operatorPackageNameOptions)
	}

	exists, _ = afs.Exists(o.prowconfig.ConfigPath)
	if !exists {
		return fmt.Errorf("validating prow config path %s failed, does not exist", o.prowconfig.ConfigPath)
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

// isValidOCPVersion validates that the version is in format X.Y where X >= 4
func isValidOCPVersion(version string) bool {
	parts := strings.Split(version, ".")
	if len(parts) != 2 {
		return false
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return false
	}

	_, err = strconv.Atoi(parts[1])
	if err != nil {
		return false
	}

	return major >= 4
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

	prowjob := pjutil.NewProwJob(pjutil.PeriodicSpec(*selectedJob), nil, nil, pjutil.RequireScheduling(config.Scheduler.Enabled))
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

	configAgent, err := o.prowconfig.ConfigAgent()
	if err != nil {
		logrus.WithError(err).Fatal("failed to read Prow configuration")
	}
	config := configAgent.Config()
	prowjob, err := getPeriodicJob(o.jobName, config)

	if err != nil {
		logrus.WithField("job-name", o.jobName).Fatal(err)
	}

	// build up the multi-stage parameters to pass to the ci-operator.
	params := map[string]string{
		Channel: o.channel,
		Package: o.operatorPackageName,
	}
	if o.installNamespace != "" {
		params[InstallNamespace] = o.installNamespace
	}
	if o.pyxisUrl != "" {
		params[PyxisUrl] = o.pyxisUrl
	}
	if o.targetNamespaces != "" {
		params[TargetNamespaces] = o.targetNamespaces
	}
	if o.customScorecardTestcase != "" {
		params[CustomScorecardTestcase] = o.customScorecardTestcase
	}
	if o.enableHybridOverlay != "false" {
		params[EnableHybridOverlay] = o.enableHybridOverlay
	}

	depOverrides := map[string]string{
		BundleImage:   o.bundleImageRef,
		IndexImage:    o.indexImageRef,
		"INDEX_IMAGE": o.indexImageRef,
	}
	appendMultiStageParams(prowjob.Spec.PodSpec, params)
	appendMultiStageDepOverrides(prowjob.Spec.PodSpec, depOverrides)

	// Add flag values to inject as ENV var entries in the prowjob configuration
	envVars := map[string]string{
		"CLUSTER_TYPE": "aws",
		"OCP_VERSION":  o.ocpVersion,
	}
	if o.releaseImageRef != "" {
		envVars[utils.ReleaseImageEnv(api.LatestReleaseName)] = o.releaseImageRef
	}
	var keys []string
	for key := range envVars {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	input := strings.Builder{}
	input.WriteString("--input-hash=")
	for _, key := range keys {
		input.WriteString(key)
		input.WriteString(envVars[key])
	}
	prowjob.Spec.PodSpec.Containers[0].Args = append(prowjob.Spec.PodSpec.Containers[0].Args, input.String())
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
		var w watch.Interface
		if err = wait.ExponentialBackoff(wait.Backoff{Steps: 10, Duration: 1 * time.Second, Factor: 2}, func() (bool, error) {
			var err2 error
			w, err2 = pjclient.Watch(interrupts.Context(), metav1.ListOptions{FieldSelector: selector.String()})
			if err2 != nil {
				logrus.Error(err2)
				return false, nil
			}
			return true, nil
		}); err != nil {
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
	gcsConfig := config.Plank.GuessDefaultDecorationConfig(identifier, prowJob.Spec.Cluster).GCSConfiguration
	jobBasePath, _, _ := gcsupload.PathsForJob(gcsConfig, &spec, "")
	return fmt.Sprintf("%s%s/%s",
		config.Deck.Spyglass.GCSBrowserPrefix,
		gcsConfig.Bucket,
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

// appendMultiStageParams passes all of the OO_ params to ci-operator as multi-stage-params.
func appendMultiStageParams(podSpec *v1.PodSpec, params map[string]string) {
	// for execution purposes, the order isn't super important, but in order to allow for consistent test verification we need
	// to sort the params.
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		podSpec.Containers[0].Args = append(podSpec.Containers[0].Args, fmt.Sprintf("--multi-stage-param=%s=%s", key, params[key]))
	}
}

// appendMultStageParams passes image dependency overrides to ci-operator
func appendMultiStageDepOverrides(podSpec *v1.PodSpec, overrides map[string]string) {
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		podSpec.Containers[0].Args = append(podSpec.Containers[0].Args, fmt.Sprintf("--dependency-override-param=%s=%s", key, overrides[key]))
	}
}
