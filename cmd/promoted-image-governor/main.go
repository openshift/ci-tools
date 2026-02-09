package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/sirupsen/logrus"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes/scheme"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/kube"
	"sigs.k8s.io/prow/pkg/logrusutil"
	"sigs.k8s.io/yaml"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	releaseconfig "github.com/openshift/ci-tools/pkg/release/config"
	"github.com/openshift/ci-tools/pkg/steps/release"
)

type options struct {
	ciOperatorconfigPath             string
	kubernetesOptions                flagutil.KubernetesOptions
	dryRun                           bool
	ignoredImageStreamTagsRaw        flagutil.Strings
	ignoredImageStreamTags           []*regexp.Regexp
	releaseControllerMirrorConfigDir string

	openshiftMappingDir        string
	openshiftMappingConfigPath string
	openshiftMappingConfig     *OpenshiftMappingConfig

	explainsRaw flagutil.Strings
	explains    map[api.ImageStreamTagReference]string

	logLevel string
}

func parseOptions() *options {
	opts := &options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	opts.kubernetesOptions.AddFlags(fs)
	fs.StringVar(&opts.logLevel, "log-level", "info", fmt.Sprintf("Log level is one of %v.", logrus.AllLevels))
	fs.StringVar(&opts.ciOperatorconfigPath, "ci-operator-config-path", "", "Path to the ci operator config")
	fs.BoolVar(&opts.dryRun, "dry-run", true, "Whether to run the controller-manager with dry-run")
	fs.Var(&opts.ignoredImageStreamTagsRaw, "ignored-image-stream-tags", "A regex to match tag in the form of namespace/name:tag format. Can be passed multiple times.")
	fs.StringVar(&opts.releaseControllerMirrorConfigDir, "release-controller-mirror-config-dir", "", "Path to the release controller mirror config directory")
	fs.StringVar(&opts.openshiftMappingDir, "openshift-mapping-dir", "", "Path to the openshift mapping directory")
	fs.StringVar(&opts.openshiftMappingConfigPath, "openshift-mapping-config", "", "Path to the openshift mapping config file")
	fs.Var(&opts.explainsRaw, "explain", "An imagestreamtag to explain its existence. It must be in namespace/name:tag format (e.G `ci/clonerefs:latest`). Can be passed multiple times.")
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse args")
	}
	return opts
}

func (o *options) validate() error {
	level, err := logrus.ParseLevel(o.logLevel)
	if err != nil {
		return fmt.Errorf("invalid log level specified: %w", err)
	}
	logrus.SetLevel(level)
	if o.ciOperatorconfigPath == "" {
		return fmt.Errorf("--ci-operator-config-path must be set")
	}
	if o.releaseControllerMirrorConfigDir == "" {
		return fmt.Errorf("--release-controller-mirror-config-dir must be set")
	}
	if (o.openshiftMappingDir == "") != (o.openshiftMappingConfigPath == "") {
		return fmt.Errorf("--openshift-mapping-dir and --openshift-mapping-config must be set together")
	}
	if o.openshiftMappingConfigPath != "" {
		c, err := loadMappingConfig(o.openshiftMappingConfigPath)
		if err != nil {
			return fmt.Errorf("could not load openshift mapping config: %w", err)
		}
		o.openshiftMappingConfig = c
	}
	for _, s := range o.ignoredImageStreamTagsRaw.Strings() {
		re, err := regexp.Compile(s)
		if err != nil {
			return fmt.Errorf("failed to compile regex from %q: %w", s, err)
		}
		logrus.WithField("re", re.String()).Info("Ignore tags as required by flag")
		o.ignoredImageStreamTags = append(o.ignoredImageStreamTags, re)
	}

	if o.openshiftMappingConfigPath != "" && len(o.explainsRaw.Strings()) > 0 {
		return fmt.Errorf("--openshift-mapping-config and --explain cannot be set together")
	}

	if err := o.kubernetesOptions.Validate(o.dryRun); err != nil {
		return err
	}

	o.explains = map[api.ImageStreamTagReference]string{}
	var errs []error
	for _, val := range o.explainsRaw.Strings() {
		slashSplit := strings.Split(val, "/")
		if len(slashSplit) != 2 {
			errs = append(errs, fmt.Errorf("--explain value %s was not in namespace/name:tag format", val))
			continue
		}
		dotSplit := strings.Split(slashSplit[1], ":")
		if len(dotSplit) != 2 {
			errs = append(errs, fmt.Errorf("name in --explain must be of imagestreamname:tag format, wasn't the case for %s", slashSplit[1]))
			continue
		}
		o.explains[api.ImageStreamTagReference{
			Namespace: slashSplit[0],
			Name:      dotSplit[0],
			Tag:       dotSplit[1],
		}] = explanationUnknown
	}
	return utilerrors.NewAggregate(errs)
}

