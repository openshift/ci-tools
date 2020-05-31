package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	prometheusapi "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/logrusutil"

	"github.com/openshift/ci-tools/pkg/dispatcher"
)

type options struct {
	prometheusURL          string
	prometheusUsername     string
	prometheusPasswordPath string
	jobVolumesPath         string

	prometheusPassword string
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.StringVar(&o.prometheusURL, "prometheus-url", "https://prometheus-prow-monitoring.apps.ci.l2s4.p1.openshiftapps.com", "The prometheus URL")
	fs.StringVar(&o.prometheusUsername, "prometheus-username", "", "The Prometheus username.")
	fs.StringVar(&o.prometheusPasswordPath, "prometheus-password-path", "", "The path to a file containing the Prometheus password")
	fs.StringVar(&o.jobVolumesPath, "job-volumes-path", filepath.Join(os.TempDir(), "job-volumes.yaml"), "The path to a file containing the job volumes")

	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse input")
	}
	return o
}

func (o *options) validate() error {
	if (o.prometheusUsername == "") != (o.prometheusPasswordPath == "") {
		return fmt.Errorf("--prometheus-username and --prometheus-password-path must be specified together")
	}
	return nil
}

func (o *options) complete(secrets *sets.String) error {
	if o.prometheusPasswordPath != "" {
		bytes, err := ioutil.ReadFile(o.prometheusPasswordPath)
		if err != nil {
			return err
		}
		o.prometheusPassword = strings.TrimSpace(string(bytes))
		if o.prometheusPassword == "" {
			return fmt.Errorf("no content in file: %s", o.prometheusPasswordPath)
		}
		secrets.Insert(o.prometheusPassword)
	}
	return nil
}

func main() {
	secrets := sets.NewString()
	logrus.SetFormatter(logrusutil.NewCensoringFormatter(logrus.StandardLogger().Formatter, func() sets.String {
		return secrets
	}))
	o := gatherOptions()
	if err := o.validate(); err != nil {
		logrus.WithError(err).Fatal("Failed to complete options.")
	}
	if err := o.complete(&secrets); err != nil {
		logrus.WithError(err).Fatal("Failed to complete options.")
	}

	promClient, err := dispatcher.NewPrometheusClient(o.prometheusURL, o.prometheusUsername, o.prometheusPassword)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create promethues client.")
	}

	v1api := prometheusapi.NewAPI(promClient)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	jobVolumes, err := dispatcher.GetJobVolumesFromPrometheus(ctx, v1api)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to get job volumes from Prometheus.")
	}
	logrus.WithField("jobVolumes", jobVolumes).Debug("loaded job volumes")
}
