package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	prowconfig "k8s.io/test-infra/prow/config"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api/ocplifecycle"
)

const (
	jobName           = "periodic-openshift-release-fast-forward"
	ocpProductName    = "ocp"
	currentReleaseArg = "--current-release"
	futureReleaseArg  = "--future-release"
)

var fs = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

type opts struct {
	lifecycleConfigFile string
	infraPeriodicsPath  string
	overwriteTimeRaw    string
	overwriteTime       *time.Time
}

func gatherOpts() (*opts, error) {
	o := &opts{}
	var errs []error

	fs.StringVar(&o.lifecycleConfigFile, "lifecycle-config", "", "Path to the lifecycle config file")
	fs.StringVar(&o.infraPeriodicsPath, "infra-periodics-path", "", "Path to the infra periodic jobs config file")
	fs.StringVar(&o.overwriteTimeRaw, "overwrite-time", "", "Act as if this was the current time, must be in RFC3339 format")
	if err := fs.Parse(os.Args[1:]); err != nil {
		errs = append(errs, fmt.Errorf("couldn't parse arguments: %w", err))
	}

	if o.lifecycleConfigFile == "" {
		errs = append(errs, errors.New("--lifecycle-config is mandatory"))
	}

	if o.overwriteTimeRaw != "" {
		if parsed, err := time.Parse(time.RFC3339, o.overwriteTimeRaw); err != nil {
			errs = append(errs, fmt.Errorf("failed to parse %q as RFC3339 time: %w", o.overwriteTimeRaw, err))
		} else {
			o.overwriteTime = &parsed
		}
	}

	return o, utilerrors.NewAggregate(errs)
}

func main() {
	o, err := gatherOpts()
	if err != nil {
		logrus.WithError(err).Fatal("failed to gather options")
	}

	c, err := prowconfig.ReadJobConfig(o.infraPeriodicsPath)
	if err != nil {
		logrus.WithError(err).Fatal("failed to read job configuration")
	}

	periodic, err := getPeriodicJob(jobName, c.Periodics)
	if err != nil {
		logrus.WithError(err).Fatal("")
	}

	lifecycleConfig, err := ocplifecycle.LoadConfig(o.lifecycleConfigFile)
	if err != nil {
		logrus.WithError(err).Fatal("failed to load the lifecycle configuration")
	}

	now := time.Now()
	if o.overwriteTime != nil {
		now = *o.overwriteTime
	}

	if err := reconcile(lifecycleConfig, now, periodic); err != nil {
		logrus.WithError(err).Fatal("failed to reconcile job from the lifecycle configuration")
	}
	updatePeriodicJob(&c, periodic)

	periodicsRaw, err := yaml.Marshal(c)
	if err != nil {
		logrus.WithError(err).Fatal("failed to serialize plugin config")
	}

	if err := os.WriteFile(o.infraPeriodicsPath, periodicsRaw, 0644); err != nil {
		logrus.WithError(err).Fatal("failed to write main plugin config")
	}
}

func reconcile(lifecycleConfig ocplifecycle.Config, now time.Time, job *prowconfig.Periodic) error {
	timelineOpts := ocplifecycle.TimelineOptions{
		OnlyEvents: sets.New[string]([]string{
			string(ocplifecycle.LifecycleEventOpen),
			string(ocplifecycle.LifecycleEventFeatureFreeze),
		}...),
	}

	timeline := lifecycleConfig.GetTimeline(ocpProductName, timelineOpts)
	before, after := timeline.DeterminePlaceInTime(now)

	if after.LifecyclePhase.Event == ocplifecycle.LifecycleEventOpen {
		parsedVersion, err := ocplifecycle.ParseMajorMinor(after.ProductVersion)
		if err != nil {
			return fmt.Errorf("failed to parse %s as majorMinor version: %w", after.ProductVersion, err)
		}

		job.Spec.Containers[0].Args = append(job.Spec.Containers[0].Args, fmt.Sprintf("%s=%s", futureReleaseArg, parsedVersion))
		sort.Strings(job.Spec.Containers[0].Args)
	} else if before.LifecyclePhase.Event == ocplifecycle.LifecycleEventOpen || before.LifecyclePhase.Event == ocplifecycle.LifecycleEventFeatureFreeze {
		parsedVersion, err := ocplifecycle.ParseMajorMinor(before.ProductVersion)
		if err != nil {
			return fmt.Errorf("failed to parse %s as majorMinor version: %w", before.ProductVersion, err)
		}

		var args []string
		for _, arg := range job.Spec.Containers[0].Args {
			if !strings.Contains(arg, currentReleaseArg) && !strings.Contains(arg, futureReleaseArg) {
				args = append(args, arg)
			}
		}

		args = append(args, fmt.Sprintf("%s=%s", currentReleaseArg, parsedVersion))
		args = append(args, fmt.Sprintf("%s=%s", futureReleaseArg, parsedVersion))
		sort.Strings(job.Spec.Containers[0].Args)
		job.Spec.Containers[0].Args = args
	}

	return nil
}

func getPeriodicJob(jobName string, periodics []prowconfig.Periodic) (*prowconfig.Periodic, error) {
	for _, job := range periodics {
		if job.Name == jobName {
			return &job, nil
		}
	}

	return nil, fmt.Errorf("failed to find the job: %s", jobName)
}

func updatePeriodicJob(config *prowconfig.JobConfig, job *prowconfig.Periodic) {
	for i, periodic := range config.Periodics {
		if periodic.Name == job.Name {
			config.Periodics[i] = *job
		}
	}
}
