package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"gopkg.in/robfig/cron.v2"
	"gopkg.in/yaml.v2"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/api/ocplifecycle"
	"github.com/openshift/ci-tools/pkg/config"
)

// ClusterProfilesConfig defines the YAML structure for cluster profiles filtering
type ClusterProfilesConfig struct {
	ClusterProfiles []string `yaml:"cluster_profiles"`
}

type options struct {
	config.ConfirmableOptions
	currentOCPVersion     string
	maxThreads            int
	clusterProfilesConfig string
}

func (o options) validate() error {
	var errs []error
	if err := o.ConfirmableOptions.Validate(); err != nil {
		errs = append(errs, err)
	}
	if o.maxThreads <= 0 {
		errs = append(errs, fmt.Errorf("max-threads must be positive, got %d", o.maxThreads))
	}

	return utilerrors.NewAggregate(errs)
}

func gatherOptions() options {
	o := options{}
	flag.StringVar(&o.currentOCPVersion, "current-release", "", "Current OCP version")
	flag.IntVar(&o.maxThreads, "max-threads", runtime.NumCPU(), "Maximum number of threads to use for parallel processing")
	flag.StringVar(&o.clusterProfilesConfig, "cluster-profiles-config", "", "Path to YAML file containing cluster profiles to filter by (optional)")

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

	// Load cluster profiles filter if provided
	var allowedClusterProfiles map[string]bool
	if o.clusterProfilesConfig != "" {
		var err error
		allowedClusterProfiles, err = loadClusterProfilesConfig(o.clusterProfilesConfig)
		if err != nil {
			logrus.WithError(err).Fatal("Could not load cluster profiles configuration.")
		}
		logrus.Infof("Loaded cluster profiles filter: %d profiles specified", len(allowedClusterProfiles))
	} else {
		logrus.Info("No cluster profiles filter specified, processing all configurations")
	}

	if err := processConfigurationsInParallel(&o, *ocpVersion, allowedClusterProfiles); err != nil {
		logrus.WithError(err).Fatal("Could not process configurations.")
	}

}

// loadClusterProfilesConfig loads and parses the cluster profiles configuration file
func loadClusterProfilesConfig(filePath string) (map[string]bool, error) {
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return nil, fmt.Errorf("cluster profiles config file does not exist: %s", filePath)
	}

	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read cluster profiles config file: %w", err)
	}

	var config ClusterProfilesConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse cluster profiles config YAML: %w", err)
	}

	if len(config.ClusterProfiles) == 0 {
		return nil, fmt.Errorf("no cluster profiles specified in config file")
	}

	// Convert to map for O(1) lookup
	allowedProfiles := make(map[string]bool)
	for _, profile := range config.ClusterProfiles {
		allowedProfiles[profile] = true
		logrus.Debugf("Allowing cluster profile: %s", profile)
	}

	return allowedProfiles, nil
}

type configJob struct {
	configuration *api.ReleaseBuildConfiguration
	info          *config.Info
	configDir     string
}

