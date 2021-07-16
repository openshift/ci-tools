package jobrunaggregator

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"google.golang.org/api/option"
	"gopkg.in/yaml.v2"

	"cloud.google.com/go/storage"
)

const (
	openshiftCIBucket string = "origin-ci-test"
)

// JobRunAggregatorFlags is used to configure the command and produce the runtime structure
type JobRunAggregatorFlags struct {
	JobName    string
	WorkingDir string
}

func NewJobRunAggregatorFlags() *JobRunAggregatorFlags {
	return &JobRunAggregatorFlags{
		JobName:    "periodic-ci-openshift-release-master-ci-4.9-e2e-gcp-upgrade",
		WorkingDir: "job-aggregator-working-dir",
	}
}

// TODO having a bind and a parse via pflags would make this more kube-like
func (f *JobRunAggregatorFlags) ParseFlags(args []string) error {
	fs := flag.NewFlagSet(args[0], flag.ExitOnError)

	fs.StringVar(&f.JobName, "job", f.JobName, "The name of the job to inspect.")
	fs.StringVar(&f.WorkingDir, "working-dir", f.WorkingDir, "The directory to store caches, output, and the like.")

	if err := fs.Parse(args[1:]); err != nil {
		return fmt.Errorf("failed to parse flags: %w", err)
	}
	return nil
}

// Validate checks to see if the user-input is likely to produce functional runtime options
func (f *JobRunAggregatorFlags) Validate() error {
	return nil
}

// ToOptions goes from the user input to the runtime values need to run the command.
// Expect to see unit tests on the options, but not on the flags which are simply value mappings.
func (f *JobRunAggregatorFlags) ToOptions(ctx context.Context) (*JobRunAggregatorOptions, error) {
	// Create a new GCS Client
	gcsClient, err := storage.NewClient(ctx, option.WithoutAuthentication())
	if err != nil {
		return nil, err
	}

	return &JobRunAggregatorOptions{
		JobName:    f.JobName,
		GCSClient:  gcsClient,
		WorkingDir: f.WorkingDir,
	}, nil
}

// JobRunAggregatorOptions is the runtime struct that is produced from the parsed flags
type JobRunAggregatorOptions struct {
	JobName    string
	GCSClient  *storage.Client
	WorkingDir string
}

func (o *JobRunAggregatorOptions) Run(ctx context.Context) error {
	fmt.Printf("Aggregating job runs of type %v.\n", o.JobName)
	jobRuns, err := o.ReadProwJob(ctx)
	if err != nil {
		return err
	}

	for _, jobRun := range jobRuns {
		buf := &bytes.Buffer{}
		writer := yaml.NewEncoder(buf)
		if err := writer.Encode(jobRun); err != nil {
			return err
		}

		// this structure matches the bucket
		targetDir := filepath.Join(o.WorkingDir, "logs", o.JobName, jobRun.ProwJob.Labels["prow.k8s.io/build-id"])
		targetFile := filepath.Join(targetDir, "prowjob.yaml")
		if err := os.MkdirAll(targetDir, 0755); err != nil {
			return err
		}
		if err := ioutil.WriteFile(targetFile, buf.Bytes(), 0644); err != nil {
			return err
		}

		if _, ok := jobRun.ProwJob.Labels["release.openshift.io/analysis"]; ok {
			// this structure is one we can work against
			nameTargetDir := filepath.Join(o.WorkingDir, "by-name", o.JobName, jobRun.ProwJob.Labels["release.openshift.io/analysis"], jobRun.ProwJob.Name)
			nameTargetFile := filepath.Join(nameTargetDir, "prowjob.yaml")
			if err := os.MkdirAll(nameTargetDir, 0755); err != nil {
				return err
			}
			if err := ioutil.WriteFile(nameTargetFile, buf.Bytes(), 0644); err != nil {
				return err
			}
		}
	}

	return nil
}
