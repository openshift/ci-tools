package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/hashicorp/go-version"
	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/promotion"
	"golang.org/x/sync/semaphore"
	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	repoAnnotation = "io.openshift.build.source-location"
)

// ocpImages is image -> repo
type ocpImages map[string]string

// getImageFromReleaseImage return version, images->repo map, error
func getImagesFromReleaseImage(releaseImage string) (string, ocpImages, error) {
	images := ocpImages{}
	releaseInfoRaw, err := exec.Command("oc", "adm", "release", "info", releaseImage, "--commits", "--output=json").CombinedOutput()
	if err != nil {
		return "", nil, err
	}

	// TODO: There's probably a struct for this somewhere
	var releaseInfo struct {
		Metadata struct {
			Version string `json:"version"`
		} `json:"metadata"`
		References struct {
			Spec struct {
				Tags []struct {
					Name        string            `json:"name"`
					Annotations map[string]string `json:"annotations"`
				} `json:"tags"`
			} `json:"spec"`
		} `json:"references"`
	}

	if err := json.Unmarshal(releaseInfoRaw, &releaseInfo); err != nil {
		return "", nil, err
	}
	ocpVersion, err := version.NewSemver(releaseInfo.Metadata.Version)
	if err != nil {
		return "", nil, err
	}
	for _, image := range releaseInfo.References.Spec.Tags {
		images[image.Name] = image.Annotations[repoAnnotation]
	}

	versionSegments := ocpVersion.Segments()
	return fmt.Sprintf("%d.%d", versionSegments[0], versionSegments[1]), images, nil
}

type Job struct {
	Name string
	// Warnings mean "job covers what we want but something is off (naming, gating...)
	Warnings []string
}

func (r Requirements) Missing() int {
	var missing int
	for _, req := range [][]Job{r.Parallel, r.Serial, r.Upgrade, r.MinorUpgrade} {
		if len(req) == 0 {
			missing += 1
		}
	}
	return missing
}

func (r *Requirements) Summary() string {
	var items []string
	reqs := map[string][]Job{
		"parallel":           r.Parallel,
		"serial":             r.Serial,
		"upgrade":            r.Upgrade,
		"upgrade from minor": r.MinorUpgrade,
	}

	for _, req := range []string{"parallel", "serial", "upgrade", "upgrade from minor"} {
		var names []string
		for _, job := range reqs[req] {
			names = append(names, job.Name)
		}
		items = append(items, fmt.Sprintf("    - %s={%s}", req, strings.Join(names, ",")))
	}

	return strings.Join(items, "\n")
}

// Could be more dynamic but good enough for now
type Requirements struct {
	Parallel     []Job
	Serial       []Job
	Upgrade      []Job
	MinorUpgrade []Job
}

func (c *Coverage) SummaryLine(repo string) string {
	jobs := c.Requirements.Summary()
	images := strings.Join(c.Images, ",")
	return fmt.Sprintf("%s for image(s) %s:\n%s", repo, images, jobs)
}

type Coverage struct {
	Images       []string
	Requirements Requirements
}

type CoverageForRepos map[string]Coverage

