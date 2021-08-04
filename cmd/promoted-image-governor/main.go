package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/sirupsen/logrus"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/logrusutil"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/steps/release"
)

type options struct {
	ciOperatorconfigPath             string
	kubeconfig                       string
	dryRun                           bool
	ignoredImageStreamTagsRaw        flagutil.Strings
	ignoredImageStreamTags           []*regexp.Regexp
	releaseControllerMirrorConfigDir string

	openshiftMappingDir        string
	openshiftMappingConfigPath string
	openshiftMappingConfig     *OpenshiftMappingConfig
}

func parseOptions() *options {
	opts := &options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	// Controller-Runtimes root package imports the package that sets this flag
	kubeconfigFlagDescription := "Path to the kubeconfig file to use for CLI requests."
	if f := fs.Lookup("kubeconfig"); f != nil {
		f.Usage = kubeconfigFlagDescription
		// https://i.kym-cdn.com/entries/icons/original/000/018/012/this_is_fine.jpeg
		defer func() { opts.kubeconfig = f.Value.String() }()
	} else {
		fs.StringVar(&opts.kubeconfig, "kubeconfig", "", kubeconfigFlagDescription)
	}
	fs.StringVar(&opts.ciOperatorconfigPath, "ci-operator-config-path", "", "Path to the ci operator config")
	fs.BoolVar(&opts.dryRun, "dry-run", true, "Whether to run the controller-manager with dry-run")
	fs.Var(&opts.ignoredImageStreamTagsRaw, "ignored-image-stream-tags", "A regex to match tag in the form of namespace/name:tag format. Can be passed multiple times.")
	fs.StringVar(&opts.releaseControllerMirrorConfigDir, "release-controller-mirror-config-dir", "", "Path to the release controller mirror config directory")
	fs.StringVar(&opts.openshiftMappingDir, "openshift-mapping-dir", "", "Path to the openshift mapping directory")
	fs.StringVar(&opts.openshiftMappingConfigPath, "openshift-mapping-config", "", "Path to the openshift mapping config file")
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse input")
	}
	return opts
}

func (o *options) validate() error {
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
			logrus.WithError(err).Fatal("could not load openshift mapping config")
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
	return nil
}

func loadMappingConfig(path string) (*OpenshiftMappingConfig, error) {
	data, err := ioutil.ReadFile(path)
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

func tagsToDelete(ctx context.Context, client ctrlruntimeclient.Client, promotedTags []api.ImageStreamTagReference, toIgnore []*regexp.Regexp, imageStreamRefs []ImageStreamRef) (map[api.ImageStreamTagReference]interface{}, error) {
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
				errs = append(errs, fmt.Errorf("could not get image stream %s in namespace %s", objectKey.Name, objectKey.Namespace))
			} else {
				logrus.WithField("objectKey", objectKey).Warn("image stream not found")
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
		return nil, utilerrors.NewAggregate(errs)
	}
	return tagsToCheck, nil
}

func mirroredTagsByReleaseController(ctx context.Context, client ctrlruntimeclient.Client, refs []ImageStreamRef) ([]api.ImageStreamTagReference, error) {
	var ret []api.ImageStreamTagReference
	for _, ref := range refs {
		imageStream := &imagev1.ImageStream{}
		if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: "ocp", Name: ref.Name}, imageStream); err != nil {
			return nil, fmt.Errorf("could not get image stream %s in namespace ocp", ref.Name)
		}
		excludedTags := sets.NewString(ref.ExcludeTags...)
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

type ReleaseControllerMirrorConfig struct {
	Publish Publish `json:"publish"`
}

type Publish struct {
	MirrorToOrigin MirrorToOrigin `json:"mirror-to-origin"`
}

type MirrorToOrigin struct {
	ImageStreamRef ImageStreamRef `json:"imageStreamRef"`
}

