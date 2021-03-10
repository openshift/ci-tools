package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"strings"

	"github.com/sirupsen/logrus"
	"gopkg.in/fsnotify.v1"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/test-infra/prow/logrusutil"
	controllerruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	imageapi "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/util"
)

const (
	apiCIContextName = "api.ci"
	appCIContextName = "app.ci"
)

type options struct {
	kubeconfig string
	dryRun     bool
}

func newOpts() *options {
	opts := &options{}
	// Controller-Runtimes root package imports the package that sets this flag
	kubeconfigFlagDescription := "The kubeconfig to use. All contexts in it will be considered a build cluster. If it does not have a context named 'app.ci', loading in-cluster config will be attempted."
	if f := flag.Lookup("kubeconfig"); f != nil {
		f.Usage = kubeconfigFlagDescription
		// https://i.kym-cdn.com/entries/icons/original/000/018/012/this_is_fine.jpeg
		defer func() { opts.kubeconfig = f.Value.String() }()
	} else {
		flag.StringVar(&opts.kubeconfig, "kubeconfig", "", kubeconfigFlagDescription)
	}

	flag.BoolVar(&opts.dryRun, "dry-run", true, "Whether to be in dry-run mode")
	flag.Parse()
	return opts
}

func main() {
	logrusutil.ComponentInit()

	opts := newOpts()

	ctx := controllerruntime.SetupSignalHandler()
	ctx, cancel := context.WithCancel(ctx)

	if err := imageapi.AddToScheme(scheme.Scheme); err != nil {
		logrus.WithError(err).Fatal("Failed to add imagev1 to scheme")
	}

	// The image api is implemented via the Openshift Extension APIServer, so contrary
	// to CRD-Based resources it supports protobuf.
	if err := apiutil.AddToProtobufScheme(imageapi.AddToScheme); err != nil {
		logrus.WithError(err).Fatal("Failed to add imagev1 api to protobuf scheme")
	}

	kubeconfigChangedCallBack := func(e fsnotify.Event) {
		logrus.WithField("event", e.String()).Info("Kubeconfig changed, exiting to get restarted by Kubelet and pick up the changes")
		cancel()
	}

	kubeconfigs, _, err := util.LoadKubeConfigs(opts.kubeconfig, kubeconfigChangedCallBack)
	if err != nil {
		logrus.WithError(err).Fatal("failed to load kubeconfigs")
	}
	if _, hasAppCi := kubeconfigs[appCIContextName]; !hasAppCi {
		kubeconfigs[appCIContextName], err = rest.InClusterConfig()
		if err != nil {
			logrus.WithError(err).Fatalf("--kubeconfig had no context for '%s' and loading InClusterConfig failed", appCIContextName)
		}
		logrus.Infof("Loaded %q context from in-cluster config", appCIContextName)
		if err := util.WatchFiles([]string{"/var/run/secrets/kubernetes.io/serviceaccount/token"}, kubeconfigChangedCallBack); err != nil {
			logrus.WithError(err).Fatal("failed to watch in-cluster token")
		}
	}

	if _, hasApiCi := kubeconfigs[apiCIContextName]; !hasApiCi {
		logrus.Fatalf("--kubeconfig had no context for '%s' and loading InClusterConfig failed", apiCIContextName)
	}

	clients := map[string]ctrlruntimeclient.Client{}
	for _, context := range []string{appCIContextName, apiCIContextName} {
		client, err := ctrlruntimeclient.New(kubeconfigs[context], ctrlruntimeclient.Options{})
		if err != nil {
			logrus.WithError(err).Fatal("could not get route client for cluster config")
		}
		clients[context] = client
	}
	mirrors, err := mirrorImages(ctx, clients)
	if err != nil {
		logrus.WithError(err).Fatal("failed to mirror images")
	}
	if err := handleMirrorImages(mirrors); err != nil {
		logrus.WithError(err).Fatal("failed to handle mirror images")
	}
	logrus.Info("Process ended gracefully")
}

func handleMirrorImages(mirrors []pair) error {
	file, err := ioutil.TempFile("", "mirror-image-")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	//defer os.Remove(file.Name())
	var sb strings.Builder
	for _, p := range mirrors {
		sb.WriteString(fmt.Sprintf("%s=%s\n", p.source, p.target))
	}
	if err := ioutil.WriteFile(file.Name(), []byte(sb.String()), 0644); err != nil {
		return fmt.Errorf("failed to write file %q: %w", file.Name(), err)
	}
	logrus.WithField("file.Name()", file.Name()).Info("saved results to file")
	return nil
}

type pair struct {
	source string
	target string
}

