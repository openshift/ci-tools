package jobrunbigqueryloader

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"sort"
	"strings"

	"cloud.google.com/go/bigquery"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
)

type BigQueryAlertUploadFlags struct {
	DataCoordinates *jobrunaggregatorlib.BigQueryDataCoordinates
	Authentication  *jobrunaggregatorlib.GoogleAuthenticationFlags

	DryRun   bool
	LogLevel string
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
	fs.StringVar(&f.LogLevel, "log-level", "info", "Log level (trace,debug,info,warn,error) (default: info)")
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
	pendingUploadLister := newAlertPendingUploadLister(ciDataClient)
	alertUploader, err := newAlertUploader(backendAlertTableInserter, ciDataClient)
	if err != nil {
		return nil, err
	}

	jobRunUploaderRegistry := JobRunUploaderRegistry{}
	jobRunUploaderRegistry.Register("alertUploader", alertUploader)
	return &allJobsLoaderOptions{
		ciDataClient: ciDataClient,
		gcsClient:    gcsClient,

		jobRunInserter: jobRunTableInserter,
		shouldCollectedDataForJobFn: func(job jobrunaggregatorapi.JobRow) bool {
			return true
		},
		jobRunUploaderRegistry:  jobRunUploaderRegistry,
		pendingUploadJobsLister: pendingUploadLister,
		logLevel:                f.LogLevel,
	}, nil
}

type alertUploader struct {
	alertInserter    jobrunaggregatorlib.BigQueryInserter
	ciDataClient     jobrunaggregatorlib.CIDataClient
	knownAlertsCache *KnownAlertsCache
}

func newAlertUploader(alertInserter jobrunaggregatorlib.BigQueryInserter,
	ciDataClient jobrunaggregatorlib.CIDataClient) (uploader, error) {

	// Query the cache of all known alerts once before we start processing results:
	allKnownAlerts, err := ciDataClient.ListAllKnownAlerts(context.Background())
	if err != nil {
		return nil, err
	}
	logrus.WithField("knownAlerts", len(allKnownAlerts)).Info("loaded cache of known alerts across all releases")
	knownAlertsCache := newKnownAlertsCache(allKnownAlerts)

	return &alertUploader{
		alertInserter:    alertInserter,
		ciDataClient:     ciDataClient,
		knownAlertsCache: knownAlertsCache,
	}, nil
}

func newAlertPendingUploadLister(ciDataClient jobrunaggregatorlib.CIDataClient) pendingUploadLister {
	return &testRunPendingUploadLister{
		tableName:    jobrunaggregatorapi.AlertJobRunTableName,
		ciDataClient: ciDataClient,
	}
}

func (o *alertUploader) uploadContent(ctx context.Context, jobRun jobrunaggregatorapi.JobRunInfo,
	jobRelease string, jobRunRow *jobrunaggregatorapi.JobRunRow, logger logrus.FieldLogger) error {
	logger.Info("uploading alert results")
	alertData, err := jobRun.GetOpenShiftTestsFilesWithPrefix(ctx, "alert")
	if err != nil {
		return err
	}
	logger.Debug("got test files with prefix")
	if len(alertData) > 0 {
		alertRows := getAlertsFromPerJobRunData(alertData, jobRunRow)

		// pass cache of known alerts in.
		alertRows = populateZeros(jobRunRow, o.knownAlertsCache, alertRows, jobRelease, logger)

		if err := o.alertInserter.Put(ctx, alertRows); err != nil {
			return err
		}
		logger.Debug("insert complete")
	} else {
		logger.Debug("no alert data found, skipping insert")
	}

	return nil
}

