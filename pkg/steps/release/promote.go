package release

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/kubernetes"
	"github.com/openshift/ci-tools/pkg/kubernetes/pkg/credentialprovider"
	"github.com/openshift/ci-tools/pkg/release/prerelease"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps"
)

// promotionStep will tag a full release suite
// of images out to the configured namespace.
type promotionStep struct {
	name              string
	configuration     *api.ReleaseBuildConfiguration
	requiredImages    sets.Set[string]
	jobSpec           *api.JobSpec
	client            kubernetes.PodClient
	pushSecret        *coreapi.Secret
	registry          string
	mirrorFunc        func(source, target string, tag api.ImageStreamTagReference, date string, imageMirror map[string]string)
	targetNameFunc    func(string, api.PromotionTarget) string
	nodeArchitectures []string
}

func (s *promotionStep) Inputs() (api.InputDefinition, error) {
	return nil, nil
}

func (*promotionStep) Validate() error { return nil }

func (s *promotionStep) Run(ctx context.Context) error {
	return results.ForReason("promoting_images").ForError(s.run(ctx))
}

func mainRefs(refs *prowapi.Refs, extra []prowapi.Refs) *prowapi.Refs {
	if refs != nil {
		return refs
	}
	if len(extra) > 0 {
		return &extra[0]
	}
	return nil
}

func (s *promotionStep) run(ctx context.Context) error {
	opts := []PromotedTagsOption{
		WithRequiredImages(s.requiredImages),
	}
	logger := logrus.WithField("name", s.name)
	isQuayPromotion := s.name == api.PromotionQuayStepName

	if isQuayPromotion {
		logger.Info("Starting promotion-quay step")
		logger.Debugf("Promotion-quay configuration: registry=%s", s.registry)
	}

	if refs := mainRefs(s.jobSpec.Refs, s.jobSpec.ExtraRefs); refs != nil {
		opts = append(opts, WithCommitSha(refs.BaseSHA))
		if isQuayPromotion {
			logger.Debugf("Including commit SHA in promotion: %s", refs.BaseSHA)
		}
	}

	tags, names := PromotedTagsWithRequiredImages(s.configuration, opts...)
	if len(names) == 0 {
		logger.Info("Nothing to promote, skipping...")
		return nil
	}

	if isQuayPromotion {
		logger.Infof("Promotion-quay: Promoting %d components: %s", len(names), strings.Join(sets.List(names), ", "))
		logger.Debugf("Promotion-quay: Total tag mappings: %d", len(tags))
		for src, dsts := range tags {
			logger.Debugf("Promotion-quay: Component %s -> %d destination(s)", src, len(dsts))
			for _, dst := range dsts {
				logger.Debugf("Promotion-quay:   -> %s/%s:%s", dst.Namespace, dst.Name, dst.Tag)
			}
		}
	}

	logger.Infof("Promoting tags to %s: %s", s.targets(), strings.Join(sets.List(names), ", "))
	pipeline := &imagev1.ImageStream{}
	if err := s.client.Get(ctx, ctrlruntimeclient.ObjectKey{
		Namespace: s.jobSpec.Namespace(),
		Name:      api.PipelineImageStream,
	}, pipeline); err != nil {
		return fmt.Errorf("could not resolve pipeline imagestream: %w", err)
	}

	timeStr := time.Now().Format("20060102150405")
	if isQuayPromotion {
		logger.Debugf("Promotion-quay: Generating image mirror targets with timestamp: %s", timeStr)
	}
	imageMirrorTarget, namespaces := getImageMirrorTarget(tags, pipeline, s.registry, timeStr, s.mirrorFunc, s.targetNameFunc)
	if len(imageMirrorTarget) == 0 {
		logger.Info("Nothing to promote, skipping...")
		return nil
	}

	if isQuayPromotion {
		logger.Infof("Promotion-quay: Generated %d image mirror targets", len(imageMirrorTarget))
		quayTargets := 0
		quayProxyTargets := 0
		for k := range imageMirrorTarget {
			if strings.Contains(k, "quay.io/openshift/ci") {
				quayTargets++
			}
			if strings.Contains(k, "-quay:") {
				quayProxyTargets++
			}
		}
		logger.Debugf("Promotion-quay: Breakdown - quay.io targets: %d, quay-proxy targets: %d", quayTargets, quayProxyTargets)
	}

	// in some cases like when we are called by the ci-chat-bot we may need to create namespaces
	// in general, we do not expect to be able to do this, so we only do it best-effort
	if err := s.ensureNamespaces(ctx, namespaces); err != nil {
		logger.WithError(err).Warn("Failed to ensure namespaces to promote to in central registry.")
	}

	version, err := prerelease.Stable4LatestMajorMinor(&http.Client{})
	if err != nil {
		logrus.WithError(err).Warn("Failed to determine the sable release version, using 4.14 instead")
		version = "4.14"
	}

	pod := getPromotionPod(imageMirrorTarget, timeStr, s.jobSpec.Namespace(), s.name, version, s.nodeArchitectures)
	if isQuayPromotion {
		logger.Infof("Promotion-quay: Starting promotion pod: %s/%s", pod.Namespace, pod.Name)
		logger.Debugf("Promotion-quay: Pod will execute %d command(s)", len(pod.Spec.Containers[0].Args))
	}

	resultPod, err := steps.RunPod(ctx, s.client, pod, false)
	if err != nil {
		// Capture and log specific errors from promotion pod
		if isQuayPromotion {
			logger.Errorf("Promotion-quay: Pod execution failed: %v", err)
			// Try to extract pod logs to identify specific errors
			if resultPod != nil {
				s.logPromotionPodErrors(ctx, resultPod, logger)
			} else {
				// Pod might not have been created, try to get it
				getPod := &coreapi.Pod{}
				if getErr := s.client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: pod.Namespace, Name: pod.Name}, getPod); getErr == nil {
					s.logPromotionPodErrors(ctx, getPod, logger)
				}
			}
		}
		return fmt.Errorf("unable to run promotion pod: %w", err)
	}

	if isQuayPromotion {
		logger.Info("Promotion-quay: Pod completed successfully")
		// Log any warnings or errors from successful pod execution
		if resultPod != nil {
			s.logPromotionPodErrors(ctx, resultPod, logger)
		}
	}
	return nil
}

