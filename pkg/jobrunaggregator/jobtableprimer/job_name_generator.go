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

type jobNameGenerator struct {
	periodicURLs      []string
	releaseConfigURLs []string
	releases          []jobrunaggregatorapi.ReleaseRow
}

var (
	periodicURLTemplates = []string{
		"https://raw.githubusercontent.com/openshift/release/master/ci-operator/jobs/openshift/release/openshift-release-release-%s-periodics.yaml",
		"https://raw.githubusercontent.com/openshift/release/master/ci-operator/jobs/openshift/hypershift/openshift-hypershift-release-%s-periodics.yaml",
	}
	releaseConfigURLTemplates = []string{
		"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-%s-arm64.json",
		"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-%s-ci.json",
		"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-%s-multi.json",
		"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-%s-ppc64le.json",
		"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-%s-s390x.json",
		"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-%s.json",
	}
)

func newJobNameGenerator() *jobNameGenerator {
	generator := &jobNameGenerator{
		periodicURLs: []string{
			"https://raw.githubusercontent.com/openshift/release/master/ci-operator/jobs/openshift/release/openshift-release-master-periodics.yaml",
			"https://raw.githubusercontent.com/openshift/release/master/ci-operator/jobs/openshift/multiarch/openshift-multiarch-master-periodics.yaml",

			"https://raw.githubusercontent.com/openshift/release/master/ci-operator/jobs/openshift/release/openshift-release-release-4.10-periodics.yaml",
			"https://raw.githubusercontent.com/openshift/release/master/ci-operator/jobs/openshift/release/openshift-release-release-4.11-periodics.yaml",
			"https://raw.githubusercontent.com/openshift/release/master/ci-operator/jobs/openshift/release/openshift-release-release-4.12-periodics.yaml",
			"https://raw.githubusercontent.com/openshift/release/master/ci-operator/jobs/openshift/release/openshift-release-release-4.13-periodics.yaml",
			"https://raw.githubusercontent.com/openshift/release/master/ci-operator/jobs/openshift/release/openshift-release-release-4.14-periodics.yaml",
			"https://raw.githubusercontent.com/openshift/release/master/ci-operator/jobs/openshift/release/openshift-release-release-4.15-periodics.yaml",
			"https://raw.githubusercontent.com/openshift/release/master/ci-operator/jobs/openshift/release/openshift-release-release-4.16-periodics.yaml",

			"https://raw.githubusercontent.com/openshift/release/master/ci-operator/jobs/openshift/hypershift/openshift-hypershift-release-4.13-periodics.yaml",
			"https://raw.githubusercontent.com/openshift/release/master/ci-operator/jobs/openshift/hypershift/openshift-hypershift-release-4.14-periodics.yaml",
			"https://raw.githubusercontent.com/openshift/release/master/ci-operator/jobs/openshift/hypershift/openshift-hypershift-release-4.15-periodics.yaml",
			"https://raw.githubusercontent.com/openshift/release/master/ci-operator/jobs/openshift/hypershift/openshift-hypershift-release-4.16-periodics.yaml",
		},
		releaseConfigURLs: []string{
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.10-arm64.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.10-ci.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.10-multi.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.10-ppc64le.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.10-s390x.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.10.json",

			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.11-arm64.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.11-ci.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.11-multi.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.11-ppc64le.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.11-s390x.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.11.json",

			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.12-arm64.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.12-ci.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.12-multi.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.12-ppc64le.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.12-s390x.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.12.json",

			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.13-arm64.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.13-ci.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.13-multi.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.13-ppc64le.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.13-s390x.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.13.json",

			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.14-arm64.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.14-ci.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.14-multi.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.14-ppc64le.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.14-s390x.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.14.json",

			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.15-arm64.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.15-ci.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.15-multi.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.15-ppc64le.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.15-s390x.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.15.json",

			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.16-arm64.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.16-ci.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.16-multi.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.16-ppc64le.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.16-s390x.json",
			"https://raw.githubusercontent.com/openshift/release/master/core-services/release-controller/_releases/release-ocp-4.16.json",
		},
	}
	sort.Strings(generator.periodicURLs)
	sort.Strings(generator.releaseConfigURLs)
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

func (s *jobNameGenerator) UpdateURLsForNewReleases(releases []jobrunaggregatorapi.ReleaseRow) {
	s.releases = releases
	newReleases := []string{}
	for _, release := range s.releases {
		found := false
		for _, periodicURL := range s.periodicURLs {
			if strings.Contains(periodicURL, release.Release) {
				found = true
				break
			}
		}
		for _, releaseConfigURL := range s.releaseConfigURLs {
			if strings.Contains(releaseConfigURL, release.Release) {
				found = true
				break
			}
		}
		if !found {
			newReleases = append(newReleases, release.Release)
		}
	}
	for _, release := range newReleases {
		s.addReleaseURLs(release)
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
			if len(s.releases) > 0 {
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
			} else if !strings.Contains(curr.Name, "-4.10") &&
				!strings.Contains(curr.Name, "-4.11") &&
				!strings.Contains(curr.Name, "-4.12") &&
				!strings.Contains(curr.Name, "-4.13") &&
				!strings.Contains(curr.Name, "-4.14") &&
				!strings.Contains(curr.Name, "-4.15") &&
				!strings.Contains(curr.Name, "-4.16") &&
				!strings.Contains(curr.Name, "-4.17") &&
				!strings.Contains(curr.Name, "-4.18") &&
				!strings.Contains(curr.Name, "-4.19") &&
				!strings.Contains(curr.Name, "-4.20") {
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
