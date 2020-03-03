package steps

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"k8s.io/test-infra/prow/clonerefs"
	"k8s.io/test-infra/prow/pod-utils/decorate"

	coreapi "k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"

	buildapi "github.com/openshift/api/build/v1"
	imageclientset "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
)

const (
	CiAnnotationPrefix = "ci.openshift.io"
	JobLabel           = "job"
	BuildIdLabel       = "build-id"
	CreatesLabel       = "creates"
	CreatedByCILabel   = "created-by-ci"

	ProwJobIdLabel = "prow.k8s.io/id"

	gopath        = "/go"
	sshPrivateKey = "/sshprivatekey"
	sshConfig     = "/ssh_config"
	oauthToken    = "/oauth-token"

	OauthSecretKey = "oauth-token"

	PullSecretName = "regcred"
)

type CloneAuthType string

var (
	CloneAuthTypeSSH   CloneAuthType = "SSH"
	CloneAuthTypeOAuth CloneAuthType = "OAuth"
)

type CloneAuthConfig struct {
	Secret *coreapi.Secret
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
	dockerCommands = append(dockerCommands, "ADD ./app.binary /clonerefs")

	if cloneAuthConfig != nil {
		switch cloneAuthConfig.Type {
		case CloneAuthTypeSSH:
			dockerCommands = append(dockerCommands, fmt.Sprintf("ADD %s /etc/ssh/ssh_config", sshConfig))
			dockerCommands = append(dockerCommands, fmt.Sprintf("COPY ./%s %s", coreapi.SSHAuthPrivateKey, sshPrivateKey))
			secretPath = sshPrivateKey
		case CloneAuthTypeOAuth:
			dockerCommands = append(dockerCommands, fmt.Sprintf("COPY ./%s %s", OauthSecretKey, oauthToken))
			secretPath = oauthToken
		}
	}

	dockerCommands = append(dockerCommands, fmt.Sprintf("RUN umask 0002 && /clonerefs && find %s/src -type d -not -perm -0775 | xargs -r chmod g+xw", gopath))
	dockerCommands = append(dockerCommands, fmt.Sprintf("WORKDIR %s/", workingDir))
	dockerCommands = append(dockerCommands, fmt.Sprintf("ENV GOPATH=%s", gopath))
	dockerCommands = append(dockerCommands, "RUN git submodule update --init")

	// After the clonerefs command, we don't need the secret anymore.
	// We don't want to let the key keep existing in the image's layer.
	if len(secretPath) > 0 {
		dockerCommands = append(dockerCommands, fmt.Sprintf("RUN rm -f %s", secretPath))
	}

	dockerCommands = append(dockerCommands, "")

	return strings.Join(dockerCommands, "\n")
}

func defaultPodLabels(jobSpec *api.JobSpec) map[string]string {
	if refs := jobSpec.JobSpec.Refs; refs != nil {
		return trimLabels(map[string]string{
			JobLabel:         jobSpec.Job,
			BuildIdLabel:     jobSpec.BuildID,
			ProwJobIdLabel:   jobSpec.ProwJobID,
			CreatedByCILabel: "true",
			RefsOrgLabel:     refs.Org,
			RefsRepoLabel:    refs.Repo,
			RefsBranchLabel:  refs.BaseRef,
		})
	}

	if extraRefs := jobSpec.JobSpec.ExtraRefs; len(extraRefs) > 0 {
		return trimLabels(map[string]string{
			JobLabel:         jobSpec.Job,
			BuildIdLabel:     jobSpec.BuildID,
			ProwJobIdLabel:   jobSpec.ProwJobID,
			CreatedByCILabel: "true",
			RefsOrgLabel:     extraRefs[0].Org,
			RefsRepoLabel:    extraRefs[0].Repo,
			RefsBranchLabel:  extraRefs[0].BaseRef,
		})
	}

	return trimLabels(map[string]string{
		JobLabel:         jobSpec.Job,
		BuildIdLabel:     jobSpec.BuildID,
		ProwJobIdLabel:   jobSpec.ProwJobID,
		CreatedByCILabel: "true",
	})
}

type sourceStep struct {
	config             api.SourceStepConfiguration
	resources          api.ResourceConfiguration
	buildClient        BuildClient
	imageClient        imageclientset.ImageV1Interface
	clonerefsSrcClient imageclientset.ImageV1Interface
	artifactDir        string
	jobSpec            *api.JobSpec
	dryLogger          *DryLogger
	cloneAuthConfig    *CloneAuthConfig
	pullSecret         *coreapi.Secret
}