func processConfigurationsInParallel(o *options, ocpVersion ocplifecycle.MajorMinor, allowedClusterProfiles map[string]bool) error {
	var jobs []configJob
	var jobsMutex sync.Mutex

	err := o.OperateOnCIOperatorConfigDir(o.ConfigDir, func(configuration *api.ReleaseBuildConfiguration, info *config.Info) error {
		jobsMutex.Lock()
		jobs = append(jobs, configJob{
			configuration: configuration,
			info:          info,
			configDir:     o.ConfigDir,
		})
		jobsMutex.Unlock()
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to collect configurations: %w", err)
	}

	jobsChan := make(chan configJob, len(jobs))
	errorsChan := make(chan error, o.maxThreads)

	var errors []error
	var errorMutex sync.Mutex
	var errorWg sync.WaitGroup
	errorWg.Add(1)

	go func() {
		defer errorWg.Done()
		for err := range errorsChan {
			errorMutex.Lock()
			errors = append(errors, err)
			errorMutex.Unlock()
		}
	}()

	var wg sync.WaitGroup
	var processedCount int64
	var processedMutex sync.Mutex

	for i := 0; i < o.maxThreads; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			workerProcessedCount := 0
			logrus.Infof("Worker %d started", workerID)

			for job := range jobsChan {
				if err := processConfiguration(job, ocpVersion, workerID, allowedClusterProfiles); err != nil {
					select {
					case errorsChan <- err:
					default:
						logrus.WithError(err).Errorf("Worker %d failed to process configuration, error channel full", workerID)
					}
				}

				workerProcessedCount++
				processedMutex.Lock()
				processedCount++
				currentProcessed := processedCount
				processedMutex.Unlock()

				if currentProcessed%100 == 0 || currentProcessed == int64(len(jobs)) {
					logrus.Infof("Progress: %d/%d configurations processed (%.1f%%)",
						currentProcessed, len(jobs), float64(currentProcessed)/float64(len(jobs))*100)
				}
			}

			logrus.Infof("Worker %d finished processing %d configurations", workerID, workerProcessedCount)
		}(i)
	}

	logrus.Infof("Processing %d configurations with %d threads", len(jobs), o.maxThreads)
	for _, job := range jobs {
		jobsChan <- job
	}
	close(jobsChan)

	wg.Wait()

	close(errorsChan)
	errorWg.Wait()

	errorMutex.Lock()
	finalErrors := make([]error, len(errors))
	copy(finalErrors, errors)
	errorMutex.Unlock()

	successCount := len(jobs) - len(finalErrors)
	logrus.Infof("Processing completed: %d successful, %d errors out of %d total configurations",
		successCount, len(finalErrors), len(jobs))

	if len(finalErrors) > 0 {
		logrus.Errorf("Failed to process %d configurations", len(finalErrors))
		return utilerrors.NewAggregate(finalErrors)
	}

	logrus.Info("All configurations processed successfully")
	return nil
}

func processConfiguration(job configJob, ocpVersion ocplifecycle.MajorMinor, workerID int, allowedClusterProfiles map[string]bool) error {
	output := config.DataWithInfo{Configuration: *job.configuration, Info: *job.info}

	configPath := job.info.RelativePath()
	logger := logrus.WithFields(logrus.Fields{
		"worker":  workerID,
		"org":     job.info.Metadata.Org,
		"repo":    job.info.Metadata.Repo,
		"branch":  job.info.Metadata.Branch,
		"config":  configPath,
		"variant": job.info.Metadata.Variant,
	})

	logger.Info("Worker processing configuration")

	originalTestCount := len(output.Configuration.Tests)
	updateIntervalFieldsForMatchedSteps(&output, ocpVersion, allowedClusterProfiles)

	modifiedTests := 0
	for _, test := range output.Configuration.Tests {
		if test.Cron != nil || test.Interval != nil {
			modifiedTests++
		}
	}

	if err := output.CommitTo(job.configDir); err != nil {
		logger.WithError(err).Error("Failed to commit configuration")
		return fmt.Errorf("failed to commit configuration for %s/%s@%s: %w",
			job.info.Metadata.Org, job.info.Metadata.Repo, job.info.Metadata.Branch, err)
	}

	logger.WithFields(logrus.Fields{
		"total_tests":    originalTestCount,
		"modified_tests": modifiedTests,
	}).Info("Worker completed processing configuration")
	return nil
}

