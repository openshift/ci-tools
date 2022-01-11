package jobrunaggregator

import (
	"github.com/spf13/cobra"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatoranalyzer"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunbigqueryloader"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobtableprimer"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/releasebigqueryloader"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/tablescreator"
)

func NewJobAggregatorCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:  "job-run-aggregator",
		Long: `Commands associated with CI job run aggregation`,
	}

	cmd.AddCommand(jobrunbigqueryloader.NewBigQueryTestRunUploadFlagsCommand())
	cmd.AddCommand(jobrunbigqueryloader.NewBigQueryDisruptionUploadFlagsCommand())
	cmd.AddCommand(jobrunbigqueryloader.NewBigQueryAlertUploadFlagsCommand())
	cmd.AddCommand(jobrunaggregatoranalyzer.NewJobRunsAnalyzerCommand())
	cmd.AddCommand(jobtableprimer.NewPrimeJobTableCommand())
	cmd.AddCommand(jobtableprimer.NewGenerateJobNamesCommand())

	cmd.AddCommand(releasebigqueryloader.NewBigQueryReleaseTableCreateFlagsCommand())
	cmd.AddCommand(releasebigqueryloader.NewBigQueryReleaseUploadFlagsCommand())

	cmd.AddCommand(tablescreator.NewBigQueryCreateTablesFlagsCommand())
	return cmd
}