// logPromotionPodErrors extracts and logs specific errors from promotion pod logs
// This helps identify issues like "invalid image stream" errors in ci-operator debug logs
func (s *promotionStep) logPromotionPodErrors(ctx context.Context, pod *coreapi.Pod, logger *logrus.Entry) {
	if pod == nil {
		return
	}

	// Check pod status for errors
	if pod.Status.Phase == coreapi.PodFailed {
		logger.Errorf("Promotion-quay: Pod failed with phase: %s, reason: %s", pod.Status.Phase, pod.Status.Reason)
		for _, status := range pod.Status.ContainerStatuses {
			if status.State.Terminated != nil && status.State.Terminated.ExitCode != 0 {
				logger.Errorf("Promotion-quay: Container %s terminated with exit code %d: %s", status.Name, status.State.Terminated.ExitCode, status.State.Terminated.Message)
				if status.State.Terminated.Reason != "" {
					logger.Errorf("Promotion-quay: Container termination reason: %s", status.State.Terminated.Reason)
				}
			}
		}
	}

	// Extract logs from the promotion container
	containerName := "promotion"
	for _, container := range pod.Spec.Containers {
		if container.Name == containerName {
			logs, err := s.client.GetLogs(pod.Namespace, pod.Name, &coreapi.PodLogOptions{
				Container: containerName,
				TailLines: int64Ptr(200),
			}).Stream(ctx)
			if err != nil {
				logger.Debugf("Promotion-quay: Could not retrieve pod logs: %v", err)
				return
			}
			defer logs.Close()

			buf := make([]byte, 64*1024) // 64KB buffer
			n, readErr := logs.Read(buf)
			if readErr != nil && n == 0 {
				logger.Debugf("Promotion-quay: Could not read pod logs: %v", readErr)
				return
			}
			logContent := string(buf[:n])

			// Extract and log specific error patterns
			errorPatterns := []string{
				"invalid image stream",
				"error:",
				"ERROR",
				"failed",
				"Tag attempt",
				"Mirror attempt",
				"WARNING",
			}

			lines := strings.Split(logContent, "\n")
			errorLines := []string{}
			for _, line := range lines {
				lineLower := strings.ToLower(line)
				for _, pattern := range errorPatterns {
					if strings.Contains(lineLower, strings.ToLower(pattern)) {
						errorLines = append(errorLines, line)
						break
					}
				}
			}

			if len(errorLines) > 0 {
				logger.Errorf("Promotion-quay: Found %d error/warning lines in pod logs:", len(errorLines))
				for i, line := range errorLines {
					if i < 50 { // Limit to first 50 error lines
						if strings.Contains(strings.ToLower(line), "invalid image stream") {
							logger.Errorf("Promotion-quay: INVALID IMAGE STREAM ERROR: %s", line)
						} else if strings.Contains(strings.ToLower(line), "error:") {
							logger.Errorf("Promotion-quay: ERROR: %s", line)
						} else {
							logger.Warnf("Promotion-quay: %s", line)
						}
					}
				}
				if len(errorLines) > 50 {
					logger.Warnf("Promotion-quay: ... and %d more error/warning lines (truncated)", len(errorLines)-50)
				}
			} else {
				logger.Debugf("Promotion-quay: No specific error patterns found in pod logs")
			}

			// Log summary of tag/mirror attempts
			tagAttempts := strings.Count(logContent, "Tag attempt")
			mirrorAttempts := strings.Count(logContent, "Mirror attempt")
			if tagAttempts > 0 || mirrorAttempts > 0 {
				logger.Infof("Promotion-quay: Pod execution summary - Tag attempts: %d, Mirror attempts: %d", tagAttempts, mirrorAttempts)
			}
			return
		}
	}
}

