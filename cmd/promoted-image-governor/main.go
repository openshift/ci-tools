package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/logrusutil"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

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
	ignoredImageStreamTags           []api.ImageStreamTagReference
	releaseControllerMirrorConfigDir string
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
	fs.Var(&opts.ignoredImageStreamTagsRaw, "ignored-image-stream-tags", "An imagestreamtag to ignore while checking. It must be in namespace/name:tag format (e.G `ci/clonerefs:latest`). Can be passed multiple times.")
	fs.StringVar(&opts.releaseControllerMirrorConfigDir, "release-controller-mirror-config-dir", "", "Path to the release controller mirror config directory")
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
	for _, s := range o.ignoredImageStreamTagsRaw.Strings() {
		namespaceAndTag := strings.Split(s, "/")
		if len(namespaceAndTag) != 2 {
			return fmt.Errorf("--ignored-image-stream-tags %s must be in namespace/name:tag format", s)
		}
		isAndTag := strings.Split(namespaceAndTag[1], ":")
		if len(isAndTag) != 2 {
			return fmt.Errorf("--ignored-image-stream-tags %s must be in namespace/name:tag format", s)
		}
		o.ignoredImageStreamTags = append(o.ignoredImageStreamTags, api.ImageStreamTagReference{
			Namespace: namespaceAndTag[0],
			Name:      isAndTag[0],
			Tag:       isAndTag[1],
		})
	}
	return nil
}

func addSchemes() error {
	if err := imagev1.AddToScheme(scheme.Scheme); err != nil {
		return fmt.Errorf("failed to add imagev1 to scheme: %w", err)
	}
	return nil
}

func validatePromotedTags(tags []api.ImageStreamTagReference) error {
	tagCount := map[api.ImageStreamTagReference]int{}
	for _, tag := range tags {
		if _, ok := tagCount[tag]; ok {
			tagCount[tag] = tagCount[tag] + 1
		} else {
			tagCount[tag] = 1
		}
	}
	var dups []string
	for tag, c := range tagCount {
		if c > 1 {
			dups = append(dups, tag.ISTagName())
		}
	}
	if len(dups) > 0 {
		return fmt.Errorf("found tags promoted more than once: %s", dups)
	}
	return nil
}

func tagsToDelete(ctx context.Context, client ctrlruntimeclient.Client, promotedTags []api.ImageStreamTagReference, toIgnore []api.ImageStreamTagReference, imageStreamRefs []ImageStreamRef) ([]api.ImageStreamTagReference, error) {
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
	for _, tag := range toIgnore {
		delete(tagsToCheck, tag)
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
	var tagsToDelete []api.ImageStreamTagReference
	for tag := range tagsToCheck {
		tagsToDelete = append(tagsToDelete, tag)
	}
	return tagsToDelete, nil
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
			if strings.HasSuffix(path, "-ci.json") {
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

	kubeConfig, err := clientcmd.BuildConfigFromFlags("", opts.kubeconfig)
	if err != nil {
		logrus.WithError(err).Fatalf("could not load kube config from path %s", opts.kubeconfig)
	}

	client, err := ctrlruntimeclient.New(kubeConfig, ctrlruntimeclient.Options{})
	if err != nil {
		logrus.WithError(err).Fatalf("could not create client")
	}

	if err := validatePromotedTags(promotedTags); err != nil {
		logrus.WithError(err).Fatalf("could not validate promoted tags")
	}

	toDelete, err := tagsToDelete(context.TODO(), client, promotedTags, opts.ignoredImageStreamTags, imageStreamRefs)
	if err != nil {
		logrus.WithError(err).Fatal("could not get tags to delete")
	}

	if opts.dryRun {
		logrus.WithField("len", len(toDelete)).WithField("toDelete", toDelete).Info("Tags to delete")
	} else {
		logrus.Info("TODO: delete tags for real")
	}
}
