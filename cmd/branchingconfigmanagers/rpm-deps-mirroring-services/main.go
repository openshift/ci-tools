package main

import (
	"errors"
	"flag"
	"fmt"
	"path"

	"github.com/sirupsen/logrus"
	"gopkg.in/ini.v1"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/openshift/ci-tools/pkg/api/ocplifecycle"
	"github.com/openshift/ci-tools/pkg/branchcuts/bumper"
	"github.com/openshift/ci-tools/pkg/branchcuts/bumper/repo"
)

const (
	rpmMirroringServicesPath              = "core-services/release-controller/_repos"
	rpmMirroringServicesGlobPatternFormat = "ocp-%s*.repo"
)

type options struct {
	curOCPVersion  string
	releaseRepoDir string
	logLevel       int
}

func gatherOptions() (*options, error) {
	var errs []error
	o := &options{}
	flag.StringVar(&o.curOCPVersion, "current-release", "", "Current OCP version")
	flag.StringVar(&o.releaseRepoDir, "release-repo", "", "Path to 'openshift/release/ folder")
	flag.IntVar(&o.logLevel, "log-level", int(logrus.DebugLevel), "Log level")
	flag.Parse()

	if _, err := ocplifecycle.ParseMajorMinor(o.curOCPVersion); o.curOCPVersion != "" && err != nil {
		errs = append(errs, fmt.Errorf("error parsing cur-ocp-ver %s", o.curOCPVersion))
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

	if err = reconcile(o); err != nil {
		logrus.WithError(err).Fatal("failed to reconcile the status")
	}
	logrus.Info("status reconciled")
}

func reconcile(o *options) error {
	logrus.Debugf("using options %+v", o)
	bumpOpts := repo.RepoBumperOptions{
		FilesDir:      path.Join(o.releaseRepoDir, rpmMirroringServicesPath),
		GlobPattern:   fmt.Sprintf(rpmMirroringServicesGlobPatternFormat, o.curOCPVersion),
		CurOCPRelease: o.curOCPVersion,
	}
	logrus.Debugf("bumpOpts: %+v", bumpOpts)

	b, err := repo.NewRepoBumper(&bumpOpts)
	if err != nil {
		return fmt.Errorf("new repo bumper: %w", err)
	}

	if err := bumper.Bump[*ini.File](b, &bumper.BumpingOptions{}); err != nil {
		return fmt.Errorf("bumper: %w", err)
	}
	return nil
}
