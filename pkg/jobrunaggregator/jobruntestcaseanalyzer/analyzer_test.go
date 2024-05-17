package jobruntestcaseanalyzer

import (
	"context"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/spf13/pflag"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
)

func TestGetJobs(t *testing.T) {

	tests := map[string]struct {
		expectedJobNames sets.Set[string]
		filters          map[string][]string
	}{
		"test upgrade filter":   {expectedJobNames: sets.Set[string]{"periodic-ci-openshift-release-master-nightly-4.12-e2e-metal-ipi-sdn-serial-ipv4": sets.Empty{}, "periodic-ci-openshift-release-master-nightly-4.12-e2e-metal-ipi-serial-ovn-ipv6": sets.Empty{}}, filters: map[string][]string{"exclude-job-names": {"upgrade"}}},
		"test no filter":        {expectedJobNames: sets.Set[string]{"periodic-ci-openshift-release-master-nightly-4.12-e2e-metal-ipi-sdn-serial-ipv4": sets.Empty{}, "periodic-ci-openshift-release-master-nightly-4.12-e2e-metal-ipi-serial-ovn-ipv6": sets.Empty{}, "periodic-ci-openshift-release-master-nightly-4.12-e2e-metal-ipi-sdn-upgrade": sets.Empty{}}},
		"test multiple filters": {expectedJobNames: sets.Set[string]{"periodic-ci-openshift-release-master-nightly-4.12-e2e-metal-ipi-sdn-serial-ipv4": sets.Empty{}}, filters: map[string][]string{"exclude-job-names": {"upgrade", "ipv6"}}},
		"test include arg":      {expectedJobNames: sets.Set[string]{"periodic-ci-openshift-release-master-nightly-4.12-e2e-metal-ipi-serial-ovn-ipv6": sets.Empty{}}, filters: map[string][]string{"include-job-names": {"ipv6"}}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {

			ctx := context.TODO()
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			mockCIDataClient := jobrunaggregatorlib.NewMockCIDataClient(mockCtrl)
			mockCIDataClient.EXPECT().ListAllJobs(ctx).Return(createJobs(), nil)

			jobGetter := &testCaseAnalyzerJobGetter{
				platform:       "metal",
				infrastructure: "ipi",
				network:        "sdn",
				ciDataClient:   mockCIDataClient,
				jobGCSPrefixes: &[]jobGCSPrefix{},
			}

			fs := pflag.NewFlagSet(t.Name(), pflag.ContinueOnError)
			args := make([]string, 0)

			for key, element := range tc.filters {
				for _, value := range element {
					args = append(args, "--"+key+"="+value)
				}
			}

			f := &JobRunsTestCaseAnalyzerFlags{}

			fs.StringArrayVar(&f.ExcludeJobNames, "exclude-job-names", f.ExcludeJobNames, "Applied only when --explicit-gcs-prefixes is not specified.  The flag can be specified multiple times to create a list of substrings used to filter JobNames from the analysis")
			fs.StringArrayVar(&f.IncludeJobNames, "include-job-names", f.IncludeJobNames, "Applied only when --explicit-gcs-prefixes is not specified.  The flag can be specified multiple times to create a list of substrings to include in matching JobNames for analysis")

			if err := fs.Parse(args); err != nil {
				t.Fatalf("%s flag set parse returned error %#v", name, err)
			}

			if f.ExcludeJobNames != nil && len(f.ExcludeJobNames) > 0 {
				jobGetter.excludeJobNames = sets.Set[string]{}
				jobGetter.excludeJobNames.Insert(f.ExcludeJobNames...)
			}

			if f.IncludeJobNames != nil && len(f.IncludeJobNames) > 0 {
				jobGetter.includeJobNames = sets.Set[string]{}
				jobGetter.includeJobNames.Insert(f.IncludeJobNames...)
			}

			returnedJobs, err := jobGetter.GetJobs(ctx)

			if nil != err {
				t.Fatalf("%s returned error %#v", name, err)
			}

			if len(returnedJobs) == 0 {
				t.Fatalf("%s returned nil jobs", name)
			}

			for key := range tc.expectedJobNames {
				foundIt := false

				for _, job := range returnedJobs {

					if key == job.JobName {
						foundIt = true
					}
				}

				if !foundIt {
					t.Fatalf("%s expected job name '%s' not found", name, key)
				}

			}
		})
	}

}

func createJobs() []jobrunaggregatorapi.JobRowWithVariants {
	jobs := make([]jobrunaggregatorapi.JobRowWithVariants, 3)
	jobs[0] = jobrunaggregatorapi.JobRowWithVariants{JobName: "periodic-ci-openshift-release-master-nightly-4.12-e2e-metal-ipi-sdn-upgrade", Platform: "metal", Network: "sdn"}
	jobs[1] = jobrunaggregatorapi.JobRowWithVariants{JobName: "periodic-ci-openshift-release-master-nightly-4.12-e2e-metal-ipi-sdn-serial-ipv4", Platform: "metal", Network: "sdn"}
	jobs[2] = jobrunaggregatorapi.JobRowWithVariants{JobName: "periodic-ci-openshift-release-master-nightly-4.12-e2e-metal-ipi-serial-ovn-ipv6", Platform: "metal", Network: "sdn"}

	return jobs
}