func int64Ptr(i int64) *int64 {
	return &i
}

func (s *promotionStep) ensureNamespaces(ctx context.Context, namespaces sets.Set[string]) error {
	if len(namespaces) == 0 {
		return nil
	}
	// Used primarily (only?) by the chatbot and we likely do not have the permission to create
	// namespaces (nor are we expected to).
	if s.configuration.PromotionConfiguration.RegistryOverride != "" {
		return nil
	}
	var dockercfg credentialprovider.DockerConfigJSON
	if err := json.Unmarshal(s.pushSecret.Data[coreapi.DockerConfigJsonKey], &dockercfg); err != nil {
		return fmt.Errorf("failed to deserialize push secret: %w", err)
	}

	appCIDockercfg, hasAppCIDockercfg := dockercfg.Auths[api.ServiceDomainAPPCIRegistry]
	if !hasAppCIDockercfg {
		return fmt.Errorf("push secret has no entry for %s", api.ServiceDomainAPPCIRegistry)
	}

	appCIKubeconfig := &rest.Config{Host: api.APPCIKubeAPIURL, BearerToken: appCIDockercfg.Password}
	client, err := corev1client.NewForConfig(appCIKubeconfig)
	if err != nil {
		return fmt.Errorf("failed to construct kubeconfig: %w", err)
	}

	for namespace := range namespaces {
		var success bool
		var errs []error
		for i := 0; i < 3; i++ {
			if _, err := client.Namespaces().Create(ctx, &coreapi.Namespace{ObjectMeta: meta.ObjectMeta{Name: namespace}}, meta.CreateOptions{}); err == nil || apierrors.IsAlreadyExists(err) {
				success = true
				break
			} else {
				errs = append(errs, err)
			}
		}
		if !success {
			return fmt.Errorf("failed to create namespace %s with retries: %w", namespace, utilerrors.NewAggregate(errs))
		}
	}

	return nil
}