const (
	explanationUnknown = "unknown"
	appCIContextName   = string(api.ClusterAPPCI)
)

func loadMappingConfig(path string) (*OpenshiftMappingConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %s", path)
	}
	openshiftMappingConfig := &OpenshiftMappingConfig{}
	if err = yaml.Unmarshal(data, openshiftMappingConfig); err != nil {
		return nil, fmt.Errorf("failed to unmarshal the config %q: %w", string(data), err)
	}
	return openshiftMappingConfig, nil
}

func addSchemes() error {
	if err := imagev1.AddToScheme(scheme.Scheme); err != nil {
		return fmt.Errorf("failed to add imagev1 to scheme: %w", err)
	}
	return nil
}

func tagsToDelete(ctx context.Context, client ctrlruntimeclient.Client, promotedTags []api.ImageStreamTagReference, toIgnore []*regexp.Regexp, imageStreamRefs []releaseconfig.ImageStreamRef) (map[api.ImageStreamTagReference]interface{}, map[ctrlruntimeclient.ObjectKey]interface{}, error) {
	imageStreamsWithPromotedTags := map[ctrlruntimeclient.ObjectKey]interface{}{}
	for _, promotedTag := range promotedTags {
		imageStreamsWithPromotedTags[ctrlruntimeclient.ObjectKey{Namespace: promotedTag.Namespace, Name: promotedTag.Name}] = nil
	}

	tagsToCheck := map[api.ImageStreamTagReference]interface{}{}
	var errs []error
	for objectKey := range imageStreamsWithPromotedTags {
		imageStream := &imagev1.ImageStream{}
		if err := client.Get(ctx, objectKey, imageStream); err != nil {
			if !kerrors.IsNotFound(err) {
				errs = append(errs, fmt.Errorf("could not get image stream %s in namespace %s: %w", objectKey.Name, objectKey.Namespace, err))
			} else {
				logrus.WithField("objectKey", objectKey).Debug("image stream not found")
			}
			continue
		}
		for _, tag := range imageStream.Status.Tags {
			tagsToCheck[api.ImageStreamTagReference{Namespace: imageStream.Namespace, Name: imageStream.Name, Tag: tag.Tag}] = nil
		}
	}

	for _, promotedTag := range promotedTags {
		delete(tagsToCheck, promotedTag)
	}
	for tag := range tagsToCheck {
		for _, re := range toIgnore {
			if re.MatchString(tag.ISTagName()) {
				logrus.WithField("tag", tag.ISTagName()).Info("Ignored tag")
				delete(tagsToCheck, tag)
			}
		}
	}
	mirroredTags, err := mirroredTagsByReleaseController(ctx, client, imageStreamRefs)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to get the mirrored tags by release controller: %w", err))
	}
	for _, tag := range mirroredTags {
		delete(tagsToCheck, tag)
	}
	if len(errs) > 0 {
		return nil, nil, utilerrors.NewAggregate(errs)
	}
	return tagsToCheck, imageStreamsWithPromotedTags, nil
}

