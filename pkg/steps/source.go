package steps

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/openshift/ci-tools/pkg/kubernetes/pkg/credentialprovider"
	"github.com/openshift/ci-tools/pkg/util/buildspec"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/yaml"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/clonerefs"
	"sigs.k8s.io/prow/pkg/pod-utils/decorate"

	buildapi "github.com/openshift/api/build/v1"
	imagev1 "github.com/openshift/api/image/v1"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	codebuildtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	"github.com/openshift/ci-tools/pkg/api"
	apiutils "github.com/openshift/ci-tools/pkg/api/utils"
	"github.com/openshift/ci-tools/pkg/kubernetes"
	"github.com/openshift/ci-tools/pkg/manifestpusher"
	"github.com/openshift/ci-tools/pkg/metrics"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	"github.com/openshift/ci-tools/pkg/steps/utils"
	"github.com/openshift/ci-tools/pkg/util"
)

const (
	CiAnnotationPrefix = "ci.openshift.io"
	CreatesLabel       = "creates"
	CreatedByCILabel   = "created-by-ci"

	ProwJobIdLabel = "prow.k8s.io/id"

	gopath        = "/go"
	sshPrivateKey = "/sshprivatekey"
	sshConfig     = "/ssh_config"
	oauthToken    = "/oauth-token"

	OauthSecretKey = "oauth-token"

	ClonerefsBinaryPath = "/clonerefs"
)

type CloneAuthType string

var (
	CloneAuthTypeSSH   CloneAuthType = "SSH"
	CloneAuthTypeOAuth CloneAuthType = "OAuth"
)

type CloneAuthConfig struct {
	Secret *corev1.Secret
	Type   CloneAuthType
}

func (c *CloneAuthConfig) getCloneURI(org, repo string) string {
	if c.Type == CloneAuthTypeSSH {
		return fmt.Sprintf("ssh://git@github.com/%s/%s.git", org, repo)
	}
	return fmt.Sprintf("https://github.com/%s/%s.git", org, repo)
}

var (
	JobSpecAnnotation = fmt.Sprintf("%s/%s", CiAnnotationPrefix, "job-spec")
)

func sourceDockerfile(fromTag api.PipelineImageStreamTagReference, workingDir string, cloneAuthConfig *CloneAuthConfig) string {
	var dockerCommands []string
	var secretPath string

	dockerCommands = append(dockerCommands, "")
	dockerCommands = append(dockerCommands, fmt.Sprintf("FROM %s:%s", api.PipelineImageStream, fromTag))
	dockerCommands = append(dockerCommands, fmt.Sprintf("ADD .%s %s", ClonerefsBinaryPath, ClonerefsBinaryPath))

	if cloneAuthConfig != nil {
		switch cloneAuthConfig.Type {
		case CloneAuthTypeSSH:
			dockerCommands = append(dockerCommands, fmt.Sprintf("ADD %s /etc/ssh/ssh_config", sshConfig))
			dockerCommands = append(dockerCommands, fmt.Sprintf("COPY ./%s %s", corev1.SSHAuthPrivateKey, sshPrivateKey))
			secretPath = sshPrivateKey
		case CloneAuthTypeOAuth:
			dockerCommands = append(dockerCommands, fmt.Sprintf("COPY ./%s %s", OauthSecretKey, oauthToken))
			secretPath = oauthToken
		}
	}

	dockerCommands = append(dockerCommands, fmt.Sprintf("RUN umask 0002 && %s && find %s/src -type d -not -perm -0775 | xargs --max-procs 10 --max-args 100 --no-run-if-empty chmod g+xw", ClonerefsBinaryPath, gopath))
	dockerCommands = append(dockerCommands, fmt.Sprintf("WORKDIR %s/", workingDir))
	dockerCommands = append(dockerCommands, fmt.Sprintf("ENV GOPATH=%s", gopath))

	// After the clonerefs command, we don't need the secret anymore.
	// We don't want to let the key keep existing in the image's layer.
	if len(secretPath) > 0 {
		dockerCommands = append(dockerCommands, fmt.Sprintf("RUN rm -f %s", secretPath))
	}

	dockerCommands = append(dockerCommands, "")

	return strings.Join(dockerCommands, "\n")
}

const (
	LabelMetadataOrg     = "ci.openshift.io/metadata.org"
	LabelMetadataRepo    = "ci.openshift.io/metadata.repo"
	LabelMetadataBranch  = "ci.openshift.io/metadata.branch"
	LabelMetadataVariant = "ci.openshift.io/metadata.variant"
	LabelMetadataTarget  = "ci.openshift.io/metadata.target"
	LabelMetadataStep    = "ci.openshift.io/metadata.step"
	LabelJobID           = "ci.openshift.io/jobid"
	LabelJobType         = "ci.openshift.io/jobtype"
	LabelJobName         = "ci.openshift.io/jobname"
)

func LabelsFor(spec *api.JobSpec, base map[string]string, ref string) map[string]string {
	if base == nil {
		base = map[string]string{}
	}
	org := spec.Metadata.Org
	repo := spec.Metadata.Repo
	branch := spec.Metadata.Branch
	variant := spec.Metadata.Variant
	jobID := spec.ProwJobID
	jobType := spec.Type
	jobName := spec.Job
	if ref != "" { //When building for a specific ref, the metadata will be empty and need to be determined from that ref
		for _, extraRef := range spec.ExtraRefs {
			orgRepo := fmt.Sprintf("%s.%s", extraRef.Org, extraRef.Repo)
			if orgRepo == ref {
				org = extraRef.Org
				repo = extraRef.Repo
				branch = extraRef.BaseRef
			}
		}
	}

	base[LabelMetadataOrg] = org
	base[LabelMetadataRepo] = repo
	base[LabelMetadataBranch] = branch
	base[LabelMetadataVariant] = variant
	base[LabelMetadataTarget] = spec.Target
	base[LabelJobID] = jobID
	base[LabelJobType] = string(jobType)
	base[LabelJobName] = jobName
	base[CreatedByCILabel] = "true"
	base[openshiftCIEnv] = "true"
	return apiutils.SanitizeLabels(base)
}