func getImageMirrorTarget(tags map[string][]api.ImageStreamTagReference, pipeline *imagev1.ImageStream, registry string, time string, mirrorFunc func(source, target string, tag api.ImageStreamTagReference, time string, imageMirror map[string]string), targetNameFunc func(string, api.PromotionTarget) string) (map[string]string, sets.Set[string]) {
	if pipeline == nil {
		return nil, nil
	}
	isQuayPromotion := registry == api.QuayOpenShiftCIRepo
	if isQuayPromotion {
		logrus.Debugf("getImageMirrorTarget: Processing quay promotion with %d source components", len(tags))
	}
	imageMirror := map[string]string{}
	// Will this ever include more than one?
	namespaces := sets.Set[string]{}
	for src, dsts := range tags {
		dockerImageReference := findDockerImageReference(pipeline, src)
		if dockerImageReference == "" {
			if isQuayPromotion {
				logrus.Debugf("getImageMirrorTarget: Skipping component %s - no docker image reference found in pipeline", src)
			}
			continue
		}
		dockerImageReference = getPublicImageReference(dockerImageReference, pipeline.Status.PublicDockerImageRepository)
		if isQuayPromotion {
			logrus.Debugf("getImageMirrorTarget: Processing component %s from %s to %d destination(s)", src, dockerImageReference, len(dsts))
		}
		for _, dst := range dsts {
			var target string
			if targetNameFunc != nil {
				// Use targetNameFunc to generate template and substitute ${component} with actual component name
				promotionTarget := api.PromotionTarget{
					Namespace: dst.Namespace,
					Name:      dst.Name,
					Tag:       dst.Tag,
				}
				template := targetNameFunc(registry, promotionTarget)
				target = strings.Replace(template, api.ComponentFormatReplacement, dst.Tag, -1)
				if isQuayPromotion {
					logrus.Debugf("getImageMirrorTarget: Generated quay target template: %s -> %s (component: %s)", template, target, dst.Tag)
				}
			} else {
				// Fallback to direct target construction for backwards compatibility
				target = fmt.Sprintf("%s/%s", registry, dst.ISTagName())
			}
			mirrorFunc(dockerImageReference, target, dst, time, imageMirror)
			namespaces.Insert(dst.Namespace)
			if isQuayPromotion {
				logrus.Debugf("getImageMirrorTarget: Added mirror mapping for %s/%s:%s -> target: %s", dst.Namespace, dst.Name, dst.Tag, target)
			}
		}
	}
	if len(imageMirror) == 0 {
		return nil, nil
	}
	if registry == api.QuayOpenShiftCIRepo {
		if isQuayPromotion {
			logrus.Debugf("getImageMirrorTarget: Quay promotion complete - generated %d total mirror mappings", len(imageMirror))
			// Log quay-proxy targets specifically
			quayProxyCount := 0
			for k := range imageMirror {
				if strings.Contains(k, "-quay:") {
					quayProxyCount++
					logrus.Debugf("getImageMirrorTarget: Quay-proxy target: %s -> %s", k, imageMirror[k])
				}
			}
			logrus.Infof("getImageMirrorTarget: Quay promotion generated %d quay-proxy targets for oc tag", quayProxyCount)
		}
		namespaces = nil
	}
	return imageMirror, namespaces
}

func getPublicImageReference(dockerImageReference, publicDockerImageRepository string) string {
	if !strings.Contains(dockerImageReference, ":5000") {
		return dockerImageReference
	}
	splits := strings.Split(publicDockerImageRepository, "/")
	if len(splits) < 2 {
		// This should never happen
		logrus.Warnf("Failed to get hostname from publicDockerImageRepository: %s.", publicDockerImageRepository)
		return dockerImageReference
	}
	publicHost := splits[0]
	splits = strings.Split(dockerImageReference, "/")
	if len(splits) < 2 {
		// This should never happen
		logrus.Warnf("Failed to get hostname from dockerImageReference: %s.", dockerImageReference)
		return dockerImageReference
	}
	return strings.Replace(dockerImageReference, splits[0], publicHost, 1)
}

func getMirrorCommand(registryConfig string, images []string, loglevel int) string {
	return fmt.Sprintf("oc image mirror --loglevel=%d --keep-manifest-list --registry-config=%s --max-per-registry=10 %s",
		loglevel, registryConfig, strings.Join(images, " "))
}

func getTagCommand(tagSpecs []string, loglevel int) string {
	return fmt.Sprintf("oc tag --source=docker --loglevel=%d --reference-policy='source' --import-mode='PreserveOriginal' %s",
		loglevel, strings.Join(tagSpecs, " "))
}

// extractQuayProxySources extracts quay-proxy source images from tag specs
// Tag specs are in format "source target", where source is the quay-proxy image
func extractQuayProxySources(tagSpecs []string) []string {
	sources := []string{}
	logrus.Debugf("extractQuayProxySources: Processing %d tag specs", len(tagSpecs))
	for _, spec := range tagSpecs {
		parts := strings.Fields(spec)
		if len(parts) >= 1 && strings.Contains(parts[0], "quay-proxy.ci.openshift.org") {
			sources = append(sources, parts[0])
			if len(parts) >= 2 {
				logrus.Debugf("extractQuayProxySources: Found quay-proxy source: %s -> target: %s", parts[0], parts[1])
			} else {
				logrus.Debugf("extractQuayProxySources: Found quay-proxy source: %s", parts[0])
			}
		}
	}
	logrus.Infof("extractQuayProxySources: Extracted %d quay-proxy source images from %d tag specs", len(sources), len(tagSpecs))
	return sources
}

