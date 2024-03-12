package jobrunaggregator

import (
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatoranalyzer"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunbigqueryloader"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunhistoricaldataanalyzer"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobruntestcaseanalyzer"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobtableprimer"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/releasebigqueryloader"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/tablescreator"
)

func NewJobAggregatorCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:  "job-run-aggregator",
		Long: `Commands associated with CI job run aggregation`,
	}

	// Add some millisecond precision to log timestamps, useful for debugging performance.
	formatter := new(log.TextFormatter)
	formatter.TimestampFormat = "2006-01-02T15:04:05.000Z07:00"
	formatter.FullTimestamp = true
	formatter.DisableColors = false
	log.SetFormatter(formatter)
	log.SetLevel(log.DebugLevel)

	cmd.AddCommand(jobrunbigqueryloader.NewBigQueryTestRunUploadFlagsCommand())
	cmd.AddCommand(jobrunbigqueryloader.NewBigQueryDisruptionUploadFlagsCommand())
	cmd.AddCommand(jobrunbigqueryloader.NewBigQueryAlertUploadFlagsCommand())
	cmd.AddCommand(jobrunaggregatoranalyzer.NewJobRunsAnalyzerCommand())
	cmd.AddCommand(jobtableprimer.NewPrimeJobTableCommand())

	cmd.AddCommand(releasebigqueryloader.NewBigQueryReleaseTableCreateFlagsCommand())
	cmd.AddCommand(releasebigqueryloader.NewBigQueryReleaseUploadFlagsCommand())

	cmd.AddCommand(tablescreator.NewBigQueryCreateTablesFlagsCommand())

	cmd.AddCommand(jobruntestcaseanalyzer.NewJobRunsTestCaseAnalyzerCommand())

	cmd.AddCommand(jobrunhistoricaldataanalyzer.NewJobRunHistoricalDataAnalyzerCommand())
	return cmd
}
