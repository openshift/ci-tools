package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/GoogleCloudPlatform/testgrid/pb/config"
	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	prowConfig "k8s.io/test-infra/prow/config"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	jc "github.com/openshift/ci-tools/pkg/jobconfig"
	releaseconfig "github.com/openshift/ci-tools/pkg/release/config"
	"github.com/openshift/ci-tools/pkg/util"
	"github.com/openshift/ci-tools/pkg/util/gzip"
)

type options struct {
	releaseConfigDir  string
	testGridConfigDir string
	prowJobConfigDir  string

	validationOnlyRun bool
	jobsAllowListFile string

	gcsBucket string
}

const defaultAggregateProwJobName = "release-openshift-release-analysis-aggregator"

func (o *options) Validate() error {
	if o.prowJobConfigDir == "" && !o.validationOnlyRun {
		return errors.New("--prow-jobs-dir is required")
	}
	if o.releaseConfigDir == "" {
		return errors.New("--release-config is required")
	}
	if o.testGridConfigDir == "" && !o.validationOnlyRun {
		return errors.New("--testgrid-config is required")
	}

	if o.jobsAllowListFile == "" {
		return errors.New("--allow-list is required")
	}

	return nil
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.prowJobConfigDir, "prow-jobs-dir", "", "Path to a root of directory structure with Prow job config files (ci-operator/jobs in openshift/release)")
	fs.StringVar(&o.releaseConfigDir, "release-config", "", "Path to Release Controller configuration directory.")
	fs.StringVar(&o.testGridConfigDir, "testgrid-config", "", "Path to TestGrid configuration directory.")
	fs.StringVar(&o.jobsAllowListFile, "allow-list", "", "Path to file containing jobs to be overridden to informing jobs")
	fs.BoolVar(&o.validationOnlyRun, "validate", false, "Validate entries in file specified by allow-list (if allow_list is not specified validation would succeed)")
	fs.StringVar(&o.gcsBucket, "google-storage-bucket", "origin-ci-test", "The optional GCS Bucket holding test artifacts")
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse input")
	}
	return o
}

// dashboard contains the release/version/type specific data for jobs
type dashboard struct {
	*config.Dashboard
	testGroups []*config.TestGroup
	existing   sets.Set[string]
}

func genericDashboardFor(role string) *dashboard {
	return &dashboard{
		Dashboard: &config.Dashboard{
			Name:         fmt.Sprintf("redhat-openshift-%s", role),
			DashboardTab: []*config.DashboardTab{},
		},
		testGroups: []*config.TestGroup{},
		existing:   sets.New[string](),
	}
}

func dashboardFor(stream, version, role string) *dashboard {
	return &dashboard{
		Dashboard: &config.Dashboard{
			Name:         fmt.Sprintf("redhat-openshift-%s-release-%s-%s", stream, version, role),
			DashboardTab: []*config.DashboardTab{},
		},
		testGroups: []*config.TestGroup{},
		existing:   sets.New[string](),
	}
}

// dashboardTabFor builds a dashboard tab with default values injected
func dashboardTabFor(name, description string) *config.DashboardTab {
	return &config.DashboardTab{
		Name:             name,
		Description:      description,
		TestGroupName:    name,
		BaseOptions:      "width=10&exclude-filter-by-regex=Monitor%5Cscluster&exclude-filter-by-regex=%5Eoperator.Run%20template.*container%20test%24",
		OpenTestTemplate: &config.LinkTemplate{Url: fmt.Sprintf("%s/view/gs/<gcs_prefix>/<changelist>", api.URLForService(api.ServiceProw))},
		FileBugTemplate: &config.LinkTemplate{
			Url: "https://bugzilla.redhat.com/enter_bug.cgi",
			Options: []*config.LinkOptionsTemplate{
				{
					Key:   "classification",
					Value: "Red Hat",
				},
				{
					Key:   "product",
					Value: "OpenShift Container Platform",
				},
				{
					Key:   "cf_internal_whiteboard",
					Value: "buildcop",
				},
				{
					Key:   "short_desc",
					Value: "test: <test-name>",
				},
				{
					Key:   "cf_environment",
					Value: "test: <test-name>",
				},
				{
					Key:   "comment",
					Value: "test: <test-name> failed, see job: <link>",
				},
			},
		},
		OpenBugTemplate:       &config.LinkTemplate{Url: "https://github.com/openshift/origin/issues/"},
		ResultsUrlTemplate:    &config.LinkTemplate{Url: fmt.Sprintf("%s/job-history/<gcs_prefix>", api.URLForService(api.ServiceProw))},
		CodeSearchPath:        "https://github.com/openshift/origin/search",
		CodeSearchUrlTemplate: &config.LinkTemplate{Url: "https://github.com/openshift/origin/compare/<start-custom-0>...<end-custom-0>"},
	}
}

