package jobrunbigqueryloader

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
)

type BigQueryAlertUploadFlags struct {
	DataCoordinates *jobrunaggregatorlib.BigQueryDataCoordinates
	Authentication  *jobrunaggregatorlib.GoogleAuthenticationFlags

	DryRun bool
}

func NewBigQueryAlertUploadFlags() *BigQueryAlertUploadFlags {
	return &BigQueryAlertUploadFlags{
		DataCoordinates: jobrunaggregatorlib.NewBigQueryDataCoordinates(),
		Authentication:  jobrunaggregatorlib.NewGoogleAuthenticationFlags(),
	}
}

func (f *BigQueryAlertUploadFlags) BindFlags(fs *pflag.FlagSet) {
	f.DataCoordinates.BindFlags(fs)
	f.Authentication.BindFlags(fs)

	fs.BoolVar(&f.DryRun, "dry-run", f.DryRun, "Run the command, but don't mutate data.")
}

func NewBigQueryAlertUploadFlagsCommand() *cobra.Command {
	f := NewBigQueryAlertUploadFlags()

	cmd := &cobra.Command{
		Use:          "upload-alerts",
		Long:         `Upload alert data to bigquery`,
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
func (f *BigQueryAlertUploadFlags) Validate() error {
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
func (f *BigQueryAlertUploadFlags) ToOptions(ctx context.Context) (*allJobsLoaderOptions, error) {
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
	var backendAlertTableInserter jobrunaggregatorlib.BigQueryInserter
	if !f.DryRun {
		ciDataSet := bigQueryClient.Dataset(f.DataCoordinates.DataSetID)
		jobRunTable := ciDataSet.Table(jobrunaggregatorapi.AlertJobRunTableName)
		backendAlertTable := ciDataSet.Table(jobrunaggregatorapi.AlertsTableName)
		jobRunTableInserter = jobRunTable.Inserter()
		backendAlertTableInserter = backendAlertTable.Inserter()
	} else {
		jobRunTableInserter = jobrunaggregatorlib.NewDryRunInserter(os.Stdout, jobrunaggregatorapi.AlertJobRunTableName)
		backendAlertTableInserter = jobrunaggregatorlib.NewDryRunInserter(os.Stdout, jobrunaggregatorapi.AlertsTableName)
	}

	return &allJobsLoaderOptions{
		ciDataClient: ciDataClient,
		gcsClient:    gcsClient,

		jobRunInserter: jobRunTableInserter,
		shouldCollectedDataForJobFn: func(job jobrunaggregatorapi.JobRow) bool {
			return true
		},
		getLastJobRunWithDataFn: ciDataClient.GetLastJobRunWithAlertDataForJobName,
		jobRunUploader:          newAlertUploader(backendAlertTableInserter),
	}, nil
}

type alertUploader struct {
	alertInserter jobrunaggregatorlib.BigQueryInserter
}

func newAlertUploader(alertInserter jobrunaggregatorlib.BigQueryInserter) uploader {
	return &alertUploader{
		alertInserter: alertInserter,
	}
}

func (o *alertUploader) uploadContent(ctx context.Context, jobRun jobrunaggregatorapi.JobRunInfo, prowJob *prowv1.ProwJob) error {
	fmt.Printf("  uploading alert results: %q/%q\n", jobRun.GetJobName(), jobRun.GetJobRunID())
	alertData, err := jobRun.GetOpenShiftTestsFilesWithPrefix(ctx, "alert")
	if err != nil {
		return err
	}
	if len(alertData) > 0 {
		alertsToPersist := getAlertsFromPerJobRunData(alertData, jobRun.GetJobRunID())
		if err := o.alertInserter.Put(ctx, alertsToPersist); err != nil {
			return err
		}
	}

	return nil
}

type AlertList struct {
	// Alerts is keyed by name to make the consumption easier
	Alerts []Alert
}

// name and namespace are consistent (usually) for every CI run
type AlertKey struct {
	Name      string
	Namespace string
	Level     AlertLevel
}

type AlertByKey []Alert

func (a AlertByKey) Len() int      { return len(a) }
func (a AlertByKey) Swap(i, j int) { a[i], a[j] = a[j], a[i] }
func (a AlertByKey) Less(i, j int) bool {
	if strings.Compare(a[i].Name, a[j].Name) < 0 {
		return true
	}
	if strings.Compare(a[i].Namespace, a[j].Namespace) < 0 {
		return true
	}
	if strings.Compare(string(a[i].Level), string(a[j].Level)) < 0 {
		return true
	}

	return false
}

type AlertLevel string

var (
	UnknownAlertLevel  AlertLevel = "Unknown"
	WarningAlertLevel  AlertLevel = "Warning"
	CriticalAlertLevel AlertLevel = "Critical"
)

type Alert struct {
	AlertKey `json:",inline"`
	Duration metav1.Duration
}

func getAlertsFromPerJobRunData(alertData map[string]string, jobRunID string) []jobrunaggregatorapi.AlertRow {
	alertMap := map[AlertKey]*Alert{}

	for _, alertJSON := range alertData {
		if len(alertJSON) == 0 {
			continue
		}
		allAlerts := &AlertList{}
		if err := json.Unmarshal([]byte(alertJSON), allAlerts); err != nil {
			continue
		}

		for i := range allAlerts.Alerts {
			curr := allAlerts.Alerts[i]
			existing, ok := alertMap[curr.AlertKey]
			if !ok {
				existing = &Alert{
					AlertKey: curr.AlertKey,
				}
			}
			existing.Duration.Duration = existing.Duration.Duration + curr.Duration.Duration
			alertMap[existing.AlertKey] = existing
		}
	}

	// sort for stable output for testing and stuch.
	alertList := []Alert{}
	for _, alert := range alertMap {
		alertList = append(alertList, *alert)
	}
	sort.Stable(AlertByKey(alertList))

	ret := []jobrunaggregatorapi.AlertRow{}
	for _, alert := range alertList {
		ret = append(ret, jobrunaggregatorapi.AlertRow{
			JobRunName:   jobRunID,
			Name:         alert.Name,
			Namespace:    alert.Namespace,
			Level:        string(alert.Level),
			AlertSeconds: int(math.Ceil(alert.Duration.Seconds())),
		})
	}

	return ret
}