func mirroredTagsByReleaseController(ctx context.Context, client ctrlruntimeclient.Client, refs []releaseconfig.ImageStreamRef) ([]api.ImageStreamTagReference, error) {
	var ret []api.ImageStreamTagReference
	for _, ref := range refs {
		imageStream := &imagev1.ImageStream{}
		if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: "ocp", Name: ref.Name}, imageStream); err != nil {
			return nil, fmt.Errorf("could not get image stream %s in namespace ocp", ref.Name)
		}
		excludedTags := sets.New[string](ref.ExcludeTags...)
		for _, tag := range imageStream.Status.Tags {
			if !excludedTags.Has(tag.Tag) {
				ret = append(ret, api.ImageStreamTagReference{
					Namespace: ref.Namespace,
					Name:      ref.Name,
					Tag:       tag.Tag,
				})
			}
		}
	}

	return ret, nil
}

// OpenshiftMappingConfig for openshift image mapping files
type OpenshiftMappingConfig struct {
	SourceRegistry  string              `json:"source_registry"`
	TargetRegistry  string              `json:"target_registry"`
	SourceNamespace string              `json:"source_namespace"`
	TargetNamespace string              `json:"target_namespace"`
	Images          map[string][]string `json:"images,omitempty"`
}

// generateMappings generates the mappings to mirror the images
// Those mappings will be stored in https://github.com/openshift/release/tree/main/core-services/image-mirroring/openshift
// and then used by the periodic-image-mirroring-openshift job
func generateMappings(promotedTags []api.ImageStreamTagReference, mappingConfig *OpenshiftMappingConfig, imageStreamRefs []releaseconfig.ImageStreamRef) (map[string]map[string]sets.Set[string], error) {
	mappings := map[string]map[string]sets.Set[string]{}
	processedTags := sets.NewString()
	var errs []error
	for _, tag := range promotedTags {
		if processedTags.Has(tag.ISTagName()) {
			logrus.WithField("tag", tag.ISTagName()).Warn("Skipping processed tag ...")
			continue
		}
		processedTags.Insert(tag.ISTagName())
		// mirror the images if it is promoted or it is mirrored by the release controllers from OCP image streams
		if tag.Namespace == mappingConfig.SourceNamespace || isMirroredFromOCP(tag, imageStreamRefs) {
			if mappingConfig.Images != nil {
				if targetTags, ok := mappingConfig.Images[tag.Name]; ok {
					for _, targetTag := range targetTags {
						filename := fmt.Sprintf("mapping_origin_%s", strings.ReplaceAll(tag.Name, ".", "_"))
						if _, ok := mappings[filename]; !ok {
							mappings[filename] = map[string]sets.Set[string]{}
						}
						src := fmt.Sprintf("%s/%s/%s:%s", mappingConfig.SourceRegistry, mappingConfig.SourceNamespace, tag.Name, tag.Tag)
						dst := fmt.Sprintf("%s/%s/%s-%s:%s", mappingConfig.TargetRegistry, mappingConfig.TargetNamespace, mappingConfig.SourceNamespace, tag.Tag, targetTag)
						if _, ok = mappings[filename][src]; !ok {
							mappings[filename][src] = sets.New[string]()
						}
						if mappings[filename][src].Has(dst) {
							errs = append(errs, fmt.Errorf("cannot define the same mirroring destination %s more than once for the source %s in filename %s", dst, src, filename))
						}
						logrus.WithField("filename", filename).WithField("src", src).WithField("dst", dst).
							WithField("tag.Namespace", tag.Namespace).
							WithField("mappingConfig.SourceNamespace", mappingConfig.SourceNamespace).
							Debug("Insert into mapping ...")
						mappings[filename][src].Insert(dst)
					}
				}
			}
		}
	}
	if len(errs) > 0 {
		return nil, utilerrors.NewAggregate(errs)
	}
	return mappings, nil
}

// isMirroredFromOCP checks if the image is mirrored by the release controllers
// See https://github.com/openshift/release/blob/0cb6f403581ac09a9112744332b504612a3b7267/core-services/release-controller/_releases/release-ocp-4.6-ci.json#L10 as an example
func isMirroredFromOCP(tag api.ImageStreamTagReference, refs []releaseconfig.ImageStreamRef) bool {
	if tag.Namespace != "ocp" {
		return false
	}
	for _, ref := range refs {
		if tag.Name != ref.Name {
			continue
		}
		for _, eTagName := range ref.ExcludeTags {
			if eTagName == tag.Tag {
				return false
			}
		}
		logrus.WithField("tag", tag.ISTagName()).Debug("mirrored from OCP")
		return true
	}
	return false
}

