package release

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
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
	skippedImages     sets.Set[string]
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
	if s.configuration != nil && s.configuration.Images.BuildIfAffected && len(s.skippedImages) > 0 {
		opts = append(opts, WithSkippedImages(s.skippedImages))
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

	if s.name == api.PromotionQuayStepName {
		return s.runQuayPromotion(ctx, imageMirrorTarget, timeStr, cliImage)
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

func getTagLoopCommand(tagSpecs []string, loglevel int) string {
	return fmt.Sprintf(`
while read tag; do
oc tag --source=docker --loglevel=%d --reference-policy='source' --import-mode='PreserveOriginal' --reference $tag || break
done <<'EOF'
%s
EOF`, loglevel, strings.Join(tagSpecs, "\n"))
}

// quayProxyTagFromISKey derives the quay-proxy floating tag from an IS tag key.
// Handles "ocp/4.13:cli" and legacy "namespace/stream-quay:tag" (ci templates).
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
	} else if api.RefersToOfficialImage(namespace, api.WithOKD) {
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
	quayPromotionLogLevel          = 2
	quayPromotionMirrorAttempts    = 5
	quayPromotionDigestTagAttempts = 5
)

const mirrorShellFunction = `function mirror {
for r in $(seq 1 $1); do
  echo Mirror attempt $r
  if oc image mirror --loglevel=$2 --keep-manifest-list --registry-config=$3 --max-per-registry=10 $4; then break; fi
  if [ "${r}" -eq $1 ]; then
    exit 1
  fi
  backoff=$(($RANDOM % 120))s
  echo Sleeping randomized $backoff before retry
  sleep $backoff
done
}`

// getMirrorRetryShell mirrors images with retries and fails the promotion pod if all attempts fail.
func getMirrorRetryShell(registryConfig string, images []string) string {
	var mirrorCmds []string
	for _, image := range images {
		mirrorCmds = append(mirrorCmds, fmt.Sprintf("mirror %d %d %s %s", quayPromotionMirrorAttempts, quayPromotionLogLevel, registryConfig, image))
	}
	return strings.Join(mirrorCmds, "\n")
}

const (
	tagRetryBackoff = "backoff=$(($RANDOM % 120))s; echo Sleeping randomized $backoff before retry; sleep $backoff"

	promotionPodKubeconfigPath   = "/etc/app-ci-kubeconfig/kubeconfig"
	quayPromotionScriptKey       = "promote.sh"
	quayPromotionScriptMountPath = "/var/run/configmaps/ci.openshift.io/promotion-quay"
	quayPromotionScriptVolume    = "promotion-quay-script"
	quayImageIncomingSuffix      = "_incoming"
	quayImagePreSuffix           = "__pre"
	quayImagePost1Suffix         = "__post1"
)

func tagRetryShell(attempts int, echoLine, tagCmd, backoff string) string {
	loop := fmt.Sprintf(`for r in {1..%d}; do
echo %s
%s
[[ $? -ne 0 ]] && break
:`, attempts, echoLine, tagCmd)
	if backoff != "" {
		return loop + "\n" + backoff + "\ndone"
	}
	return loop + "\ndone"
}

func (s *promotionStep) runQuayPromotion(ctx context.Context, imageMirrorTarget map[string]string, timeStr, cliImage string) error {
	configMap := &coreapi.ConfigMap{
		ObjectMeta: meta.ObjectMeta{
			Name:      fmt.Sprintf("promotion-quay-%s", timeStr),
			Namespace: s.jobSpec.Namespace(),
		},
		Data: map[string]string{
			quayPromotionScriptKey: getQuayPromotionShell(imageMirrorTarget, timeStr, s.nodeArchitectures),
		},
	}
	if err := s.client.Create(ctx, configMap); err != nil {
		return fmt.Errorf("create promotion-quay configmap: %w", err)
	}
	defer func() {
		deleteCtx, cancel := context.WithTimeout(steps.CleanupCtx, 30*time.Second)
		defer cancel()
		if err := s.client.Delete(deleteCtx, configMap); err != nil {
			logrus.WithError(err).WithField("configmap", configMap.Name).Warn("failed to delete promotion-quay configmap")
		}
	}()

	pod := newPromotionPod(s.jobSpec.Namespace(), s.name, s.name, cliImage, s.nodeArchitectures,
		[]string{"/bin/sh", quayPromotionScriptMountPath + "/" + quayPromotionScriptKey}, nil, configMap.Name)
	if _, err := steps.RunPod(ctx, s.client, pod, false); err != nil {
		return fmt.Errorf("unable to run promotion-quay pod: %w", err)
	}
	return nil
}

func getQuayPromotionShell(imageMirrorTarget map[string]string, timeStr string, nodeArchitectures []string) string {
	var incomingImages, floatTags []string
	pruneTagForFloat := map[string]string{}
	var tags []string
	var resolveAndTagPairs [][2]string

	for _, k := range slices.Sorted(maps.Keys(imageMirrorTarget)) {
		if strings.Contains(k, fmt.Sprintf("%s_prune_", timeStr)) {
			pruneTagForFloat[imageMirrorTarget[k]] = k
			continue
		}
		src := imageMirrorTarget[k]
		if strings.HasPrefix(k, api.QuayOpenShiftCIRepo+":") {
			incomingImages = append(incomingImages, promotionMirrorPair(src, k+quayImageIncomingSuffix))
			floatTags = append(floatTags, k)
			continue
		}
		if !strings.Contains(k, api.ComponentFormatReplacement) {
			if quayProxyTag, ok := quayProxyTagFromISKey(k); ok {
				if ns := k[:strings.Index(k, "/")]; api.RefersToOfficialImage(ns, api.WithOKD) {
					resolveAndTagPairs = append(resolveAndTagPairs, [2]string{quayProxyTag, k})
					continue
				}
			}
		}
		tags = append(tags, fmt.Sprintf("%s %s", src, k))
	}

	registryConfig := promotionRegistryConfigPath()
	filterOS := promotionCLIImageInfoFilterOS(nodeArchitectures)
	script := []string{mirrorShellFunction}
	if len(incomingImages) > 0 {
		script = append(script, getMirrorRetryShell(registryConfig, incomingImages))
	}
	sort.Strings(floatTags)
	for _, floatTag := range floatTags {
		script = append(script, getStagedQuayFloatPromotionShell(registryConfig, floatTag, pruneTagForFloat[floatTag], filterOS))
	}
	for _, pair := range resolveAndTagPairs {
		script = append(script, getResolveAndTagRetryShell(registryConfig, pair[0], pair[1], 2, filterOS))
	}
	if len(tags) > 0 {
		script = append(script, tagRetryShell(2, `"Tag attempt $r"`, getTagLoopCommand(tags, 2), ""))
	}
	return strings.Join(script, "\n")
}

func getResolveAndTagRetryShell(registryConfig, quayProxyTag, isTag string, loglevel int, filterByOS string) string {
	colon := strings.LastIndex(quayProxyTag, ":")
	if colon == -1 {
		return fmt.Sprintf("echo promotion-quay: malformed quay proxy tag %q >&2\nexit 1", quayProxyTag)
	}
	repo := quayProxyTag[:colon]
	quayIOTag := strings.Replace(quayProxyTag, api.QCIAPPCIDomain, "quay.io", 1)
	n := quayPromotionDigestTagAttempts
	// Prefer Manifest List dig so ocp IS references the multi-arch index (children stay
	// reachable while a Quay tag holds that list). Fall back to Digest for single-arch.
	return fmt.Sprintf(`for r in {1..%d}; do
  _info=$(oc image info --registry-config=%s --filter-by-os=%s %s)
  _digest=$(echo "${_info}" | sed -n '/^Manifest List:[[:space:]]/s/^Manifest List:[[:space:]]*//p' | head -n1)
  if [ -z "${_digest}" ]; then
    _digest=$(echo "${_info}" | sed -n '/^Digest:[[:space:]]/s/^Digest:[[:space:]]*//p' | head -n1)
  fi
  if [ -n "${_digest}" ] && oc tag --source=docker --loglevel=%d --reference-policy='source' --import-mode='PreserveOriginal' --reference %s@${_digest} %s; then
    break
  fi
  echo "promotion: digest-tag failed for %s attempt ${r}/%d (QCI digest may have moved after mirror)" >&2
  if [ "${r}" -eq %d ]; then
    exit 1
  fi
  echo "promotion: retrying digest-tag for %s (attempt $((r+1))/%d after randomized backoff)" >&2
  backoff=$(($RANDOM %% %d))s
  sleep "${backoff}"
done
`, n, registryConfig, filterByOS, quayIOTag, loglevel, repo, isTag,
		isTag, n, n, isTag, n, 120)
}

func getStagedQuayFloatPromotionShell(registryConfig, floatTag, pruneTag, filterByOS string) string {
	// Must use --filter-by-os: oc image info exits non-zero on multi-arch floats without it,
	// which skipped _prune_ backups and let Quay GC prior digests under active payload jobs.
	checkOld := fmt.Sprintf(`_OLD_EXISTS=false
if oc image info --registry-config=%s --filter-by-os=%s %s >/dev/null 2>&1; then _OLD_EXISTS=true; fi`, registryConfig, filterByOS, floatTag)

	var backupPairs []string
	if pruneTag != "" {
		backupPairs = append(backupPairs, promotionMirrorPair(floatTag, pruneTag))
	}
	backupPairs = append(backupPairs, promotionMirrorPair(floatTag, floatTag+quayImagePreSuffix))

	return strings.Join([]string{
		checkOld,
		ifOldExistsMirrors(registryConfig, backupPairs...),
		getMirrorRetryShell(registryConfig, []string{promotionMirrorPair(floatTag+quayImageIncomingSuffix, floatTag)}),
		ifOldExistsMirrors(registryConfig, promotionMirrorPair(floatTag, floatTag+quayImagePost1Suffix)),
	}, "\n")
}

func ifOldExistsMirrors(registryConfig string, pairs ...string) string {
	return fmt.Sprintf(`if [ "${_OLD_EXISTS}" = "true" ]; then
%s
fi`, indentShell(getMirrorRetryShell(registryConfig, pairs)))
}

func indentShell(script string) string {
	lines := strings.Split(script, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = "  " + line
		}
	}
	return strings.Join(lines, "\n")
}