func main() {
	// TODO: Make into options
	oReleaseRepoPath := "../release/"
	releaseImage := "4.8.0-fc.8"
	maxConcurrency := int64(1024)

	version, images, err := getImagesFromReleaseImage(releaseImage)
	if err != nil {
		os.Exit(1)
	}

	lock := &sync.Mutex{}
	coverage := CoverageForRepos{}

	sem := semaphore.NewWeighted(maxConcurrency)
	ctx := context.TODO()
	if err := config.OperateOnCIOperatorConfigDir(filepath.Join(oReleaseRepoPath, "ci-operator", "config"), func(configuration *api.ReleaseBuildConfiguration, info *config.Info) error {
		if err := sem.Acquire(ctx, 1); err != nil {
			return err
		}
		go func() {
			defer sem.Release(1)
			if !promotion.PromotesOfficialImages(configuration) {
				return
			}
			if configuration.PromotionConfiguration.Name != version {
				return
			}

			var currentImages []string
			for _, image := range configuration.Images {
				_, ok := images[string(image.To)]
				if !ok {
					continue
				}
				currentImages = append(currentImages, string(image.To))
			}
			if len(currentImages) == 0 {
				return
			}

			orgRepo := fmt.Sprintf("%s/%s", info.Org, info.Repo)
			defer lock.Unlock()
			lock.Lock()
			if _, ok := coverage[orgRepo]; !ok {
				coverage[orgRepo] = Coverage{
					Images: append(currentImages),
				}
			}
			cov := coverage[orgRepo]
			cov.Requirements.Parallel = checkParallel(configuration.Tests)
			cov.Requirements.Serial = checkSerial(configuration.Tests)
			// cov.Requirements.Upgrade = checkUpgrade(configuration.Tests)
			// cov.Requirements.MinorUpgrade = checkMinorUpgrade(configuration.Tests)

			coverage[orgRepo] = cov
		}()
		return nil
	}); err != nil {
		os.Exit(1)
	}
	if err := sem.Acquire(ctx, maxConcurrency); err != nil {
		os.Exit(1)
	}

	var repos []string
	for repo := range coverage {
		repos = append(repos, repo)
	}
	sort.Slice(repos, func(i, j int) bool {
		return coverage[repos[i]].Requirements.Missing() < coverage[repos[j]].Requirements.Missing()
	})

	current := -1
	for _, repo := range repos {
		cov := coverage[repo]
		if cov.Requirements.Missing() > current {
			current = cov.Requirements.Missing()
			fmt.Printf("\n=== Repositories that miss %d jobs:\n", current)
		}
		fmt.Printf(" * %s\n", cov.SummaryLine(repo))
	}
}

var parallelWorflows = sets.NewString(
	"openshift-e2e-aws",
	"openshift-e2e-aws-loki",
	"openshift-e2e-aws-hosted-loki",
	"openshift-e2e-azure",
	"openshift-e2e-gcp",
	"openshift-e2e-gcp-hosted-loki",
	"openshift-e2e-gcp-loki",
	"baremetalds-e2e",
)

func isMultiStagePresubmitAndNotModified(test api.TestStepConfiguration) bool {
	if test.Postsubmit || test.Interval != nil || test.Cron != nil {
		return false
	}
	if test.MultiStageTestConfiguration == nil || test.MultiStageTestConfiguration.Workflow == nil {
		return false
	}
	// Overrides pre or test section, so we no longer know what the job does
	if len(test.MultiStageTestConfiguration.Pre) > 0 || len(test.MultiStageTestConfiguration.Test) > 0 {
		return false
	}

	return true
}

var serialWorflows = sets.NewString(
	"openshift-e2e-aws-serial",
	"openshift-e2e-azure-serial",
	"openshift-e2e-gcp-serial",
	"openshift-e2e-vsphere-serial",
)

func checkParallel(tests []api.TestStepConfiguration) []Job {
	var jobs []Job
	for _, test := range tests {
		if !isMultiStagePresubmitAndNotModified(test) {
			continue
		}

		// Not a well-known workflow that implements a parallel testsuite
		if !parallelWorflows.Has(*test.MultiStageTestConfiguration.Workflow) {
			continue
		}

		// Overrides default (parallel) testsuite to something else than a parallel testsuite
		if suite := test.MultiStageTestConfiguration.Environment["TEST_SUITE"]; suite != "" && suite != "openshift/conformance/parallel" {
			continue
		}
		jobs = append(jobs, Job{Name: test.As})
	}

	return jobs
}

func checkSerial(tests []api.TestStepConfiguration) []Job {
	var jobs []Job
	for _, test := range tests {
		if !isMultiStagePresubmitAndNotModified(test) {
			continue
		}

		// Not a well-known workflow that implements a parallel testsuite
		if !serialWorflows.Has(*test.MultiStageTestConfiguration.Workflow) {
			continue
		}

		// Overrides default (parallel) testsuite to something else than a parallel testsuite
		if suite := test.MultiStageTestConfiguration.Environment["TEST_SUITE"]; suite != "" && suite != "openshift/conformance/parallel" {
			continue
		}
		jobs = append(jobs, Job{Name: test.As})
	}

	return jobs
}
