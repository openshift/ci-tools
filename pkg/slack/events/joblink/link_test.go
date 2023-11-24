package joblink

import (
	"net/url"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestExtractInfo(t *testing.T) {
	var testCases = []struct {
		link     string
		expected *jobInfo
	}{
		{
			link:     "https://prow.ci.openshift.org/job-history/gs/origin-ci-test/logs/release-openshift-origin-installer-e2e-gcp-upgrade-4.7",
			expected: &jobInfo{Name: "release-openshift-origin-installer-e2e-gcp-upgrade-4.7", Id: ""},
		},
		{
			link:     "https://prow.ci.openshift.org/log?job=pull-ci-openshift-installer-release-4.6-e2e-metal-ipi&id=1319125780608847872",
			expected: &jobInfo{Name: "pull-ci-openshift-installer-release-4.6-e2e-metal-ipi", Id: "1319125780608847872"},
		},
		{
			link:     "https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com/gcs/origin-ci-test/pr-logs/pull/25585/pull-ci-openshift-origin-master-e2e-aws-disruptive/1319310480841379840/build-log.txt",
			expected: &jobInfo{Name: "pull-ci-openshift-origin-master-e2e-aws-disruptive", Id: "1319310480841379840"},
		},
		{
			link:     "https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com/gcs/origin-ci-test/pr-logs/pull/25585/pull-ci-openshift-origin-master-e2e-aws-disruptive/1319310480841379840/artifacts/123/123123/123/12/312/3/1",
			expected: &jobInfo{Name: "pull-ci-openshift-origin-master-e2e-aws-disruptive", Id: "1319310480841379840"},
		},
		{
			link:     "https://prow.ci.openshift.org/view/gs/origin-ci-test/pr-logs/pull/openshift_release/12371/rehearse-12371-periodic-ci-kubevirt-kubevirt-master-e2e-nested-virt/1318930182802771968",
			expected: &jobInfo{Name: "rehearse-12371-periodic-ci-kubevirt-kubevirt-master-e2e-nested-virt", Id: "1318930182802771968"},
		},
		{
			link:     "https://prow.ci.openshift.org/view/gs/origin-ci-test/pr-logs/pull/openshift_tektoncd-pipeline-operator/495/pull-ci-openshift-tektoncd-pipeline-operator-release-next-4.4-csv/1319068241137504256#1:build-log.txt%3A58",
			expected: &jobInfo{Name: "pull-ci-openshift-tektoncd-pipeline-operator-release-next-4.4-csv", Id: "1319068241137504256", Line: 58},
		},
		{
			link:     "https://prow.ci.openshift.org/view/gs/origin-ci-test/logs/periodic-openshift-library-import/1319699861964066816",
			expected: &jobInfo{Name: "periodic-openshift-library-import", Id: "1319699861964066816"},
		},
		{
			link:     "https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com/gcs/origin-ci-test/logs/periodic-openshift-library-import/1319699861964066816/clone-records.json",
			expected: &jobInfo{Name: "periodic-openshift-library-import", Id: "1319699861964066816"},
		},
		{
			link:     "https://prow.ci.openshift.org/view/gs/origin-ci-test/pr-logs/pull/batch/pull-ci-openshift-origin-release-4.5-unit/1319699245074223104",
			expected: &jobInfo{Name: "pull-ci-openshift-origin-release-4.5-unit", Id: "1319699245074223104"},
		},
		{
			link:     "https://prow.ci.openshift.org/view/gs/origin-ci-test/logs/branch-ci-openshift-k8s-prometheus-adapter-release-4.8-images/1319705916215398400",
			expected: &jobInfo{Name: "branch-ci-openshift-k8s-prometheus-adapter-release-4.8-images", Id: "1319705916215398400"},
		},
		{
			link:     "https://github.com/openshift/release/pull/13221",
			expected: nil,
		},
		{
			link:     "https://storage.googleapis.com/origin-ci-test/pr-logs/pull/25585/pull-ci-openshift-origin-master-e2e-aws-disruptive/1319310480841379840/build-log.txt",
			expected: &jobInfo{Name: "pull-ci-openshift-origin-master-e2e-aws-disruptive", Id: "1319310480841379840"},
		},
		{
			link:     "https://storage.googleapis.com/origin-ci-test/pr-logs/pull/25585/pull-ci-openshift-origin-master-e2e-aws-disruptive/1319310480841379840/artifacts/123/123123/123/12/312/3/1",
			expected: &jobInfo{Name: "pull-ci-openshift-origin-master-e2e-aws-disruptive", Id: "1319310480841379840"},
		},
	}

	for _, testCase := range testCases {
		url, err := url.Parse(testCase.link)
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(testCase.expected, infoFromUrl(url)); diff != "" {
			t.Errorf("%s: got incorrect info: %v", testCase.link, diff)
		}
	}
}

func TestRehearsalFromName(t *testing.T) {
	var testCases = []struct {
		job       string
		name      string
		rehearsal string
	}{
		{
			job:       "branch-ci-openshift-k8s-prometheus-adapter-release-4.8-images",
			name:      "branch-ci-openshift-k8s-prometheus-adapter-release-4.8-images",
			rehearsal: "",
		},
		{
			job:       "rehearse-13070-pull-ci-jianzhangbjz-learn-operator-master-learn-oo-test-aws",
			name:      "pull-ci-jianzhangbjz-learn-operator-master-learn-oo-test-aws",
			rehearsal: "13070",
		},
	}
	for _, testCase := range testCases {
		name, rehearsal := rehearsalFromName(testCase.job)
		if name != testCase.name {
			t.Errorf("got incorrect name, wanted %s got %s", testCase.name, name)
		}
		if rehearsal != testCase.rehearsal {
			t.Errorf("got incorrect rehearsal, wanted %s got %s", testCase.rehearsal, rehearsal)
		}
	}
}