func testGroupFor(bucket, name string, daysOfResults int32) *config.TestGroup {
	return &config.TestGroup{
		Name:          name,
		GcsPrefix:     fmt.Sprintf("%s/logs/%s", bucket, name),
		DaysOfResults: daysOfResults,
	}
}

func (d *dashboard) add(bucket, name string, description string, daysOfResults int32) {
	if d.existing.Has(name) {
		return
	}
	d.existing.Insert(name)
	d.Dashboard.DashboardTab = append(d.Dashboard.DashboardTab, dashboardTabFor(name, description))
	d.testGroups = append(d.testGroups, testGroupFor(bucket, name, daysOfResults))
}

func getAllowList(data []byte) (map[string]string, error) {
	var allowList map[string]string
	var errs []error
	if err := yaml.Unmarshal(data, &allowList); err != nil {
		return nil, fmt.Errorf("could not unmarshal allow-list: %w", err)
	}
	// Validate that there is no entry in the allow-list marked as blocking
	// since blocking jobs must be in the release controller configuration
	for jobName, releaseType := range allowList {
		if releaseType == "blocking" {
			errs = append(errs, fmt.Errorf("release_type 'blocking' not permitted in the allow-list for %s, blocking jobs must be in the release controller configuration", jobName))
		} else if releaseType != "informing" && releaseType != "broken" && releaseType != "generic-informing" && releaseType != "osde2e" && releaseType != "olm" {
			errs = append(errs, fmt.Errorf("%s: release_type must be one of 'informing', 'broken', 'generic-informing', 'osde2e' or 'olm'", jobName))
		}
	}
	return allowList, utilerrors.NewAggregate(errs)
}

var reVersion = regexp.MustCompile(`-(\d+\.\d+)(-|$)`)

