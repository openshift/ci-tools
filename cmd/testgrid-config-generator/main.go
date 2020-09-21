package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
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
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	jc "github.com/openshift/ci-tools/pkg/jobconfig"
)

type options struct {
	releaseConfigDir  string
	testGridConfigDir string
	prowJobConfigDir  string
	jobsAllowListFile string
	validationOnlyRun bool
}

func (o *options) Validate() error {
	if o.prowJobConfigDir == "" {
		return errors.New("--prow-jobs-dir is required")
	}
	if o.releaseConfigDir == "" {
		return errors.New("--release-config is required")
	}
	if o.testGridConfigDir == "" {
		return errors.New("--testgrid-config is required")
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
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse input")
	}
	return o
}

// dashboard contains the release/version/type specific data for jobs
type dashboard struct {
	*config.Dashboard
	testGroups []*config.TestGroup
	existing   sets.String
}

func genericDashboardFor(role string) *dashboard {
	return &dashboard{
		Dashboard: &config.Dashboard{
			Name:         fmt.Sprintf("redhat-openshift-%s", role),
			DashboardTab: []*config.DashboardTab{},
		},
		testGroups: []*config.TestGroup{},
		existing:   sets.NewString(),
	}
}

func dashboardFor(stream, version, role string) *dashboard {
	return &dashboard{
		Dashboard: &config.Dashboard{
			Name:         fmt.Sprintf("redhat-openshift-%s-release-%s-%s", stream, version, role),
			DashboardTab: []*config.DashboardTab{},
		},
		testGroups: []*config.TestGroup{},
		existing:   sets.NewString(),
	}
}

// dashboardTabFor builds a dashboard tab with default values injected
func dashboardTabFor(name, description string) *config.DashboardTab {
	return &config.DashboardTab{
		Name:             name,
		Description:      description,
		TestGroupName:    name,
		BaseOptions:      "width=10&exclude-filter-by-regex=Monitor%5Cscluster&exclude-filter-by-regex=%5Eoperator.Run%20template.*container%20test%24",
		OpenTestTemplate: &config.LinkTemplate{Url: fmt.Sprintf("%s/view/gcs/<gcs_prefix>/<changelist>", api.URLForService(api.ServiceProw))},
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

func testGroupFor(name string, daysOfResults int32) *config.TestGroup {
	return &config.TestGroup{
		Name:          name,
		GcsPrefix:     fmt.Sprintf("origin-ci-test/logs/%s", name),
		DaysOfResults: daysOfResults,
	}
}

func (d *dashboard) add(name string, description string, daysOfResults int32) {
	if d.existing.Has(name) {
		return
	}
	d.existing.Insert(name)
	d.Dashboard.DashboardTab = append(d.Dashboard.DashboardTab, dashboardTabFor(name, description))
	d.testGroups = append(d.testGroups, testGroupFor(name, daysOfResults))
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
		} else if releaseType != "informing" && releaseType != "broken" && releaseType != "generic-informing" {
			errs = append(errs, fmt.Errorf("%s: release_type must be one of 'informing', 'broken' or 'generic-informing'", jobName))
		}
	}
	return allowList, utilerrors.NewAggregate(errs)
}

// release is a subset of fields from the release controller's config
type release struct {
	Name   string
	Verify map[string]struct {
		Optional bool `json:"optional"`
		Upgrade  bool `json:"upgrade"`
		ProwJob  struct {
			Name        string            `json:"name"`
			Annotations map[string]string `json:"annotations"`
		} `json:"prowJob"`
	} `json:"verify"`
}