// waitForQuayProxyImages generates a shell command to wait for quay-proxy images to be available
// It uses oc image info to verify images are accessible, with retries and exponential backoff
// The wait ensures images are available in quay-proxy before oc tag attempts to use them
// Errors are logged but do not fail the pod - we continue with tagging even if some images aren't ready
func waitForQuayProxyImages(sources []string) string {
	if len(sources) == 0 {
		return ""
	}

	var checks []string
	for _, source := range sources {
		// Use oc image info to check if the image is accessible in quay-proxy
		// This verifies the image exists and can be accessed, which is required for oc tag
		// Retry up to 20 times with exponential backoff (max ~5 minutes)
		// The quay-proxy images should be available shortly after quay.io mirroring completes
		// Log warnings but don't fail - continue with tagging even if image isn't ready
		check := fmt.Sprintf(`for i in {1..20}; do
  # Check if image is accessible using oc image info (same method oc tag will use)
  if oc image info %s >/dev/null 2>&1; then
    echo "Image %s is available and accessible (attempt $i)"
    break
  fi
  if [ $i -eq 20 ]; then
    echo "WARNING: Image %s not accessible after 20 attempts, proceeding anyway - oc tag may fail for this image but will continue with others" >&2
    break
  fi
  backoff=$((i * 3))
  echo "Waiting for %s to be accessible (attempt $i/20, sleeping ${backoff}s)..."
  sleep $backoff
done`, source, source, source, source)
		checks = append(checks, check)
	}

	return fmt.Sprintf("echo 'Waiting for quay-proxy images to be accessible after mirroring...'\n%s\necho 'All quay-proxy images checked, proceeding with tagging (errors will be logged but won't fail the pod)...'", strings.Join(checks, "\n"))
}