type sourceStep struct {
	config          api.SourceStepConfiguration
	resources       api.ResourceConfiguration
	client          BuildClient
	podClient       kubernetes.PodClient
	jobSpec         *api.JobSpec
	cloneAuthConfig *CloneAuthConfig
	pullSecret      *corev1.Secret
	architectures   sets.Set[string]
	metricsAgent    *metrics.MetricsAgent
	buildType       string
}

func (s *sourceStep) Inputs() (api.InputDefinition, error) {
	return s.jobSpec.Inputs(), nil
}

func (*sourceStep) Validate() error { return nil }

func (s *sourceStep) Run(ctx context.Context) error {
	return results.ForReason("cloning_source").ForError(s.run(ctx))
}

func (s *sourceStep) run(ctx context.Context) error {
	clonerefsRef := corev1.ObjectReference{Kind: "DockerImage", Name: s.config.ClonerefsPullSpec}

	fromDigest, err := resolvePipelineImageStreamTagReference(ctx, s.client, s.config.From, s.jobSpec)
	if err != nil {
		return err
	}
	return handleBuilds(
		ctx,
		s.client,
		s.podClient,
		s.buildType,
		*createBuild(s.config, s.jobSpec, clonerefsRef, s.resources, s.cloneAuthConfig, s.pullSecret, fromDigest), s.metricsAgent, newImageBuildOptions(s.architectures.UnsortedList()),
	)
}

func createBuild(config api.SourceStepConfiguration, jobSpec *api.JobSpec, clonerefsRef corev1.ObjectReference, resources api.ResourceConfiguration, cloneAuthConfig *CloneAuthConfig, pullSecret *corev1.Secret, fromDigest string) *buildapi.Build {
	var refs []prowv1.Refs
	if jobSpec.Refs != nil {
		r := *jobSpec.Refs
		orgRepo := fmt.Sprintf("%s.%s", r.Org, r.Repo)
		if config.Ref == "" || orgRepo == config.Ref {
			if cloneAuthConfig != nil {
				r.CloneURI = cloneAuthConfig.getCloneURI(r.Org, r.Repo)
			}
			refs = append(refs, r)
		}
	}

	for _, r := range jobSpec.ExtraRefs {
		orgRepo := fmt.Sprintf("%s.%s", r.Org, r.Repo)
		if config.Ref == "" || orgRepo == config.Ref {
			if cloneAuthConfig != nil {
				r.CloneURI = cloneAuthConfig.getCloneURI(r.Org, r.Repo)
			}
			refs = append(refs, r)
		}
	}

	for i := range refs {
		refs[i].SparseCheckoutFiles = nil
	}

	dockerfile := sourceDockerfile(config.From, decorate.DetermineWorkDir(gopath, refs), cloneAuthConfig)
	buildSource := buildapi.BuildSource{
		Type:       buildapi.BuildSourceDockerfile,
		Dockerfile: &dockerfile,
		Images: []buildapi.ImageSource{
			{
				From: clonerefsRef,
				Paths: []buildapi.ImageSourcePath{
					{
						SourcePath:     config.ClonerefsPath,
						DestinationDir: ".",
					},
				},
			},
		},
	}

	optionsSpec := clonerefs.Options{
		SrcRoot:      gopath,
		Log:          "/dev/null",
		GitUserName:  "ci-robot",
		GitUserEmail: "ci-robot@openshift.io",
		GitRefs:      refs,
		Fail:         true,
	}

	if cloneAuthConfig != nil {
		buildSource.Secrets = append(buildSource.Secrets,
			buildapi.SecretBuildSource{
				Secret: *getSourceSecretFromName(cloneAuthConfig.Secret.Name),
			},
		)
		if cloneAuthConfig.Type == CloneAuthTypeSSH {
			for i, image := range buildSource.Images {
				if image.From == clonerefsRef {
					buildSource.Images[i].Paths = append(buildSource.Images[i].Paths, buildapi.ImageSourcePath{
						SourcePath: sshConfig, DestinationDir: "."})
				}
			}
			optionsSpec.KeyFiles = append(optionsSpec.KeyFiles, sshPrivateKey)
		} else {
			optionsSpec.OauthTokenFile = oauthToken

		}
	}

	// hack to work around a build subsystem string-escaping bug w.r.t. escaping in env vars
	for i := range optionsSpec.GitRefs {
		for j := range optionsSpec.GitRefs[i].Pulls {
			optionsSpec.GitRefs[i].Pulls[j].Title = ""
		}
	}

	optionsJSON, err := clonerefs.Encode(optionsSpec)
	if err != nil {
		panic(fmt.Errorf("couldn't create JSON spec for clonerefs: %w", err))
	}

	build := buildFromSource(jobSpec, config.From, config.To, buildSource, fromDigest, "", resources, pullSecret, nil, config.Ref)
	build.Spec.CommonSpec.Strategy.DockerStrategy.Env = append(
		build.Spec.CommonSpec.Strategy.DockerStrategy.Env,
		corev1.EnvVar{Name: clonerefs.JSONConfigEnvVar, Value: optionsJSON},
	)

	return build
}

func resolvePipelineImageStreamTagReference(ctx context.Context, client loggingclient.LoggingClient, tag api.PipelineImageStreamTagReference, jobSpec *api.JobSpec) (string, error) {
	ist := &imagev1.ImageStreamTag{}
	if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: jobSpec.Namespace(), Name: fmt.Sprintf("%s:%s", api.PipelineImageStream, tag)}, ist); err != nil {
		return "", fmt.Errorf("could not resolve pipeline image stream tag %s: %w", tag, err)
	}
	return ist.Image.Name, nil
}