type ImageStreamRef struct {
	Namespace   string   `json:"namespace"`
	Name        string   `json:"name"`
	ExcludeTags []string `json:"excludeTags,omitempty"`
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
// Those mappings will be stored in https://github.com/openshift/release/tree/master/core-services/image-mirroring/openshift
// and then used by the periodic-image-mirroring-openshift job
func generateMappings(promotedTags []api.ImageStreamTagReference, mappingConfig *OpenshiftMappingConfig, imageStreamRefs []ImageStreamRef) map[string]map[string][]string {
	mappings := map[string]map[string][]string{}
	for _, tag := range promotedTags {
		// mirror the images if it is promoted or it is mirrored by the release controllers from OCP image streams
		if tag.Namespace == mappingConfig.SourceNamespace || isMirroredFromOCP(tag, imageStreamRefs) {
			if mappingConfig.Images != nil {
				if targetTags, ok := mappingConfig.Images[tag.Name]; ok {
					for _, targetTag := range targetTags {
						filename := fmt.Sprintf("mapping_origin_%s", strings.ReplaceAll(tag.Name, ".", "_"))
						_, ok := mappings[filename]
						if !ok {
							mappings[filename] = map[string][]string{}
						}
						src := fmt.Sprintf("%s/%s/%s:%s", mappingConfig.SourceRegistry, mappingConfig.SourceNamespace, tag.Name, tag.Tag)
						mappings[filename][src] = append(mappings[filename][src],
							fmt.Sprintf("%s/%s/%s-%s:%s", mappingConfig.TargetRegistry, mappingConfig.TargetNamespace, mappingConfig.SourceNamespace, tag.Tag, targetTag))

					}
				}
			}
		}
	}
	return mappings
}

// isMirroredFromOCP checks if the image is mirrored by the release controllers
// See https://github.com/openshift/release/blob/0cb6f403581ac09a9112744332b504612a3b7267/core-services/release-controller/_releases/release-ocp-4.6-ci.json#L10 as an example
func isMirroredFromOCP(tag api.ImageStreamTagReference, refs []ImageStreamRef) bool {
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
		return true
	}
	return false
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

	var imageStreamRefs []ImageStreamRef
	if err := filepath.Walk(abs,
		func(path string, info os.FileInfo, err error) error {
			if err != nil {
				logrus.WithField("source-file", path).WithError(err).Error("Failed to walk release controller mirror config dir")
				return err
			}
			if strings.HasSuffix(path, ".json") {
				data, err := ioutil.ReadFile(path)
				if err != nil {
					logrus.WithField("source-file", path).WithError(err).Error("Failed to read file")
					return err
				}
				c := &ReleaseControllerMirrorConfig{}
				if err := json.Unmarshal(data, c); err != nil {
					logrus.WithField("source-file", path).WithError(err).Error("Failed to unmarshal ReleaseControllerMirrorConfig")
					return err
				}
				ref := c.Publish.MirrorToOrigin.ImageStreamRef
				if ref.Namespace == "" {
					logrus.WithField("source-file", path).Warn("publish.mirror-to-origin.imageStreamRef.namespace is empty")
				}
				if ref.Name == "" {
					logrus.WithField("source-file", path).Warn("publish.mirror-to-origin.imageStreamRef.name is empty")
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
	if err := config.OperateOnCIOperatorConfigDir(abs, func(cfg *api.ReleaseBuildConfiguration, metadata *config.Info) error {
		promotedTags = append(promotedTags, release.PromotedTags(cfg)...)
		return nil
	}); err != nil {
		logrus.WithField("path", abs).Fatal("failed to operate on CI Operator's config directory")
	}

	if opts.openshiftMappingConfigPath != "" {
		mappings := generateMappings(promotedTags, opts.openshiftMappingConfig, imageStreamRefs)
		for filename, mapping := range mappings {
			var b bytes.Buffer
			keys := make([]string, 0, len(mapping))
			for k := range mapping {
				keys = append(keys, k)
			}
			sort.Strings(keys)

			for _, src := range keys {
				b.WriteString(fmt.Sprintf("%s\n", strings.Join(append([]string{src}, mapping[src]...), " ")))
			}
			f := filepath.Join(opts.openshiftMappingDir, filename)
			logrus.WithField("filename", f).Info("Writing to file")
			if err := ioutil.WriteFile(f, b.Bytes(), 0644); err != nil {
				logrus.WithError(err).WithField("filename", f).Fatal("could not write to file")
			}
		}
		return
	}

	kubeConfig, err := clientcmd.BuildConfigFromFlags("", opts.kubeconfig)
	if err != nil {
		logrus.WithError(err).Fatalf("could not load kube config from path %s", opts.kubeconfig)
	}

	client, err := ctrlruntimeclient.New(kubeConfig, ctrlruntimeclient.Options{})
	if err != nil {
		logrus.WithError(err).Fatalf("could not create client")
	}

	ctx := interrupts.Context()
	toDelete, err := tagsToDelete(ctx, client, promotedTags, opts.ignoredImageStreamTags, imageStreamRefs)
	if err != nil {
		logrus.WithError(err).Fatal("could not get tags to delete")
	}

	var errs []error
	for tag := range toDelete {
		logrus.WithField("tag", tag.ISTagName()).Info("deleting tag")
		if opts.dryRun {
			continue
		}
		if err := client.Delete(ctx, &imagev1.ImageStreamTag{ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s:%s", tag.Name, tag.Tag),
			Namespace: tag.Namespace,
		},
		}); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		logrus.WithError(utilerrors.NewAggregate(errs)).Fatal("could not delete tags")
	}
}