func (s *sourceStep) Inputs(dry bool) (api.InputDefinition, error) {
	return s.jobSpec.Inputs(), nil
}

func (s *sourceStep) Run(ctx context.Context, dry bool) error {
	clonerefsRef, err := istObjectReference(s.clonerefsSrcClient, s.config.ClonerefsImage)
	if err != nil {
		return fmt.Errorf("could not resolve clonerefs source: %v", err)
	}

	return handleBuild(ctx, s.buildClient, createBuild(s.config, s.jobSpec, clonerefsRef, s.resources, s.cloneAuthConfig, s.pullSecret), dry, s.artifactDir, s.dryLogger)
}

func createBuild(config api.SourceStepConfiguration, jobSpec *api.JobSpec, clonerefsRef coreapi.ObjectReference, resources api.ResourceConfiguration, cloneAuthConfig *CloneAuthConfig, pullSecret *coreapi.Secret) *buildapi.Build {
	var refs []prowapi.Refs
	if jobSpec.Refs != nil {
		r := *jobSpec.Refs
		if cloneAuthConfig != nil {
			r.CloneURI = cloneAuthConfig.getCloneURI(r.Org, r.Repo)
		}
		refs = append(refs, r)
	}

	for _, r := range jobSpec.ExtraRefs {
		if cloneAuthConfig != nil {
			r.CloneURI = cloneAuthConfig.getCloneURI(r.Org, r.Repo)
		}
		refs = append(refs, r)
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

	optionsJSON, err := clonerefs.Encode(optionsSpec)
	if err != nil {
		panic(fmt.Errorf("couldn't create JSON spec for clonerefs: %v", err))
	}

	build := buildFromSource(jobSpec, config.From, config.To, buildSource, "", resources, pullSecret)
	build.Spec.CommonSpec.Strategy.DockerStrategy.Env = append(
		build.Spec.CommonSpec.Strategy.DockerStrategy.Env,
		coreapi.EnvVar{Name: clonerefs.JSONConfigEnvVar, Value: optionsJSON},
	)

	return build
}

func buildFromSource(jobSpec *api.JobSpec, fromTag, toTag api.PipelineImageStreamTagReference, source buildapi.BuildSource, dockerfilePath string, resources api.ResourceConfiguration, pullSecret *coreapi.Secret) *buildapi.Build {
	log.Printf("Building %s", toTag)
	buildResources, err := resourcesFor(resources.RequirementsForStep(string(toTag)))
	if err != nil {
		panic(fmt.Errorf("unable to parse resource requirement for build %s: %v", toTag, err))
	}
	var from *coreapi.ObjectReference
	if len(fromTag) > 0 {
		from = &coreapi.ObjectReference{
			Kind:      "ImageStreamTag",
			Namespace: jobSpec.Namespace,
			Name:      fmt.Sprintf("%s:%s", api.PipelineImageStream, fromTag),
		}
	}

	layer := buildapi.ImageOptimizationSkipLayers
	labels := defaultPodLabels(jobSpec)
	labels[CreatesLabel] = string(toTag)
	build := &buildapi.Build{
		ObjectMeta: meta.ObjectMeta{
			Name:      string(toTag),
			Namespace: jobSpec.Namespace,
			Labels:    labels,
			Annotations: map[string]string{
				JobSpecAnnotation: jobSpec.RawSpec(),
			},
		},
		Spec: buildapi.BuildSpec{
			CommonSpec: buildapi.CommonSpec{
				Resources:      buildResources,
				ServiceAccount: "builder", // TODO: remove when build cluster has https://github.com/openshift/origin/pull/17668
				Source:         source,
				Strategy: buildapi.BuildStrategy{
					Type: buildapi.DockerBuildStrategyType,
					DockerStrategy: &buildapi.DockerBuildStrategy{
						DockerfilePath:          dockerfilePath,
						From:                    from,
						ForcePull:               true,
						NoCache:                 true,
						Env:                     []coreapi.EnvVar{{Name: "foo", Value: "bar"}}, //workaround https://bugzilla.redhat.com/show_bug.cgi?id=1784163#c8
						ImageOptimizationPolicy: &layer,
					},
				},
				Output: buildapi.BuildOutput{
					To: &coreapi.ObjectReference{
						Kind:      "ImageStreamTag",
						Namespace: jobSpec.Namespace,
						Name:      fmt.Sprintf("%s:%s", api.PipelineImageStream, toTag),
					},
				},
			},
		},
	}
	if pullSecret != nil {
		build.Spec.Strategy.DockerStrategy.PullSecret = getSourceSecretFromName(PullSecretName)
	}
	if owner := jobSpec.Owner(); owner != nil {
		build.OwnerReferences = append(build.OwnerReferences, *owner)
	}

	return build
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
			From: coreapi.ObjectReference{
				Kind: "ImageStreamTag",
				Name: fmt.Sprintf("%s:%s", api.PipelineImageStream, name),
			},
			As:    value.As,
			Paths: paths,
		})
	}
	return refs
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