func buildFromSource(jobSpec *api.JobSpec, fromTag, toTag api.PipelineImageStreamTagReference, source buildapi.BuildSource, fromTagDigest, dockerfilePath string, resources api.ResourceConfiguration, pullSecret *corev1.Secret, buildArgs []api.BuildArg, ref string) *buildapi.Build {
	logrus.Infof("Building %s", toTag)
	buildName := string(toTag)
	// Build names cannot contain '_', but repository names can. When building from a multi-pr config, there could be '_' present in the toTag
	if strings.Contains(buildName, "_") {
		buildName = strings.Replace(buildName, "_", "-", -1)
		logrus.Infof("replacing '_' chars in build name; new name: %s", buildName)
	}
	buildResources, err := ResourcesFor(resources.RequirementsForStep(string(toTag)))
	if err != nil {
		panic(fmt.Errorf("unable to parse resource requirement for build %s: %w", toTag, err))
	}
	var from *corev1.ObjectReference
	if len(fromTag) > 0 {
		from = &corev1.ObjectReference{
			Kind:      "ImageStreamTag",
			Namespace: jobSpec.Namespace(),
			Name:      fmt.Sprintf("%s:%s", api.PipelineImageStream, fromTag),
		}
	}

	layer := buildapi.ImageOptimizationSkipLayers
	labels := LabelsFor(jobSpec, map[string]string{CreatesLabel: buildName}, ref)
	build := &buildapi.Build{
		ObjectMeta: metav1.ObjectMeta{
			Name:      buildName,
			Namespace: jobSpec.Namespace(),
			Labels:    labels,
			Annotations: map[string]string{
				JobSpecAnnotation: jobSpec.RawSpec(),
			},
		},
		Spec: buildapi.BuildSpec{
			CommonSpec: buildapi.CommonSpec{
				Resources: buildResources,
				Source:    source,
				Strategy: buildapi.BuildStrategy{
					Type: buildapi.DockerBuildStrategyType,
					DockerStrategy: &buildapi.DockerBuildStrategy{
						DockerfilePath:          dockerfilePath,
						From:                    from,
						ForcePull:               true,
						NoCache:                 true,
						Env:                     []corev1.EnvVar{{Name: "BUILD_LOGLEVEL", Value: "0"}}, // this mirrors the default and is done for documentary purposes
						ImageOptimizationPolicy: &layer,
						BuildArgs:               toEnv(buildArgs),
					},
				},
				Output: buildapi.BuildOutput{
					To: &corev1.ObjectReference{
						Kind:      "ImageStreamTag",
						Namespace: jobSpec.Namespace(),
						Name:      fmt.Sprintf("%s:%s", api.PipelineImageStream, toTag),
					},
				},
			},
		},
	}
	if len(fromTag) > 0 {
		build.Spec.Output.ImageLabels = append(build.Spec.Output.ImageLabels, buildapi.ImageLabel{
			Name:  api.ImageVersionLabel(fromTag),
			Value: fromTagDigest,
		})
	}
	if pullSecret != nil {
		build.Spec.Strategy.DockerStrategy.PullSecret = getSourceSecretFromName(api.RegistryPullCredentialsSecret)
	}
	if owner := jobSpec.Owner(); owner != nil {
		build.OwnerReferences = append(build.OwnerReferences, *owner)
	}

	relevantRefs := jobSpec.Refs
	if ref != "" {
		for _, extraRef := range jobSpec.ExtraRefs {
			orgRepo := fmt.Sprintf("%s.%s", extraRef.Org, extraRef.Repo)
			if orgRepo == ref {
				relevantRefs = &extraRef
				break
			}
		}
	}
	addLabelsToBuild(relevantRefs, build, source.ContextDir)
	return build
}

func toEnv(args []api.BuildArg) []corev1.EnvVar {
	var ret []corev1.EnvVar
	for _, arg := range args {
		ret = append(ret, corev1.EnvVar{Name: arg.Name, Value: arg.Value})
	}
	return ret
}

func buildInputsFromStep(inputs map[string]api.ImageBuildInputs) []buildapi.ImageSource {
	var names []string
	for k := range inputs {
		names = append(names, k)
	}
	sort.Strings(names)
	var refs []buildapi.ImageSource
	for _, name := range names {
		value := inputs[name]
		var paths []buildapi.ImageSourcePath
		for _, path := range value.Paths {
			paths = append(paths, buildapi.ImageSourcePath{SourcePath: path.SourcePath, DestinationDir: path.DestinationDir})
		}
		if len(value.As) == 0 && len(paths) == 0 {
			continue
		}
		refs = append(refs, buildapi.ImageSource{
			From: corev1.ObjectReference{
				Kind: "ImageStreamTag",
				Name: fmt.Sprintf("%s:%s", api.PipelineImageStream, name),
			},
			As:    value.As,
			Paths: paths,
		})
	}
	return refs
}

func handleFailedBuild(ctx context.Context, client BuildClient, ns, name string, err error) error {
	b := &buildapi.Build{}
	if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: ns, Name: name}, b); err != nil {
		return fmt.Errorf("could not get build %s: %w", name, err)
	}

	if !isBuildPhaseTerminated(b.Status.Phase) {
		logrus.Debugf("Build %q (created at %v) still in phase %q", name, b.CreationTimestamp, b.Status.Phase)
		return err
	}

	if !(isInfraReason(b.Status.Reason) || hintsAtInfraReason(b.Status.LogSnippet)) {
		logrus.Debugf("Build %q (created at %v) classified as legitimate failure, will not be retried", name, b.CreationTimestamp)
		return err
	}

	logrus.Infof("Build %s previously failed from an infrastructure error (%s), retrying...", name, b.Status.Reason)

	// Remove workload from metrics watching since we're about to delete and recreate the build
	client.MetricsAgent().RemoveNodeWorkload(name)

	zero := int64(0)
	foreground := metav1.DeletePropagationForeground
	opts := metav1.DeleteOptions{
		GracePeriodSeconds: &zero,
		Preconditions:      &metav1.Preconditions{UID: &b.UID},
		PropagationPolicy:  &foreground,
	}
	if err := client.Delete(ctx, b, &ctrlruntimeclient.DeleteOptions{Raw: &opts}); err != nil && !kerrors.IsNotFound(err) && !kerrors.IsConflict(err) {
		return fmt.Errorf("could not delete build %s: %w", name, err)
	}
	if err := waitForBuildDeletion(ctx, client, ns, name); err != nil {
		return fmt.Errorf("could not wait for build %s to be deleted: %w", name, err)
	}
	return nil
}

func isBuildPhaseTerminated(phase buildapi.BuildPhase) bool {
	switch phase {
	case buildapi.BuildPhaseNew,
		buildapi.BuildPhasePending,
		buildapi.BuildPhaseRunning:
		return false
	}
	return true
}