func promotionMirrorPair(src, dst string) string {
	return fmt.Sprintf("%s=%s", src, dst)
}

func getPromotionPod(imageMirrorTarget map[string]string, timeStr string, namespace string, name string, cliImage string, nodeArchitectures []string) *coreapi.Pod {
	var images, pruneImages, tags []string

	for _, k := range slices.Sorted(maps.Keys(imageMirrorTarget)) {
		if strings.Contains(k, fmt.Sprintf("%s_prune_", timeStr)) {
			pruneImages = append(pruneImages, promotionMirrorPair(imageMirrorTarget[k], k))
			continue
		}
		src := imageMirrorTarget[k]
		if strings.HasPrefix(k, api.QuayOpenShiftCIRepo+":") {
			images = append(images, promotionMirrorPair(src, k))
		} else if strings.Contains(k, api.ComponentFormatReplacement) || !strings.Contains(src, "@sha256:") || strings.Contains(src, api.QCIAPPCIDomain) {
			tags = append(tags, fmt.Sprintf("%s %s", src, k))
		} else {
			images = append(images, promotionMirrorPair(src, k))
		}
	}

	registryConfig := promotionRegistryConfigPath()
	var commands []string
	if len(images) > 0 {
		commands = append(commands, getMirrorRetryShell(registryConfig, images))
	}
	if len(tags) > 0 {
		commands = append(commands, tagRetryShell(5, "Tag attempt $r", getTagLoopCommand(tags, 2), tagRetryBackoff))
	}

	script := []string{mirrorShellFunction}
	if len(pruneImages) > 0 {
		// See https://github.com/openshift/release/blob/2080ec4a49337c27577a4b2ff08a538e96436e65/hack/qci_registry_pruner.py for details.
		script = append(script, fmt.Sprintf("%s || true", getMirrorCommand(registryConfig, pruneImages, 2)))
	}
	script = append(script, commands...)
	return newPromotionPod(namespace, name, "promotion", cliImage, nodeArchitectures, []string{"/bin/sh", "-c"}, []string{strings.Join(script, "\n")}, "")
}

