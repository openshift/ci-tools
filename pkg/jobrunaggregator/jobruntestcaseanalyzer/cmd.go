package jobruntestcaseanalyzer

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"k8s.io/apimachinery/pkg/util/sets"
	prowjobclientset "k8s.io/test-infra/prow/client/clientset/versioned"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
)

const (
	defaultMinimumSuccessfulTestCount int = 1
	// maxTimeout is our guess of the maximum duration for a job run
	maxTimeout time.Duration = 4*time.Hour + 35*time.Minute
)

var (
	knownPlatforms = sets.Set[string]{
		"aws":     sets.Empty{},
		"azure":   sets.Empty{},
		"gcp":     sets.Empty{},
		"libvirt": sets.Empty{},
		"metal":   sets.Empty{},
		"ovirt":   sets.Empty{},
		"vsphere": sets.Empty{},
	}
	knownNetworks        = sets.Set[string]{"ovn": sets.Empty{}, "sdn": sets.Empty{}}
	knownInfrastructures = sets.Set[string]{"upi": sets.Empty{}, "ipi": sets.Empty{}}
)

type jobGCSPrefix struct {
	jobName   string
	gcsPrefix string
}

type jobGCSPrefixSlice struct {
	values *[]jobGCSPrefix
}

func (s *jobGCSPrefixSlice) String() string {
	if len(*s.values) == 0 {
		return ""
	}
	var jobPairs []string
	for _, value := range *s.values {
		jobPairs = append(jobPairs, fmt.Sprintf("%s=%s", value.jobName, value.gcsPrefix))
	}
	return strings.Join(jobPairs, ",")
}

func (s *jobGCSPrefixSlice) Set(value string) error {
	if len(value) == 0 {
		*s.values = nil
		return nil
	}
	jobPairs := strings.Split(value, ",")
	if len(jobPairs) == 0 {
		return fmt.Errorf("need at least one GCS prefix configured with explicit-gcs-prefixes")
	}
	for _, jobPair := range jobPairs {
		jStrs := strings.Split(jobPair, "=")
		if len(jStrs) != 2 {
			return fmt.Errorf("GCS prefix should consist of job name and GCS prefix separated by '='")
		}
		*s.values = append(*s.values, jobGCSPrefix{jobName: jStrs[0], gcsPrefix: jStrs[1]})
	}
	return nil
}

func (s *jobGCSPrefixSlice) Type() string {
	return "jobGCSPrefixSlice"
}

type JobRunsTestCaseAnalyzerFlags struct {
	DataCoordinates *jobrunaggregatorlib.BigQueryDataCoordinates
	Authentication  *jobrunaggregatorlib.GoogleAuthenticationFlags

	TestGroup                   string
	WorkingDir                  string
	PayloadTag                  string
	Timeout                     time.Duration
	EstimatedJobStartTimeString string
	Platform                    string
	Infrastructure              string
	Network                     string
	MinimumSuccessfulTestCount  int
	PayloadInvocationID         string
	JobGCSPrefixes              []jobGCSPrefix
	ExcludeJobNames             []string
	IncludeJobNames             []string
	JobStateQuerySource         string

	StaticJobRunIdentifierPath string
	StaticJobRunIdentifierJSON string
	GCSBucket                  string
}

func NewJobRunsTestCaseAnalyzerFlags() *JobRunsTestCaseAnalyzerFlags {
	return &JobRunsTestCaseAnalyzerFlags{
		DataCoordinates: jobrunaggregatorlib.NewBigQueryDataCoordinates(),
		Authentication:  jobrunaggregatorlib.NewGoogleAuthenticationFlags(),

		WorkingDir:                  "test-case-analyzer-working-dir",
		EstimatedJobStartTimeString: time.Now().Format(kubeTimeSerializationLayout),
		Timeout:                     3*time.Hour + 30*time.Minute,
		MinimumSuccessfulTestCount:  defaultMinimumSuccessfulTestCount,
	}
}

const kubeTimeSerializationLayout = time.RFC3339

