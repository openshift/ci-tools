package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/rest"

	pjapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	pjclientset "k8s.io/test-infra/prow/client/clientset/versioned"
	prowconfig "k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/pjutil"

	"github.com/openshift/ci-tools/pkg/util"
)

const (
	prowConfigPathOption = "prow-config-path"
	jobConfigPathOption  = "job-config-path"
	periodicOption       = "periodic"
)

type options struct {
	confirm bool

	prowConfigPath string
	jobConfigPath  string

	periodic string
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.BoolVar(&o.confirm, "confirm", false, "Whether to actually submit the job to Prow")
	fs.StringVar(&o.prowConfigPath, prowConfigPathOption, "", "Path to the Prow config file")
	fs.StringVar(&o.jobConfigPath, jobConfigPathOption, "", "Path to the Prow job config directory")
	fs.StringVar(&o.periodic, "periodic", "", "Name of the Periodic job to manually trigger")

	_ = fs.Parse(os.Args[1:])
	return o
}

func (o options) validate() error {
	if o.prowConfigPath == "" {
		return fmt.Errorf("required parameter %s was not provided", prowConfigPathOption)
	}

	if o.jobConfigPath == "" {
		return fmt.Errorf("required parameter %s was not provided", jobConfigPathOption)
	}

	if o.periodic == "" {
		return fmt.Errorf("required parameter %s was not provided", periodicOption)
	}

	return nil
}

func main() {
	o := gatherOptions()
	if err := o.validate(); err != nil {
		logrus.WithError(err).Fatal("incorrect options")
	}

	go func() {
		interrupts.WaitForGracefulShutdown()
		os.Exit(1)
	}()

	config, err := prowconfig.Load(o.prowConfigPath, o.jobConfigPath)
	if err != nil {
		logrus.WithError(err).Fatal("failed to read Prow configuration")
	}

	var selected *prowconfig.Periodic
	for _, job := range config.AllPeriodics() {
		if job.Name == o.periodic {
			selected = &job
			break
		}
	}

	if selected == nil {
		logrus.WithField("job-name", o.periodic).Fatal("failed to find the job")
	}

	prowjob := pjutil.NewProwJob(pjutil.PeriodicSpec(*selected), nil, nil)
	if !o.confirm {
		jobAsYAML, err := yaml.Marshal(prowjob)
		if err != nil {
			logrus.WithError(err).Fatal("failed to marshal the prowjob to YAML")
		}
		fmt.Printf(string(jobAsYAML))
		os.Exit(0)
	}

	var clusterConfig *rest.Config
	clusterConfig, err = util.LoadClusterConfig()
	if err != nil {
		logrus.WithError(err).Fatal("failed to load cluster configuration")
	}

	pjcset, err := pjclientset.NewForConfig(clusterConfig)
	if err != nil {
		logrus.WithError(err).Fatal("failed to create prowjob clientset")
	}
	pjclient := pjcset.ProwV1().ProwJobs(config.ProwJobNamespace)

	logrus.WithFields(pjutil.ProwJobFields(&prowjob)).Info("submitting a new prowjob")
	created, err := pjclient.Create(&prowjob)
	if err != nil {
		logrus.WithError(err).Fatal("failed to submit the prowjob")
	}

	logger := logrus.WithFields(pjutil.ProwJobFields(created))
	logger.Info("submitted the prowjob, waiting for its result")

	selector := fields.SelectorFromSet(map[string]string{"metadata.name": created.Name})

	for {
		w, err := pjclient.Watch(metav1.ListOptions{FieldSelector: selector.String()})
		if err != nil {
			logrus.WithError(err).Fatal("failed to create watch for ProwJobs")
		}

		for event := range w.ResultChan() {
			prowJob, ok := event.Object.(*pjapi.ProwJob)
			if !ok {
				logrus.WithField("object-type", fmt.Sprintf("%T", event.Object)).Fatal("received an unexpected object from Watch")
			}
			switch prowJob.Status.State {
			case pjapi.FailureState, pjapi.AbortedState, pjapi.ErrorState:
				logrus.Fatal("job failed")
			case pjapi.SuccessState:
				logrus.Info("job succeeded")
				os.Exit(0)
			}
		}
	}
}
