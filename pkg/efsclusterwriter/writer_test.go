package efsclusterwriter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/dispatcher"
	"github.com/openshift/ci-tools/pkg/jobconfig"
)

type fakeClient struct {
	assignments *Assignments
}

func (f *fakeClient) Assignments(context.Context, uint64) (*Assignments, error) {
	return f.assignments, nil
}
func (f *fakeClient) Rescan(context.Context) error { return nil }

func writeJob(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "openshift", "repo", "repo-main-presubmits.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	jc := &prowconfig.JobConfig{PresubmitsStatic: map[string][]prowconfig.Presubmit{
		"openshift/repo": {{JobBase: prowconfig.JobBase{Name: "pull-ci-openshift-repo-main-unit", Spec: &corev1.PodSpec{Containers: []corev1.Container{{Name: "test"}}}}}},
	}}
	data, _ := yaml.Marshal(jc)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReconcileAssignsOnlyCluster(t *testing.T) {
	root := t.TempDir()
	path := writeJob(t, root)
	before, _ := os.ReadFile(path)

	client := &fakeClient{assignments: &Assignments{
		Generation:  7,
		Assignments: map[string]Assignment{"pull-ci-openshift-repo-main-unit": {Cluster: "build01", BaselineCluster: "build01"}},
		Inventory:   map[string]dispatcher.ClusterInfo{"build01": {Provider: "aws", Capacity: 100}},
	}}
	r := &Runner{Client: client, Config: Config{EFSRoot: root}}
	res, err := r.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Updated != 1 || res.Files != 1 {
		t.Fatalf("unexpected result: %+v", res)
	}
	after, _ := os.ReadFile(path)
	if strings.Contains(string(before), "cluster:") {
		t.Fatal("test precondition: job already had a cluster")
	}
	if !strings.Contains(string(after), "cluster: build01") {
		t.Fatalf("cluster not written:\n%s", after)
	}
	// Second pass must be idempotent.
	res, err = r.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 0 {
		t.Fatalf("expected no writes on second pass, got %+v", res)
	}
}

func TestReconcileSkipsMissingAndBlocked(t *testing.T) {
	root := t.TempDir()
	path := writeJob(t, root)
	client := &fakeClient{assignments: &Assignments{
		Generation: 1,
		Assignments: map[string]Assignment{
			"pull-ci-openshift-repo-main-unit": {Cluster: "build02", BaselineCluster: "build02"},
		},
		Inventory: map[string]dispatcher.ClusterInfo{"build02": {Provider: "aws", Capacity: 100}},
		Blocked:   []string{"build02"},
	}}
	r := &Runner{Client: client, Config: Config{EFSRoot: root}}
	res, err := r.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Blocked != 1 || res.Files != 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "cluster:") {
		t.Fatal("blocked target must not be written")
	}
}

func TestReconcilePersistsBaselineNotOverride(t *testing.T) {
	root := t.TempDir()
	path := writeJob(t, root)
	client := &fakeClient{assignments: &Assignments{
		Generation: 3,
		Assignments: map[string]Assignment{
			"pull-ci-openshift-repo-main-unit": {Cluster: "build02", BaselineCluster: "build01"},
		},
		Inventory: map[string]dispatcher.ClusterInfo{
			"build01": {Provider: "aws", Capacity: 100},
			"build02": {Provider: "aws", Capacity: 100},
		},
	}}
	r := &Runner{Client: client, Config: Config{EFSRoot: root}}
	if _, err := r.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "cluster: build01") {
		t.Fatalf("baseline cluster not written:\n%s", data)
	}
}

func TestReconcileSeedsNewTreePastMoveLimit(t *testing.T) {
	root := t.TempDir()
	writeJob(t, root)
	client := &fakeClient{assignments: &Assignments{
		Generation: 4,
		Assignments: map[string]Assignment{
			"pull-ci-openshift-repo-main-unit": {Cluster: "build01", BaselineCluster: "build01"},
		},
		Inventory: map[string]dispatcher.ClusterInfo{"build01": {Provider: "aws", Capacity: 100}},
	}}
	r := &Runner{Client: client, Config: Config{EFSRoot: root, MaxMoves: 0, MaxMovePercent: 0}}
	res, err := r.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("first-time assignments must not trip the breaker: %v", err)
	}
	if res.Updated != 1 {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestReconcileMergesPublishDuringPass(t *testing.T) {
	root := t.TempDir()
	path := writeJob(t, root)
	client := &fakeClient{assignments: &Assignments{
		Generation: 5,
		Assignments: map[string]Assignment{
			"pull-ci-openshift-repo-main-unit": {Cluster: "build01", BaselineCluster: "build01"},
		},
		Inventory: map[string]dispatcher.ClusterInfo{"build01": {Provider: "aws", Capacity: 100}},
	}}
	// The publisher replaces the file between planning and writing.
	published := &prowconfig.JobConfig{
		PresubmitsStatic: map[string][]prowconfig.Presubmit{
			"openshift/repo": {{
				JobBase: prowconfig.JobBase{
					Name: "pull-ci-openshift-repo-main-unit",
					Spec: &corev1.PodSpec{Containers: []corev1.Container{{Name: "test", Image: "published-image"}}},
				},
			}},
		},
		PostsubmitsStatic: map[string][]prowconfig.Postsubmit{
			"openshift/repo": {{JobBase: prowconfig.JobBase{Name: "branch-ci-openshift-repo-main-images", Cluster: "build02"}}},
		},
	}
	r := &Runner{Client: client, Config: Config{EFSRoot: root, beforeWrite: func(string) {
		if err := jobconfig.WriteToFileAtomic(path, published); err != nil {
			t.Fatal(err)
		}
	}}}
	res, err := r.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Updated != 1 || res.Files != 1 {
		t.Fatalf("expected the assignment to be applied to the merged file, got %+v", res)
	}
	data, _ := os.ReadFile(path)
	got := &prowconfig.JobConfig{}
	if err := yaml.Unmarshal(data, got); err != nil {
		t.Fatal(err)
	}
	if len(got.PostsubmitsStatic["openshift/repo"]) != 1 {
		t.Fatalf("publisher change was dropped:\n%s", data)
	}
	presubmits := got.PresubmitsStatic["openshift/repo"]
	if len(presubmits) != 1 || presubmits[0].Cluster != "build01" {
		t.Fatalf("expected the cluster to be applied to the published job:\n%s", data)
	}
	if presubmits[0].Spec == nil || presubmits[0].Spec.Containers[0].Image != "published-image" {
		t.Fatalf("publisher container change was dropped:\n%s", data)
	}
}

func TestReconcileWritesManualClusterOutsideInventory(t *testing.T) {
	root := t.TempDir()
	path := writeJob(t, root)
	// vsphere02 is a manual destination and is deliberately absent from the
	// build-farm inventory; the dispatcher's assignment is still authoritative.
	client := &fakeClient{assignments: &Assignments{
		Generation: 9,
		Assignments: map[string]Assignment{
			"pull-ci-openshift-repo-main-unit": {Cluster: "vsphere02", BaselineCluster: "vsphere02"},
		},
		Inventory: map[string]dispatcher.ClusterInfo{"build01": {Provider: "aws", Capacity: 100}},
	}}
	r := &Runner{Client: client, Config: Config{EFSRoot: root}}
	res, err := r.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Updated != 1 || res.Blocked != 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "cluster: vsphere02") {
		t.Fatalf("manual cluster not written:\n%s", data)
	}
}
