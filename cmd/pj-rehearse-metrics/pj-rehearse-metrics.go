package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"

	"cloud.google.com/go/storage"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/semaphore"
	"google.golang.org/api/iterator"

	"github.com/openshift/ci-tools/pkg/rehearse"
)

const (
	originCiBucket   = "origin-ci-test"
	rehearseJobName  = "pull-ci-openshift-release-master-pj-rehearse"
	metricsPathInRun = "artifacts/rehearse-metrics.json"
	workersCount     = 20
)

type scraper struct {
	cacheDir string
	bucket   *storage.BucketHandle
	ctx      context.Context
}

func scrape(reader io.Reader, base string, run *runInfo) {
	data, err := ioutil.ReadAll(reader)
	if err != nil {
		logrus.WithError(err).Error("Failed to read rehearse metrics")
		return
	}

	if err := os.MkdirAll(run.localPrDir(base), 0755); err != nil {
		logrus.WithError(err).Error("Failed to create a directory to store metrics for PR")
		return
	}

	if err := ioutil.WriteFile(run.localMetricsArtifact(base), data, 0644); err != nil {
		logrus.WithError(err).Error("Failed to save metrics to a local file")
		return
	}
}

type runInfo struct {
	gcsPrDir  string
	gcsRunDir string
}

func (r *runInfo) gcsJobDir() string {
	jobPrefix := path.Join(r.gcsPrDir, rehearseJobName)
	return fmt.Sprintf("%s/", jobPrefix) // Needed because path.Join() removes trailing slash
}

func (r *runInfo) gcsMetricsArtifact() string {
	return path.Join(r.gcsRunDir, metricsPathInRun)
}

func (r *runInfo) prNumber() string {
	return path.Base(r.gcsPrDir)
}

func (r *runInfo) localPrDir(base string) string {
	return filepath.Join(base, r.prNumber())
}

func (r *runInfo) runNumber() string {
	return path.Base(r.gcsRunDir)
}

func (r *runInfo) localMetricsArtifact(base string) string {
	return filepath.Join(r.localPrDir(base), r.runNumber())
}

// Given a path to a directory storing logs of single run of a rehearsal job for a given PR, scrape
// the metrics artifact from GCS
func (s *scraper) scrapeRun(run *runInfo) {
	// If the metrics are already scraped, do nothing
	if _, err := os.Stat(run.localMetricsArtifact(s.cacheDir)); err == nil {
		return
	}

	reader, err := s.bucket.Object(run.gcsMetricsArtifact()).NewReader(s.ctx)
	if err != nil {
		return
	}
	defer func() {
		if err := reader.Close(); err != nil {
			logrus.WithError(err).Error("Failed to close GCS object reader")
		}
	}()

	scrape(reader, s.cacheDir, run)
}

// Given a path to a directory for a single PR, find all runs of the rehearsal job and
// scrape the metrics artifact from it
func (s *scraper) scrapePr(prefix string) {
	run := &runInfo{gcsPrDir: prefix}

	// Iterate over runs of rehearsal job for a single PR
	// Example: "pr-logs/pull/openshift_release/3000/pull-ci-openshift-release-master-pj-rehearse/123
	runs := s.bucket.Objects(s.ctx, &storage.Query{Prefix: run.gcsJobDir(), Delimiter: "/"})
	for {
		attrs, err := runs.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			logrus.WithError(err).Error("Failed while iterating over rehearse job run paths using a bucket client")
			break
		}

		// Non-empty `Name` attribute means we got a full object name (a file), not a prefix (a dir)
		// We only care about dirs, not files.
		if attrs.Name != "" {
			continue
		}
		run.gcsRunDir = attrs.Prefix
		s.scrapeRun(run)
	}
}

type options struct {
	cacheDir string
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.StringVar(&o.cacheDir, "cache-dir", "", "Path to a directory where scraped metrics data will be saved. If not provided, cache will not be used.")

	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("Failed to parse options")
	}
	return o
}

