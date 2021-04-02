package main

import (
	"context"
	"flag"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/pkg/flagutil"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/deck/jobs"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/io"
	"k8s.io/test-infra/prow/logrusutil"
	"k8s.io/test-infra/prow/spyglass"
	"k8s.io/test-infra/prow/spyglass/lenses/common"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/lenses/stepgraph"
)

type options struct {
	configPath    string
	jobConfigPath string

	storage prowflagutil.StorageClientOptions
}

func gatherOptions() options {
	o := options{}
	fs := flag.CommandLine
	fs.StringVar(&o.configPath, "config-path", "", "Path to config.yaml.")
	fs.StringVar(&o.jobConfigPath, "job-config-path", "", "Path to prow job configs.")

	for _, group := range []flagutil.OptionGroup{&o.storage} {
		group.AddFlags(fs)
	}
	flag.Parse()
	return o
}

const spyglassLocalLensListenerAddr = "127.0.0.1:1235"

func main() {
	o := gatherOptions()
	logrusutil.ComponentInit()

	configAgent := &config.Agent{}
	if err := configAgent.Start(o.configPath, o.jobConfigPath, []string{}); err != nil {
		logrus.WithError(err).Fatal("Error starting config agent.")
	}

	ja := jobs.NewJobAgent(context.Background(), &fakePJListingClient{}, false, false, nil, configAgent.Config)
	ja.Start()

	opener, err := io.NewOpener(interrupts.Context(), o.storage.GCSCredentialsFile, o.storage.S3CredentialsFile)
	if err != nil {
		logrus.WithError(err).Fatal("Error creating opener")
	}

	lens := stepgraph.Lens{}
	localLenses := []common.LensWithConfiguration{{
		Config: common.LensOpt{
			LensName:  lens.Config().Name,
			LensTitle: lens.Config().Title,
		},
		Lens: lens,
	}}

	lensServer, err := common.NewLensServer(spyglassLocalLensListenerAddr, ja, spyglass.NewStorageArtifactFetcher(opener, configAgent.Config, false), spyglass.NewPodLogArtifactFetcher(ja), configAgent.Config, localLenses)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to start lens server")
	}

	interrupts.ListenAndServe(lensServer, 5*time.Second)
	defer interrupts.WaitForGracefulShutdown()
}

type fakePJListingClient struct {
}

func (w *fakePJListingClient) List(
	ctx context.Context,
	pjl *prowapi.ProwJobList,
	opts ...ctrlruntimeclient.ListOption) error {
	return nil
}
