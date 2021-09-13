package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/logrusutil"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	crcontrollerutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api/ocplifecycle"
)

type options struct {
	logLevel          string
	scheduleDir       string
	dryRun            bool
	kubernetesOptions flagutil.KubernetesOptions
}

func gatherOptions() (*options, error) {
	o := &options{kubernetesOptions: flagutil.KubernetesOptions{NOInClusterConfigDefault: true}}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.logLevel, "log-level", logrus.InfoLevel.String(), "Level at which to log output.")
	fs.StringVar(&o.scheduleDir, "schedule-dir", "", "Directory holding schedules.")
	o.kubernetesOptions.AddFlags(fs)
	fs.BoolVar(&o.dryRun, "dry-run", true, "Do not mutate cluster state.")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return o, fmt.Errorf("failed to parse flags: %w", err)
	}
	return o, nil
}

func validateOptions(o *options) error {
	_, err := logrus.ParseLevel(o.logLevel)
	if err != nil {
		return fmt.Errorf("invalid --log-level: %w", err)
	}
	return o.kubernetesOptions.Validate(o.dryRun)
}

func main() {
	logrusutil.ComponentInit()
	o, err := gatherOptions()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to gather options")
	}
	if err := validateOptions(o); err != nil {
		logrus.WithError(err).Fatalf("Invalid options")
	}
	level, _ := logrus.ParseLevel(o.logLevel)
	logrus.SetLevel(level)

	kubeConfigs, err := o.kubernetesOptions.LoadClusterConfigs(nil)
	if err != nil {
		logrus.WithError(err).Fatal("Could not load kube config")
	}

	schedules, err := readSchedules(o.scheduleDir)
	if err != nil {
		logrus.WithError(err).Fatal("Could not read schedules.")
	}

	raw, err := yaml.Marshal(schedules)
	if err != nil {
		logrus.WithError(err).Fatal("Could not find marshal schedules")
	}

	var errors []error
	for ctx, config := range kubeConfigs {
		client, err := ctrlruntimeclient.New(&config, ctrlruntimeclient.Options{})
		if err != nil {
			errors = append(errors, fmt.Errorf("could not get client for cluster %q: %w", ctx, err))
		}
		if err := upsertConfigMap(string(raw), client); err != nil {
			errors = append(errors, fmt.Errorf("could not upsert configmap for cluster %q: %w", ctx, err))
		}
	}
	if len(errors) > 0 {
		logrus.WithError(kerrors.NewAggregate(errors)).Fatal("Failed to update cluster state.")
	}
}

func readSchedules(scheduleDir string) (*ocplifecycle.Config, error) {
	var config *ocplifecycle.Config
	if err := filepath.Walk(scheduleDir, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if filepath.Ext(info.Name()) != ".yaml" {
			return nil
		}
		raw, err := ioutil.ReadFile(path)
		if err != nil {
			return fmt.Errorf("could not read file: %w", err)
		}
		var schedule Schedule
		if err := yaml.Unmarshal(raw, &schedule); err != nil {
			return fmt.Errorf("could not unmarshal file: %w", err)
		}
		config = addToConfig(schedule, config)
		return nil
	}); err != nil {
		return nil, err
	}
	return config, nil
}

const (
	scheduleNamespace = "ci"
	scheduleConfigmap = "release-schedules"
	scheduleKey       = "schedules.yaml"
)

func upsertConfigMap(schedules string, client ctrlruntimeclient.Client) error {
	configmap := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scheduleConfigmap,
			Namespace: scheduleNamespace,
		},
	}
	mutate := func() error {
		if configmap.Data == nil {
			configmap.Data = map[string]string{}
		}
		configmap.Data[scheduleKey] = schedules
		return nil
	}
	_, err := crcontrollerutil.CreateOrUpdate(context.Background(), client, configmap, mutate)
	return err
}