func handleBuild(ctx context.Context, buildClient BuildClient, build *buildapi.Build, dry bool, artifactDir string, dryLogger *DryLogger) error {
	if dry {
		dryLogger.AddBuild(build)
		return nil
	}

	if _, err := buildClient.Builds(build.Namespace).Create(build); err != nil {
		if !errors.IsAlreadyExists(err) {
			return fmt.Errorf("could not create build %s: %v", build.Name, err)
		}
		b, err := buildClient.Builds(build.Namespace).Get(build.Name, meta.GetOptions{})
		if err != nil {
			return fmt.Errorf("could not get build %s: %v", build.Name, err)
		}

		if isBuildPhaseTerminated(b.Status.Phase) &&
			(isInfraReason(b.Status.Reason) || hintsAtInfraReason(b.Status.LogSnippet)) {
			log.Printf("Build %s previously failed from an infrastructure error (%s), retrying...\n", b.Name, b.Status.Reason)
			zero := int64(0)
			foreground := meta.DeletePropagationForeground
			opts := &meta.DeleteOptions{
				GracePeriodSeconds: &zero,
				Preconditions:      &meta.Preconditions{UID: &b.UID},
				PropagationPolicy:  &foreground,
			}
			if err := buildClient.Builds(build.Namespace).Delete(build.Name, opts); err != nil && !errors.IsNotFound(err) && !errors.IsConflict(err) {
				return fmt.Errorf("could not delete build %s: %v", build.Name, err)
			}
			if err := waitForBuildDeletion(ctx, buildClient, build.Namespace, build.Name); err != nil {
				return fmt.Errorf("could not wait for build %s to be deleted: %v", build.Name, err)
			}
			if _, err := buildClient.Builds(build.Namespace).Create(build); err != nil && !errors.IsAlreadyExists(err) {
				return fmt.Errorf("could not recreate build %s: %v", build.Name, err)
			}
		}
	}
	err := waitForBuild(ctx, buildClient, build.Namespace, build.Name)
	if err == nil && len(artifactDir) > 0 {
		if err := gatherSuccessfulBuildLog(buildClient, artifactDir, build.Namespace, build.Name); err != nil {
			// log error but do not fail successful build
			log.Printf("problem gathering successful build %s logs into artifacts: %v", build.Name, err)
		}
	}
	// this will still be the err from waitForBuild
	return err

}