func updateIntervalFieldsForMatchedSteps(
	configuration *config.DataWithInfo,
	version ocplifecycle.MajorMinor,
	allowedClusterProfiles map[string]bool,
) {
	testVersion, err := ocplifecycle.ParseMajorMinor(extractVersion(configuration.Info.Metadata.Branch))
	if err != nil {
		return
	}
	if configuration.Info.Metadata.Org == "openshift" || configuration.Info.Metadata.Org == "openshift-priv" {
		for i := range configuration.Configuration.Tests {
			test := &configuration.Configuration.Tests[i]
			if !strings.Contains(test.As, "mirror-nightly-image") && !strings.Contains(test.As, "promote-") {
				// Skip tests that don't match the cluster profiles filter
				if allowedClusterProfiles != nil && !shouldProcessTest(test, allowedClusterProfiles) {
					continue
				}
				if test.Cron != nil {
					n3Version := ocplifecycle.MajorMinor{Major: version.Major, Minor: version.Minor - 3}
					if testVersion.Less(n3Version) || *testVersion == n3Version {
						correctCron, err := isExecutedAtMostOncePerYear(*test.Cron)
						if err != nil {
							logrus.Warningf("Can't parse cron string %s", *test.Cron)
							continue
						}
						if !correctCron {
							*test.Cron = generateYearlyCron()
						}
					} else if testVersion.GetVersion() == fmt.Sprintf("%d.%d", version.Major, version.Minor-2) {
						correctCron, err := isExecutedAtMostXTimesAMonth(*test.Cron, 2)
						if err != nil {
							logrus.Warningf("Can't parse cron string %s", *test.Cron)
							continue
						}
						if !correctCron {
							*test.Cron = generateBiWeeklyCron()
						}
					} else if testVersion.GetVersion() == version.GetPastVersion() {
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
					n3Version := ocplifecycle.MajorMinor{Major: version.Major, Minor: version.Minor - 3}
					if testVersion.Less(n3Version) || *testVersion == n3Version {
						duration, err := time.ParseDuration(*test.Interval)
						if err != nil {
							logrus.Warningf("Can't parse interval string %s", *test.Interval)
							continue
						}
						if duration < time.Hour*24*365 {
							cronExpr := generateYearlyCron()
							test.Cron = &cronExpr
							test.Interval = nil
						}
					} else if testVersion.GetVersion() == fmt.Sprintf("%d.%d", version.Major, version.Minor-2) {
						duration, err := time.ParseDuration(*test.Interval)
						if err != nil {
							logrus.Warningf("Can't parse interval string %s", *test.Interval)
							continue
						}
						if duration < time.Hour*24*14 {
							cronExpr := generateBiWeeklyCron()
							test.Cron = &cronExpr
							test.Interval = nil
						}
					} else if testVersion.GetVersion() == version.GetPastVersion() {
						duration, err := time.ParseDuration(*test.Interval)
						if err != nil {
							logrus.Warningf("Can't parse interval string %s", *test.Interval)
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

// shouldProcessTest checks if a test should be processed based on cluster profiles filter
func shouldProcessTest(test *api.TestStepConfiguration, allowedClusterProfiles map[string]bool) bool {
	clusterProfile := test.GetClusterProfileName()

	// If the test doesn't have a cluster profile, include it
	if clusterProfile == "" {
		return true
	}

	// Check if the cluster profile is in the allowed list
	return allowedClusterProfiles[clusterProfile]
}

func isExecutedAtMostOncePerYear(cronExpr string) (bool, error) {
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
	end := start.AddDate(1, 0, 0)

	executionCount := 0
	maxIterations := 400
	iterations := 0

	for {
		iterations++
		if iterations > maxIterations {
			logrus.Warningf("Cron expression '%s' might be invalid, stopping after %d iterations", cronExpr, maxIterations)
			return false, fmt.Errorf("cron expression '%s' appears to be invalid or causes infinite loop", cronExpr)
		}

		next := schedule.Next(start)
		if next.After(end) || next.Equal(end) {
			break
		}
		executionCount++
		start = next
	}

	return executionCount <= 1, nil
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
	maxIterations := 100 // Allow counting up to ~100 executions per month (daily = ~31)
	iterations := 0

	for {
		iterations++
		if iterations > maxIterations {
			logrus.Warningf("Cron expression '%s' might be invalid, stopping after %d iterations", cronExpr, maxIterations)
			return false, fmt.Errorf("cron expression '%s' appears to be invalid or causes infinite loop", cronExpr)
		}

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

func generateYearlyCron() string {
	// Generate a cron that runs once per year on a random day
	// Format: minute hour day month *
	// Pick a random month (1-12) and day (1-28 to avoid month boundary issues)
	month := rand.Intn(12) + 1
	day := rand.Intn(28) + 1
	hour := rand.Intn(24)
	minute := rand.Intn(60)

	return fmt.Sprintf("%d %d %d %d *", minute, hour, day, month)
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