var reVersion = regexp.MustCompile(`-(\d+\.\d+)(-|$)`)

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

	// read the list of jobs from the allow list along with its release-type
	var allowList map[string]string
	if o.jobsAllowListFile != "" {
		data, err := ioutil.ReadFile(o.jobsAllowListFile)
		if err != nil {
			logrus.WithError(err).Fatalf("could not read allow-list at %s", o.jobsAllowListFile)
		}
		allowList, err = getAllowList(data)
		if err != nil {
			logrus.WithError(err).Fatalf("failed to build allow-list dictionary")
		}
		if o.validationOnlyRun {
			os.Exit(0)
		}
	}

	if err := o.Validate(); err != nil {
		logrus.Fatalf("Invalid options: %v", err)
	}

	// find the default type for jobs referenced by the release controllers
	configuredJobs := make(map[string]string)
	if err := filepath.Walk(o.releaseConfigDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		data, err := ioutil.ReadFile(path)
		if err != nil {
			return fmt.Errorf("could not read release controller config at %s: %w", path, err)
		}

		var releaseConfig release
		if err := json.Unmarshal(data, &releaseConfig); err != nil {
			return fmt.Errorf("could not unmarshal release controller config at %s: %w", path, err)
		}

		for _, job := range releaseConfig.Verify {
			existing := configuredJobs[job.ProwJob.Name]
			var dashboardType string
			switch {
			case job.Upgrade:
				dashboardType = "generic-informing"
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
			configuredJobs[job.ProwJob.Name] = dashboardType
		}
		return nil
	}); err != nil {
		logrus.WithError(err).Fatal("Could not process input configurations.")
	}

	// find and assign all jobs to the dashboards
	dashboards := make(map[string]*dashboard)
	jobConfig, err := jc.ReadFromDir(o.prowJobConfigDir)
	if err != nil {
		logrus.WithError(err).Fatalf("Failed to load Prow jobs %s", o.prowJobConfigDir)
	}

	for _, p := range jobConfig.Periodics {
		name := p.Name
		calculateDays := len(p.Cron) > 0 || len(p.Interval) > 0
		var dashboardType string
		label := allowList[name]
		switch label {
		case "informing", "blocking", "broken", "generic-informing":
			dashboardType = label
			if label == "informing" && configuredJobs[p.Name] == "blocking" {
				dashboardType = "blocking"
				calculateDays = false
			}
		default:
			label, ok := configuredJobs[name]
			if !ok {
				continue
			}
			dashboardType = label
			calculateDays = false
		}

		var current *dashboard
		switch dashboardType {
		case "generic-informing":
			current = genericDashboardFor("informing")
		default:
			var stream string
			switch {
			case strings.Contains(name, "-ocp-") || strings.Contains(name, "-origin-"):
				stream = "ocp"
			case strings.Contains(name, "-okd-"):
				stream = "okd"
			case strings.HasPrefix(name, "promote-release-openshift-"):
				// TODO fix these jobs to have a consistent name
				stream = "ocp"
			default:
				logrus.Warningf("unrecognized release type in job: %s", name)
				continue
			}
			version := p.Labels["job-release"]
			if len(version) == 0 {
				m := reVersion.FindStringSubmatch(name)
				if len(m) == 0 {
					logrus.Warningf("release is not in -X.Y- form: %s", name)
					continue
				}
				version = m[1]
			}

			current = dashboardFor(stream, version, dashboardType)
		}
		if existing, ok := dashboards[current.Name]; ok {
			current = existing
		} else {
			dashboards[current.Name] = current
		}

		daysOfResults := int32(0)
		if calculateDays {
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
		}
		current.add(p.Name, p.Annotations["description"], daysOfResults)
	}

	// first, update the overall list of dashboards that exist for the redhat group
	dashboardNames := sets.NewString()
	for _, dash := range dashboards {
		if len(dash.testGroups) == 0 {
			continue
		}
		dashboardNames.Insert(dash.Name)
	}

	groupFile := path.Join(o.testGridConfigDir, "groups.yaml")
	data, err := ioutil.ReadFile(groupFile)
	if err != nil {
		logrus.WithError(err).Fatal("Could not read TestGrid group config")
	}

	var groups config.Configuration
	if err := yaml.Unmarshal(data, &groups); err != nil {
		logrus.WithError(err).Fatal("Could not unmarshal TestGrid group config")
	}

	for _, dashGroup := range groups.DashboardGroups {
		if dashGroup.Name == "redhat" {
			dashboardNames.Insert(dashGroup.DashboardNames...)
			dashGroup.DashboardNames = dashboardNames.List() // sorted implicitly
		}
	}

	data, err = yaml.Marshal(&groups)
	if err != nil {
		logrus.WithError(err).Fatal("Could not marshal TestGrid group config")
	}

	if err := ioutil.WriteFile(groupFile, data, 0664); err != nil {
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

		if err := ioutil.WriteFile(path.Join(o.testGridConfigDir, fmt.Sprintf("%s.yaml", dash.Name)), data, 0664); err != nil {
			logrus.WithError(err).Fatalf("Could not write TestGrid config for %s", dash.Name)
		}
	}
	logrus.Info("Finished generating TestGrid dashboards.")
}
