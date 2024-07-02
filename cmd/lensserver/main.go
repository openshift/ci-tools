package main

import (
	"context"
	"flag"
	"time"

	"github.com/sirupsen/logrus"

	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/deck/jobs"
	"sigs.k8s.io/prow/pkg/flagutil"
	prowflagutil "sigs.k8s.io/prow/pkg/flagutil"
	configflagutil "sigs.k8s.io/prow/pkg/flagutil/config"
	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/io"
	"sigs.k8s.io/prow/pkg/logrusutil"
	"sigs.k8s.io/prow/pkg/spyglass"
	"sigs.k8s.io/prow/pkg/spyglass/lenses/common"

	"github.com/openshift/ci-tools/pkg/lenses/stepgraph"
)

type options struct {
	config configflagutil.ConfigOptions

	storage prowflagutil.StorageClientOptions
}

func gatherOptions() options {
	o := options{}
	fs := flag.CommandLine

	for _, group := range []flagutil.OptionGroup{&o.storage, &o.config} {
		group.AddFlags(fs)
	}
	flag.Parse()
	return o
}

const spyglassLocalLensListenerAddr = "127.0.0.1:1235"

func main() {
	o := gatherOptions()
	logrusutil.ComponentInit()

	configAgent, err := o.config.ConfigAgent()
	if err != nil {
		logrus.WithError(err).Fatal("Error starting config agent.")
	}

	ja := jobs.NewJobAgent(context.Background(), &fakePJListingClient{}, false, false, nil, nil, configAgent.Config)
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
