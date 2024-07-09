package main

import (
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/client-go/kubernetes/scheme"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/interrupts"
	"sigs.k8s.io/prow/pkg/logrusutil"
	"sigs.k8s.io/prow/pkg/pjutil"

	prpqv1 "github.com/openshift/ci-tools/pkg/api/pullrequestpayloadqualification/v1"
	"github.com/openshift/ci-tools/pkg/html"
	"github.com/openshift/ci-tools/pkg/util"
)

type options struct {
	logLevel               string
	port                   int
	gracePeriod            time.Duration
	instrumentationOptions flagutil.InstrumentationOptions
	namespace              string
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.logLevel, "log-level", "info", "Level at which to log output.")
	fs.IntVar(&o.port, "port", 8080, "Port to run server on")
	fs.StringVar(&o.namespace, "namespace", "", "Namespace where resources are located")
	o.instrumentationOptions.AddFlags(fs)
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatalf("cannot parse args: '%s'", os.Args[1:])
	}
	return o
}

func validateOptions(o options) error {
	_, err := logrus.ParseLevel(o.logLevel)
	if err != nil {
		return fmt.Errorf("invalid --log-level: %w", err)
	}
	return nil
}

func main() {
	logrusutil.ComponentInit()
	o := gatherOptions()
	if err := validateOptions(o); err != nil {
		logrus.Fatalf("invalid options: %v", err)
	}
	level, _ := logrus.ParseLevel(o.logLevel)
	logrus.SetLevel(level)
	if err := prpqv1.AddToScheme(scheme.Scheme); err != nil {
		logrus.WithError(err).Fatal("failed to add prpqv1 to scheme")
	}
	kubeconfig, err := util.LoadClusterConfig()
	if err != nil {
		logrus.WithError(err).Fatal("failed to load kubeconfig")
	}
	kubeClient, err := ctrlruntimeclient.New(kubeconfig, ctrlruntimeclient.Options{})
	if err != nil {
		logrus.WithError(err).Fatal("could not create client from kube config")
	}
	static, err := fs.Sub(html.StaticFS, html.StaticSubdir)
	if err != nil {
		logrus.WithError(err).Fatal("failed to open static subdirectory")
	}
	health := pjutil.NewHealthOnPort(o.instrumentationOptions.HealthPort)
	server, err := newServer(kubeClient, interrupts.Context(), o.namespace)
	if err != nil {
		logrus.WithError(err).Fatal("failed to create server")
	}
	http.HandleFunc(html.StaticURL, http.StripPrefix(html.StaticURL, http.FileServer(http.FS(static))).ServeHTTP)
	http.HandleFunc(runsURL, server.RunsList().ServeHTTP)
	http.HandleFunc("/readyz", func(_ http.ResponseWriter, _ *http.Request) {})
	interrupts.ListenAndServe(&http.Server{Addr: ":" + strconv.Itoa(o.port)}, o.gracePeriod)
	health.ServeReady(func() bool {
		resp, err := http.DefaultClient.Get("http://127.0.0.1:" + strconv.Itoa(o.port) + "/readyz")
		if resp != nil {
			resp.Body.Close()
		}
		return err == nil && resp.StatusCode == 200
	})
	interrupts.WaitForGracefulShutdown()
}
