package steps

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	coreapi "k8s.io/api/core/v1"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/clonerefs"
	"sigs.k8s.io/prow/pkg/pod-utils/downwardapi"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestBorrowedSourceClonerefs(t *testing.T) {
	t.Parallel()
	source := prowapi.Refs{Org: "openshift", Repo: "cluster-api-actuator-pkg", BaseRef: "release-5.0", SparseCheckoutFiles: []string{".ci-operator.yaml"}}
	job := &api.JobSpec{JobSpec: downwardapi.JobSpec{
		Refs:      &prowapi.Refs{Org: "openshift", Repo: "installer", BaseRef: "main", WorkDir: true, Pulls: []prowapi.Pull{{Number: 10978, SHA: "head"}}},
		ExtraRefs: []prowapi.Refs{source},
	}}
	job.SetNamespace("test")
	before, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	config := api.SourceStepConfiguration{From: "root", To: "src", Ref: "openshift.cluster-api-actuator-pkg", ClonerefsPullSpec: api.ClonerefsPullSpec, ClonerefsPath: api.ClonerefsPath}
	build := createBuild(config, job, coreapi.ObjectReference{Kind: "DockerImage", Name: api.ClonerefsPullSpec}, nil, nil, nil)
	var cloneOptions clonerefs.Options
	for _, env := range build.Spec.Strategy.DockerStrategy.Env {
		if env.Name == clonerefs.JSONConfigEnvVar {
			if err := json.Unmarshal([]byte(env.Value), &cloneOptions); err != nil {
				t.Fatal(err)
			}
		}
	}
	source.SparseCheckoutFiles = nil
	if diff := cmp.Diff([]prowapi.Refs{source}, cloneOptions.GitRefs); diff != "" {
		t.Errorf("wrong clonerefs inputs: %s", diff)
	}
	if build.Spec.Source.Dockerfile == nil || !strings.Contains(*build.Spec.Source.Dockerfile, "WORKDIR /go/src/github.com/openshift/cluster-api-actuator-pkg/") {
		t.Fatal("source image has the PR repository working directory")
	}
	after, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(before, after); diff != "" {
		t.Errorf("PR refs changed: %s", diff)
	}
}
