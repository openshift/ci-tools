package main

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"

	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/pod-utils/downwardapi"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestInjectedTestResolverInfo(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name  string
		pulls []prowapi.Pull
		want  api.Metadata
	}{
		{name: "checkout only source uses injection coordinates", want: api.Metadata{Org: "openshift", Repo: "installer", Branch: "main"}},
		{name: "source PR keeps its own build config variant", pulls: []prowapi.Pull{{Number: 1}}, want: api.Metadata{Org: "openshift,openshift", Repo: "installer,cluster-api-actuator-pkg", Branch: "main,release-5.0"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			job := &api.JobSpec{JobSpec: downwardapi.JobSpec{Refs: &prowapi.Refs{Org: "openshift", Repo: "installer", BaseRef: "main", Pulls: []prowapi.Pull{{Number: 10978}}}, ExtraRefs: []prowapi.Refs{{Org: "openshift", Repo: "cluster-api-actuator-pkg", BaseRef: "release-5.0", Pulls: testCase.pulls}}}}
			before, err := json.Marshal(job)
			if err != nil {
				t.Fatal(err)
			}
			options := options{injectTest: "openshift/cluster-api-actuator-pkg@release-5.0__periodics:e2e-gcp-operator-periodic"}
			if diff := cmp.Diff(&testCase.want, options.getResolverInfo(job)); diff != "" {
				t.Errorf("resolver metadata: %s", diff)
			}
			after, err := json.Marshal(job)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(before, after); diff != "" {
				t.Errorf("PR job refs were modified: %s", diff)
			}
		})
	}
}

func TestInjectedTestWithoutPRs(t *testing.T) {
	t.Parallel()
	options := options{injectTest: "openshift/cluster-api-actuator-pkg@release-5.0__periodics:e2e-gcp-operator-periodic"}
	job := &api.JobSpec{JobSpec: downwardapi.JobSpec{ExtraRefs: []prowapi.Refs{{Org: "openshift", Repo: "cluster-api-actuator-pkg", BaseRef: "release-5.0"}}}}
	want := &api.Metadata{Org: "openshift", Repo: "cluster-api-actuator-pkg", Branch: "release-5.0", Variant: "periodics"}
	if diff := cmp.Diff(want, options.getResolverInfo(job)); diff != "" {
		t.Errorf("source config coordinates: %s", diff)
	}
}
