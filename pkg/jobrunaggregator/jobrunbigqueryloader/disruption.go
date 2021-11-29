package jobrunbigqueryloader

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"k8s.io/apimachinery/pkg/util/sets"
	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
	"github.com/openshift/ci-tools/pkg/junit"
)

type BigQueryDisruptionUploadFlags struct {
	DataCoordinates *jobrunaggregatorlib.BigQueryDataCoordinates
	Authentication  *jobrunaggregatorlib.GoogleAuthenticationFlags

	DryRun bool
}

func NewBigQueryDisruptionUploadFlags() *BigQueryDisruptionUploadFlags {
	return &BigQueryDisruptionUploadFlags{
		DataCoordinates: jobrunaggregatorlib.NewBigQueryDataCoordinates(),
		Authentication:  jobrunaggregatorlib.NewGoogleAuthenticationFlags(),
	}
}

func (f *BigQueryDisruptionUploadFlags) BindFlags(fs *pflag.FlagSet) {
	f.DataCoordinates.BindFlags(fs)
	f.Authentication.BindFlags(fs)

	fs.BoolVar(&f.DryRun, "dry-run", f.DryRun, "Run the command, but don't mutate data.")
}

func NewBigQueryDisruptionUploadFlagsCommand() *cobra.Command {
	f := NewBigQueryDisruptionUploadFlags()

	cmd := &cobra.Command{
		Use:          "upload-disruptions",
		Long:         `Upload disruption data to bigquery`,
		SilenceUsage: true,

		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			if err := f.Validate(); err != nil {
				logrus.WithError(err).Fatal("Flags are invalid")
			}
			o, err := f.ToOptions(ctx)
			if err != nil {
				logrus.WithError(err).Fatal("Failed to build runtime options")
			}

			if err := o.Run(ctx); err != nil {
				logrus.WithError(err).Fatal("Command failed")
			}

			return nil
		},

		Args: jobrunaggregatorlib.NoArgs,
	}

	f.BindFlags(cmd.Flags())

	return cmd
}

// Validate checks to see if the user-input is likely to produce functional runtime options
func (f *BigQueryDisruptionUploadFlags) Validate() error {
	if err := f.DataCoordinates.Validate(); err != nil {
		return err
	}
	if err := f.Authentication.Validate(); err != nil {
		return err
	}

	return nil
}

// ToOptions goes from the user input to the runtime values need to run the command.
// Expect to see unit tests on the options, but not on the flags which are simply value mappings.
func (f *BigQueryDisruptionUploadFlags) ToOptions(ctx context.Context) (*allJobsLoaderOptions, error) {
	// Create a new GCS Client
	gcsClient, err := f.Authentication.NewCIGCSClient(ctx, "origin-ci-test")
	if err != nil {
		return nil, err
	}

	bigQueryClient, err := f.Authentication.NewBigQueryClient(ctx, f.DataCoordinates.ProjectID)
	if err != nil {
		return nil, err
	}
	ciDataClient := jobrunaggregatorlib.NewRetryingCIDataClient(
		jobrunaggregatorlib.NewCIDataClient(*f.DataCoordinates, bigQueryClient),
	)

	var jobRunTableInserter jobrunaggregatorlib.BigQueryInserter
	var backendDisruptionTableInserter jobrunaggregatorlib.BigQueryInserter
	if !f.DryRun {
		ciDataSet := bigQueryClient.Dataset(f.DataCoordinates.DataSetID)
		jobRunTable := ciDataSet.Table(jobrunaggregatorapi.DisruptionJobRunTableName)
		backendDisruptionTable := ciDataSet.Table(jobrunaggregatorapi.BackendDisruptionTableName)
		jobRunTableInserter = jobRunTable.Inserter()
		backendDisruptionTableInserter = backendDisruptionTable.Inserter()
	} else {
		jobRunTableInserter = jobrunaggregatorlib.NewDryRunInserter(os.Stdout, jobrunaggregatorapi.DisruptionJobRunTableName)
		backendDisruptionTableInserter = jobrunaggregatorlib.NewDryRunInserter(os.Stdout, jobrunaggregatorapi.BackendDisruptionTableName)
	}

	return &allJobsLoaderOptions{
		ciDataClient: ciDataClient,
		gcsClient:    gcsClient,

		jobRunInserter:              jobRunTableInserter,
		shouldCollectedDataForJobFn: wantsDisruptionData,
		getLastJobRunWithDataFn:     ciDataClient.GetLastJobRunWithDisruptionDataForJobName,
		jobRunUploader:              newDisruptionUploader(backendDisruptionTableInserter),
	}, nil
}

