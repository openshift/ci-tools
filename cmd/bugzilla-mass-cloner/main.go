package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/test-infra/prow/bugzilla"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
)

type options struct {
	prowflagutil.BugzillaOptions

	fromRelease string
	toRelease   string

	dryRun bool
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.BoolVar(&o.dryRun, "dry-run", true, "Dry run for testing. Uses API tokens but does not mutate.")
	fs.StringVar(&o.fromRelease, "from-release", "", "From which targeted release the bug will be cloned to")
	fs.StringVar(&o.toRelease, "to-release", "", "To which release value the cloned bugs will hold")

	o.BugzillaOptions.AddFlags(fs)

	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse input")
	}

	return o
}

func (o *options) validate() error {
	if err := o.BugzillaOptions.Validate(o.dryRun); err != nil {
		return err
	}

	if o.fromRelease == "" {
		return fmt.Errorf("--from-release must be specified")
	}

	if o.toRelease == "" {
		return fmt.Errorf("--to-release must be specified")
	}

	if o.fromRelease == o.toRelease {
		return fmt.Errorf("--from-release and --to-release can't hold the same value")
	}

	return nil
}

type bugzillaClient interface {
	SearchBugs(filters map[string]string) ([]*bugzilla.Bug, error)
	CloneBug(bug *bugzilla.Bug, mutations ...func(bug *bugzilla.BugCreate)) (int, error)
	GetClones(bug *bugzilla.Bug) ([]*bugzilla.Bug, error)
}

func (o options) getBugsByStatus(client bugzillaClient, statuses []string) ([]*bugzilla.Bug, error) {
	var bugsToClone []*bugzilla.Bug

	for _, status := range statuses {
		bugs, err := client.SearchBugs(map[string]string{"target_release": o.fromRelease, "status": status})
		if err != nil {
			return nil, fmt.Errorf("couldn't search for bugs: %w", err)
		}
		bugsToClone = append(bugsToClone, bugs...)
	}
	return bugsToClone, nil
}

func hasCloneForTargetRelease(client bugzillaClient, bug *bugzilla.Bug, toRelease string) (bool, error) {
	clonedBugs, err := client.GetClones(bug)
	if err != nil {
		return false, fmt.Errorf("couldn't get clones for bug with id %d: %w", bug.ID, err)
	}

	for _, clonedBug := range clonedBugs {
		targetRelease := sets.New[string](clonedBug.TargetRelease...)
		if targetRelease.Has(toRelease) {
			return true, nil
		}
	}

	return false, nil
}

func (o options) massCloneBugs(client bugzillaClient, bugs []*bugzilla.Bug) error {
	var errs []error
	for _, bug := range bugs {
		logger := logrus.WithField("bug-id", bug.ID)
		hasClone, err := hasCloneForTargetRelease(client, bug, o.toRelease)
		if err != nil {
			errs = append(errs, fmt.Errorf("couldn't check if bug with id %d has clones: %w", bug.ID, err))
			continue
		}

		if hasClone {
			logger.Infof("Bug already has a clone for target release: %s", o.toRelease)
			continue
		}

		if o.dryRun {
			logger.Info("(dry-run) Will clone bug...")
			continue
		}

		newBugID, err := client.CloneBug(bug, func(bug *bugzilla.BugCreate) { bug.TargetRelease = []string{o.toRelease} })
		if err != nil {
			errs = append(errs, fmt.Errorf("couldn't clone bug with id %d: %w", bug.ID, err))
			continue
		}
		logger.WithField("new-bug-id", newBugID).Info("Bug has been cloned")
	}

	return utilerrors.NewAggregate(errs)
}

func main() {
	o := gatherOptions()
	if err := o.validate(); err != nil {
		logrus.WithError(err).Fatal("Invalid options")
	}

	bugzillaClient, err := o.BugzillaOptions.BugzillaClient()
	if err != nil {
		logrus.WithError(err).Fatal("couldn't create a bugzilla client")
	}

	statuses := []string{"NEW", "ASSIGNED", "ON_DEV", "POST"}
	bugs, err := o.getBugsByStatus(bugzillaClient, statuses)
	if err != nil {
		logrus.WithError(err).Fatalf("error occurred while searching the bugs with statuses: %s", strings.Join(statuses, ","))

	}

	if err := o.massCloneBugs(bugzillaClient, bugs); err != nil {
		logrus.WithError(err).Fatal("error occurred while cloning the bugs")
	}
}
