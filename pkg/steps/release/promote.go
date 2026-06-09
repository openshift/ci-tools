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

	if refs := mainRefs(s.jobSpec.Refs, s.jobSpec.ExtraRefs); refs != nil {
		opts = append(opts, WithCommitSha(refs.BaseSHA))
	}

	tags, names := PromotedTagsWithRequiredImages(s.configuration, opts...)
	if len(names) == 0 {
		logger.Info("Nothing to promote, skipping...")
		return nil
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
	imageMirrorTarget, namespaces := getImageMirrorTarget(tags, pipeline, s.registry, timeStr, s.mirrorFunc, s.targetNameFunc)
	if len(imageMirrorTarget) == 0 {
		logger.Info("Nothing to promote, skipping...")
		return nil
	}

	// in some cases like when we are called by the ci-chat-bot we may need to create namespaces
	// in general, we do not expect to be able to do this, so we only do it best-effort
	if err := s.ensureNamespaces(ctx, namespaces); err != nil {
		logger.WithError(err).Warn("Failed to ensure namespaces to promote to in central registry.")
	}

	cliImage, err := promotionCLIImage(ctx, s.client, s.jobSpec.Namespace())
	if err != nil {
		return fmt.Errorf("resolve promotion cli image: %w", err)
	}

	if _, err := steps.RunPod(ctx, s.client, getPromotionPod(imageMirrorTarget, timeStr, s.jobSpec.Namespace(), s.name, cliImage, s.nodeArchitectures), false); err != nil {
		return fmt.Errorf("unable to run promotion pod: %w", err)
	}
	return nil
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
	imageMirror := map[string]string{}
	// Will this ever include more than one?
	namespaces := sets.Set[string]{}
	for src, dsts := range tags {
		dockerImageReference := findDockerImageReference(pipeline, src)
		if dockerImageReference == "" {
			continue
		}
		dockerImageReference = getPublicImageReference(dockerImageReference, pipeline.Status.PublicDockerImageRepository)
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
			} else {
				// Fallback to direct target construction for backwards compatibility
				target = fmt.Sprintf("%s/%s", registry, dst.ISTagName())
			}
			mirrorFunc(dockerImageReference, target, dst, time, imageMirror)
			namespaces.Insert(dst.Namespace)
		}
	}
	if len(imageMirror) == 0 {
		return nil, nil
	}
	if registry == api.QuayOpenShiftCIRepo {
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

func getLatestStableCLIVersion(client *http.Client) (string, error) {
	for major := 9; major >= 4; major-- {
		stream := fmt.Sprintf("%d-stable", major)
		if version, err := prerelease.StableLatestMajorMinor(client, stream); err == nil {
			return version, nil
		}
	}
	return "", fmt.Errorf("no stable CLI version found in any stream (tried 9-stable through 4-stable)")
}

func promotionCLIImage(ctx context.Context, client ctrlruntimeclient.Client, namespace string) (string, error) {
	return promotionCLIImageWithResolver(ctx, client, namespace, getLatestStableCLIVersion)
}

func promotionCLIImageWithResolver(
	ctx context.Context,
	client ctrlruntimeclient.Client,
	namespace string,
	stableVersionResolver func(*http.Client) (string, error),
) (string, error) {
	streamName := api.ReleaseStreamFor(api.LatestReleaseName)
	is := &imagev1.ImageStream{}
	err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: namespace, Name: streamName}, is)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return promotionCLIImageFromRegistryWithResolver(stableVersionResolver)
		}
		return "", fmt.Errorf("get imagestream %s/%s: %w", namespace, streamName, err)
	}
	if findDockerImageReference(is, "cli") != "" {
		cliImage := fmt.Sprintf("%s:cli", streamName)
		logrus.WithField("image", cliImage).Info("Using payload cli image for promotion")
		return cliImage, nil
	}
	logrus.WithField("stream", streamName).Info("No cli tag on release payload stream; using quay-proxy stable cli for promotion")
	return promotionCLIImageFromRegistryWithResolver(stableVersionResolver)
}