type ImageBuildOptions struct {
	Architectures []string
}

func newImageBuildOptions(archs []string) ImageBuildOptions {
	return ImageBuildOptions{Architectures: archs}
}

func handleBuilds(ctx context.Context, buildClient BuildClient, podClient kubernetes.PodClient, buildType string, build buildapi.Build, metricsAgent *metrics.MetricsAgent, opts ...ImageBuildOptions) error {
	var wg sync.WaitGroup

	o := ImageBuildOptions{}
	if len(opts) > 0 {
		o = opts[0]
	}

	builds := constructMultiArchBuilds(build, o.Architectures)
	errChan := make(chan error, len(builds))

	wg.Add(len(builds))
	for _, build := range builds {
		go func(b buildapi.Build) {
			defer wg.Done()
			metricsAgent.AddNodeWorkload(ctx, b.Namespace, fmt.Sprintf("%s-build", b.Name), b.Name, podClient)
			if err := handleBuild(ctx, buildClient, podClient, b, buildType); err != nil {
				errChan <- fmt.Errorf("error occurred handling build %s: %w", b.Name, err)
			}
			metricsAgent.RemoveNodeWorkload(b.Name)
		}(build)
	}

	wg.Wait()
	close(errChan)

	for _, b := range builds {
		metricsAgent.Record(metrics.NewBuildEvent(b.Name, b.Namespace, build.Spec.Output.To.Name))
	}

	var errs []error
	for err := range errChan {
		errs = append(errs, err)
	}

	if len(errs) == 0 {
		manifestPusher := manifestpusher.NewManifestPusher(logrus.WithField("for-build", build.Name), buildClient.LocalRegistryDNS(), buildClient.ManifestToolDockerCfg(), buildClient.Client())
		if err := manifestPusher.PushImageWithManifest(builds, fmt.Sprintf("%s/%s", build.Spec.Output.To.Namespace, build.Spec.Output.To.Name)); err != nil {
			errs = append(errs, err)
		}
	}

	return utilerrors.NewAggregate(errs)
}

// constructMultiArchBuilds gets a specific build and constructs multiple builds for each architecture.
// The name and the output image of the build is suffixed with the architecture name and it will include the nodeSelector for the specific architecture.
// e.x if the build name is "foo" and the architectures are "amd64,arm64", the new builds will be "foo-amd64" and "foo-arm64".
func constructMultiArchBuilds(build buildapi.Build, stepArchitectures []string) []buildapi.Build {
	var ret []buildapi.Build

	archs := stepArchitectures
	if len(stepArchitectures) == 0 {
		archs = append(archs, "amd64")
	}

	for _, arch := range archs {
		b := build
		b.Name = fmt.Sprintf("%s-%s", b.Name, arch)
		b.Spec.NodeSelector = map[string]string{
			corev1.LabelArchStable: arch,
		}

		b.Spec.Output.To = &corev1.ObjectReference{
			Kind:      "ImageStreamTag",
			Namespace: b.Namespace,
			Name:      fmt.Sprintf("%s:%s", api.PipelineImageStream, b.Name),
		}
		ret = append(ret, b)
	}

	return ret
}

func handleBuild(ctx context.Context, buildClient BuildClient, podClient kubernetes.PodClient, build buildapi.Build, buildType string) error {
	const attempts = 5
	var errs []error
	if err := wait.ExponentialBackoff(wait.Backoff{Duration: time.Minute, Factor: 1.5, Steps: attempts}, func() (bool, error) {
		if buildType == "aws" {
			return awsBuild(ctx, buildClient, build, &errs)
		}
		return openshiftBuild(ctx, buildClient, podClient, build, &errs)
	}); err != nil {
		if err == wait.ErrWaitTimeout {
			return fmt.Errorf("build not successful after %d attempts: %w", attempts, utilerrors.NewAggregate(errs))
		}
		return err
	}
	return nil
}

func waitForBuildDeletion(ctx context.Context, client ctrlruntimeclient.Client, ns, name string) error {
	ch := make(chan error)
	go func() {
		ch <- wait.ExponentialBackoff(wait.Backoff{
			Duration: 10 * time.Millisecond, Factor: 2, Steps: 10,
		}, func() (done bool, err error) {
			if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: ns, Name: name}, &buildapi.Build{}); err != nil {
				if kerrors.IsNotFound(err) {
					return true, nil
				}
				return false, err
			}
			return false, nil
		})
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-ch:
		return err
	}
}

func isInfraReason(reason buildapi.StatusReason) bool {
	infraReasons := []buildapi.StatusReason{
		buildapi.StatusReasonBuildPodEvicted,
		buildapi.StatusReasonBuildPodDeleted,
		buildapi.StatusReasonBuildPodExists,
		buildapi.StatusReasonCannotCreateBuildPod,
		buildapi.StatusReasonCannotRetrieveServiceAccount,
		buildapi.StatusReasonExceededRetryTimeout,
		buildapi.StatusReasonFailedContainer,
		buildapi.StatusReasonFetchImageContentFailed,
		buildapi.StatusReasonFetchSourceFailed,
		buildapi.StatusReasonGenericBuildFailed,
		buildapi.StatusReasonNoBuildContainerStatus,
		buildapi.StatusReasonOutOfMemoryKilled,
		buildapi.StatusReasonPullBuilderImageFailed,
		buildapi.StatusReasonPushImageToRegistryFailed,
	}
	for _, option := range infraReasons {
		if reason == option {
			return true
		}
	}
	return false
}

func hintsAtInfraReason(logSnippet string) bool {
	return strings.Contains(logSnippet, "error: build error: no such image") ||
		strings.Contains(logSnippet, "[Errno 256] No more mirrors to try.") ||
		strings.Contains(logSnippet, "Error: Failed to synchronize cache for repo") ||
		strings.Contains(logSnippet, "Could not resolve host: ") ||
		strings.Contains(logSnippet, "net/http: TLS handshake timeout") ||
		strings.Contains(logSnippet, "All mirrors were tried") ||
		strings.Contains(logSnippet, "connection reset by peer")
}

func waitForBuildOrTimeout(
	ctx context.Context,
	buildClient BuildClient,
	podClient kubernetes.PodClient,
	namespace, name string,
) error {
	return waitForBuild(ctx, buildClient, podClient, namespace, name)
}