// populateZeros adds in 0 entries for observed alerts we know exist, but did not observe in this run.
// This is critical for properly calculating the percentiles as we do not have a fixed set of possible
// alerts.
// For details on calculating the list of all known alerts, see ListAllKnownAlerts in the data client,
// but TL;DR, it's every alert/namespace combo we've ever observed for a given release.
func populateZeros(jobRunRow *jobrunaggregatorapi.JobRunRow,
	knownAlertsCache *KnownAlertsCache,
	observedAlertRows []jobrunaggregatorapi.AlertRow,
	release string, logger logrus.FieldLogger) []jobrunaggregatorapi.AlertRow {

	origCount := len(observedAlertRows)
	injectedCtr := 0
	for _, known := range knownAlertsCache.ListAllKnownAlertsForRelease(release) {
		var found bool
		for _, observed := range observedAlertRows {
			if observed.Name == known.AlertName &&
				observed.Namespace == known.AlertNamespace &&
				observed.Level == known.AlertLevel {
				found = true
				break
			}
		}
		if !found {
			// If we did not observe an Alert+Namespace+Level combo we know exists in this release,
			// we need to inject a 0 for the value so our percentile calculations work. (as origin does
			// not know the list of all possible alerts, only what it observed)
			logger.WithFields(logrus.Fields{
				"AlertName":      known.AlertName,
				"AlertNamespace": known.AlertNamespace,
				"AlertLevel":     known.AlertLevel,
			}).Debug("injecting 0s for a known but not observed alert on this run")
			observedAlertRows = append(observedAlertRows, jobrunaggregatorapi.AlertRow{
				Name:         known.AlertName,
				Namespace:    known.AlertNamespace,
				Level:        known.AlertLevel,
				AlertSeconds: 0,
				JobName: bigquery.NullString{
					StringVal: jobRunRow.JobName,
					Valid:     true,
				},
				JobRunName: jobRunRow.Name,
				JobRunStartTime: bigquery.NullTimestamp{
					Timestamp: jobRunRow.StartTime,
					Valid:     true,
				},
				JobRunEndTime: bigquery.NullTimestamp{
					Timestamp: jobRunRow.EndTime,
					Valid:     true,
				},
				Cluster: bigquery.NullString{
					StringVal: jobRunRow.Cluster,
					Valid:     true,
				},
				ReleaseTag: bigquery.NullString{
					StringVal: jobRunRow.ReleaseTag,
					Valid:     true,
				},
				JobRunStatus: bigquery.NullString{
					StringVal: jobRunRow.Status,
					Valid:     true,
				},
				MasterNodesUpdated: jobRunRow.MasterNodesUpdated,
			})
			injectedCtr++
		}
	}
	logger.Infof("job observed %d alerts, injected additional %d 0s entries for remaining known alerts",
		origCount, injectedCtr)
	return observedAlertRows
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

func getAlertsFromPerJobRunData(alertData map[string]string, jobRunRow *jobrunaggregatorapi.JobRunRow) []jobrunaggregatorapi.AlertRow {
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

	// sort for stable output for testing and such.
	alertList := []Alert{}
	for _, alert := range alertMap {
		alertList = append(alertList, *alert)
	}
	sort.Stable(AlertByKey(alertList))

	ret := []jobrunaggregatorapi.AlertRow{}
	for _, alert := range alertList {
		ret = append(ret, jobrunaggregatorapi.AlertRow{
			Name:         alert.Name,
			Namespace:    alert.Namespace,
			Level:        string(alert.Level),
			AlertSeconds: int(math.Ceil(alert.Duration.Seconds())),
			JobName: bigquery.NullString{
				StringVal: jobRunRow.JobName,
				Valid:     true,
			},
			JobRunName: jobRunRow.Name,
			JobRunStartTime: bigquery.NullTimestamp{
				Timestamp: jobRunRow.StartTime,
				Valid:     true,
			},
			JobRunEndTime: bigquery.NullTimestamp{
				Timestamp: jobRunRow.EndTime,
				Valid:     true,
			},
			Cluster: bigquery.NullString{
				StringVal: jobRunRow.Cluster,
				Valid:     true,
			},
			ReleaseTag: bigquery.NullString{
				StringVal: jobRunRow.ReleaseTag,
				Valid:     true,
			},
			JobRunStatus: bigquery.NullString{
				StringVal: jobRunRow.Status,
				Valid:     true,
			},
			MasterNodesUpdated: jobRunRow.MasterNodesUpdated,
		})
	}

	return ret
}

func newKnownAlertsCache(allKnownAlerts []*jobrunaggregatorapi.KnownAlertRow) *KnownAlertsCache {
	knownAlertsByRelease := map[string][]*jobrunaggregatorapi.KnownAlertRow{}
	for _, ka := range allKnownAlerts {
		if _, haveReleaseAlready := knownAlertsByRelease[ka.Release]; !haveReleaseAlready {
			knownAlertsByRelease[ka.Release] = []*jobrunaggregatorapi.KnownAlertRow{}
		}
		knownAlertsByRelease[ka.Release] = append(knownAlertsByRelease[ka.Release], ka)

	}
	return &KnownAlertsCache{
		knownAlertsByRelease: knownAlertsByRelease,
	}
}

type KnownAlertsCache struct {
	knownAlertsByRelease map[string][]*jobrunaggregatorapi.KnownAlertRow
}

func (k *KnownAlertsCache) ListAllKnownAlertsForRelease(release string) []*jobrunaggregatorapi.KnownAlertRow {
	return k.knownAlertsByRelease[release]
}
