package jobrunaggregator

import (
	"github.com/spf13/cobra"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatoranalyzer"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunbigqueryloader"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobtableprimer"
)

func NewJobAggregatorCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:  "job-run-aggregator",
		Long: `Commands associated with CI job run aggregation`,
	}

	cmd.AddCommand(jobrunbigqueryloader.NewBigQueryTestRunUploadFlagsCommand())
	cmd.AddCommand(jobrunbigqueryloader.NewBigQueryDisruptionUploadFlagsCommand())
	cmd.AddCommand(jobrunbigqueryloader.NewBigQuerySummarizationFlagsCommand())
	cmd.AddCommand(jobrunaggregatoranalyzer.NewJobRunsAnalyzerCommand())
	cmd.AddCommand(jobtableprimer.NewPrimeJobTableCommand())

	return cmd
}
