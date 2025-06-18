package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"

	"k8s.io/apimachinery/pkg/util/sets"

	v1sippy "github.com/openshift/ci-tools/pkg/api/sippy/v1"
	jc "github.com/openshift/ci-tools/pkg/jobconfig"
	releaseconfig "github.com/openshift/ci-tools/pkg/release/config"
	"github.com/openshift/ci-tools/pkg/util"
)

const defaultAggregateProwJobName = "release-openshift-release-analysis-aggregator"

type options struct {
	releaseConfigDir  string
	prowJobConfigDir  string
	customizationFile string
}

func (o *options) Validate() error {
	if o.prowJobConfigDir == "" {
		return errors.New("--prow-jobs-dir is required")
	}
	if o.releaseConfigDir == "" {
		return errors.New("--release-config is required")
	}

	return nil
}

func gatherOptions() options {
	o := options{}
	flagSet := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	flagSet.StringVar(&o.prowJobConfigDir, "prow-jobs-dir", "", "Path to a root of directory structure with Prow job config files (ci-operator/jobs in openshift/release)")
	flagSet.StringVar(&o.releaseConfigDir, "release-config", "", "Path to Release Controller configuration directory.")
	flagSet.StringVar(&o.customizationFile, "customization-file", "", "Path to file containing additional customization to the config")
	if err := flagSet.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse input")
	}
	return o
}

func main() {
	o := gatherOptions()
	if err := o.Validate(); err != nil {
		logrus.Fatalf("Invalid options: %v", err)
	}

	sippyConfig := v1sippy.SippyConfig{}
	if o.customizationFile != "" {
		data, err := os.ReadFile(o.customizationFile)
		if err != nil {
			logrus.WithError(err).Fatalf("could not read customization file")
		}

		if err := yaml.Unmarshal(data, &sippyConfig); err != nil {
			logrus.WithError(err).Fatalf("could not unmarshal customization file")
		}
	}
	if sippyConfig.Releases == nil {
		sippyConfig.Releases = make(map[string]v1sippy.ReleaseConfig)

	}
	for key, release := range sippyConfig.Releases {
		if release.Jobs == nil {
			release.Jobs = make(map[string]bool)
		}
		if release.Regexp == nil {
			release.Regexp = make([]string, 0)
		}
		if release.BlockingJobs == nil {
			release.BlockingJobs = make([]string, 0)
		}
		if release.InformingJobs == nil {
			release.InformingJobs = make([]string, 0)
		}
		sippyConfig.Releases[key] = release
	}

	informingJobs := sets.New[string]()
	blockingingJobs := sets.New[string]()
	aggregateJobsMap := make(map[string][]string)
	if err := filepath.WalkDir(o.releaseConfigDir, func(path string, info fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("could not read release controller config at %s: %w", path, err)
		}

		var releaseConfig releaseconfig.Config
		if err := json.Unmarshal(data, &releaseConfig); err != nil {
			return fmt.Errorf("could not unmarshal release controller config at %s: %w", path, err)
		}

		for name, job := range releaseConfig.Verify {
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
			if job.Optional {
				informingJobs.Insert(job.ProwJob.Name)
			} else {
				blockingingJobs.Insert(job.ProwJob.Name)
			}
		}
		return nil
	}); err != nil {
		logrus.WithError(err).Fatal("Could not process input configurations.")
	}

	jobConfig, err := jc.ReadFromDir(o.prowJobConfigDir)
	if err != nil {
		logrus.WithError(err).Fatalf("Failed to load Prow jobs %s", o.prowJobConfigDir)
	}

	// Ensure periodics list is sorted to produce a deterministic update
	// to our config file.
	sort.Slice(jobConfig.Periodics, func(i, j int) bool {
		return jobConfig.Periodics[i].Name < jobConfig.Periodics[j].Name
	})

	for _, p := range jobConfig.Periodics {
		if release, ok := p.Labels["job-release"]; ok {
			// include OKD jobs but as a different release by appending `okd`
			// to the release name.
			if strings.Contains(p.Name, "-okd") {
				release = fmt.Sprintf("%s-okd", release)
			}
			if _, ok := sippyConfig.Releases[release]; !ok {
				sippyConfig.Releases[release] = v1sippy.ReleaseConfig{
					Jobs:          make(map[string]bool),
					Regexp:        make([]string, 0),
					BlockingJobs:  make([]string, 0),
					InformingJobs: make([]string, 0),
				}
			}
			if _, ok := sippyConfig.Releases[release].Jobs[p.Name]; !ok {
				sippyConfig.Releases[release].Jobs[p.Name] = true
			}

			if aggregates, ok := aggregateJobsMap[p.Name]; ok {
				for _, aggregate := range aggregates {
					if _, ok := sippyConfig.Releases[release].Jobs[aggregate]; !ok {
						sippyConfig.Releases[release].Jobs[aggregate] = true
					}
				}
			}

			if releaseConfig, ok := sippyConfig.Releases[release]; ok {
				if blockingingJobs.Has(p.Name) {
					releaseConfig.BlockingJobs = append(releaseConfig.BlockingJobs, p.Name)
				}

				if informingJobs.Has(p.Name) || util.IsSpecialInformingJobOnTestGrid(p.Name) {
					releaseConfig.InformingJobs = append(releaseConfig.InformingJobs, p.Name)
				}
				sippyConfig.Releases[release] = releaseConfig
			}
		}
	}

	out, err := yaml.Marshal(sippyConfig)
	if err != nil {
		logrus.WithError(err).Fatalf("could not marshal config")
	}
	fmt.Printf("# Generated by openshift/ci-tools sippy-config-generator on %v\n", time.Now())
	fmt.Println(string(out))
}