// waitForBuild watches a build until it either succeeds or fails
//
// Several subtle aspects are involved in the implementation:
//
//   - The particular ci-operator instance executing this function may be the
//     one that just created the build, but it may also be one that executes in
//     parallel with the one that did, or even one that is being executed at a
//     later point and simply reusing an existing build.  This means we may be
//     watching a build at any point in its lifetime, including long after it
//     has been created and/or after it has succeeded/failed.
//   - Because builds cannot be completely validated a priori, there is a
//     potential that the object in question will stay pending forever.  The
//     timeout parameter (passed via the Pod client) is used to fail the
//     execution early in that case.  A timeout must result in an immediate
//     error.
//   - Because of the volume of tests executing in a given build cluster (and,
//     to a lesser extent, to avoid unnecessary delays), this function must use
//     a watch instead of polling in order to not overwhelm the API server.
//     Economizing API requests when possible is also helpful.
func waitForBuild(
	ctx context.Context,
	buildClient BuildClient,
	podClient kubernetes.PodClient,
	namespace, name string,
) error {
	logrus.WithFields(logrus.Fields{
		"namespace": namespace,
		"name":      name,
	}).Trace("Waiting for build to be complete.")
	// ret contains the latest version of the object received from the watch
	// It is always valid in the `pendingCheck` thread since it is only started
	// after the first version is seen.
	var ret atomic.Pointer[buildapi.Build]
	var eg *errgroup.Group
	eg, ctx = errgroup.WithContext(ctx)
	pendingCtx, cancel := context.WithCancel(ctx)
	pendingCheck := func() error {
		timeout := podClient.GetPendingTimeout()
		select {
		case <-pendingCtx.Done():
		case <-time.After(time.Until(ret.Load().CreationTimestamp.Add(timeout))):
			// This second load happens much later and must look at the latest
			// version of the object.
			if err := checkPending(ctx, podClient, ret.Load(), timeout, time.Now()); err != nil {
				logrus.Infof("%s", err.Error())
				return err
			}
		}
		return nil
	}

	buildPodName := fmt.Sprintf("%s-build", name)
	eg.Go(func() error {
		defer cancel()
		return kubernetes.WaitForConditionOnObject(ctx, buildClient, ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: name}, &buildapi.BuildList{}, &buildapi.Build{}, func(obj runtime.Object) (bool, error) {
			build := obj.(*buildapi.Build)
			// Is this the first time we've received an object?
			// Also updates the shared pointer every time so that `pendingCheck`
			// has access to the latest version
			first := ret.Swap(build) == nil
			switch build.Status.Phase {
			case buildapi.BuildPhaseNew, buildapi.BuildPhasePending:
				// Iff this is a (relatively) new build, we need to verify that
				// it does not stay pending forever.
				if first {
					eg.Go(pendingCheck)
				}
			case buildapi.BuildPhaseComplete:
				logrus.Infof("Build %s succeeded after %s", build.Name, buildDuration(build).Truncate(time.Second))
				podClient.MetricsAgent().StorePodLifecycleMetrics(buildPodName, build.Namespace, corev1.PodSucceeded)
				podClient.MetricsAgent().StoreMachinesSnapshot(build)
				return true, nil
			case buildapi.BuildPhaseFailed, buildapi.BuildPhaseCancelled, buildapi.BuildPhaseError:
				logrus.Infof("Build %s failed, printing logs:", build.Name)
				printBuildLogs(buildClient, build.Namespace, build.Name)
				podClient.MetricsAgent().StorePodLifecycleMetrics(buildPodName, build.Namespace, corev1.PodFailed)
				podClient.MetricsAgent().StoreMachinesSnapshot(build)
				return true, util.AppendLogToError(fmt.Errorf("the build %s failed after %s with reason %s: %s", build.Name, buildDuration(build).Truncate(time.Second), build.Status.Reason, build.Status.Message), build.Status.LogSnippet)
			}
			return false, nil
		}, 0)
	})
	return eg.Wait()
}

func checkPending(
	ctx context.Context,
	podClient kubernetes.PodClient,
	build *buildapi.Build,
	timeout time.Duration,
	now time.Time,
) error {
	switch build.Status.Phase {
	case buildapi.BuildPhaseNew, buildapi.BuildPhasePending:
		if build.CreationTimestamp.Add(timeout).Before(now) {
			return util.PendingBuildError(ctx, podClient, build)
		}
	}
	return nil
}

func buildDuration(build *buildapi.Build) time.Duration {
	start := build.Status.StartTimestamp
	if start == nil {
		start = &build.CreationTimestamp
	}
	end := build.Status.CompletionTimestamp
	if end == nil {
		end = &metav1.Time{Time: time.Now()}
	}
	duration := end.Sub(start.Time)
	return duration
}

func printBuildLogs(buildClient BuildClient, namespace, name string) {
	if s, err := buildClient.Logs(namespace, name, &buildapi.BuildLogOptions{
		NoWait: true,
	}); err == nil {
		defer s.Close()
		if _, err := io.Copy(os.Stdout, s); err != nil {
			logrus.WithError(err).Warn("Unable to copy log output from failed build.")
		}
	} else {
		logrus.WithError(err).Warn("Unable to retrieve logs from failed build")
	}
}

func ResourcesFor(req api.ResourceRequirements) (corev1.ResourceRequirements, error) {
	apireq := corev1.ResourceRequirements{}
	for name, value := range req.Requests {
		q, err := resource.ParseQuantity(value)
		if err != nil {
			return corev1.ResourceRequirements{}, fmt.Errorf("invalid resource request: %w", err)
		}
		if apireq.Requests == nil {
			apireq.Requests = make(corev1.ResourceList)
		}
		apireq.Requests[corev1.ResourceName(name)] = q
	}
	for name, value := range req.Limits {
		q, err := resource.ParseQuantity(value)
		if err != nil {
			return corev1.ResourceRequirements{}, fmt.Errorf("invalid resource limit: %w", err)
		}
		if apireq.Limits == nil {
			apireq.Limits = make(corev1.ResourceList)
		}
		apireq.Limits[corev1.ResourceName(name)] = q
	}
	return apireq, nil
}