func run() error {
	o := gatherOptions()

	ctx := context.Background()
	client, err := storage.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create client: %v", err)
	}

	if o.cacheDir == "" {
		o.cacheDir, err = ioutil.TempDir("", "")
		if err != nil {
			return fmt.Errorf("failed to create a temporary directory: %v", err)
		}
		defer func() {
			if err := os.RemoveAll(o.cacheDir); err != nil {
				logrus.Warnf("Failed to clean up a temporary directory %s (err=%v)", o.cacheDir, err)
			}
		}()
	}

	bucket := client.Bucket(originCiBucket)

	// We will iterate over directories under openshift_release, each corresponds to a single PR to openshift/release
	// Example: "pr-logs/pull/openshift_release/3000
	pullRequests := bucket.Objects(ctx, &storage.Query{
		Prefix:    "pr-logs/pull/openshift_release/",
		Delimiter: "/",
	})

	scraper := scraper{ctx: ctx, bucket: bucket, cacheDir: o.cacheDir}
	prPrefixes := make(chan string, 100)
	progress := make(chan string, 100)

	sem := semaphore.NewWeighted(workersCount)

	// Spawn X workers. The workers read PR directory paths from a channel, scrape all runs for a given PR and feed
	// the completed paths to another channel. The workers finish once the input channel is closed.
	for w := 0; w < workersCount; w++ {
		if err := sem.Acquire(ctx, 1); err != nil {
			logrus.WithError(err).Error("Failed to acquire semaphore to spawn a worker")
			break
		}
		go func() {
			defer sem.Release(1)
			for prefix := range prPrefixes {
				scraper.scrapePr(prefix)
				progress <- prefix
			}
		}()
	}

	// Iterate over PR directories and send them to the workers for scraping.
	go func() {
		defer close(progress)
		for {
			attrs, err := pullRequests.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				logrus.WithError(err).Error("Failed while iterating over PR directory paths using a bucket client")
				break
			}

			// Non-empty `Name` attribute means we got a full object name (a file), not a prefix (a dir)
			// We only care about dirs, not files.
			if attrs.Name != "" {
				continue
			}

			prPrefixes <- attrs.Prefix
		}

		// Close the channel so that workers stop iterating after processing all directories
		// Then wait for all workers to finish.
		close(prPrefixes)
		if err := sem.Acquire(ctx, workersCount); err != nil {
			logrus.WithError(err).Error("Failed to acquire semaphore to wait on workers to finish")
		}
	}()

	// Activity indicator
	counter := 0
	for done := range progress {
		counter++
		fmt.Printf("Scraped PR %s (processed %d PRs)\r", done, counter)
	}
	fmt.Printf("\n")

	overLimit := rehearse.NewMetricsCounter("PRs hitting the limit of rehearsed jobs", func(m *rehearse.Metrics) bool {
		return len(m.Actual) > 0 && m.Execution == nil
	})
	allBuilds := rehearse.AllBuilds{Pulls: map[int][]*rehearse.Metrics{}}
	staleJobs := &rehearse.StaleStatusCounter{Builds: &allBuilds}

	counters := []rehearse.MetricsCounter{overLimit, staleJobs}

	if err := filepath.Walk(o.cacheDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		metrics, err := rehearse.LoadMetrics(path)
		if err != nil {
			return err
		}
		if metrics.JobSpec.BuildID == "" {
			metrics.JobSpec.BuildID = filepath.Base(path)
		}

		for _, counter := range counters {
			counter.Process(metrics)
		}

		return nil
	}); err != nil {
		return fmt.Errorf("failed to iterate over scraped metrics: %v", err)
	}

	for _, counter := range counters {
		fmt.Printf("\n%s", counter.Report())
	}

	return nil
}

func main() {
	if err := run(); err != nil {
		logrus.WithError(err).Fatal("Failed to compute rehearsal metrics")
	}
}