func promotionRegistryCLIReference(version string) api.ImageStreamTagReference {
	return api.ImageStreamTagReference{Namespace: "ocp", Name: version, Tag: "cli"}
}

func promotionCLIImageFromRegistryWithResolver(stableVersionResolver func(*http.Client) (string, error)) (string, error) {
	version, err := stableVersionResolver(&http.Client{Timeout: 15 * time.Second})
	if err != nil {
		logrus.WithError(err).Warn("Failed to determine the stable release version, using 4.22 instead")
		version = "4.22"
	}
	ref := promotionRegistryCLIReference(version)
	// registry.ci ocp/*:cli and ocp-arm64/*:cli are not reliably present after
	// source-reference migration, so use quay-proxy ocp_<version>_cli fallback.
	// Arm64-only jobs still resolve/tag arm64 payload digests via --filter-by-os.
	image := api.QuayImageReference(ref)
	logrus.WithField("image", image).Info("Using quay-proxy cli image for promotion")
	return image, nil
}

func getMirrorCommand(registryConfig string, images []string, loglevel int) string {
	return fmt.Sprintf("oc image mirror --loglevel=%d --keep-manifest-list --registry-config=%s --max-per-registry=10 %s",
		loglevel, registryConfig, strings.Join(images, " "))
}

func getTagCommand(tagSpecs []string, loglevel int) string {
	return fmt.Sprintf("oc tag --source=docker --loglevel=%d --reference-policy='source' --import-mode='PreserveOriginal' --reference %s",
		loglevel, strings.Join(tagSpecs, " "))
}

// quayProxyTagFromISKey derives the quay-proxy floating tag from an IS tag key.
// Handles "namespace/stream-quay:tag" (4.23+) and consolidated "ocp/4.13:tag" (4.11–4.22).
// Example: "ocp/4.13:cli" → "quay-proxy.ci.openshift.org/openshift/ci:ocp_4.13_cli".
func quayProxyTagFromISKey(isTagKey string) (string, bool) {
	slashIdx := strings.Index(isTagKey, "/")
	if slashIdx == -1 {
		return "", false
	}
	namespace := isTagKey[:slashIdx]
	rest := isTagKey[slashIdx+1:]
	lastColon := strings.LastIndex(rest, ":")
	if lastColon == -1 {
		return "", false
	}
	streamPart := rest[:lastColon]
	tag := rest[lastColon+1:]
	if namespace == "" || streamPart == "" || tag == "" {
		return "", false
	}
	const quayStreamSuffix = "-quay"
	var streamName string
	if strings.HasSuffix(streamPart, quayStreamSuffix) {
		streamName = strings.TrimSuffix(streamPart, quayStreamSuffix)
	} else if api.ConsolidatedQuayPromotionVersion(streamPart) {
		streamName = streamPart
	} else {
		return "", false
	}
	if streamName == "" {
		return "", false
	}
	return fmt.Sprintf("%s/openshift/ci:%s_%s_%s", api.QCIAPPCIDomain, namespace, streamName, tag), true
}

// promotionCLIImageInfoFilterOS returns the --filter-by-os value matching the promotion pod's
// node architecture (amd64 vs arm64-only) so oc image info resolves one digest for manifest lists.
func promotionCLIImageInfoFilterOS(nodeArchitectures []string) string {
	archs := sets.New[string](nodeArchitectures...)
	if !archs.Has("amd64") && archs.Has("arm64") {
		return "linux/arm64"
	}
	return "linux/amd64"
}

const (
	quayPromotionDigestTagAttempts = 5
	quayPromotionMirrorAttempts    = 5
)

// getMirrorRetryShell mirrors images with retries and fails the promotion pod if all attempts fail.
func getMirrorRetryShell(registryConfig string, images []string) string {
	mirrorCmd := getMirrorCommand(registryConfig, images, 2)
	n := quayPromotionMirrorAttempts
	return fmt.Sprintf(`for r in {1..%d}; do
  echo Mirror attempt $r
  if %s; then break; fi
  if [ "${r}" -eq %d ]; then
    exit 1
  fi
  backoff=$(($RANDOM %% 120))s
  echo Sleeping randomized $backoff before retry
  sleep $backoff
done`, n, mirrorCmd, n)
}