func waitForBuildDeletion(ctx context.Context, client BuildClient, ns, name string) error {
	ch := make(chan error)
	go func() {
		ch <- wait.ExponentialBackoff(wait.Backoff{
			Duration: 10 * time.Millisecond, Factor: 2, Steps: 10,
		}, func() (done bool, err error) {
			if _, err := client.Builds(ns).Get(name, meta.GetOptions{}); err != nil {
				if errors.IsNotFound(err) {
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
		buildapi.StatusReasonCannotCreateBuildPod,
		buildapi.StatusReasonBuildPodDeleted,
		buildapi.StatusReasonExceededRetryTimeout,
		buildapi.StatusReasonPushImageToRegistryFailed,
		buildapi.StatusReasonPullBuilderImageFailed,
		buildapi.StatusReasonFetchSourceFailed,
		buildapi.StatusReasonBuildPodExists,
		buildapi.StatusReasonNoBuildContainerStatus,
		buildapi.StatusReasonFailedContainer,
		buildapi.StatusReasonOutOfMemoryKilled,
		buildapi.StatusReasonCannotRetrieveServiceAccount,
		buildapi.StatusReason("BuildPodEvicted"), // vendoring to get this is so hard
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
		strings.Contains(logSnippet, "Error: Failed to synchronize cache for repo")
}

func waitForBuild(ctx context.Context, buildClient BuildClient, namespace, name string) error {
	for {
		retry, err := waitForBuildOrTimeout(ctx, buildClient, namespace, name)
		if err != nil {
			return fmt.Errorf("could not wait for build: %v", err)
		}
		if !retry {
			break
		}
	}
	return nil
}

func waitForBuildOrTimeout(ctx context.Context, buildClient BuildClient, namespace, name string) (bool, error) {
	isOK := func(b *buildapi.Build) bool {
		return b.Status.Phase == buildapi.BuildPhaseComplete
	}
	isFailed := func(b *buildapi.Build) bool {
		return b.Status.Phase == buildapi.BuildPhaseFailed ||
			b.Status.Phase == buildapi.BuildPhaseCancelled ||
			b.Status.Phase == buildapi.BuildPhaseError
	}

	// First we set up a watcher to catch all events that happen while we check
	// the build status
	watcher, err := buildClient.Builds(namespace).Watch(meta.ListOptions{
		FieldSelector: fields.Set{"metadata.name": name}.AsSelector().String(),
		Watch:         true,
	})
	if err != nil {
		return false, fmt.Errorf("could not create watcher for build %s: %v", name, err)
	}
	defer watcher.Stop()

	list, err := buildClient.Builds(namespace).List(meta.ListOptions{FieldSelector: fields.Set{"metadata.name": name}.AsSelector().String()})
	if err != nil {
		return false, fmt.Errorf("could not list builds: %v", err)
	}
	if len(list.Items) != 1 {
		return false, fmt.Errorf("could not find build %s", name)
	}
	build := &list.Items[0]
	if isOK(build) {
		log.Printf("Build %s already succeeded in %s", build.Name, buildDuration(build))
		return false, nil
	}
	if isFailed(build) {
		log.Printf("Build %s failed, printing logs:", build.Name)
		printBuildLogs(buildClient, build.Namespace, build.Name)
		return false, appendLogToError(fmt.Errorf("the build %s failed with reason %s: %s", build.Name, build.Status.Reason, build.Status.Message), build.Status.LogSnippet)
	}
	done := ctx.Done()
	ch := watcher.ResultChan()
	for {
		var event watch.Event
		var ok bool
		select {
		case <-done:
			return false, ctx.Err()
		case event, ok = <-ch:
		}
		if !ok {
			// restart
			return true, nil
		}
		build, ok := event.Object.(*buildapi.Build)
		if !ok {
			continue
		}

		if isOK(build) {
			log.Printf("Build %s succeeded after %s", build.Name, buildDuration(build).Truncate(time.Second))
			return false, nil
		}
		if isFailed(build) {
			log.Printf("Build %s failed, printing logs:", build.Name)
			printBuildLogs(buildClient, build.Namespace, build.Name)
			// BUG: builds report Failed before they set log snippet
			build = waitForBuildWithSnippet(build, ch)
			return false, appendLogToError(fmt.Errorf("the build %s failed after %s with reason %s: %s", build.Name, buildDuration(build).Truncate(time.Second), build.Status.Reason, build.Status.Message), build.Status.LogSnippet)
		}
	}
}

func waitForBuildWithSnippet(build *buildapi.Build, ch <-chan watch.Event) *buildapi.Build {
	timeout := time.After(10 * time.Second)
	for len(build.Status.LogSnippet) == 0 {
		select {
		case <-timeout:
			return build
		case event, ok := <-ch:
			if !ok {
				return build
			}
			nextBuild, ok := event.Object.(*buildapi.Build)
			if !ok {
				continue
			}
			if nextBuild.Status.Phase != build.Status.Phase {
				return build
			}
			build = nextBuild
		}
	}
	return build
}

func appendLogToError(err error, log string) error {
	log = strings.TrimSpace(log)
	if len(log) == 0 {
		return err
	}
	return fmt.Errorf("%s\n\n%s", err.Error(), log)
}

func buildDuration(build *buildapi.Build) time.Duration {
	start := build.Status.StartTimestamp
	if start == nil {
		start = &build.CreationTimestamp
	}
	end := build.Status.CompletionTimestamp
	if end == nil {
		end = &meta.Time{Time: time.Now()}
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
			log.Printf("error: Unable to copy log output from failed build: %v", err)
		}
	} else {
		log.Printf("error: Unable to retrieve logs from failed build: %v", err)
	}
}

func resourcesFor(req api.ResourceRequirements) (coreapi.ResourceRequirements, error) {
	apireq := coreapi.ResourceRequirements{}
	for name, value := range req.Requests {
		q, err := resource.ParseQuantity(value)
		if err != nil {
			return coreapi.ResourceRequirements{}, fmt.Errorf("invalid resource request: %v", err)
		}
		if apireq.Requests == nil {
			apireq.Requests = make(coreapi.ResourceList)
		}
		apireq.Requests[coreapi.ResourceName(name)] = q
	}
	for name, value := range req.Limits {
		q, err := resource.ParseQuantity(value)
		if err != nil {
			return coreapi.ResourceRequirements{}, fmt.Errorf("invalid resource limit: %v", err)
		}
		if apireq.Limits == nil {
			apireq.Limits = make(coreapi.ResourceList)
		}
		apireq.Limits[coreapi.ResourceName(name)] = q
	}
	return apireq, nil
}

func imageStreamTagExists(reference api.PipelineImageStreamTagReference, istClient imageclientset.ImageStreamTagInterface) (bool, error) {
	log.Printf("Checking for existence of %s:%s", api.PipelineImageStream, reference)
	_, err := istClient.Get(
		fmt.Sprintf("%s:%s", api.PipelineImageStream, reference),
		meta.GetOptions{},
	)
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		} else {
			return false, fmt.Errorf("could not get output imagestreamtag: %v", err)
		}
	} else {
		return true, nil
	}
}

func (s *sourceStep) Requires() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(s.config.From)}
}

