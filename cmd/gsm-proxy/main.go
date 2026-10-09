package main

import (
	"errors"
	"flag"
	"net/http"
	"os"
	"time"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/logrusutil"

	"github.com/openshift/ci-tools/pkg/group"
	gsmproxy "github.com/openshift/ci-tools/pkg/gsm-proxy"
)

type options struct {
	listenAddr          string
	groupsOwnershipFile string
	gracePeriod         time.Duration
}

func gatherOptions() (*options, error) {
	o := &options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.listenAddr, "listen-addr", ":8080", "Address to listen on.")
	fs.StringVar(&o.groupsOwnershipFile, "groups-ownership-file", "", "Path to core-services/sync-rover-groups/_config.yaml.")
	fs.DurationVar(&o.gracePeriod, "grace-period", 10*time.Second, "Grace period for server shutdown.")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return nil, err
	}
	return o, nil
}

func (o *options) validate() error {
	if o.groupsOwnershipFile == "" {
		return errors.New("--groups-ownership-file must not be empty")
	}
	return nil
}

func main() {
	logrusutil.ComponentInit()
	o, err := gatherOptions()
	if err != nil {
		logrus.WithError(err).Fatal("failed to gather options")
	}
	if err := o.validate(); err != nil {
		logrus.WithError(err).Fatal("invalid options")
	}

	config, err := group.LoadConfig(o.groupsOwnershipFile)
	if err != nil {
		logrus.WithError(err).Fatal("failed to load rover groups config")
	}
	logrus.WithField("groups", len(config.Groups)).Info("loaded rover group config")

	server := &http.Server{
		Addr:              o.listenAddr,
		Handler:           gsmproxy.NewServeMux(nil),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       time.Minute,
		MaxHeaderBytes:    16 << 10,
	}
	logrus.WithField("listen-addr", o.listenAddr).Info("starting gsm-proxy")
	interrupts.ListenAndServe(server, o.gracePeriod)
	interrupts.WaitForGracefulShutdown()
}