func addDashboardTab(p prowConfig.Periodic,
	dashboards map[string]*dashboard,
	configuredJobs map[string]string,
	allowList map[string]string,
	aggregateJobName *string, bucket string) {
	prowName := p.Name
	jobName := p.Name
	var dashboardType string

	if aggregateJobName != nil {
		jobName = *aggregateJobName
	}
	label, ok := allowList[jobName]
	if len(label) == 0 && ok {
		// if the allow list has an empty label for the type, exclude it from dashboards
		return
	}

	switch label {
	case "informing", "blocking", "broken", "generic-informing", "osde2e", "olm":
		dashboardType = label
		if (label == "informing" || label == "osde2e" || label == "olm") && (aggregateJobName != nil || configuredJobs[p.Name] == "blocking") {
			dashboardType = "blocking"
		}
	default:
		if aggregateJobName != nil {
			dashboardType = "blocking"
			break
		} else if label, ok := configuredJobs[prowName]; ok {
			dashboardType = label
			break
		}
		switch {
		case util.IsSpecialInformingJobOnTestGrid(prowName):
			// the standard release periodics should always appear in testgrid
			dashboardType = "informing"
		case strings.Contains(prowName, "-lp-interop"):
			// OpenShift layered product interop testing
			dashboardType = "informing"
		case strings.Contains(prowName, "-lp-rosa-hypershift"):
			// OpenShift layered product rosa hypershift interop testing
			dashboardType = "informing"
		case strings.Contains(prowName, "CSPI-QE-MSI"):
			// Managed Services Integration (MSI) testing
			dashboardType = "informing"
		default:
			// unknown labels or non standard jobs do not appear in testgrid
			return
		}
	}

	var current *dashboard
	switch dashboardType {
	case "generic-informing":
		current = genericDashboardFor("informing")
	case "osde2e":
		current = genericDashboardFor("osd")
	case "olm":
		current = genericDashboardFor("olm")
	default:
		var stream string
		switch {
		case
			// these will be removable once most / all jobs are generated periodics and are for legacy release-* only
			strings.Contains(prowName, "-ocp-"),
			strings.Contains(prowName, "-origin-"),
			// these prefixes control whether a job is ocp or okd going forward
			strings.HasPrefix(prowName, "periodic-ci-openshift-hypershift-main-periodics-"),
			strings.HasPrefix(prowName, "periodic-ci-openshift-multiarch"),
			strings.HasPrefix(prowName, "periodic-ci-openshift-openshift-tests-"),
			strings.HasPrefix(prowName, "periodic-ci-openshift-release-master-ci-"),
			strings.HasPrefix(prowName, "periodic-ci-openshift-release-master-nightly-"),
			strings.HasPrefix(prowName, "periodic-ci-openshift-verification-tests-master-"),
			strings.HasPrefix(prowName, "periodic-ci-shiftstack-shiftstack-ci-main-periodic-"),
			strings.HasPrefix(prowName, "periodic-ci-openshift-osde2e-main-nightly-"):
			stream = "ocp"
		case strings.Contains(prowName, "-okd-"):
			stream = "okd"
		case strings.HasPrefix(prowName, "promote-release-openshift-"):
			// TODO fix these jobs to have a consistent name
			stream = "ocp"
		case strings.Contains(prowName, "-lp-interop"):
			// OpenShift layered product interop testing
			stream = "lp-interop"
		case strings.Contains(prowName, "-lp-rosa-hypershift"):
			// OpenShift layered product rosa hypershift interop testing
			stream = "lp-rosa-hypershift"
		case strings.Contains(prowName, "CSPI-QE-MSI"):
			// Managed Services Integration (MSI) testing
			stream = "CSPI-QE-MSI"
		default:
			logrus.Warningf("unrecognized release type in job: %s", prowName)
			return
		}

		version := p.Labels["job-release"]
		if len(version) == 0 {
			m := reVersion.FindStringSubmatch(prowName)
			if len(m) == 0 {
				logrus.Warningf("release is not in -X.Y- form and will go into the generic informing dashboard: %s", prowName)
				current = genericDashboardFor("informing")
				break
			}
			version = m[1]
		}
		current = dashboardFor(stream, version, dashboardType)
	}

	daysOfResults := int32(0)
	// for infrequently run jobs (at 12h or 24h intervals) we'd prefer to have more history than just the default
	// 7-10 days (specified by the default testgrid config), so try to set number of days of results so that we
	// see at least 100 entries, capping out at 2 months (60 days).
	desiredResults := 100
	if len(p.Interval) > 0 {
		if interval, err := time.ParseDuration(p.Interval); err == nil && interval > 0 && interval < (14*24*time.Hour) {
			daysOfResults = int32(math.Round(float64(time.Duration(desiredResults)*interval) / float64(24*time.Hour)))
			if daysOfResults < 7 {
				daysOfResults = 0
			}
			if daysOfResults > 60 {
				daysOfResults = 60
			}
		}
	}

	if existing, ok := dashboards[current.Name]; ok {
		current = existing
	} else {
		dashboards[current.Name] = current
	}

	current.add(bucket, jobName, p.Annotations["description"], daysOfResults)

}

