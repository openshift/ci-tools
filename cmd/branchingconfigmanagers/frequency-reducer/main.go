package main

import (
	"flag"
	"fmt"
	"math/rand"
	"regexp"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"gopkg.in/robfig/cron.v2"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/api/ocplifecycle"
	"github.com/openshift/ci-tools/pkg/config"
)

type options struct {
	config.ConfirmableOptions
	currentOCPVersion string
}

func (o options) validate() error {
	var errs []error
	if err := o.ConfirmableOptions.Validate(); err != nil {
		errs = append(errs, err)
	}

	return utilerrors.NewAggregate(errs)
}

func gatherOptions() options {
	o := options{}
	flag.StringVar(&o.currentOCPVersion, "current-release", "", "Current OCP version")

	o.Bind(flag.CommandLine)
	flag.Parse()

	return o
}

func main() {
	o := gatherOptions()
	if err := o.validate(); err != nil {
		logrus.Fatalf("Invalid options: %v", err)
	}

	ocpVersion, err := ocplifecycle.ParseMajorMinor(o.currentOCPVersion)
	if err != nil {
		logrus.Fatalf("Not valid --current-release: %v", err)
	}

	if err := o.ConfirmableOptions.Complete(); err != nil {
		logrus.Fatalf("Couldn't complete the config options: %v", err)
	}

	if err := o.OperateOnCIOperatorConfigDir(o.ConfigDir, func(configuration *api.ReleaseBuildConfiguration, info *config.Info) error {
		output := config.DataWithInfo{Configuration: *configuration, Info: *info}
		updateIntervalFieldsForMatchedSteps(&output, *ocpVersion)

		if err := output.CommitTo(o.ConfigDir); err != nil {
			logrus.WithError(err).Fatal("commitTo failed")
		}
		return nil
	}); err != nil {
		logrus.WithError(err).Fatal("Could not branch configurations.")
	}

}

func updateIntervalFieldsForMatchedSteps(
	configuration *config.DataWithInfo,
	version ocplifecycle.MajorMinor,
) {
	testVersion, err := ocplifecycle.ParseMajorMinor(extractVersion(configuration.Info.Metadata.Branch))
	if err != nil {
		return
	}

	pastVersion, err := version.GetPastVersion()
	if err != nil {
		logrus.Warningf("Can't get past version for %s: %v", version.GetVersion(), err)
		pastVersion = ""
	}
	pastPastVersion, err := version.GetPastPastVersion()
	if err != nil {
		logrus.Debugf("Can't get past-past version for %s: %v", version.GetVersion(), err)
		pastPastVersion = ""
	}

	if configuration.Info.Metadata.Org == "openshift" || configuration.Info.Metadata.Org == "openshift-priv" {
		for _, test := range configuration.Configuration.Tests {
			if !strings.Contains(test.As, "mirror-nightly-image") && !strings.Contains(test.As, "promote-") {
				if test.Cron != nil {
					// check if less then past past version
					if testVersion.Less(ocplifecycle.MajorMinor{Major: version.Major, Minor: version.Minor - 2}) {
						correctCron, err := isExecutedAtMostXTimesAMonth(*test.Cron, 1)
						if err != nil {
							logrus.Warningf("Can't parse cron string %s", *test.Cron)
							continue
						}
						if !correctCron {
							*test.Cron = generateMonthlyCron()
						}
					} else if pastPastVersion != "" && testVersion.GetVersion() == pastPastVersion {
						correctCron, err := isExecutedAtMostXTimesAMonth(*test.Cron, 2)
						if err != nil {
							logrus.Warningf("Can't parse cron string %s", *test.Cron)
							continue
						}
						if !correctCron {
							*test.Cron = generateBiWeeklyCron()
						}
					} else if pastVersion != "" && testVersion.GetVersion() == pastVersion {
						correctCron, err := isExecutedAtMostXTimesAMonth(*test.Cron, 4)
						if err != nil {
							logrus.Warningf("Can't parse cron string %s", *test.Cron)
							continue
						}
						if !correctCron {
							*test.Cron = generateWeeklyWeekendCron()
						}
					}
				}
				if test.Interval != nil {
					if testVersion.Less(ocplifecycle.MajorMinor{Major: version.Major, Minor: version.Minor - 2}) {
						duration, err := time.ParseDuration(*test.Interval)
						if err != nil {
							logrus.Warningf("Can't parse interval string %s", *test.Cron)
							continue
						}
						if duration < time.Hour*24*28 {
							cronExpr := generateWeeklyWeekendCron()
							test.Cron = &cronExpr
							test.Interval = nil
						}
					} else if pastPastVersion != "" && testVersion.GetVersion() == pastPastVersion {
						duration, err := time.ParseDuration(*test.Interval)
						if err != nil {
							logrus.Warningf("Can't parse interval string %s", *test.Cron)
							continue
						}
						if duration < time.Hour*24*14 {
							cronExpr := generateBiWeeklyCron()
							test.Cron = &cronExpr
							test.Interval = nil
						}
					} else if pastVersion != "" && testVersion.GetVersion() == pastVersion {
						duration, err := time.ParseDuration(*test.Interval)
						if err != nil {
							logrus.Warningf("Can't parse interval string %s", *test.Cron)
							continue
						}
						if duration < time.Hour*24*7 {
							cronExpr := generateWeeklyWeekendCron()
							test.Cron = &cronExpr
							test.Interval = nil
						}
					}
				}
			}
		}
	}
}

func isExecutedAtMostXTimesAMonth(cronExpr string, x int) (bool, error) {
	switch strings.ToLower(cronExpr) {
	case "@daily":
		cronExpr = "0 0 * * *"
	case "@weekly":
		cronExpr = "0 0 * * 0"
	case "@monthly":
		cronExpr = "0 0 1 * *"
	case "@yearly", "@annually":
		cronExpr = "0 0 1 1 *"
	}

	schedule, err := cron.Parse(cronExpr)
	if err != nil {
		return false, err
	}
	start := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)

	executionCount := 0
	for {
		next := schedule.Next(start)
		if next.After(end) {
			break
		}
		executionCount++
		start = next
	}

	return executionCount <= x, nil
}

func generateWeeklyWeekendCron() string {
	randDay := rand.Intn(2)
	selectedDay := randDay * 6
	return fmt.Sprintf("%d %d * * %d", rand.Intn(60), rand.Intn(24), selectedDay)
}

func generateBiWeeklyCron() string {
	return fmt.Sprintf("%d %d %d,%d * *", rand.Intn(60), rand.Intn(24), rand.Intn(10)+5, rand.Intn(14)+15)
}

func generateMonthlyCron() string {
	return fmt.Sprintf("%d %d %d * *", rand.Intn(60), rand.Intn(24), rand.Intn(28)+1)
}

func extractVersion(s string) string {
	pattern := `^(release|openshift)-(\d+\.\d+)$`
	re := regexp.MustCompile(pattern)

	matches := re.FindStringSubmatch(s)

	if len(matches) > 2 {
		return matches[2]
	}
	return ""
}