// getResolveAndTagRetryShell resolves digest from quay.io (push-secret) and tags the IST with quay-proxy@digest.
func getResolveAndTagRetryShell(registryConfig, quayProxyTag, isTag string, loglevel int, filterByOS string) string {
	repo := quayProxyTag[:strings.LastIndex(quayProxyTag, ":")]
	quayIOTag := strings.Replace(quayProxyTag, api.QCIAPPCIDomain, "quay.io", 1)
	n := quayPromotionDigestTagAttempts
	return fmt.Sprintf(`for r in {1..%d}; do
  _digest=$(oc image info --registry-config=%s --filter-by-os=%s %s | sed -n '/^Digest:[[:space:]]/s/^Digest:[[:space:]]*//p' | head -n1)
  if [ -n "${_digest}" ] && oc tag --source=docker --loglevel=%d --reference-policy='source' --import-mode='PreserveOriginal' --reference %s@${_digest} %s; then
    break
  fi
  echo "promotion-quay: digest-tag failed for %s attempt ${r}/%d (QCI digest may have moved after mirror)" >&2
  if [ "${r}" -eq %d ]; then
    exit 1
  fi
  echo "promotion-quay: retrying digest-tag for %s (attempt $((r+1))/%d after randomized backoff)" >&2
  backoff=$(($RANDOM %% %d))s
  sleep "${backoff}"
done
`, n, registryConfig, filterByOS, quayIOTag, loglevel, repo, isTag,
		isTag, n,
		n,
		isTag, n,
		120)
}

const (
	retryLoopTemplate    = "for r in {1..%d}; do echo %s; %s && break; %s; done"
	retryLoopWithBackoff = "backoff=$(($RANDOM % 120))s; echo Sleeping randomized $backoff before retry; sleep $backoff"

	quayImageIncomingSuffix = "_incoming"
	quayImagePreSuffix      = "__pre"
	quayImagePost1Suffix    = "__post1"
)

type quayFloatPromotion struct {
	floatTag string
	newSrc   string
	pruneTag string
}

// getStagedQuayFloatPromotionShell returns shell that repoints floatTag from its incoming staging tag.
// If floatTag already exists on the registry, the current image is copied to pruneTag (when set) and
// floatTag+quayImagePreSuffix before the repoint; floatTag+quayImagePost1Suffix is added afterward.
func getStagedQuayFloatPromotionShell(registryConfig, floatTag, pruneTag string) string {
	incomingTag := floatTag + quayImageIncomingSuffix
	preTag := floatTag + quayImagePreSuffix
	post1Tag := floatTag + quayImagePost1Suffix

	checkOld := fmt.Sprintf(`_OLD_EXISTS=false
if oc image info --registry-config=%s %s >/dev/null 2>&1; then _OLD_EXISTS=true; fi`, registryConfig, floatTag)

	var backupOld string
	if pruneTag != "" {
		backupOld = fmt.Sprintf(`if [ "${_OLD_EXISTS}" = "true" ]; then
%s
%s
fi`,
			indentShell(getMirrorRetryShell(registryConfig, []string{fmt.Sprintf("%s=%s", floatTag, pruneTag)})),
			indentShell(getMirrorRetryShell(registryConfig, []string{fmt.Sprintf("%s=%s", floatTag, preTag)})),
		)
	} else {
		backupOld = fmt.Sprintf(`if [ "${_OLD_EXISTS}" = "true" ]; then
%s
fi`, indentShell(getMirrorRetryShell(registryConfig, []string{fmt.Sprintf("%s=%s", floatTag, preTag)})))
	}

	latch := getMirrorRetryShell(registryConfig, []string{fmt.Sprintf("%s=%s", incomingTag, floatTag)})

	postLatch := fmt.Sprintf(`if [ "${_OLD_EXISTS}" = "true" ]; then
%s
fi`, indentShell(getMirrorRetryShell(registryConfig, []string{fmt.Sprintf("%s=%s", floatTag, post1Tag)})))

	return strings.Join([]string{checkOld, backupOld, latch, postLatch}, "\n")
}