func getPromotionPod(imageMirrorTarget map[string]string, timeStr string, namespace string, name string, cliVersion string, nodeArchitectures []string) *coreapi.Pod {
	isQuayPromotion := name == api.PromotionQuayStepName
	if isQuayPromotion {
		logrus.Infof("getPromotionPod: Building promotion-quay pod with %d image mirror targets", len(imageMirrorTarget))
	}
	keys := make([]string, 0, len(imageMirrorTarget))
	for k := range imageMirrorTarget {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var images []string
	var pruneImages []string
	var tags []string

	for _, k := range keys {
		if strings.Contains(k, fmt.Sprintf("%s_prune_", timeStr)) {
			pruneImages = append(pruneImages, fmt.Sprintf("%s=%s", imageMirrorTarget[k], k))
			if isQuayPromotion {
				logrus.Debugf("getPromotionPod: Added prune image: %s=%s", imageMirrorTarget[k], k)
			}
		} else {
			// Detect based on target format: quay-proxy targets should be tagged, others mirrored
			if strings.Contains(k, "-quay:") || strings.Contains(k, "${component}") {
				tags = append(tags, fmt.Sprintf("%s %s", imageMirrorTarget[k], k))
				if isQuayPromotion {
					logrus.Debugf("getPromotionPod: Detected quay-proxy target for oc tag: %s -> %s", imageMirrorTarget[k], k)
				}
			} else {
				// Default to mirroring for quay.io targets and other registries
				images = append(images, fmt.Sprintf("%s=%s", imageMirrorTarget[k], k))
				if isQuayPromotion {
					logrus.Debugf("getPromotionPod: Added quay.io mirror target: %s=%s", imageMirrorTarget[k], k)
				}
			}
		}
	}

	if isQuayPromotion {
		logrus.Infof("getPromotionPod: Promotion-quay breakdown - mirror: %d, tag: %d, prune: %d", len(images), len(tags), len(pruneImages))
	}

	registryConfig := filepath.Join(api.RegistryPushCredentialsCICentralSecretMountPath, coreapi.DockerConfigJsonKey)
	command := []string{"/bin/sh", "-c"}

	var commands []string

	// Generate mirror commands if there are images to mirror
	if len(images) > 0 {
		mirrorCommand := fmt.Sprintf("for r in {1..5}; do echo Mirror attempt $r; %s && break; backoff=$(($RANDOM %% 120))s; echo Sleeping randomized $backoff before retry; sleep $backoff; done", getMirrorCommand(registryConfig, images, 10))
		commands = append(commands, mirrorCommand)
	}

	// Generate tag commands if there are tags to create
	if len(tags) > 0 {
		if isQuayPromotion {
			logrus.Infof("getPromotionPod: Generating oc tag commands for %d quay-proxy targets", len(tags))
		}
		// Wait for quay-proxy images to be available before tagging
		// Extract quay-proxy source images from tag specs and wait for them to be available
		quayProxySources := extractQuayProxySources(tags)
		if len(quayProxySources) > 0 {
			if isQuayPromotion {
				logrus.Infof("getPromotionPod: Adding wait logic for %d quay-proxy source images before tagging", len(quayProxySources))
			}
			// Add a short delay after mirroring to allow quay-proxy to sync
			commands = append(commands, "echo 'Waiting for quay-proxy to sync images after mirroring...'\nsleep 30")
			waitCommand := waitForQuayProxyImages(quayProxySources)
			commands = append(commands, waitCommand)
			if isQuayPromotion {
				logrus.Debugf("getPromotionPod: Added wait command for quay-proxy image availability")
			}
		} else if isQuayPromotion {
			logrus.Warnf("getPromotionPod: No quay-proxy sources found in %d tag specs - proceeding without wait", len(tags))
		}
		// Generate tag command that logs errors but doesn't fail the pod
		// Each tag operation is attempted individually so one failure doesn't stop others
		if isQuayPromotion {
			// For promotion-quay, handle each tag separately to log errors without failing
			// Use set +e to continue on errors, but log them all
			tagCommands := []string{
				"set +e", // Don't exit on error - continue processing all tags
				"echo 'Starting oc tag operations for quay-proxy targets...'",
				"tag_errors=0", // Track error count for summary
			}
			for _, tagSpec := range tags {
				parts := strings.Fields(tagSpec)
				if len(parts) >= 2 {
					source := parts[0]
					target := parts[1]
					// Attempt each tag individually, log errors but continue
					// Capture both stdout and stderr to detect specific errors
					tagCmd := fmt.Sprintf(`tag_output=$(oc tag --source=docker --loglevel=10 --reference-policy='source' --import-mode='PreserveOriginal' %s %s 2>&1)
tag_err=$?
if [ $tag_err -eq 0 ]; then
  echo "Successfully tagged: %s -> %s"
else
  tag_errors=$((tag_errors + 1))
  echo "ERROR: Failed to tag %s -> %s (exit code: $tag_err)" >&2
  echo "$tag_output" >&2
  # Check for specific error patterns and log them prominently
  if echo "$tag_output" | grep -q "invalid image stream"; then
    echo "ERROR: Invalid image stream detected for %s - image may not be available in quay-proxy yet" >&2
    echo "ERROR: This error is logged but will not fail the pod - other tags will continue" >&2
  fi
  # Don't fail - continue with other tags
fi`, source, target, source, target, source, target, source, target)
					tagCommands = append(tagCommands, tagCmd)
				}
			}
			// Add summary - errors are logged but don't fail the pod
			tagCommands = append(tagCommands,
				"echo 'Completed all oc tag operations'",
				"if [ $tag_errors -gt 0 ]; then",
				"  echo \"WARNING: $tag_errors tag operation(s) failed (errors logged above) - continuing with other operations\" >&2",
				"else",
				"  echo 'All tag operations completed successfully'",
				"fi",
				"set -e", // Re-enable error checking for subsequent commands (like prune)
			)
			commands = append(commands, strings.Join(tagCommands, "\n"))
		} else {
			// For regular promotion, use the original retry logic
			tagCommand := fmt.Sprintf("for r in {1..5}; do echo Tag attempt $r; %s && break; backoff=$(($RANDOM %% 120))s; echo Sleeping randomized $backoff before retry; sleep $backoff; done", getTagCommand(tags, 10))
			commands = append(commands, tagCommand)
		}
		if isQuayPromotion {
			logrus.Debugf("getPromotionPod: Added oc tag command with retry logic")
		}
	}

	var args []string
	if len(pruneImages) > 0 {
		// See https://github.com/openshift/release/blob/2080ec4a49337c27577a4b2ff08a538e96436e65/hack/qci_registry_pruner.py for details.
		// Note that we don't retry here and we ignore failures because (a) it may be the first time an image tag is
		// being promoted to and trying to add a pruning tag to the existing image is doomed to fail. (b) pruning tags
		// help eliminate a rare race condition. The cost of an occasional failure in establishing them is very low.
		args = append(args, fmt.Sprintf("%s || true", getMirrorCommand(registryConfig, pruneImages, 10)))
	}

	args = append(args, commands...)
	args = []string{strings.Join(args, "\n")}

	image := fmt.Sprintf("%s/%s/%s:cli", api.DomainForService(api.ServiceRegistry), "ocp", cliVersion)
	nodeSelector := map[string]string{"kubernetes.io/arch": "amd64"}

	archs := sets.New[string](nodeArchitectures...)
	if !archs.Has("amd64") && archs.Has("arm64") {
		image = fmt.Sprintf("%s/%s/4.14:cli", api.DomainForService(api.ServiceRegistry), "ocp-arm64")
		nodeSelector = map[string]string{"kubernetes.io/arch": "arm64"}
	}

	return &coreapi.Pod{
		ObjectMeta: meta.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{steps.AnnotationSaveContainerLogs: "true"},
		},
		Spec: coreapi.PodSpec{
			NodeSelector:  nodeSelector,
			RestartPolicy: coreapi.RestartPolicyNever,
			Containers: []coreapi.Container{
				{
					Name:    "promotion",
					Image:   image,
					Command: command,
					Args:    args,
					Env: []coreapi.EnvVar{
						{
							Name:  "KUBECONFIG",
							Value: "/etc/app-ci-kubeconfig/kubeconfig",
						},
					},
					VolumeMounts: []coreapi.VolumeMount{
						{
							Name:      "push-secret",
							MountPath: "/etc/push-secret",
							ReadOnly:  true,
						},
						{
							Name:      "app-ci-kubeconfig",
							MountPath: "/etc/app-ci-kubeconfig",
							ReadOnly:  true,
						},
					},
				},
			},
			Volumes: []coreapi.Volume{
				{
					Name: "push-secret",
					VolumeSource: coreapi.VolumeSource{
						Secret: &coreapi.SecretVolumeSource{SecretName: api.RegistryPushCredentialsCICentralSecret},
					},
				},
				{
					Name: "app-ci-kubeconfig",
					VolumeSource: coreapi.VolumeSource{
						Secret: &coreapi.SecretVolumeSource{SecretName: api.PromotionQuayTaggerKubeconfigSecret},
					},
				},
			},
		},
	}
}