// This tool is intended to make the process of maintaining TestGrid dashboards for
// release-gating and release-informing tests simple.
//
// We read all jobs that are annotated for the grid. The release controller's configuration
// is used to default those roles but they can be overridden per job. We partition by overall
// type (blocking, informing, broken), version or generic (generic have no version), and by
// release type (ocp or okd). If the job is blocking on a release definition it will be
// upgraded from informing to blocking if the job is set to informing.
func main() {
	o := gatherOptions()

	if err := o.Validate(); err != nil {
		logrus.Fatalf("Invalid options: %v", err)
	}

	// find the default type for jobs referenced by the release controllers
	configuredJobs := make(map[string]string)
	aggregateJobsMap := make(map[string][]string)
	if err := filepath.WalkDir(o.releaseConfigDir, func(path string, info fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		data, err := gzip.ReadFileMaybeGZIP(path)
		if err != nil {
			return fmt.Errorf("could not read release controller config at %s: %w", path, err)
		}

		var releaseConfig releaseconfig.Config
		if err := json.Unmarshal(data, &releaseConfig); err != nil {
			return fmt.Errorf("could not unmarshal release controller config at %s: %w", path, err)
		}

		for name, job := range releaseConfig.Verify {
			existing := configuredJobs[job.ProwJob.Name]
			var dashboardType string
			switch {
			case job.Upgrade:
				dashboardType = "informing"
			case job.Optional:
				if existing == "generic-informing" || existing == "blocking" {
					continue
				}
				dashboardType = "informing"
			default:
				if existing == "generic-informing" {
					continue
				}
				dashboardType = "blocking"
			}
			if job.AggregatedProwJob != nil {
				jobName := defaultAggregateProwJobName
				if job.AggregatedProwJob.ProwJob != nil && len(job.AggregatedProwJob.ProwJob.Name) > 0 {
					jobName = job.AggregatedProwJob.ProwJob.Name
				}
				aggregateJobName := fmt.Sprintf("%s-%s", name, jobName)
				if _, ok := aggregateJobsMap[job.ProwJob.Name]; !ok {
					aggregateJobsMap[job.ProwJob.Name] = []string{aggregateJobName}
				} else {
					aggregateJobsMap[job.ProwJob.Name] = append(aggregateJobsMap[job.ProwJob.Name], aggregateJobName)
				}
			}
			configuredJobs[job.ProwJob.Name] = dashboardType
		}
		return nil
	}); err != nil {
		logrus.WithError(err).Fatal("Could not process input configurations.")
	}

	// read the list of jobs from the allow list along with its release-type
	var allowList map[string]string
	if o.jobsAllowListFile != "" {
		data, err := gzip.ReadFileMaybeGZIP(o.jobsAllowListFile)
		if err != nil {
			logrus.WithError(err).Fatalf("could not read allow-list at %s", o.jobsAllowListFile)
		}
		allowList, err = getAllowList(data)
		if err != nil {
			logrus.WithError(err).Fatalf("failed to build allow-list dictionary")
		}
		var disallowed []string
		for job := range allowList {
			if value, configured := configuredJobs[job]; configured && value == "blocking" {
				disallowed = append(disallowed, job)
			}
		}
		if len(disallowed) != 0 {
			logrus.Fatalf("The following jobs are blocking by virtue of being in the release-controller configuration, but are also in the allow-list. Their entries in the allow-list are disallowed and should be removed: %v", strings.Join(disallowed, ", "))
		}
		if o.validationOnlyRun {
			os.Exit(0)
		}
	}

	// find and assign all jobs to the dashboards
	dashboards := make(map[string]*dashboard)
	jobConfig, err := jc.ReadFromDir(o.prowJobConfigDir)
	if err != nil {
		logrus.WithError(err).Fatalf("Failed to load Prow jobs %s", o.prowJobConfigDir)
	}

	for _, p := range jobConfig.Periodics {
		addDashboardTab(p, dashboards, configuredJobs, allowList, nil, o.gcsBucket)
		if aggregateJobs, ok := aggregateJobsMap[p.Name]; ok {
			for _, aggregateJob := range aggregateJobs {
				addDashboardTab(p, dashboards, configuredJobs, allowList, &aggregateJob, o.gcsBucket)
			}
		}
	}

	// first, update the overall list of dashboards that exist for the redhat group
	dashboardNames := sets.New[string]()
	for _, dash := range dashboards {
		if len(dash.testGroups) == 0 {
			continue
		}
		dashboardNames.Insert(dash.Name)
	}

	groupFile := path.Join(o.testGridConfigDir, "groups.yaml")
	data, err := gzip.ReadFileMaybeGZIP(groupFile)
	if err != nil {
		logrus.WithError(err).Fatal("Could not read TestGrid group config")
	}

	var groups config.Configuration
	if err := yaml.Unmarshal(data, &groups); err != nil {
		logrus.WithError(err).Fatal("Could not unmarshal TestGrid group config")
	}

	toRemove := sets.New[string]()
	for _, dashGroup := range groups.DashboardGroups {
		if dashGroup.Name == "redhat" {
			for _, name := range sets.List(sets.New[string](dashGroup.DashboardNames...).Difference(dashboardNames)) {
				if strings.HasPrefix(name, "redhat-openshift-") && strings.Contains(name, "-release-") {
					// this is a good-enough heuristic to identify a board that was generated by this tool in the past,
					// but is no longer generated and should be pruned.
					toRemove.Insert(name)
				}
			}
			dashboardNames.Insert(dashGroup.DashboardNames...).Delete(sets.List(toRemove)...)
			dashGroup.DashboardNames = sets.List(dashboardNames) // sorted implicitly
		}
	}

	data, err = yaml.Marshal(&groups)
	if err != nil {
		logrus.WithError(err).Fatal("Could not marshal TestGrid group config")
	}

	if err := os.WriteFile(groupFile, data, 0664); err != nil {
		logrus.WithError(err).Fatal("Could not write TestGrid group config")
	}

	// then, rewrite any dashboard configs we are generating
	for _, dash := range dashboards {
		if len(dash.testGroups) == 0 {
			continue
		}
		partialConfig := config.Configuration{
			TestGroups: dash.testGroups,
			Dashboards: []*config.Dashboard{dash.Dashboard},
		}
		sort.Slice(partialConfig.TestGroups, func(i, j int) bool {
			return partialConfig.TestGroups[i].Name < partialConfig.TestGroups[j].Name
		})
		sort.Slice(partialConfig.Dashboards, func(i, j int) bool {
			return partialConfig.Dashboards[i].Name < partialConfig.Dashboards[j].Name
		})
		for k := range partialConfig.Dashboards {
			sort.Slice(partialConfig.Dashboards[k].DashboardTab, func(i, j int) bool {
				return partialConfig.Dashboards[k].DashboardTab[i].Name < partialConfig.Dashboards[k].DashboardTab[j].Name
			})
		}
		data, err = yaml.Marshal(&partialConfig)
		if err != nil {
			logrus.WithError(err).Fatalf("Could not marshal TestGrid config for %s", dash.Name)
		}

		if err := os.WriteFile(path.Join(o.testGridConfigDir, fmt.Sprintf("%s.yaml", dash.Name)), data, 0664); err != nil {
			logrus.WithError(err).Fatalf("Could not write TestGrid config for %s", dash.Name)
		}
	}

	// remove any configs we used to generate but no longer do
	for _, name := range sets.List(toRemove) {
		if err := os.Remove(path.Join(o.testGridConfigDir, fmt.Sprintf("%s.yaml", name))); err != nil {
			logrus.WithError(err).Fatalf("Could not remove stale TestGrid config for %s", name)
		}
	}

	logrus.Info("Finished generating TestGrid dashboards.")
}
