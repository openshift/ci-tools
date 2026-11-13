package jobtableprimer

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/sirupsen/logrus"
	yaml "gopkg.in/yaml.v3"

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
	periodicURLs          []string
	componentPeriodicURLs []string
	releaseConfigURLs     []string
	releases              []jobrunaggregatorapi.ReleaseRow
}

var (
	// templateException is for cases where a particular release is known not to have a matching periodicURL definition
	templateException    = []string{}
	periodicURLTemplates = []string{
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
		componentPeriodicURLs: []string{
			"https://raw.githubusercontent.com/openshift/release/main/ci-operator/jobs/openshift/cluster-api-actuator-pkg/openshift-cluster-api-actuator-pkg-release-5.0-periodics.yaml",
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

func isTemplateException(url string) bool {
	for _, exception := range templateException {
		if strings.Contains(url, exception) {
			return true
		}
	}
	return false
}

func readConfigURL(url string, into interface{}, unmarshal func(data []byte, v any) error) (skip bool, failure error) {
	resp, err := http.Get(url)
	if err != nil {
		return false, fmt.Errorf("error requesting %v: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound && isTemplateException(url) {
		logrus.WithField("url", url).Info("skipping template exception config: URL 404 not found")
		return true, nil
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return false, fmt.Errorf("error reading %v: %v", url, resp.StatusCode)
	}

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("error loading content %v: %w", url, err)
	}

	if err = unmarshal(content, into); err != nil {
		return false, fmt.Errorf("error unmarshaling %v: %w", url, err)
	}
	return false, nil
}

func (s *jobNameGenerator) GenerateJobNames() ([]string, error) {
	jobNames := []string{}

	for _, url := range s.releaseConfigURLs {
		releaseConfig := &FakeReleaseConfig{}
		if skip, err := readConfigURL(url, releaseConfig, json.Unmarshal); err != nil {
			return jobNames, err
		} else if skip {
			continue
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

	for _, url := range append(slices.Clone(s.periodicURLs), s.componentPeriodicURLs...) {
		periodicConfig := &FakePeriodicConfig{}
		if skip, err := readConfigURL(url, periodicConfig, yaml.Unmarshal); err != nil {
			return jobNames, err
		} else if skip {
			continue
		}

		jobNames = append(jobNames, fmt.Sprintf("// begin %v", url))
		localLines := []string{}
		for _, curr := range periodicConfig.Periodics {
			foundRelease := slices.Contains(s.componentPeriodicURLs, url)
			for _, release := range s.releases {
				if eligibleRelease(release) {
					if strings.Contains(curr.Name, "-"+release.Release+"-") || strings.HasSuffix(curr.Name, "-"+release.Release) {
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
