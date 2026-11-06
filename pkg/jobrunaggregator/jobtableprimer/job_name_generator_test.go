package jobtableprimer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"cloud.google.com/go/bigquery"
	"github.com/google/go-cmp/cmp"
	"go.uber.org/mock/gomock"

	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorapi"
	"github.com/openshift/ci-tools/pkg/jobrunaggregator/jobrunaggregatorlib"
)

const capGCPJob = "periodic-ci-openshift-cluster-api-actuator-pkg-release-5.0-periodics-e2e-gcp-operator-periodic"

func TestGenerateJobNames(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/release":
			fmt.Fprint(writer, `{"verify":{"gcp":{"prowJob":{"name":"periodic-ci-openshift-release-main-nightly-4.15-e2e-gcp"}}}}`)
		case "/cap":
			fmt.Fprintf(writer, "periodics:\n- name: %s\n- name: %s-disruptive\n- name: %s-microshift\n- name: %s-okd\n", capGCPJob, capGCPJob, capGCPJob, capGCPJob)
		default:
			fmt.Fprint(writer, `periodics:
- name: periodic-ci-openshift-release-main-nightly-4.15-e2e-gcp
- name: periodic-ci-openshift-multiarch-main-nightly-4.15-e2e-aws
- name: periodic-ci-openshift-hypershift-release-4.15-e2e-aws
- name: periodic-ci-openshift-release-main-nightly-4.150-e2e-gcp
- name: periodic-ci-openshift-release-main-nightly-4.14-e2e-gcp
- name: periodic-ci-openshift-release-main-nightly-5.1-e2e-gcp
- name: periodic-ci-openshift-release-main-nightly-4.16-e2e-gcp
- name: periodic-ci-openshift-release-main-nightly-4.15-e2e-disruptive
- name: periodic-ci-openshift-release-main-nightly-4.15-e2e-microshift
- name: periodic-ci-openshift-release-main-nightly-4.15-e2e-okd
`)
		}
	}))
	defer server.Close()
	releases := []jobrunaggregatorapi.ReleaseRow{
		{Release: "4.15", Major: 4, Minor: 15, Product: bigquery.NullString{StringVal: "OCP", Valid: true}},
		{Release: "4.14", Major: 4, Minor: 14, Product: bigquery.NullString{StringVal: "OCP", Valid: true}},
		{Release: "5.1", Major: 5, Minor: 1, Product: bigquery.NullString{StringVal: "OCP", Valid: true}},
		{Release: "4.16", Major: 4, Minor: 16, Product: bigquery.NullString{StringVal: "OKD", Valid: true}},
	}
	generator := &jobNameGenerator{
		periodicURLs:          []string{server.URL + "/periodics"},
		componentPeriodicURLs: []string{server.URL + "/cap"},
		releaseConfigURLs:     []string{server.URL + "/release"},
		releases:              releases,
	}
	names, err := generator.GenerateJobNames()
	if err != nil {
		t.Fatal(err)
	}
	rows := newJobRowListBuilder(releases).CreateAllJobRows(names)
	var actual []string
	for _, row := range rows {
		actual = append(actual, row.JobName)
		if row.GCSJobHistoryLocationPrefix != "logs/"+row.JobName || !row.CollectDisruption || !row.CollectTestRuns {
			t.Errorf("incorrect job metadata: %#v", row)
		}
	}
	expected := []string{
		"periodic-ci-openshift-release-main-nightly-4.15-e2e-gcp",
		"periodic-ci-openshift-multiarch-main-nightly-4.15-e2e-aws",
		"periodic-ci-openshift-hypershift-release-4.15-e2e-aws",
		"periodic-ci-openshift-release-main-nightly-5.1-e2e-gcp",
		capGCPJob,
	}
	slices.Sort(actual)
	slices.Sort(expected)
	if diff := cmp.Diff(expected, actual); diff != "" {
		t.Errorf("unexpected registered jobs (-want +got):\n%s", diff)
	}
	client := jobrunaggregatorlib.NewMockCIDataClient(gomock.NewController(t))
	client.EXPECT().ListReleases(gomock.Any()).Return(releases, nil)
	eligible, err := (&CreateJobsOptions{}).getNewReleases(context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]jobrunaggregatorapi.ReleaseRow{releases[0], releases[2]}, eligible); diff != "" {
		t.Errorf("incorrect eligibility: %s", diff)
	}
	generator.releases = eligible[:1]
	names, err = generator.GenerateJobNames()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(names, capGCPJob) {
		t.Fatal("CAP periodic missing with only eligible OCP 4.x records")
	}
	t.Logf("registered exact CAP job with only OCP %s: %s", generator.releases[0].Release, capGCPJob)
}

func TestJobDiscoverySources(t *testing.T) {
	t.Parallel()
	generator := newJobNameGenerator()
	generator.UpdateURLsForAllReleases([]jobrunaggregatorapi.ReleaseRow{{Release: "4.15"}})
	for _, path := range []string{"openshift-release-main-periodics.yaml", "openshift-multiarch-main-periodics.yaml", "openshift-hypershift-release-4.15-periodics.yaml"} {
		if !slices.ContainsFunc(generator.periodicURLs, func(url string) bool { return strings.HasSuffix(url, path) }) {
			t.Errorf("missing source %s", path)
		}
	}
	if len(generator.releaseConfigURLs) != len(releaseConfigURLTemplates) {
		t.Fatal("release-controller sources were lost")
	}
	if !slices.ContainsFunc(generator.componentPeriodicURLs, func(url string) bool {
		return strings.HasSuffix(url, "/cluster-api-actuator-pkg/openshift-cluster-api-actuator-pkg-release-5.0-periodics.yaml")
	}) {
		t.Fatal("missing CAP source independent of OCP release eligibility")
	}
}
