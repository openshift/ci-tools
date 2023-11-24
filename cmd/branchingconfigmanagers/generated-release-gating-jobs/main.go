package main

import (
	"errors"
	"flag"
	"fmt"
	"path"

	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/openshift/ci-tools/pkg/api/ocplifecycle"
	"github.com/openshift/ci-tools/pkg/branchcuts/bumper"
	cioperatorcfg "github.com/openshift/ci-tools/pkg/config"
)

const (
	releaseJobsPath = "ci-operator/config/openshift/release"
)

type options struct {
	curOCPVersion    string
	releaseRepoDir   string
	logLevel         int
	newIntervalValue int
}

func gatherOptions() (*options, error) {
	var errs []error
	o := &options{}
	flag.StringVar(&o.curOCPVersion, "current-release", "", "Current OCP version")
	flag.StringVar(&o.releaseRepoDir, "release-repo", "", "Path to 'openshift/release/ folder")
	flag.IntVar(&o.newIntervalValue, "interval", 168, "New interval to set")
	flag.IntVar(&o.logLevel, "log-level", int(logrus.DebugLevel), "Log level")
	flag.Parse()

	if _, err := ocplifecycle.ParseMajorMinor(o.curOCPVersion); o.curOCPVersion != "" && err != nil {
		errs = append(errs, fmt.Errorf("error parsing current-release %s", o.curOCPVersion))
	}

	if o.newIntervalValue < 0 {
		errs = append(errs, errors.New("error parsing interval: value is not a positive integer"))
	}

	if o.releaseRepoDir != "" {
		if !path.IsAbs(o.releaseRepoDir) {
			errs = append(errs, errors.New("error parsing release repo path: path has to be absolute"))
		}
	} else {
		errs = append(errs, errors.New("error parsing release repo path: path is mandatory"))
	}

	return o, utilerrors.NewAggregate(errs)
}

func main() {
	o, err := gatherOptions()
	if err != nil {
		logrus.WithError(err).Fatal("failed to gather options")
	}

	logrus.SetLevel(logrus.Level(o.logLevel))
	logrus.Debugf("using options %+v", o)

	if err := reconcile(o); err != nil {
		logrus.WithError(err).Fatal("failed to reconcile the status")
	}
	logrus.Info("status reconciled")
}

func reconcile(o *options) error {
	logrus.Debugf("using options %+v", o)
	releaseJobsDir := path.Join(o.releaseRepoDir, releaseJobsPath)
	b, err := bumper.NewGeneratedReleaseGatingJobsBumper(o.curOCPVersion, releaseJobsDir, o.newIntervalValue)
	if err != nil {
		return fmt.Errorf("new bumper: %w", err)
	}
	if err := bumper.Bump[*cioperatorcfg.DataWithInfo](b, &bumper.BumpingOptions{}); err != nil {
		return fmt.Errorf("bumper: %w", err)
	}
	return nil
}
