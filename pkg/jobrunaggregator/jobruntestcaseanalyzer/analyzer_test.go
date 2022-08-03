package jobruntestcaseanalyzer

import (
	"context"
	"testing"

	"github.com/spf13/pflag"

	"k8s.io/apimachinery/pkg/util/sets"
)

func TestGetJobs(t *testing.T) {

	tests := map[string]struct {
		expectedJobNames sets.String
		filters          map[string][]string
	}{
		"test upgrade filter":   {expectedJobNames: sets.String{"periodic-ci-openshift-release-master-nightly-4.12-e2e-metal-ipi-sdn-serial-ipv4": sets.Empty{}, "periodic-ci-openshift-release-master-nightly-4.12-e2e-metal-ipi-serial-ovn-ipv6": sets.Empty{}}, filters: map[string][]string{"exclude-job-names": {"upgrade"}}},
		"test no filter":        {expectedJobNames: sets.String{"periodic-ci-openshift-release-master-nightly-4.12-e2e-metal-ipi-sdn-serial-ipv4": sets.Empty{}, "periodic-ci-openshift-release-master-nightly-4.12-e2e-metal-ipi-serial-ovn-ipv6": sets.Empty{}, "periodic-ci-openshift-release-master-nightly-4.12-e2e-metal-ipi-sdn-upgrade": sets.Empty{}}},
		"test multiple filters": {expectedJobNames: sets.String{"periodic-ci-openshift-release-master-nightly-4.12-e2e-metal-ipi-sdn-serial-ipv4": sets.Empty{}}, filters: map[string][]string{"exclude-job-names": {"upgrade", "ipv6"}}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {

			jobGetter := &testCaseAnalyzerJobGetter{
				platform:       "metal",
				infrastructure: "ipi",
				network:        "sdn",
				ciDataClient:   &ciDataClientTester{},
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

			if err := fs.Parse(args); err != nil {
				t.Fatalf("%s flag set parse returned error %#v", name, err)
			}

			if f.ExcludeJobNames != nil && len(f.ExcludeJobNames) > 0 {
				jobGetter.excludeJobNames = sets.String{}
				jobGetter.excludeJobNames.Insert(f.ExcludeJobNames...)
			}

			returnedJobs, err := jobGetter.GetJobs(context.TODO())

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
