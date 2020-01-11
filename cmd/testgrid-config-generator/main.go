package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/GoogleCloudPlatform/testgrid/pb/config"
	jc "github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/sirupsen/logrus"
	prowconfig "k8s.io/test-infra/prow/config"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/yaml"
)

type options struct {
	releaseConfigDir  string
	testGridConfigDir string
	prowJobConfigDir  string
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
func dashboardTabFor(name string) *config.DashboardTab {
	return &config.DashboardTab{
		Name:             name,
		TestGroupName:    name,
		BaseOptions:      "width=10&exclude-filter-by-regex=Monitor%5Cscluster&exclude-filter-by-regex=%5Eoperator.Run%20template.*container%20test%24",
		OpenTestTemplate: &config.LinkTemplate{Url: "https://prow.svc.ci.openshift.org/view/gcs/<gcs_prefix>/<changelist>"},
		FileBugTemplate: &config.LinkTemplate{
			Url: "https://github.com/openshift/origin/issues/new",
			Options: []*config.LinkOptionsTemplate{
				{Key: "title", Value: "E2E: <test-name>"},
				{Key: "body", Value: "<test-url>"},
			},
		},
		OpenBugTemplate:       &config.LinkTemplate{Url: "https://github.com/openshift/origin/issues/"},
		ResultsUrlTemplate:    &config.LinkTemplate{Url: "https://prow.svc.ci.openshift.org/job-history/<gcs_prefix>"},
		CodeSearchPath:        "https://github.com/openshift/origin/search",
		CodeSearchUrlTemplate: &config.LinkTemplate{Url: "https://github.com/openshift/origin/compare/<start-custom-0>...<end-custom-0>"},
	}
}

func testGroupFor(name string) *config.TestGroup {
	return &config.TestGroup{
		Name:      name,
		GcsPrefix: fmt.Sprintf("origin-ci-test/logs/%s", name),
	}
}

func (d *dashboard) add(name string) {
	if d.existing.Has(name) {
		return
	}
	d.existing.Insert(name)
	d.Dashboard.DashboardTab = append(d.Dashboard.DashboardTab, dashboardTabFor(name))
	d.testGroups = append(d.testGroups, testGroupFor(name))
}

// release is a subset of fields from the release controller's config
type release struct {
	Name   string
	Verify map[string]struct {
		Optional bool `json:"optional"`
		ProwJob  struct {
			Name string `json:"name"`
		} `json:"prowJob"`
	} `json:"verify"`
}

var reVersion = regexp.MustCompile(`^(\d+\.\d+)\.\d+-0\.`)

// This tool is intended to make the process of maintaining TestGrid dashboards for
// release-gating and release-informing tests simple.
//
// We read the release controller's configuration for all of the release candidates
// being tested and auto-generate TestGrid configuration for the jobs involved,
// partitioning them by which release they are using (OKD or OCP), which version they
// run for and whether or not they are informing or blocking.
func main() {
	o := gatherOptions()
	if err := o.Validate(); err != nil {
		logrus.Fatalf("Invalid options: %v", err)
	}

	informingPeriodics := make(map[string]prowconfig.Periodic)
	jobConfig, err := jc.ReadFromDir(o.prowJobConfigDir)
	if err != nil {
		logrus.WithError(err).Fatalf("Failed to load Prow jobs %s", o.prowJobConfigDir)
	}
	for _, p := range jobConfig.Periodics {
		if p.Labels["ci.openshift.io/release-type"] == "informing" {
			informingPeriodics[p.Name] = p
		}
	}

	unique := sets.NewString()
	dashboards := make(map[string]*dashboard)
	genericInforming := genericDashboardFor("informing")
	dashboards[genericInforming.Name] = genericInforming

	if err := filepath.Walk(o.releaseConfigDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		data, err := ioutil.ReadFile(path)
		if err != nil {
			return fmt.Errorf("could not read release controller config at %s: %v", path, err)
		}

		var releaseConfig release
		if err := json.Unmarshal(data, &releaseConfig); err != nil {
			return fmt.Errorf("could not unmarshal release controller config at %s: %v", path, err)
		}

		var stream string
		switch {
		case strings.HasSuffix(releaseConfig.Name, ".ci") || strings.HasSuffix(releaseConfig.Name, ".nightly") || strings.HasPrefix(releaseConfig.Name, "stable-4."):
			stream = "ocp"
		case strings.HasSuffix(releaseConfig.Name, ".okd"):
			stream = "okd"
		default:
			logrus.Infof("release is not recognized: %s", releaseConfig.Name)
			return nil
		}
		m := reVersion.FindStringSubmatch(releaseConfig.Name)
		if len(m) == 0 {
			logrus.Infof("release is not in X.Y.Z form: %s", releaseConfig.Name)
			return nil
		}
		version := m[1]

		blocking := dashboardFor(stream, version, "blocking")
		if existing, ok := dashboards[blocking.Name]; ok {
			blocking = existing
		} else {
			dashboards[blocking.Name] = blocking
		}
		informing := dashboardFor(stream, version, "informing")
		if existing, ok := dashboards[informing.Name]; ok {
			informing = existing
		} else {
			dashboards[informing.Name] = informing
		}

		for _, job := range releaseConfig.Verify {
			if unique.Has(job.ProwJob.Name) {
				continue
			}
			unique.Insert(job.ProwJob.Name)

			delete(informingPeriodics, job.ProwJob.Name)
			if job.ProwJob.Name == "release-openshift-origin-installer-e2e-aws-upgrade" {
				genericInforming.add(job.ProwJob.Name)
				continue
			}
			if job.Optional {
				informing.add(job.ProwJob.Name)
			} else {
				blocking.add(job.ProwJob.Name)
			}
		}
		for _, p := range informingPeriodics {
			if p.Labels["job-release"] != version {
				continue
			}
			switch stream {
			case "okd":
				if !strings.Contains(p.Name, "-openshift-okd-") {
					continue
				}
			case "ocp":
				// preparing to rename the jobs from -openshift-origin- to -openshift-ci-, remove -origin-
				// after that rename
				if !strings.Contains(p.Name, "-openshift-origin-") && !strings.Contains(p.Name, "-openshift-ci-") && !strings.Contains(p.Name, "-openshift-ocp-") {
					continue
				}
			}

			if unique.Has(p.Name) {
				continue
			}
			unique.Insert(p.Name)

			informing.add(p.Name)
			delete(informingPeriodics, p.Name)
		}
		return nil
	}); err != nil {
		logrus.WithError(err).Fatal("Could not process input configurations.")
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
