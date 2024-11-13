package release

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
)

func TestValidate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		o       *balanceProfileOptions
		wantErr error
		wantO   *balanceProfileOptions
	}{
		{
			name: "Valid",
			o: &balanceProfileOptions{
				SrcProfile:       "foo",
				DstProfiles:      []string{"super", "duper", "bar"},
				ScalingFactor:    0.75,
				BalancingFactors: []float64{0.33, 0.33, 0.33},
				ExcludeOrgs:      []string{"openshift-priv"},
			},
			wantO: &balanceProfileOptions{
				SrcProfile:       "foo",
				DstProfiles:      []string{"super", "duper", "bar"},
				ScalingFactor:    0.75,
				BalancingFactors: []float64{0.33, 0.33, 0.33},
				ExcludeOrgs:      []string{"openshift-priv"},
			},
		},
		{
			name:    "Scaling factor out of range",
			o:       &balanceProfileOptions{ScalingFactor: 2},
			wantErr: errors.New("scaling factor not in range [0, 1]"),
		},
		{
			name: "Balancing factors should match target profiles",
			o: &balanceProfileOptions{
				DstProfiles:      []string{"foo", "bar"},
				BalancingFactors: []float64{0.5},
			},
			wantErr: errors.New("balancing factors must match target profiles"),
		},
		{
			name: "Balancing factors don't sum up to 1.0",
			o: &balanceProfileOptions{
				DstProfiles:      []string{"foo", "bar"},
				BalancingFactors: []float64{0.5, 0.6},
			},
			wantErr: errors.New("balancing factors must sum up to 1.0"),
		},
		{
			name: "Default balancing factors",
			o: &balanceProfileOptions{
				DstProfiles: []string{"foo", "bar"},
			},
			wantO: &balanceProfileOptions{
				DstProfiles:      []string{"foo", "bar"},
				BalancingFactors: []float64{0.5, 0.5},
			},
		},
	} {
		t.Run(tc.name, func(tt *testing.T) {
			tt.Parallel()
			err := tc.o.validate()

			if err != nil && tc.wantErr == nil {
				t.Fatalf("want err nil but got: %v", err)
			}
			if err == nil && tc.wantErr != nil {
				t.Fatalf("want err %v but got nil", tc.wantErr)
			}
			if err != nil && tc.wantErr != nil {
				if tc.wantErr.Error() != err.Error() {
					t.Fatalf("expect error %q but got %q", tc.wantErr.Error(), err.Error())
				}
				return
			}

			if diff := cmp.Diff(tc.wantO, tc.o, cmpopts.IgnoreUnexported(balanceProfileOptions{})); diff != "" {
				t.Errorf("options differ:\n%s", diff)
			}
		})
	}
}

func TestDstProfilesInfo(t *testing.T) {
	for _, tc := range []struct {
		name           string
		testsToBalance int
		dstProfiles    []string
		factors        []float64
		wantProfiles   []dstProfileInfo
	}{
		{
			name:           "Fix truncation err",
			testsToBalance: 10,
			dstProfiles:    []string{"foo", "bar", "super"},
			factors:        []float64{0.3, 0.3, 0.3},
			wantProfiles: []dstProfileInfo{
				{Name: "foo", Target: 4},
				{Name: "bar", Target: 3},
				{Name: "super", Target: 3},
			},
		},
		{
			name:           "No truncation err",
			testsToBalance: 12,
			dstProfiles:    []string{"foo", "bar", "super", "duper"},
			factors:        []float64{0.25, 0.25, 0.25, 0.25},
			wantProfiles: []dstProfileInfo{
				{Name: "foo", Target: 3},
				{Name: "bar", Target: 3},
				{Name: "super", Target: 3},
				{Name: "duper", Target: 3},
			},
		},
	} {
		t.Run(tc.name, func(tt *testing.T) {
			tt.Parallel()

			dstProfiles := dstProfilesInfo(tc.dstProfiles, tc.factors, tc.testsToBalance)

			ss := func(a, b string) bool { return strings.Compare(a, b) <= 0 }
			if diff := cmp.Diff(tc.wantProfiles, dstProfiles, cmpopts.SortSlices(ss)); diff != "" {
				t.Errorf("profiles differ:\n%s", diff)
			}
		})
	}
}

func TestBalanceRandomly(t *testing.T) {
	genTests := func(p api.ClusterProfile, n int) []testInfo {
		tests := make([]testInfo, 0)
		for i := range n {
			t := api.TestStepConfiguration{MultiStageTestConfiguration: &api.MultiStageTestConfiguration{ClusterProfile: p}}
			info := config.Info{Metadata: api.Metadata{Org: "org", Repo: "repo", Branch: fmt.Sprintf("branch-%d", i)}}
			tests = append(tests, testInfo{test: &t, config: &api.ReleaseBuildConfiguration{}, info: &info})
		}
		return tests
	}
	collectStats := func(tests []testInfo) map[api.ClusterProfile]int {
		stats := make(map[api.ClusterProfile]int)
		for _, ti := range tests {
			n := stats[ti.test.MultiStageTestConfiguration.ClusterProfile]
			stats[ti.test.MultiStageTestConfiguration.ClusterProfile] = n + 1
		}
		return stats
	}
	for _, tc := range []struct {
		name            string
		tests           []testInfo
		testsToBalance  int
		dstProfilesInfo []dstProfileInfo
		wantStats       map[api.ClusterProfile]int
	}{
		{
			name:           "Do nothing",
			tests:          genTests(api.ClusterProfileGCP, 1),
			testsToBalance: 0,
			wantStats:      map[api.ClusterProfile]int{api.ClusterProfileGCP: 1},
		},
		{
			name:           "Distribute equally",
			tests:          genTests(api.ClusterProfileGCP, 100),
			testsToBalance: 40,
			dstProfilesInfo: []dstProfileInfo{
				{Name: string(api.ClusterProfileGCP2), Target: 20},
				{Name: string(api.ClusterProfileGCP3), Target: 20},
			},
			wantStats: map[api.ClusterProfile]int{
				api.ClusterProfileGCP:  60,
				api.ClusterProfileGCP2: 20,
				api.ClusterProfileGCP3: 20,
			},
		},
		{
			name:           "Distribute all",
			tests:          genTests(api.ClusterProfileGCP, 100),
			testsToBalance: 100,
			dstProfilesInfo: []dstProfileInfo{
				{Name: string(api.ClusterProfileGCP2), Target: 75},
				{Name: string(api.ClusterProfileGCP3), Target: 25},
			},
			wantStats: map[api.ClusterProfile]int{
				api.ClusterProfileGCP2: 75,
				api.ClusterProfileGCP3: 25,
			},
		},
	} {
		t.Run(tc.name, func(tt *testing.T) {
			tt.Parallel()

			modifiedConfigs := balanceRandomly(tc.tests, tc.testsToBalance, tc.dstProfilesInfo)
			stats := collectStats(tc.tests)

			if len(modifiedConfigs) != tc.testsToBalance {
				t.Errorf("expected %d modified configs but got %d instead", tc.testsToBalance, len(modifiedConfigs))
			}

			if diff := cmp.Diff(tc.wantStats, stats); diff != "" {
				t.Errorf("stats differ:\n%s", diff)
			}
		})
	}
}