func (s *sourceStep) Requires() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(s.config.From)}
}

func (s *sourceStep) Creates() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(s.config.To)}
}

func (s *sourceStep) Provides() api.ParameterMap {
	return api.ParameterMap{
		utils.PipelineImageEnvFor(s.config.To): utils.ImageDigestFor(s.client, s.jobSpec.Namespace, api.PipelineImageStream, string(s.config.To)),
	}
}

func (s *sourceStep) Name() string { return s.config.TargetName() }

func (s *sourceStep) Description() string {
	return fmt.Sprintf("Clone the correct source code into an image and tag it as %s", s.config.To)
}

func (s *sourceStep) Objects() []ctrlruntimeclient.Object {
	return s.client.Objects()
}

func (s *sourceStep) ResolveMultiArch() sets.Set[string] {
	return s.architectures
}

func (s *sourceStep) AddArchitectures(archs []string) {
	s.architectures.Insert(archs...)
}

func SourceStep(
	config api.SourceStepConfiguration,
	resources api.ResourceConfiguration,
	buildClient BuildClient,
	podClient kubernetes.PodClient,
	jobSpec *api.JobSpec,
	cloneAuthConfig *CloneAuthConfig,
	pullSecret *corev1.Secret,
	metricsAgent *metrics.MetricsAgent,
	buildType string,
) api.Step {
	return &sourceStep{
		config:          config,
		resources:       resources,
		client:          buildClient,
		podClient:       podClient,
		jobSpec:         jobSpec,
		cloneAuthConfig: cloneAuthConfig,
		pullSecret:      pullSecret,
		architectures:   sets.New[string](),
		metricsAgent:    metricsAgent,
		buildType:       buildType,
	}
}

func getSourceSecretFromName(secretName string) *corev1.LocalObjectReference {
	if len(secretName) == 0 {
		return nil
	}
	return &corev1.LocalObjectReference{Name: secretName}
}

func addLabelsToBuild(refs *prowv1.Refs, build *buildapi.Build, contextDir string) {
	labels := make(map[string]string)
	// reset all labels that may be set by a lower level
	for _, key := range []string{
		"vcs-type",
		"vcs-ref",
		"vcs-url",
		"io.openshift.build.name",
		"io.openshift.build.namespace",
		"io.openshift.build.commit.id",
		"io.openshift.build.commit.ref",
		"io.openshift.build.commit.message",
		"io.openshift.build.commit.author",
		"io.openshift.build.commit.date",
		"io.openshift.build.source-location",
		"io.openshift.build.source-context-dir",
	} {
		labels[key] = ""
	}
	if refs != nil {
		if len(refs.Pulls) == 0 {
			labels["vcs-type"] = "git"
			labels["vcs-ref"] = refs.BaseSHA
			labels["io.openshift.build.commit.id"] = refs.BaseSHA
			labels["io.openshift.build.commit.ref"] = refs.BaseRef
			labels["vcs-url"] = fmt.Sprintf("https://github.com/%s/%s", refs.Org, refs.Repo)
			labels["io.openshift.build.source-location"] = labels["vcs-url"]
			labels["io.openshift.build.source-context-dir"] = contextDir
		}
		// TODO: we should consider setting enough info for a caller to reconstruct pulls to support
		// oc adm release info tooling
	}

	for k, v := range labels {
		build.Spec.Output.ImageLabels = append(build.Spec.Output.ImageLabels, buildapi.ImageLabel{
			Name:  k,
			Value: v,
		})
	}
	sort.Slice(build.Spec.Output.ImageLabels, func(i, j int) bool {
		return build.Spec.Output.ImageLabels[i].Name < build.Spec.Output.ImageLabels[j].Name
	})
}

func openshiftBuild(ctx context.Context, buildClient BuildClient, podClient kubernetes.PodClient, build buildapi.Build, errs *[]error) (bool, error) {
	var attempt buildapi.Build
	ns, name := build.Namespace, build.Name
	build.DeepCopyInto(&attempt)
	if err := buildClient.Create(ctx, &attempt); err == nil {
		logrus.Infof("Created build %q", name)
	} else if kerrors.IsAlreadyExists(err) {
		logrus.Infof("Found existing build %q", name)
	} else {
		return false, fmt.Errorf("could not create build %s: %w", name, err)
	}

	buildClient.MetricsAgent().AddNodeWorkload(ctx, ns, fmt.Sprintf("%s-build", name), name, podClient)
	buildClient.MetricsAgent().StoreMachinesSnapshotForBuildPod(ctx, ns, fmt.Sprintf("%s-build", name), podClient)
	if err := waitForBuildOrTimeout(ctx, buildClient, podClient, ns, name); err != nil {
		*errs = append(*errs, err)
		return false, handleFailedBuild(ctx, buildClient, ns, name, err)
	}
	if err := gatherSuccessfulBuildLog(buildClient, ns, name); err != nil {
		// log error but do not fail successful build
		logrus.WithError(err).Warnf("Failed gathering successful build %s logs into artifacts.", name)
	}
	return true, nil
}

func awsBuild(ctx context.Context, buildClient BuildClient, build buildapi.Build, errs *[]error) (bool, error) {
	projectName := fmt.Sprintf("%s-%s", build.Namespace, build.Name)
	sdkConfig, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		*errs = append(*errs, err)
		return false, fmt.Errorf("could not load aws credentials: %w", err)
	}

	cbClient := codebuild.NewFromConfig(sdkConfig)
	projects, err := cbClient.BatchGetProjects(ctx, &codebuild.BatchGetProjectsInput{Names: []string{projectName}})
	if err != nil {
		*errs = append(*errs, err)
		return false, fmt.Errorf("could not get project %s: %w", projectName, err)
	}

	projectExists := len(projects.Projects) > 0
	if !projectExists {
		is := &imagev1.ImageStreamTag{}
		credentials, err := awsBuildAssemblyCredentials(ctx, buildClient, build, is, errs)
		if err != nil {
			return false, err
		}
		buildSpec, err := awsBuildSpec(build, string(credentials), is.Image.DockerImageReference, buildClient.LocalRegistryDNS())
		if err != nil {
			return false, err
		}

		err = awsBuildCreateCloudBuildProject(ctx, build, cbClient, buildSpec, projectName, errs)
		if err != nil {
			return false, err
		}
	}

	sbi := &codebuild.StartBuildInput{
		ProjectName: &projectName,
	}

	awsBuildResult, err := cbClient.StartBuild(ctx, sbi)
	if err != nil {
		*errs = append(*errs, err)
		return false, fmt.Errorf("could not start build at codebuild project %s: %w", projectName, err)
	}

	err = awsBuildWaitForIt(ctx, sdkConfig, cbClient, awsBuildResult, build.Name)
	if err != nil {
		*errs = append(*errs, err)
		return false, fmt.Errorf("could not wait/pull logs for build %s: %w", build.Namespace, err)
	}

	return true, nil
}