func indentShell(script string) string {
	const prefix = "  "
	lines := strings.Split(script, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = prefix + line
		}
	}
	return strings.Join(lines, "\n")
}

func getPromotionPod(imageMirrorTarget map[string]string, timeStr string, namespace string, name string, cliImage string, nodeArchitectures []string) *coreapi.Pod {
	keys := make([]string, 0, len(imageMirrorTarget))
	for k := range imageMirrorTarget {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var images []string
	var incomingImages []string
	var pruneImages []string
	var quayFloatPromotions []quayFloatPromotion
	pruneTagForFloat := map[string]string{}
	var tags []string
	// resolveAndTagPairs holds [quayProxyTag, isTag] for official ocp IS targets (consolidated
	// ocp/4.x:tag and legacy *-quay). Resolved post-mirror via oc image info + oc tag.
	var resolveAndTagPairs [][2]string

	isQuayStep := name == api.PromotionQuayStepName

	for _, k := range keys {
		if strings.Contains(k, fmt.Sprintf("%s_prune_", timeStr)) {
			floatTag := imageMirrorTarget[k]
			if isQuayStep {
				pruneTagForFloat[floatTag] = k
			} else {
				pruneImages = append(pruneImages, fmt.Sprintf("%s=%s", floatTag, k))
			}
		} else {
			src := imageMirrorTarget[k]
			if strings.HasPrefix(k, api.QuayOpenShiftCIRepo+":") {
				// Images promoted into quay.io/openshift/ci always use oc image mirror (tag or digest sources).
				if isQuayStep {
					incomingImages = append(incomingImages, fmt.Sprintf("%s=%s", src, k+quayImageIncomingSuffix))
					quayFloatPromotions = append(quayFloatPromotions, quayFloatPromotion{
						floatTag: k,
						newSrc:   src,
					})
				} else {
					images = append(images, fmt.Sprintf("%s=%s", src, k))
				}
			} else if isQuayStep && !strings.Contains(k, api.ComponentFormatReplacement) {
				// Concrete quay IS target: resolve the QCI digest after mirroring instead of
				// using a pre-computed (potentially tag-only) source so spec.from is always digest-based.
				// Non-release namespaces use oc tag directly.
				quayProxyTag, ok := quayProxyTagFromISKey(k)
				if ok {
					ns := k[:strings.Index(k, "/")]
					if api.RefersToOfficialImage(ns, api.WithOKD) {
						resolveAndTagPairs = append(resolveAndTagPairs, [2]string{quayProxyTag, k})
						continue
					}
				}
				tags = append(tags, fmt.Sprintf("%s %s", src, k))
			} else if strings.Contains(k, api.ComponentFormatReplacement) || !strings.Contains(src, "@sha256:") || strings.Contains(src, api.QCIAPPCIDomain) {
				// Cluster ImageStream tags: oc tag for non-digest sources, quay-proxy imports, or ${component} templates.
				tags = append(tags, fmt.Sprintf("%s %s", src, k))
			} else {
				images = append(images, fmt.Sprintf("%s=%s", src, k))
			}
		}
	}

	for i := range quayFloatPromotions {
		quayFloatPromotions[i].pruneTag = pruneTagForFloat[quayFloatPromotions[i].floatTag]
	}

	registryConfig := filepath.Join(api.RegistryPushCredentialsCICentralSecretMountPath, coreapi.DockerConfigJsonKey)
	command := []string{"/bin/sh", "-c"}

	var commands []string

	// Generate mirror commands if there are images to mirror
	if len(images) > 0 {
		commands = append(commands, getMirrorRetryShell(registryConfig, images))
	}

	if isQuayStep {
		for _, pair := range resolveAndTagPairs {
			quayProxyTag, isTag := pair[0], pair[1]
			commands = append(commands, getResolveAndTagRetryShell(registryConfig, quayProxyTag, isTag, 2, promotionCLIImageInfoFilterOS(nodeArchitectures)))
		}
	}

	// Non-official IS tags (ci/ci-quay, ${component} templates) keep best-effort batch tagging.
	if isQuayStep && len(tags) > 0 {
		tagCommands := []string{"set +e"}

		singleCmd := fmt.Sprintf(retryLoopTemplate, 2, `"Tag attempt $r (all together)"`, getTagCommand(tags, 2), ":")
		tagCommands = append(tagCommands, singleCmd)
		for _, tagPair := range tags {
			individualCmd := fmt.Sprintf(retryLoopTemplate, 3, `"Tag attempt $r (individual)"`, getTagCommand([]string{tagPair}, 2), retryLoopWithBackoff)
			tagCommands = append(tagCommands, individualCmd)
		}

		tagCommands = append(tagCommands, "set -e")
		commands = append(commands, strings.Join(tagCommands, "\n"))
	} else if len(tags) > 0 {
		// For regular promotion, use the original retry logic
		tagCommand := fmt.Sprintf(retryLoopTemplate, 5, "Tag attempt $r", getTagCommand(tags, 2), retryLoopWithBackoff)
		commands = append(commands, tagCommand)
	}

	var args []string
	if isQuayStep && len(incomingImages) > 0 {
		args = append(args, getMirrorRetryShell(registryConfig, incomingImages))
	}
	if isQuayStep && len(quayFloatPromotions) > 0 {
		sort.Slice(quayFloatPromotions, func(i, j int) bool {
			return quayFloatPromotions[i].floatTag < quayFloatPromotions[j].floatTag
		})
		for _, promotion := range quayFloatPromotions {
			args = append(args, getStagedQuayFloatPromotionShell(registryConfig, promotion.floatTag, promotion.pruneTag))
		}
	}
	if len(pruneImages) > 0 {
		// See https://github.com/openshift/release/blob/2080ec4a49337c27577a4b2ff08a538e96436e65/hack/qci_registry_pruner.py for details.
		// Note that we don't retry here and we ignore failures because (a) it may be the first time an image tag is
		// being promoted to and trying to add a pruning tag to the existing image is doomed to fail. (b) pruning tags
		// help eliminate a rare race condition. The cost of an occasional failure in establishing them is very low.
		args = append(args, fmt.Sprintf("%s || true", getMirrorCommand(registryConfig, pruneImages, 2)))
	}

	args = append(args, commands...)
	args = []string{strings.Join(args, "\n")}

	image := cliImage
	nodeSelector := map[string]string{"kubernetes.io/arch": "amd64"}

	archs := sets.New[string](nodeArchitectures...)
	// Keep arm64 pinning only when using payload stable:cli. Registry fallback image
	// is amd64-only, but promotion logic itself is architecture-agnostic.
	if cliImage == "stable:cli" && !archs.Has("amd64") && archs.Has("arm64") {
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
						// Discovery cache only: without KUBECACHEDIR, client-go defaults to $HOME/.kube/cache; with no
						// writable HOME in the container that becomes /.kube and logs many INFO lines ("permission denied").
						{Name: "KUBECONFIG", Value: "/etc/app-ci-kubeconfig/kubeconfig"},
						{Name: "KUBECACHEDIR", Value: "/tmp/.kube/cache"},
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
// to a tag if it exists in the ImageStream's Spec.
// When the recorded DockerImageReference is tag-only (no digest) but the status item
// carries a sha256 image ID, a digest-anchored pullspec is returned instead so that
// callers can always pin to the exact image (e.g. qciPullSpec can always succeed).
func findDockerImageReference(is *imagev1.ImageStream, tag string) string {
	for _, t := range is.Status.Tags {
		if t.Tag != tag {
			continue
		}
		if len(t.Items) == 0 {
			return ""
		}
		ref := t.Items[0].DockerImageReference
		if !strings.Contains(ref, "@sha256:") && strings.HasPrefix(t.Items[0].Image, "sha256:") {
			if idx := strings.LastIndex(ref, ":"); idx != -1 {
				return ref[:idx] + "@" + t.Items[0].Image
			}
		}
		return ref
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
		tags, names := toPromote(target, configuration.Images.Items, opts.requiredImages)
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
