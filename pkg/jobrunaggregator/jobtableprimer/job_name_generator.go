package jobtableprimer

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
)

type FakeReleaseConfig struct {
	Verify map[string]FakeReleaseConfigVerify
}
type FakeReleaseConfigVerify struct {
	ProwJob FakeProwJob
}
type FakeProwJob struct {
	Name string
}

type FakePeriodicConfig struct {
	Periodics []FakePeriodic `yaml:"periodics"`
}
type FakePeriodic struct {
	Name string `yaml:"name"`
}

type jobNameGenerator struct {
	periodicURLs      []string
	releaseConfigURLs []string
	releases          []jobrunaggregatorapi.ReleaseRow
}

var (
	periodicURLTemplates = []string{
		"https://raw.githubusercontent.com/openshift/release/main/ci-operator/jobs/openshift/release/openshift-release-release-%s-periodics.yaml",
		"https://raw.githubusercontent.com/openshift/release/main/ci-operator/jobs/openshift/hypershift/openshift-hypershift-release-%s-periodics.yaml",
	}
	releaseConfigURLTemplates = []string{
		"https://raw.githubusercontent.com/openshift/release/main/core-services/release-controller/_releases/release-ocp-%s-arm64.json",
		"https://raw.githubusercontent.com/openshift/release/main/core-services/release-controller/_releases/release-ocp-%s-ci.json",
		"https://raw.githubusercontent.com/openshift/release/main/core-services/release-controller/_releases/release-ocp-%s-multi.json",
		"https://raw.githubusercontent.com/openshift/release/main/core-services/release-controller/_releases/release-ocp-%s-ppc64le.json",
		"https://raw.githubusercontent.com/openshift/release/main/core-services/release-controller/_releases/release-ocp-%s-s390x.json",
		"https://raw.githubusercontent.com/openshift/release/main/core-services/release-controller/_releases/release-ocp-%s.json",
	}
)

func newJobNameGenerator() *jobNameGenerator {
	generator := &jobNameGenerator{
		periodicURLs: []string{
			"https://raw.githubusercontent.com/openshift/release/main/ci-operator/jobs/openshift/release/openshift-release-main-periodics.yaml",
			"https://raw.githubusercontent.com/openshift/release/main/ci-operator/jobs/openshift/multiarch/openshift-multiarch-main-periodics.yaml",
		},
		releaseConfigURLs: []string{},
	}
	sort.Strings(generator.periodicURLs)
	return generator
}

func (s *jobNameGenerator) addReleaseURLs(release string) {
	for _, urlTemplate := range periodicURLTemplates {
		url := fmt.Sprintf(urlTemplate, release)
		s.periodicURLs = append(s.periodicURLs, url)
	}
	for _, urlTemplate := range releaseConfigURLTemplates {
		url := fmt.Sprintf(urlTemplate, release)
		s.releaseConfigURLs = append(s.releaseConfigURLs, url)
	}
	sort.Strings(s.periodicURLs)
	sort.Strings(s.releaseConfigURLs)
}

func (s *jobNameGenerator) UpdateURLsForAllReleases(releases []jobrunaggregatorapi.ReleaseRow) {
	s.releases = releases
	for _, release := range s.releases {
		s.addReleaseURLs(release.Release)
	}
}

func (s *jobNameGenerator) GenerateJobNames() ([]string, error) {
	jobNames := []string{}

	for _, url := range s.releaseConfigURLs {
		resp, err := http.Get(url)
		if err != nil {
			return jobNames, fmt.Errorf("error reading %v: %w", url, err)
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return jobNames, fmt.Errorf("error reading %v: %v", url, resp.StatusCode)
		}

		content, err := io.ReadAll(resp.Body)
		if err != nil {
			return jobNames, fmt.Errorf("error reading %v: %w", url, err)
		}
		resp.Body.Close()

		releaseConfig := &FakeReleaseConfig{}
		if err := json.Unmarshal(content, releaseConfig); err != nil {
			return jobNames, fmt.Errorf("error reading %v: %w", url, err)
		}

		jobNames = append(jobNames, fmt.Sprintf("// begin %v", url))
		localLines := []string{}
		for _, curr := range releaseConfig.Verify {
			localLines = append(localLines, curr.ProwJob.Name)
		}
		sort.Strings(localLines)
		jobNames = append(jobNames, localLines...)
		jobNames = append(jobNames, fmt.Sprintf("// end %v", url))
		jobNames = append(jobNames, "")
	}

	for _, url := range s.periodicURLs {
		resp, err := http.Get(url)
		if err != nil {
			return jobNames, fmt.Errorf("error reading %v: %w", url, err)
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return jobNames, fmt.Errorf("error reading %v: %v", url, resp.StatusCode)
		}

		content, err := io.ReadAll(resp.Body)
		if err != nil {
			return jobNames, fmt.Errorf("error reading %v: %w", url, err)
		}
		resp.Body.Close()

		periodicConfig := &FakePeriodicConfig{}
		if err := yaml.Unmarshal(content, periodicConfig); err != nil {
			return jobNames, fmt.Errorf("error reading %v: %w", url, err)
		}

		jobNames = append(jobNames, fmt.Sprintf("// begin %v", url))
		localLines := []string{}
		for _, curr := range periodicConfig.Periodics {
			// TODO: the single file for say "master" actually contains every release, but we only want jobs 4.10+
			// where we started disruption monitoring. Adding a bunch of future rows to buy us time but this could
			// stand some logic.
			foundRelease := false
			for _, release := range s.releases {
				if release.Major > 4 || (release.Major == 4 && release.Minor > 9) {
					if strings.Contains(curr.Name, "-"+release.Release) {
						foundRelease = true
						break
					}
				}
			}
			if !foundRelease {
				continue
			}

			// Disruptive jobs can dramatically alter our data for certain NURP combos:
			if strings.Contains(curr.Name, "-disruptive") {
				continue
			}

			// Microshift is not yet stable, jobs are not clearly named, and we're unsure what platform/topology
			// they should be lumped in with.
			// Today they run using a single UPI GCP vm, HA may be coming later.
			if strings.Contains(curr.Name, "microshift") {
				continue
			}

			// OKD jobs are not something we monitor and keep slipping into our disruption data skewing results quite badly.
			if strings.Contains(curr.Name, "-okd") {
				continue
			}

			localLines = append(localLines, curr.Name)
		}
		sort.Strings(localLines)
		jobNames = append(jobNames, localLines...)
		jobNames = append(jobNames, fmt.Sprintf("// end %v", url))
		jobNames = append(jobNames, "")
	}
	return jobNames, nil
}