func awsBuildSpec(build buildapi.Build, credentials string, imageStreamReference, localRegistryDNS string) (buildspec.BuildSpec, error) {
	commands := []string{
		`mkdir -p ~/.docker`,
		`echo "$credentials" > ~/.docker/config.json`,
		`echo "$dockerfile" > Dockerfile`,
	}
	for i, image := range build.Spec.CommonSpec.Source.Images {
		commands = append(commands, fmt.Sprintf(`docker create --name c%d %s`, i, util.ShellEscape(image.From.Name)))
		for _, path := range image.Paths {
			commands = append(commands, fmt.Sprintf(`docker cp c%d:%s %s`, i, util.ShellEscape(path.SourcePath), util.ShellEscape(path.DestinationDir)))
		}
		commands = append(commands, fmt.Sprintf(`docker rm c%d`, i))
	}

	buildEnv := []string{}
	buildArgs := []string{}
	for _, env := range build.Spec.CommonSpec.Strategy.DockerStrategy.Env {
		buildEnv = append(buildEnv, fmt.Sprintf(`--build-arg %s='%s'`, env.Name, util.ShellEscape(env.Value)))
		buildArgs = append(buildArgs, fmt.Sprintf(`ARG %s`, env.Name))
	}

	re := regexp.MustCompile(`image-registry\.openshift-image-registry\.svc[.:a-z0-9]*`)
	imageStreamReference = re.ReplaceAllString(imageStreamReference, localRegistryDNS)
	commands = append(commands, fmt.Sprintf(`docker buildx build %s --platform linux/%s --tag %s/%s/%s --output type=registry .`, strings.Join(buildEnv, " "), build.Spec.NodeSelector["kubernetes.io/arch"], localRegistryDNS, build.Namespace, build.Spec.Output.To.Name))
	if build.Spec.CommonSpec.Source.Dockerfile == nil {
		return buildspec.BuildSpec{}, fmt.Errorf("no Dockerfile defined on build.spec")
	}
	dockerFile := strings.ReplaceAll(*build.Spec.CommonSpec.Source.Dockerfile, build.Spec.CommonSpec.Strategy.DockerStrategy.From.Name, fmt.Sprintf("%s\n%s", imageStreamReference, strings.Join(buildArgs, "\n")))

	return buildspec.BuildSpec{
		Env: buildspec.Env{
			Variables: buildspec.Variables{
				DockerFile:  dockerFile,
				Credentials: credentials,
			},
		},
		Phases: buildspec.Phases{
			Build: buildspec.BuildPhase{
				OnFailure: "ABORT",
				Commands:  commands,
			},
		},
		Version: "0.2",
	}, nil
}

func awsBuildAssemblyCredentials(ctx context.Context, buildClient BuildClient, build buildapi.Build, is *imagev1.ImageStreamTag, errs *[]error) ([]byte, error) {
	if build.Spec.Strategy.DockerStrategy == nil || build.Spec.Strategy.DockerStrategy.PullSecret == nil {
		err := fmt.Errorf("build %s has no pull secret configured", build.Name)
		*errs = append(*errs, err)
		return nil, err
	}
	secretName := build.Spec.Strategy.DockerStrategy.PullSecret.Name
	secret := &corev1.Secret{}
	err := buildClient.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: build.Namespace, Name: secretName}, secret)
	if err != nil {
		*errs = append(*errs, err)
		if kerrors.IsNotFound(err) {
			return nil, fmt.Errorf("could not get secret %s: %w", secretName, err)
		}
		return nil, fmt.Errorf("could not get secret %s: %w", secretName, err)
	}

	isName := build.Spec.Strategy.DockerStrategy.From.Name
	err = buildClient.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: build.Namespace, Name: isName}, is)
	if err != nil {
		*errs = append(*errs, err)
		if kerrors.IsNotFound(err) {
			return nil, fmt.Errorf("could not get imagestream %s: %w", isName, err)
		}
		return nil, fmt.Errorf("could not get imagestream %s: %w", isName, err)
	}

	dockerConfigJSON, found := secret.Data[corev1.DockerConfigJsonKey]
	if !found {
		*errs = append(*errs, fmt.Errorf("key %s not found on secret %s", corev1.DockerConfigJsonKey, secret.Name))
		return nil, fmt.Errorf("key %s not found on secret %s", corev1.DockerConfigJsonKey, secret.Name)
	}

	manifestToolDockerCfg := buildClient.ManifestToolDockerCfg()
	pushCredentialsData, err := os.ReadFile(manifestToolDockerCfg)
	if err != nil {
		*errs = append(*errs, err)
		return nil, fmt.Errorf("could not read secret %s: %w", manifestToolDockerCfg, err)
	}
	pushCredentials := credentialprovider.DockerConfigJSON{}
	err = json.Unmarshal(pushCredentialsData, &pushCredentials)
	if err != nil {
		*errs = append(*errs, err)
		return nil, fmt.Errorf("could not unmarshal secret %s: %w", manifestToolDockerCfg, err)
	}

	registryCredentials := credentialprovider.DockerConfigJSON{}
	err = json.Unmarshal(dockerConfigJSON, &registryCredentials)
	if err != nil {
		*errs = append(*errs, err)
		return nil, fmt.Errorf("could not unmarshal secret %s: %w", secret.Name, err)
	}
	if registryCredentials.Auths == nil {
		registryCredentials.Auths = map[string]credentialprovider.DockerConfigEntry{}
	}
	localRegistryDNS := buildClient.LocalRegistryDNS()
	localRegistryAuth, exists := pushCredentials.Auths[localRegistryDNS]
	if !exists {
		err := fmt.Errorf("local registry auth %s not found", localRegistryDNS)
		*errs = append(*errs, err)
		return nil, err
	}
	registryCredentials.Auths[localRegistryDNS] = localRegistryAuth
	marshalledRegistryCredentials, err := json.Marshal(registryCredentials)
	if err != nil {
		*errs = append(*errs, err)
		return nil, fmt.Errorf("could not marshal manifesttooldockercfg combined with registry-pull-credentials: %w", err)
	}
	return marshalledRegistryCredentials, nil
}