type disruptionUploader struct {
	backendDisruptionInserter jobrunaggregatorlib.BigQueryInserter
}

func newDisruptionUploader(backendDisruptionInserter jobrunaggregatorlib.BigQueryInserter) uploader {
	return &disruptionUploader{
		backendDisruptionInserter: backendDisruptionInserter,
	}
}

func (o *disruptionUploader) uploadContent(ctx context.Context, jobRun jobrunaggregatorapi.JobRunInfo, prowJob *prowv1.ProwJob) error {
	fmt.Printf("  uploading backend disruption results: %q/%q\n", jobRun.GetJobName(), jobRun.GetJobRunID())
	backendDisruptionData, err := jobRun.GetOpenShiftTestsFilesWithPrefix(ctx, "backend-disruption")
	if err != nil {
		return err
	}
	if len(backendDisruptionData) > 0 {
		return o.uploadBackendDisruptionFromDirectData(ctx, jobRun.GetJobRunID(), backendDisruptionData)
	}

	dateWeStartedTrackingDirectDisruptionData, err := time.Parse(time.RFC3339, "2021-11-24T00:00:00Z")
	if err != nil {
		return err
	}
	// TODO fix better before we hit 4.20
	releaseHasDisruptionData := strings.Contains(jobRun.GetJobName(), "4.10") ||
		strings.Contains(jobRun.GetJobName(), "4.11") ||
		strings.Contains(jobRun.GetJobName(), "4.12") ||
		strings.Contains(jobRun.GetJobName(), "4.13") ||
		strings.Contains(jobRun.GetJobName(), "4.14") ||
		strings.Contains(jobRun.GetJobName(), "4.15") ||
		strings.Contains(jobRun.GetJobName(), "4.16") ||
		strings.Contains(jobRun.GetJobName(), "4.17") ||
		strings.Contains(jobRun.GetJobName(), "4.17") ||
		strings.Contains(jobRun.GetJobName(), "4.19")
	if releaseHasDisruptionData && prowJob.CreationTimestamp.After(dateWeStartedTrackingDirectDisruptionData) {
		fmt.Printf("  No disruption data found, returning: %v/%v\n", jobRun.GetJobName(), jobRun.GetJobRunID())
		// we  have no data, just return
		return nil
	}

	fmt.Printf("  missing direct backend disruption results, trying to read from junit: %v/%v\n", jobRun.GetJobName(), jobRun.GetJobRunID())
	// if we don't have
	combinedJunitContent, err := jobRun.GetCombinedJUnitTestSuites(ctx)
	if err != nil {
		return err
	}

	return o.uploadBackendDisruptionFromJunit(ctx, jobRun.GetJobRunID(), combinedJunitContent)
}

func (o *disruptionUploader) uploadBackendDisruptionFromJunit(ctx context.Context, jobRunName string, suites *junit.TestSuites) error {
	serverAvailabilityResults := getServerAvailabilityResultsFromJunit(suites)
	return o.uploadBackendDisruption(ctx, jobRunName, serverAvailabilityResults)
}

func (o *disruptionUploader) uploadBackendDisruptionFromDirectData(ctx context.Context, jobRunName string, backendDisruptionData map[string]string) error {
	serverAvailabilityResults := jobrunaggregatorlib.GetServerAvailabilityResultsFromDirectData(backendDisruptionData)
	return o.uploadBackendDisruption(ctx, jobRunName, serverAvailabilityResults)
}
func (o *disruptionUploader) uploadBackendDisruption(ctx context.Context, jobRunName string, serverAvailabilityResults map[string]jobrunaggregatorlib.AvailabilityResult) error {
	rows := []*jobrunaggregatorapi.BackendDisruptionRow{}
	for _, backendName := range sets.StringKeySet(serverAvailabilityResults).List() {
		unavailability := serverAvailabilityResults[backendName]
		row := &jobrunaggregatorapi.BackendDisruptionRow{
			BackendName:       backendName,
			JobRunName:        jobRunName,
			DisruptionSeconds: unavailability.SecondsUnavailable,
		}
		rows = append(rows, row)
	}
	if err := o.backendDisruptionInserter.Put(ctx, rows); err != nil {
		return err
	}
	return nil
}