func (f *JobRunsTestCaseAnalyzerFlags) BindFlags(fs *pflag.FlagSet) {
	f.DataCoordinates.BindFlags(fs)
	f.Authentication.BindFlags(fs)

	fs.StringVar(&f.TestGroup, "test-group", "install", "Test group to analyze, like install or overall")
	fs.StringVar(&f.PayloadTag, "payload-tag", f.PayloadTag, "The release controller payload tag to analyze test case status, like 4.9.0-0.ci-2021-07-19-185802")
	fs.StringVar(&f.EstimatedJobStartTimeString, "job-start-time", f.EstimatedJobStartTimeString, fmt.Sprintf("Start time in RFC822Z: %s. This defines the search window for job runs. Only job runs whose start time is in between job-start-time - %s and job-start-time + %s will be included.", kubeTimeSerializationLayout, jobrunaggregatorlib.JobSearchWindowStartOffset, jobrunaggregatorlib.JobSearchWindowEndOffset))
	fs.StringVar(&f.Platform, "platform", f.Platform, "The platform used to narrow down a subset of the jobs to analyze, ex: aws|gcp|azure|vsphere")
	fs.StringVar(&f.Infrastructure, "infrastructure", f.Infrastructure, "The infrastructure used to narrow down a subset of the jobs to analyze, ex: upi|ipi")
	fs.StringVar(&f.Network, "network", f.Network, "The network used to narrow down a subset of the jobs to analyze, ex: sdn|ovn")
	fs.IntVar(&f.MinimumSuccessfulTestCount, "minimum-successful-count", defaultMinimumSuccessfulTestCount, "minimum number of successful test counts among jobs meeting criteria")
	usage := fmt.Sprintf("mutually exclusive to --payload-tag.  Matches the .label[%s] on the prowjob, which is a UID", jobrunaggregatorlib.ProwJobPayloadInvocationIDLabel)
	fs.StringVar(&f.PayloadInvocationID, "payload-invocation-id", f.PayloadInvocationID, usage)

	fs.StringVar(&f.WorkingDir, "working-dir", f.WorkingDir, "The directory to store caches, output, and the like.")
	fs.DurationVar(&f.Timeout, "timeout", f.Timeout, "Time to wait for analyzing job to complete.")
	fs.Var(&jobGCSPrefixSlice{&f.JobGCSPrefixes}, "explicit-gcs-prefixes", "a list of gcs prefixes for jobs created for payload. Only used by per PR payload promotion jobs. The format is comma-separated elements, each consisting of job name and gcs prefix separated by =, like openshift-machine-config-operator=3028-ci-4.11-e2e-aws-ovn-upgrade~logs/openshift-machine-config-operator-3028-ci-4.11-e2e-aws-ovn-upgrade")

	fs.StringArrayVar(&f.ExcludeJobNames, "exclude-job-names", f.ExcludeJobNames, "Applied only when --explicit-gcs-prefixes is not specified.  The flag can be specified multiple times to create a list of substrings used to filter JobNames from the analysis")
	fs.StringArrayVar(&f.IncludeJobNames, "include-job-names", f.IncludeJobNames, "Applied only when --explicit-gcs-prefixes is not specified.  The flag can be specified multiple times to create a list of substrings to include in matching JobNames for analysis")
	fs.StringVar(&f.JobStateQuerySource, "query-source", jobrunaggregatorlib.JobStateQuerySourceBigQuery, "The source from which job states are found. It is either bigquery or cluster")

	// optional for local use or potentially gangway results
	fs.StringVar(&f.StaticJobRunIdentifierPath, "static-run-info-path", f.StaticJobRunIdentifierPath, "The optional path to a file containing JSON formatted JobRunIdentifier array used for aggregated analysis")
	fs.StringVar(&f.StaticJobRunIdentifierJSON, "static-run-info-json", f.StaticJobRunIdentifierJSON, "The optional JSON formatted string of JobRunIdentifier array used for aggregated analysis")

	fs.StringVar(&f.GCSBucket, "google-storage-bucket", "test-platform-results", "The optional GCS Bucket holding test artifacts")

}

