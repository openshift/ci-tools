package jobrunaggregator

import (
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatoranalyzer"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorcachebuilder"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunbigqueryloader"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobtableprimer"
	"github.com/spf13/cobra"
)

// Overall usage
// 1. launch an job run that is a job run aggregator
// 2. keep track of when the job starts (possibly based on when the binary executes?)
// 3. start caching of completed runs, cycle every minute until it is 20 minutes passed when this job has started
// 4. look at the cache, including incomplete runs somehow (in-memory?  separate dir?), and find the set of
//    prowjobs that are for the current aggregation target.  For release-controller jobs, this is based on the
//    value of the tag.
// 5. keep refreshing the cache, waiting for all the currentAggregationTarget prowjobs to complete
// 6. once all currentAggregationTarget prowjobs are complete, we start analysis
// 7. analysis reads the combined junits and produces aggregated junit for the currentAggregationTarget and for
//    baselines.  Baselines are somehow selected from the set of jobs we have stored.
//
// problems
// 1. We will be constantly building these caches with duplicate information in many different jobs.
//    This makes it difficult to store data for long term retrieval and we have no authoritative copy.
// 2.

func NewJobAggregatorCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:  "job-run-aggregator",
		Long: `Commands associated with CI job run aggregation`,
	}

	cmd.AddCommand(jobrunaggregatorcachebuilder.NewJobRunsAggregatorCacheBuilderCommand())
	cmd.AddCommand(jobrunbigqueryloader.NewBigQueryTestRunUploadFlagsCommand())
	cmd.AddCommand(jobrunbigqueryloader.NewBigQueryDisruptionUploadFlagsCommand())
	cmd.AddCommand(jobrunbigqueryloader.NewBigQuerySummarizationFlagsCommand())
	cmd.AddCommand(jobrunaggregatoranalyzer.NewJobRunsAnalyzerCommand())
	cmd.AddCommand(jobtableprimer.NewPrimeJobTableCommand())

	return cmd
}