func mirrorImages(cxt context.Context, clients map[string]ctrlruntimeclient.Client) ([]pair, error) {
	var mirror []pair
	apiCIClient := clients[apiCIContextName]
	srcImagestreams := &imageapi.ImageStreamList{}
	//logrus.SetLevel(logrus.DebugLevel)
	//ctrlruntimeclient.InNamespace("hongkliu-test")
	if err := apiCIClient.List(cxt, srcImagestreams); err != nil {
		return nil, fmt.Errorf("failed to list srcImagestreams: %w", err)
	}
	logrus.WithField("len(srcImagestreams.Items)", len(srcImagestreams.Items)).Info("srcImagestreams")

	appCIClient := clients[appCIContextName]
	dstImagestreams := &imageapi.ImageStreamList{}
	if err := appCIClient.List(cxt, dstImagestreams); err != nil {
		return nil, fmt.Errorf("failed to list dstImagestreams: %w", err)
	}
	logrus.WithField("len(dstImagestreams.Items)", len(dstImagestreams.Items)).Info("imagedstImagestreamsstreamtags")

	for _, srcIS := range srcImagestreams.Items {
		for _, srcTag := range srcIS.Status.Tags {
			log := logrus.WithField("srcIS.Namespace", srcIS.Namespace).WithField("srcIS.Name", srcIS.Name).WithField("srcTag.Tag", srcTag.Tag)
			if needToMirror(&srcTag, &srcIS, dstImagestreams, log) {
				digest := findDockerImageDigest(&srcIS, srcTag.Tag)
				if digest == "" {
					logrus.WithField("srcTag.Tag", srcTag.Tag).Warn("failed to get digest")
					continue
				}
				mirror = append(mirror, pair{source: fmt.Sprintf("%s@%s", srcIS.Status.PublicDockerImageRepository, digest),
					target: fmt.Sprintf("%s/%s/%s:%s", api.DomainForService(api.ServiceRegistry), srcIS.Namespace, srcIS.Name, srcTag.Tag),
				})
			}
		}

	}
	return mirror, nil
}

// findDockerImageDigest returns the digest of the image,
// to a tag if it exists in the ImageStream's Spec
func findDockerImageDigest(is *imageapi.ImageStream, tag string) string {
	for _, t := range is.Status.Tags {
		if t.Tag != tag {
			continue
		}
		if len(t.Items) == 0 {
			return ""
		}
		return t.Items[0].Image
	}
	return ""
}

var (
	deniedNamespacePrefixes = sets.NewString("kube", "openshift", "default", "redhat", "ci-op", "ci-ln")
	rcNamespaces            = sets.NewString("origin", "ocp", "ocp-priv", "ocp-ppc64le", "ocp-ppc64le-priv", "ocp-s390x", "ocp-s390x-priv")
)

func needToMirror(tag *imageapi.NamedTagEventList, srcIS *imageapi.ImageStream, dstImagestreams *imageapi.ImageStreamList, log *logrus.Entry) bool {
	for _, deniedNamespacePrefix := range deniedNamespacePrefixes.List() {
		if strings.HasPrefix(srcIS.Namespace, deniedNamespacePrefix) {
			return false
		}
	}
	for _, ns := range rcNamespaces.List() {
		if srcIS.Namespace == ns && strings.HasPrefix(srcIS.Name, "4.") {
			return false
		}
	}
	if len(tag.Items) == 0 {
		log.Debug("tag's items on api.ci is empty")
		return false
	}
	if len(tag.Conditions) > 0 && tag.Conditions[0].Generation != tag.Items[0].Generation {
		log.WithField("tag.Conditions[0].Generation", tag.Conditions[0].Generation).WithField("tag.Items[0].Generation", tag.Items[0].Generation).Debug("not the same generation")
		return false
	}
	if !strings.Contains(tag.Items[0].DockerImageReference, ":5000") && !strings.Contains(tag.Items[0].DockerImageReference, "registry.svc.ci.openshift.org") {
		log.WithField("tag.Items[0].DockerImageReference", tag.Items[0].DockerImageReference).Debug("the root source is not on api.ci")
		return false
	}
	var foundIS, foundTag bool
	for _, dstIS := range dstImagestreams.Items {
		if srcIS.Name == dstIS.Name && srcIS.Namespace == dstIS.Namespace {
			foundIS = true
			for _, dstTag := range dstIS.Status.Tags {
				if tag.Tag == dstTag.Tag {
					foundTag = true
				} else {
					continue
				}
				if len(dstTag.Items) == 0 {
					log.Debug("tag's items on app.ci is empty")
					return true
				}
				if strings.Contains(dstTag.Items[0].DockerImageReference, "registry.svc.ci.openshift.org") {
					log.WithField("dstTag.Items[0].DockerImageReference", dstTag.Items[0].DockerImageReference).Debug("tag on app.ci is a pull-through to api.ci")
					return true
				}
			}
		}
	}
	if !foundIS || !foundTag {
		log.WithField("foundIS", foundIS).WithField("foundTag", foundTag).Warn("not found on app.ci")
		return true
	}
	log.WithField("tag.Items[0].DockerImageReference", tag.Items[0].DockerImageReference).Debug("tag on api.ci is ignored")
	return false
}