// findDockerImageReference returns DockerImageReference, the string that can be used to pull this image,
// to a tag if it exists in the ImageStream's Spec
func findDockerImageReference(is *imagev1.ImageStream, tag string) string {
	for _, t := range is.Status.Tags {
		if t.Tag != tag {
			continue
		}
		if len(t.Items) == 0 {
			return ""
		}
		return t.Items[0].DockerImageReference
	}
	return ""
}

// toPromote determines the mapping of local tag to external tag which should be promoted
func toPromote(config api.PromotionTarget, images []api.ProjectDirectoryImageBuildStepConfiguration, requiredImages sets.Set[string]) (map[string]string, sets.Set[string]) {
	tagsByDst := map[string]string{}
	names := sets.New[string]()

	if config.Disabled {
		return tagsByDst, names
	}

	for _, image := range images {
		// if the image is required or non-optional, include it in promotion
		tag := string(image.To)
		if requiredImages.Has(tag) || !image.Optional {
			tagsByDst[tag] = tag
			names.Insert(tag)
		}
	}

	for _, tag := range config.ExcludedImages {
		if tag == api.PromotionExcludeImageWildcard {
			clear(tagsByDst)
			names.Clear()
			break
		}
		delete(tagsByDst, tag)
		names.Delete(tag)
	}

	for dst, src := range config.AdditionalImages {
		tagsByDst[dst] = src
		names.Insert(dst)
	}

	return tagsByDst, names
}

// PromotedTags returns the tags that are being promoted for the given ReleaseBuildConfiguration
func PromotedTags(configuration *api.ReleaseBuildConfiguration) []api.ImageStreamTagReference {
	var tags []api.ImageStreamTagReference
	mapping, _ := PromotedTagsWithRequiredImages(configuration)
	for _, dest := range mapping {
		tags = append(tags, dest...)
	}
	sort.Slice(tags, func(i, j int) bool {
		return tags[i].ISTagName() < tags[j].ISTagName()
	})
	return tags
}

type PromotedTagsOptions struct {
	requiredImages sets.Set[string]
	commitSha      string
}

type PromotedTagsOption func(options *PromotedTagsOptions)

// WithRequiredImages ensures that the images are promoted, even if they would otherwise be skipped.
func WithRequiredImages(images sets.Set[string]) PromotedTagsOption {
	return func(options *PromotedTagsOptions) {
		options.requiredImages = images
	}
}