func deleteTagsOnBuildFarm(ctx context.Context, appCIClient ctrlruntimeclient.Client, buildClusterClients map[string]ctrlruntimeclient.Client, imageStreamsWithPromotedTags map[ctrlruntimeclient.ObjectKey]interface{}, dryRun bool) error {
	var errs []error
	for streamKey := range imageStreamsWithPromotedTags {
		for cluster, client := range buildClusterClients {
			imageStream := &imagev1.ImageStream{}
			if err := client.Get(ctx, streamKey, imageStream); err != nil {
				if !kerrors.IsNotFound(err) {
					errs = append(errs, fmt.Errorf("could not get image stream %s in namespace %s on cluster %s: %w", streamKey.Name, streamKey.Namespace, cluster, err))
				} else {
					logrus.WithField("cluster", cluster).WithField("streamKey", streamKey).Debug("image stream not found")
				}
				continue
			}

			appCIImageStream := &imagev1.ImageStream{}
			if err := appCIClient.Get(ctx, streamKey, appCIImageStream); err != nil {
				if !kerrors.IsNotFound(err) {
					errs = append(errs, fmt.Errorf("could not get image stream %s in namespace %s on cluster %s: %w", streamKey.Name, streamKey.Namespace, appCIContextName, err))
				} else {
					logrus.WithField("cluster", cluster).WithField("streamKey", streamKey).Info("deleting image stream on build farm")
					if dryRun {
						continue
					}
					if err := client.Delete(ctx, imageStream); err != nil {
						if !kerrors.IsNotFound(err) {
							errs = append(errs, fmt.Errorf("could not delete image stream %s in namespace %s on cluster %s: %w", streamKey.Name, streamKey.Namespace, cluster, err))
						} else {
							logrus.WithField("cluster", cluster).WithField("streamKey", streamKey).Debug("image stream not found upon deleting")
						}
						continue
					}
					logrus.WithField("cluster", cluster).WithField("streamKey", streamKey).Info("image stream is deleted")
				}
				continue
			}

			tags := sets.New[string]()
			for _, tag := range imageStream.Status.Tags {
				tags.Insert(tag.Tag)
			}

			appCITags := sets.New[string]()
			for _, tag := range appCIImageStream.Status.Tags {
				appCITags.Insert(tag.Tag)
			}

			for _, tag := range sets.List(tags.Difference(appCITags)) {
				isTagOnBuildFarm := &imagev1.ImageStreamTag{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: imageStream.Namespace,
						Name:      fmt.Sprintf("%s:%s", imageStream.Name, tag),
					},
				}
				tagKey := fmt.Sprintf("%s/%s", isTagOnBuildFarm.Namespace, isTagOnBuildFarm.Name)
				logrus.WithField("cluster", cluster).WithField("tagKey", tagKey).Info("deleting image stream tag on build farm")
				if dryRun {
					continue
				}
				if err := client.Delete(ctx, isTagOnBuildFarm); err != nil {
					if !kerrors.IsNotFound(err) {
						errs = append(errs, fmt.Errorf("could not delete image stream tag %s in namespace %s on cluster %s", isTagOnBuildFarm.Name, isTagOnBuildFarm.Namespace, cluster))
					} else {
						logrus.WithField("cluster", cluster).WithField("tagKey", tagKey).Debug("image stream tag not found upon deleting")
					}
					continue
				}
				logrus.WithField("cluster", cluster).WithField("tagKey", tagKey).Info("image stream tag is deleted")
			}
		}
	}
	return utilerrors.NewAggregate(errs)
}