func (s *sourceStep) Creates() []api.StepLink {
	return []api.StepLink{api.InternalImageLink(s.config.To)}
}

func (s *sourceStep) Provides() (api.ParameterMap, api.StepLink) {
	return api.ParameterMap{
		"LOCAL_IMAGE_SRC": func() (string, error) {
			is, err := s.imageClient.ImageStreams(s.jobSpec.Namespace).Get(api.PipelineImageStream, meta.GetOptions{})
			if err != nil {
				return "", fmt.Errorf("could not get output imagestream: %v", err)
			}
			var registry string
			if len(is.Status.PublicDockerImageRepository) > 0 {
				registry = is.Status.PublicDockerImageRepository
			} else if len(is.Status.DockerImageRepository) > 0 {
				registry = is.Status.DockerImageRepository
			} else {
				return "", fmt.Errorf("image stream %s has no accessible image registry value", s.config.To)
			}
			return fmt.Sprintf("%s:%s", registry, s.config.To), nil
		},
	}, api.InternalImageLink("src")
}

func (s *sourceStep) Name() string { return string(s.config.To) }

func (s *sourceStep) Description() string {
	return fmt.Sprintf("Clone the correct source code into an image and tag it as %s", s.config.To)
}

func SourceStep(config api.SourceStepConfiguration, resources api.ResourceConfiguration, buildClient BuildClient,
	clonerefsSrcClient imageclientset.ImageV1Interface, imageClient imageclientset.ImageV1Interface,
	artifactDir string, jobSpec *api.JobSpec, dryLogger *DryLogger, cloneAuthConfig *CloneAuthConfig, pullSecret *coreapi.Secret) api.Step {
	return &sourceStep{
		config:             config,
		resources:          resources,
		buildClient:        buildClient,
		imageClient:        imageClient,
		clonerefsSrcClient: clonerefsSrcClient,
		artifactDir:        artifactDir,
		jobSpec:            jobSpec,
		dryLogger:          dryLogger,
		cloneAuthConfig:    cloneAuthConfig,
		pullSecret:         pullSecret,
	}
}

// trimLabels ensures that all label values are less than 64 characters
// in length and thus valid.
func trimLabels(labels map[string]string) map[string]string {
	for k, v := range labels {
		if len(v) > 63 {
			labels[k] = v[:60] + "XXX"
		}
	}
	return labels
}

func getSourceSecretFromName(secretName string) *coreapi.LocalObjectReference {
	if len(secretName) == 0 {
		return nil
	}
	return &coreapi.LocalObjectReference{Name: secretName}
}