func newPromotionPod(namespace, name, containerName, cliImage string, nodeArchitectures []string, command, args []string, configMapName string) *coreapi.Pod {
	volumes := promotionPodVolumes()
	mounts := promotionPodVolumeMounts()
	if configMapName != "" {
		volumes = append(volumes, coreapi.Volume{
			Name: quayPromotionScriptVolume,
			VolumeSource: coreapi.VolumeSource{
				ConfigMap: &coreapi.ConfigMapVolumeSource{
					LocalObjectReference: coreapi.LocalObjectReference{Name: configMapName},
					Items:                []coreapi.KeyToPath{{Key: quayPromotionScriptKey, Path: quayPromotionScriptKey}},
				},
			},
		})
		mounts = append(mounts, coreapi.VolumeMount{
			Name: quayPromotionScriptVolume, MountPath: quayPromotionScriptMountPath, ReadOnly: true,
		})
	}

	container := coreapi.Container{
		Name:    containerName,
		Image:   cliImage,
		Command: command,
		Env: []coreapi.EnvVar{
			{Name: "KUBECONFIG", Value: promotionPodKubeconfigPath},
			{Name: "KUBECACHEDIR", Value: "/tmp/.kube/cache"},
		},
		VolumeMounts: mounts,
	}
	if len(args) > 0 {
		container.Args = args
	}

	return &coreapi.Pod{
		ObjectMeta: meta.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{steps.AnnotationSaveContainerLogs: "true"},
		},
		Spec: coreapi.PodSpec{
			NodeSelector:  promotionPodNodeSelector(cliImage, nodeArchitectures),
			RestartPolicy: coreapi.RestartPolicyNever,
			Containers:    []coreapi.Container{container},
			Volumes:       volumes,
		},
	}
}