func NewJobRunsTestCaseAnalyzerCommand() *cobra.Command {
	f := NewJobRunsTestCaseAnalyzerFlags()

	/* Example runs for release controller:
	 ./job-run-aggregator analyze-test-case
	    --google-service-account-credential-file=credential.json
	    --test-group=install
	    --platform=aws
	    --network=sdn
	    --infrastructure=ipi
	    --payload-tag=4.11.0-0.nightly-2022-04-28-102605
	    --job-start-time=2022-04-28T10:28:48Z
	    --minimum-successful-count=10
		--exclude-job-names=upgrade
		--exclude-job-names=ipv6

	Example runs for PR based paylaod:
	 ./job-run-aggregator analyze-test-case
	    --google-service-account-credential-file=credential.json
	    --test-group=install
	    --payload-invocation-id=09406e30ea661e228c17120f28eff3c6
	    --job-start-time=2022-03-18T13:10:20Z
	    --minimum-successful-count=10
	    --explicit-gcs-prefixes=periodic-ci-openshift-release-master-ci-4.11-e2e-aws-ovn-upgrade=logs/openshift-machine-config-operator-3028-ci-4.11-e2e-aws-ovn-upgrade,periodic-ci-openshift-release-master-ci-4.11-upgrade-from-stable-4.10-e2e-aws-ovn-upgrade=/logs/openshift-machine-config-operator-3028-ci-4.11-upgrade-from-stable-4.10-e2e-aws-ovn-upgrade
	*/
	cmd := &cobra.Command{
		Use: "analyze-test-case",
		Long: `Analyze status of a test case of certain group to make sure they meet minimum criteria specified.
The goal is to analyze test results across job runs of multiple different jobs. This enhances
the functionality of current aggregator, which analyzes job runs of the same job. The result
of the analysis can be used to gate nightly or CI payloads.

The candidate job runs can be a subset of jobs started by nightly or CI payload runs. They can
also be a subset of jobs started by PR payload command. For nightly or CI payload jobs, payload-tag 
is used to select jobs that belong to the particular payload run. For PR payload jobs, we use 
payload-invocation-id to select the jobs.

Each group is matched to a subset of known tests. Currently only 'install' group is supported. Other 
groups like 'upgrade' can be added in the future.
`,
		SilenceUsage: true,

		Example: `To make sure there are at least 10 successful installs for all aws sdn ipi jobs for
payload 4.11.0-0.nightly-2022-04-28-102605, run this command:

./job-run-aggregator analyze-test-case
--google-service-account-credential-file=credential.json
--test-group=install
--platform=aws
--network=sdn
--infrastructure=ipi
--payload-tag=4.11.0-0.nightly-2022-04-28-102605
--job-start-time=2022-04-28T10:28:48Z
--minimum-successful-count=10
`,

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
func (f *JobRunsTestCaseAnalyzerFlags) Validate() error {
	if len(f.WorkingDir) == 0 {
		return fmt.Errorf("missing --working-dir: like test-analyzer-working-dir")
	}
	if _, err := time.Parse(kubeTimeSerializationLayout, f.EstimatedJobStartTimeString); err != nil {
		return err
	}
	if err := f.DataCoordinates.Validate(); err != nil {
		return err
	}
	if err := f.Authentication.Validate(); err != nil {
		return err
	}
	if f.TestGroup == "" {
		return fmt.Errorf("test group has to be specified")
	}
	if len(f.PayloadTag) > 0 && len(f.PayloadInvocationID) > 0 {
		return fmt.Errorf("cannot specify both --payload-tag and --payload-invocation-id")
	}
	if len(f.PayloadTag) == 0 && len(f.PayloadInvocationID) == 0 {
		return fmt.Errorf("exactly one of --payload-tag or --payload-invocation-id must be specified")
	}
	if len(f.PayloadInvocationID) > 0 && len(f.JobGCSPrefixes) == 0 {
		return fmt.Errorf("if --payload-invocation-id is specified, you must specify --explicit-gcs-prefixes")
	}
	if len(f.PayloadInvocationID) > 0 && (len(f.Platform) > 0 || len(f.Network) > 0 || len(f.Infrastructure) > 0) {
		return fmt.Errorf("if --payload-invocation-id is specified, --platform, --network or --infrastructure cannot be specified")
	}

	if len(f.Platform) > 0 {
		if _, ok := knownPlatforms[f.Platform]; !ok {
			return fmt.Errorf("unknown platform %s, valid values are: %+q", f.Platform, sets.List(knownPlatforms))
		}
	}

	if len(f.Network) > 0 {
		if _, ok := knownNetworks[f.Network]; !ok {
			return fmt.Errorf("unknown network %s, valid values are: %+q", f.Network, sets.List(knownNetworks))
		}
	}

	if len(f.Infrastructure) > 0 {

		if _, ok := knownInfrastructures[f.Infrastructure]; !ok {
			return fmt.Errorf("unknown infrastructure %s, valid values are: %+q", f.Infrastructure, sets.List(knownInfrastructures))
		}
	}

	if f.Timeout > maxTimeout {
		return fmt.Errorf("timeout value of %s is out of range, valid value should be less than %s", f.Timeout, maxTimeout)
	}

	if len(f.JobStateQuerySource) > 0 {
		if _, ok := jobrunaggregatorlib.KnownQuerySources[f.JobStateQuerySource]; !ok {
			return fmt.Errorf("unknown query-source %s, valid values are: %+q", f.JobStateQuerySource, sets.List(jobrunaggregatorlib.KnownQuerySources))
		}
	}

	return nil
}

// testNameSuffix allows TestCaseCheckers to append filter parameters to test names for easy categorization
func (f *JobRunsTestCaseAnalyzerFlags) testNameSuffix() string {
	suffix := ""
	if len(f.Platform) > 0 {
		suffix += fmt.Sprintf("platform:%s ", f.Platform)
	}
	if len(f.Network) > 0 {
		suffix += fmt.Sprintf("network:%s ", f.Network)
	}
	if len(f.Infrastructure) > 0 {
		suffix += fmt.Sprintf("infrastructure:%s ", f.Infrastructure)
	}

	if len(f.IncludeJobNames) > 0 {
		suffix += fmt.Sprintf("including:%s ", strings.Join(f.IncludeJobNames, ","))
	}
	if len(f.ExcludeJobNames) > 0 {
		suffix += fmt.Sprintf("excluding:%s ", strings.Join(f.ExcludeJobNames, ","))
	}

	return strings.TrimSpace(suffix)
}

// ToOptions creates a new JobRunTestCaseAnalyzerOptions struct
func (f *JobRunsTestCaseAnalyzerFlags) ToOptions(ctx context.Context) (*JobRunTestCaseAnalyzerOptions, error) {
	estimatedStartTime, err := time.Parse(kubeTimeSerializationLayout, f.EstimatedJobStartTimeString)
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

	ciGCSClient, err := f.Authentication.NewCIGCSClient(ctx, f.GCSBucket)
	if err != nil {
		return nil, err
	}

	jobGetter := NewTestCaseAnalyzerJobGetter(f.Platform, f.Infrastructure, f.Network, f.testNameSuffix(), f.ExcludeJobNames, f.IncludeJobNames, &f.JobGCSPrefixes, ciDataClient)

	var staticJobRunIdentifiers []jobrunaggregatorlib.JobRunIdentifier
	if len(f.StaticJobRunIdentifierJSON) > 0 || len(f.StaticJobRunIdentifierPath) > 0 {
		staticJobRunIdentifiers, err = jobrunaggregatorlib.GetStaticJobRunInfo(f.StaticJobRunIdentifierJSON, f.StaticJobRunIdentifierPath)
		if err != nil {
			return nil, err
		}
	}

	var testIdentifierOpt testIdentifier
	switch f.TestGroup {
	case installTestGroup:
		testIdentifierOpt = installTestIdentifier
	case overallTestGroup:
		testIdentifierOpt = overallTestIdentifier
	case upgradeTestGroup:
		testIdentifierOpt = upgradeTestIdentifier
	default:
		return nil, fmt.Errorf("unknown test group: %s", f.TestGroup)
	}

	var prowJobClient *prowjobclientset.Clientset
	if f.JobStateQuerySource != jobrunaggregatorlib.JobStateQuerySourceBigQuery {
		prowJobClient, err = jobrunaggregatorlib.GetProwJobClient()
		if err != nil {
			return nil, err
		}
	}

	return &JobRunTestCaseAnalyzerOptions{
		payloadTag:          f.PayloadTag,
		workingDir:          f.WorkingDir,
		jobRunStartEstimate: estimatedStartTime,
		timeout:             f.Timeout,
		ciDataClient:        ciDataClient,
		ciGCSClient:         ciGCSClient,
		testCaseCheckers:    []TestCaseChecker{minimumRequiredPassesTestCaseChecker{testIdentifierOpt, f.testNameSuffix(), f.MinimumSuccessfulTestCount}},
		testNameSuffix:      f.testNameSuffix(),
		payloadInvocationID: f.PayloadInvocationID,
		jobGCSPrefixes:      &f.JobGCSPrefixes,
		jobGetter:           jobGetter,
		prowJobClient:       prowJobClient,
		jobStateQuerySource: f.JobStateQuerySource,
		prowJobMatcherFunc:  jobGetter.shouldAggregateJob,

		staticJobRunIdentifiers: staticJobRunIdentifiers,
		gcsBucket:               f.GCSBucket,
	}, nil
}