// WithCommitSha ensures that images are tagged by the commit SHA as well as any other options in the configuration.
func WithCommitSha(commitSha string) PromotedTagsOption {
	return func(options *PromotedTagsOptions) {
		options.commitSha = commitSha
	}
}

// PromotedTagsWithRequiredImages returns the tags that are being promoted for the given ReleaseBuildConfiguration
// accounting for the list of required images. Promoted tags are mapped by the source tag in the pipeline ImageStream
// we will promote to the output.
func PromotedTagsWithRequiredImages(configuration *api.ReleaseBuildConfiguration, options ...PromotedTagsOption) (map[string][]api.ImageStreamTagReference, sets.Set[string]) {
	opts := &PromotedTagsOptions{
		requiredImages: sets.New[string](),
	}
	for _, opt := range options {
		opt(opts)
	}

	promotedTags := map[string][]api.ImageStreamTagReference{}
	requiredImages := sets.Set[string]{}

	if configuration == nil || configuration.PromotionConfiguration == nil {
		return promotedTags, requiredImages
	}

	for _, target := range api.PromotionTargets(configuration.PromotionConfiguration) {
		tags, names := toPromote(target, configuration.Images, opts.requiredImages)
		requiredImages.Insert(names.UnsortedList()...)
		for dst, src := range tags {
			var tag api.ImageStreamTagReference
			if target.Name != "" {
				tag = api.ImageStreamTagReference{
					Namespace: target.Namespace,
					Name:      target.Name,
					Tag:       dst,
				}
			} else { // promotion.Tag must be set
				tag = api.ImageStreamTagReference{
					Namespace: target.Namespace,
					Name:      dst,
					Tag:       target.Tag,
				}
			}
			promotedTags[src] = append(promotedTags[src], tag)
			if target.TagByCommit && opts.commitSha != "" {
				promotedTags[src] = append(promotedTags[src], api.ImageStreamTagReference{
					Namespace: target.Namespace,
					Name:      dst,
					Tag:       opts.commitSha,
				})
			}
		}
	}
	// promote the binary build if one exists and this isn't disabled
	if configuration.BinaryBuildCommands != "" && !configuration.PromotionConfiguration.DisableBuildCache {
		promotedTags[string(api.PipelineImageStreamTagReferenceBinaries)] = append(promotedTags[string(api.PipelineImageStreamTagReferenceBinaries)], api.BuildCacheFor(configuration.Metadata))
	}
	for _, tags := range promotedTags {
		sort.Slice(tags, func(i, j int) bool {
			return tags[i].ISTagName() < tags[j].ISTagName()
		})
	}
	return promotedTags, requiredImages
}

func (s *promotionStep) Requires() []api.StepLink {
	return []api.StepLink{api.AllStepsLink()}
}

func (s *promotionStep) Creates() []api.StepLink {
	return []api.StepLink{}
}

func (s *promotionStep) Provides() api.ParameterMap {
	return nil
}

func (s *promotionStep) Name() string { return fmt.Sprintf("[%s]", s.name) }

func (s *promotionStep) targets() string {
	var targets []string
	for _, target := range api.PromotionTargets(s.configuration.PromotionConfiguration) {
		targets = append(targets, s.targetNameFunc(s.registry, target))
	}
	return strings.Join(targets, ", ")
}

func (s *promotionStep) Description() string {
	return fmt.Sprintf("Promote built images into the release image streams: %s", s.targets())
}

func (s *promotionStep) Objects() []ctrlruntimeclient.Object {
	return s.client.Objects()
}

// PromotionStep copies tags from the pipeline image stream to the destination defined in the promotion config.
// If the source tag does not exist it is silently skipped.
func PromotionStep(
	name string,
	configuration *api.ReleaseBuildConfiguration,
	requiredImages sets.Set[string],
	jobSpec *api.JobSpec,
	client kubernetes.PodClient,
	pushSecret *coreapi.Secret,
	registry string,
	mirrorFunc func(source, target string, tag api.ImageStreamTagReference, date string, imageMirror map[string]string),
	targetNameFunc func(string, api.PromotionTarget) string,
	nodeArchitectures []string,
) api.Step {
	return &promotionStep{
		name:              name,
		configuration:     configuration,
		requiredImages:    requiredImages,
		jobSpec:           jobSpec,
		client:            client,
		pushSecret:        pushSecret,
		registry:          registry,
		mirrorFunc:        mirrorFunc,
		targetNameFunc:    targetNameFunc,
		nodeArchitectures: nodeArchitectures,
	}
}