func main() {
	logrusutil.ComponentInit()

	opts := parseOptions()

	if err := opts.validate(); err != nil {
		logrus.WithError(err).Fatal("failed to validate the option")
	}

	if err := addSchemes(); err != nil {
		logrus.WithError(err).Fatal("failed to add schemes")
	}

	abs, err := filepath.Abs(opts.releaseControllerMirrorConfigDir)
	if err != nil {
		logrus.WithError(err).Fatal("failed to determine absolute release controller mirror config path")
	}

	var imageStreamRefs []releaseconfig.ImageStreamRef
	if err := filepath.Walk(abs,
		func(path string, info os.FileInfo, err error) error {
			if err != nil {
				logrus.WithField("source-file", path).WithError(err).Error("Failed to walk release controller mirror config dir")
				return err
			}
			if strings.HasSuffix(path, ".json") {
				data, err := os.ReadFile(path)
				if err != nil {
					logrus.WithField("source-file", path).WithError(err).Error("Failed to read file")
					return err
				}
				c := &releaseconfig.Config{}
				if err := json.Unmarshal(data, c); err != nil {
					logrus.WithField("source-file", path).WithError(err).Error("Failed to unmarshal ReleaseControllerMirrorConfig")
					return err
				}
				ref := c.Publish.MirrorToOrigin.ImageStreamRef
				if ref.Namespace == "" {
					logrus.WithField("source-file", path).Debug("publish.mirror-to-origin.imageStreamRef.namespace is empty")
				}
				if ref.Name == "" {
					logrus.WithField("source-file", path).Debug("publish.mirror-to-origin.imageStreamRef.name is empty")
				}
				if ref.Namespace != "" && ref.Name != "" {
					imageStreamRefs = append(imageStreamRefs, ref)
				}
			}
			return nil
		}); err != nil {
		logrus.WithError(err).Fatal("Failed to walk release controller mirror config dir")
	}

	logrus.WithField("imageStreamRefs", imageStreamRefs).Info("Found imageStreamRefs from release controller config directory")

	abs, err = filepath.Abs(opts.ciOperatorconfigPath)
	if err != nil {
		logrus.WithError(err).Fatal("failed to determine absolute CI Operator configuration path")
	}
	var promotedTags []api.ImageStreamTagReference
	var ignoredCommitTags []*regexp.Regexp
	if err := config.OperateOnCIOperatorConfigDir(abs, func(cfg *api.ReleaseBuildConfiguration, metadata *config.Info) error {
		for _, isTagRef := range release.PromotedTags(cfg) {
			logrus.WithField("metadata", metadata).WithField("tag", isTagRef.ISTagName()).Debug("Appending promoted tag ...")
			promotedTags = append(promotedTags, isTagRef)
			if _, ok := opts.explains[isTagRef]; ok {
				opts.explains[isTagRef] = cfg.Metadata.AsString()
			}
			var taggedByCommit bool
			for _, target := range api.PromotionTargets(cfg.PromotionConfiguration) {
				taggedByCommit = taggedByCommit || target.TagByCommit
			}
			if taggedByCommit {
				ignoreRegex, err := regexp.Compile(fmt.Sprintf("%s/%s:[0-9a-f]{5,40}", isTagRef.Namespace, isTagRef.Name))
				if err != nil {
					return fmt.Errorf("could not create a regex for ignoring tagged-by-commit images for %s: %w", isTagRef.ISTagName(), err)
				}
				ignoredCommitTags = append(ignoredCommitTags, ignoreRegex)
			}
		}
		return nil
	}); err != nil {
		logrus.WithField("path", abs).Fatal("failed to operate on CI Operator's config directory")
	}

	if opts.openshiftMappingConfigPath != "" {
		mappings, err := generateMappings(promotedTags, opts.openshiftMappingConfig, imageStreamRefs)
		if err != nil {
			logrus.WithError(err).Fatal("failed to generate the openshift mapping files for image mirroring")
		}
		for filename, mapping := range mappings {
			var b bytes.Buffer
			keys := make([]string, 0, len(mapping))
			for k := range mapping {
				keys = append(keys, k)
			}
			sort.Strings(keys)

			for _, src := range keys {
				b.WriteString(fmt.Sprintf("%s\n", strings.Join(append([]string{src}, sets.List(mapping[src])...), " ")))
			}
			f := filepath.Join(opts.openshiftMappingDir, filename)
			logrus.WithField("filename", f).Info("Writing to file")
			if err := os.WriteFile(f, b.Bytes(), 0644); err != nil {
				logrus.WithError(err).WithField("filename", f).Fatal("could not write to file")
			}
		}
		return
	}

	kubeconfigs, err := opts.kubernetesOptions.LoadClusterConfigs()
	if err != nil {
		logrus.WithError(err).Fatal("failed to load kubeconfigs")
	}

	inClusterConfig, hasInClusterConfig := kubeconfigs[kube.InClusterContext]
	delete(kubeconfigs, kube.InClusterContext)
	delete(kubeconfigs, kube.DefaultClusterAlias)

	if _, hasAppCi := kubeconfigs[appCIContextName]; !hasAppCi {
		if !hasInClusterConfig {
			logrus.WithError(err).Fatalf("had no context for '%s' and loading InClusterConfig failed", appCIContextName)
		}
		logrus.Infof("use InClusterConfig for %s", appCIContextName)
		kubeconfigs[appCIContextName] = inClusterConfig
	}

	kubeConfig := kubeconfigs[appCIContextName]
	appCIClient, err := ctrlruntimeclient.New(&kubeConfig, ctrlruntimeclient.Options{})
	if err != nil {
		logrus.WithError(err).Fatalf("could not create client")
	}

	clients := map[string]ctrlruntimeclient.Client{}
	for cluster, kubeConfig := range kubeconfigs {
		cluster, kubeConfig := cluster, kubeConfig
		if cluster == appCIContextName {
			continue
		}
		client, err := ctrlruntimeclient.New(&kubeConfig, ctrlruntimeclient.Options{})
		if err != nil {
			logrus.WithError(err).WithField("cluster", cluster).Fatal("could not create client for cluster")
		}
		clients[cluster] = client
	}

	ctx := interrupts.Context()

	if len(opts.explains) > 0 {
		w := tabwriter.NewWriter(os.Stdout, 20, 30, 1, ' ', tabwriter.AlignRight)
		fmt.Fprintf(w, "tag\texplanation\t\n")
		for tag, e := range opts.explains {
			if e == explanationUnknown {
				name := fmt.Sprintf("%s:%s", tag.Name, tag.Tag)
				isTag := &imagev1.ImageStreamTag{}
				if err := appCIClient.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: tag.Namespace, Name: name}, isTag); err != nil {
					if kerrors.IsNotFound(err) {
						fmt.Fprintf(w, "%s\t%s\t\n", tag.ISTagName(), "imagestreamtag does not exit")
						continue
					} else {
						logrus.WithError(err).Fatalf("could not get image stream tag %s", tag.ISTagName())
					}
				}
			}
			fmt.Fprintf(w, "%s\t%s\t\n", tag.ISTagName(), e)
		}
		w.Flush()
		return
	}

	toDelete, imageStreamsWithPromotedTags, err := tagsToDelete(ctx, appCIClient, promotedTags, append(opts.ignoredImageStreamTags, ignoredCommitTags...), imageStreamRefs)
	if err != nil {
		logrus.WithError(err).Fatal("could not get tags to delete")
	}

	var errs []error
	for tag := range toDelete {
		logrus.WithField("tag", tag.ISTagName()).Info("deleting tag")
		if opts.dryRun {
			continue
		}
		if err := appCIClient.Delete(ctx, &imagev1.ImageStreamTag{ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s:%s", tag.Name, tag.Tag),
			Namespace: tag.Namespace,
		},
		}); err != nil {
			errs = append(errs, err)
			continue
		}
		logrus.WithField("tag", tag.ISTagName()).Info("image stream tag is deleted")
	}
	if len(errs) > 0 {
		logrus.WithError(utilerrors.NewAggregate(errs)).Fatal("could not delete tags")
	}

	if err := deleteTagsOnBuildFarm(ctx, appCIClient, clients, imageStreamsWithPromotedTags, opts.dryRun); err != nil {
		logrus.WithError(err).Fatal("could not delete tags on build farm")
	}
}