func promotionPodNodeSelector(cliImage string, nodeArchitectures []string) map[string]string {
	nodeSelector := map[string]string{"kubernetes.io/arch": "amd64"}
	archs := sets.New[string](nodeArchitectures...)
	if cliImage == "stable:cli" && !archs.Has("amd64") && archs.Has("arm64") {
		nodeSelector = map[string]string{"kubernetes.io/arch": "arm64"}
	}
	return nodeSelector
}

func promotionPodVolumeMounts() []coreapi.VolumeMount {
	return []coreapi.VolumeMount{
		{Name: "push-secret", MountPath: api.RegistryPushCredentialsCICentralSecretMountPath, ReadOnly: true},
		{Name: "app-ci-kubeconfig", MountPath: "/etc/app-ci-kubeconfig", ReadOnly: true},
	}
}

func promotionPodVolumes() []coreapi.Volume {
	return []coreapi.Volume{
		{Name: "push-secret", VolumeSource: coreapi.VolumeSource{
			Secret: &coreapi.SecretVolumeSource{SecretName: api.RegistryPushCredentialsCICentralSecret},
		}},
		{Name: "app-ci-kubeconfig", VolumeSource: coreapi.VolumeSource{
			Secret: &coreapi.SecretVolumeSource{SecretName: api.PromotionQuayTaggerKubeconfigSecret},
		}},
	}
}

func promotionRegistryConfigPath() string {
	return api.RegistryPushCredentialsCICentralSecretMountPath + "/" + coreapi.DockerConfigJsonKey
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

// toPromote determines the mapping of local tag to external tag which should be promoted.
// skippedImages excludes tools not built when build_if_affected is enabled.
func toPromote(config api.PromotionTarget, images []api.ProjectDirectoryImageBuildStepConfiguration, requiredImages, skippedImages sets.Set[string]) (map[string]string, sets.Set[string]) {
	tagsByDst := map[string]string{}
	names := sets.New[string]()

	if config.Disabled {
		return tagsByDst, names
	}

	for _, image := range images {
		tag := string(image.To)
		if skippedImages.Has(tag) && !requiredImages.Has(tag) {
			continue
		}
		// if the image is required or non-optional, include it in promotion
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
	skippedImages  sets.Set[string]
	commitSha      string
}

type PromotedTagsOption func(options *PromotedTagsOptions)

// WithRequiredImages ensures that the images are promoted, even if they would otherwise be skipped.
func WithRequiredImages(images sets.Set[string]) PromotedTagsOption {
	return func(options *PromotedTagsOptions) {
		options.requiredImages = images
	}
}

// WithSkippedImages excludes tools not built when build_if_affected is enabled.
func WithSkippedImages(images sets.Set[string]) PromotedTagsOption {
	return func(options *PromotedTagsOptions) {
		options.skippedImages = images
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

	skippedImages := sets.Set[string]{}
	if configuration.Images.BuildIfAffected {
		skippedImages = opts.skippedImages
	}

	for _, target := range api.PromotionTargets(configuration.PromotionConfiguration) {
		tags, names := toPromote(target, configuration.Images.Items, opts.requiredImages, skippedImages)
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
	skippedImages sets.Set[string],
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
		skippedImages:     skippedImages,
		jobSpec:           jobSpec,
		client:            client,
		pushSecret:        pushSecret,
		registry:          registry,
		mirrorFunc:        mirrorFunc,
		targetNameFunc:    targetNameFunc,
		nodeArchitectures: nodeArchitectures,
	}
}