func awsBuildCreateCloudBuildProject(ctx context.Context, build buildapi.Build, cbClient *codebuild.Client, buildSpec buildspec.BuildSpec, projectName string, errs *[]error) error {
	awsBuildImage := "aws/codebuild/amazonlinux2-x86_64-standard:6.0"
	awsEnvironmentTypeContainer := codebuildtypes.EnvironmentTypeLinuxContainer
	if build.Spec.NodeSelector["kubernetes.io/arch"] == "arm64" {
		awsBuildImage = "aws/codebuild/amazonlinux2-aarch64-standard:3.0"
		awsEnvironmentTypeContainer = codebuildtypes.EnvironmentTypeArmContainer
	}
	const ServiceRole string = "codebuild-ci-operator"
	marshaledBuildSpec, err := yaml.Marshal(buildSpec)
	if err != nil {
		*errs = append(*errs, err)
		return fmt.Errorf("could not marshal codebuild buildspec: %w", err)
	}
	logrus.Infof("Creating %s project", projectName)
	_, err = cbClient.CreateProject(ctx, &codebuild.CreateProjectInput{
		Name:             &projectName,
		TimeoutInMinutes: ptr.To(int32(60 * 8)),
		Artifacts: &codebuildtypes.ProjectArtifacts{
			Type: codebuildtypes.ArtifactsTypeNoArtifacts,
		},
		Environment: &codebuildtypes.ProjectEnvironment{
			Type:        awsEnvironmentTypeContainer,
			Image:       &awsBuildImage,
			ComputeType: codebuildtypes.ComputeTypeBuildGeneral1Medium,
		},
		Source: &codebuildtypes.ProjectSource{
			Type:      codebuildtypes.SourceTypeNoSource,
			Buildspec: ptr.To(string(marshaledBuildSpec)),
		},
		ServiceRole: ptr.To(ServiceRole),
	})
	if err != nil {
		*errs = append(*errs, err)
		return fmt.Errorf("could not create codebuild project %s: %w", projectName, err)
	}
	return nil
}

func awsBuildWaitForIt(ctx context.Context, sdkConfig aws.Config, cbClient *codebuild.Client, buildOutput *codebuild.StartBuildOutput, imageName string) error {
	artifactDir, set := api.Artifacts()
	if !set {
		return fmt.Errorf("no artifacts directory configured")
	}
	dir := filepath.Join(artifactDir, "build-logs")
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("could not create artifacts/build-logs dir: %w", err)
	}
	logPath := filepath.Join(dir, fmt.Sprintf("%s.log", imageName))
	log, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("failed to create %s: %w", logPath, err)
	}
	defer log.Close()
	buildId := aws.ToString(buildOutput.Build.Id)
	logrus.Infof("Waiting for build %s to complete", buildId)
	for {
		// avoid maximum number of attempts
		time.Sleep(10 * time.Second)

		if ctx.Err() != nil {
			return fmt.Errorf("build polling canceled: %w", ctx.Err())
		}

		buildInputs := codebuild.BatchGetBuildsInput{Ids: []string{buildId}}
		builds, err := cbClient.BatchGetBuilds(ctx, &buildInputs)
		if err != nil {
			return fmt.Errorf("could not get build %s: %w", buildId, err)
		}

		if len(builds.Builds) == 0 {
			return fmt.Errorf("no builds found with id %s", buildId)
		}

		build := builds.Builds[0]
		status := build.BuildStatus
		if status == codebuildtypes.StatusTypeSucceeded {
			return awsBuildGatherLogs(ctx, sdkConfig, build, log)
		} else if status == codebuildtypes.StatusTypeFailed || status == codebuildtypes.StatusTypeStopped {
			if err := awsBuildGatherLogs(ctx, sdkConfig, build, log); err != nil {
				return err
			}
			logFile, err := os.Open(logPath)
			defer logFile.Close()
			if err != nil {
				return fmt.Errorf("could not open %s: %w", logPath, err)
			}
			if _, err := io.Copy(os.Stdout, logFile); err != nil {
				logrus.WithError(err).Warn("Unable to copy log output from failed aws codebuild.")
			}
			return fmt.Errorf("build %s failed or was stopped with status: %s", buildId, status)
		}
	}
}

func awsBuildGatherLogs(ctx context.Context, sdkConfig aws.Config, build codebuildtypes.Build, log *os.File) error {
	buildId := aws.ToString(build.Id)
	if build.Logs == nil || build.Logs.GroupName == nil || build.Logs.StreamName == nil {
		return fmt.Errorf("cloudwatch log location is missing for build %s", buildId)
	}
	cloudwatch := cloudwatchlogs.NewFromConfig(sdkConfig)
	input := &cloudwatchlogs.GetLogEventsInput{
		LogGroupName:  build.Logs.GroupName,
		LogStreamName: build.Logs.StreamName,
		StartFromHead: &[]bool{true}[0],
	}
	paginator := cloudwatchlogs.NewGetLogEventsPaginator(cloudwatch, input)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("could not read logs for build %s: %w", buildId, err)
		}
		if len(page.Events) == 0 {
			break
		}
		for _, event := range page.Events {
			if event.Timestamp == nil || event.Message == nil {
				continue
			}
			timestamp := time.Unix(*event.Timestamp/1000, 0)
			_, err := log.Write([]byte(fmt.Sprintf("[%s] %s", timestamp.Format(time.RFC3339), *event.Message)))
			if err != nil {
				return fmt.Errorf("could not write log for %s on %s: %w", buildId, log.Name(), err)
			}
		}
	}
	return nil
}
