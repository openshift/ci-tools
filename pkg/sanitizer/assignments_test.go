package sanitizer

import (
	"os"
	"path/filepath"
	"testing"

	"k8s.io/apimachinery/pkg/util/sets"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/dispatcher"
)

func TestApplyDefaultClusterAssignmentsOnlyChangesCluster(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.yaml")
	jobConfig := prowconfig.JobConfig{
		PresubmitsStatic: map[string][]prowconfig.Presubmit{
			"org/repo": {{JobBase: prowconfig.JobBase{Name: "presubmit", Agent: "kubernetes"}, Brancher: prowconfig.Brancher{Branches: []string{"main"}}}},
		},
	}
	writeJobConfig(t, path, jobConfig)

	if err := ApplyDefaultClusterAssignments(dir, &dispatcher.Config{Default: "api.ci"}, sets.New[string](), dispatcher.ClusterMap{}); err != nil {
		t.Fatalf("ApplyDefaultClusterAssignments() returned an error: %v", err)
	}

	actual := readJobConfig(t, path)
	job := actual.PresubmitsStatic["org/repo"][0]
	if job.Cluster != "api.ci" {
		t.Errorf("cluster = %q, want %q", job.Cluster, "api.ci")
	}
	if len(job.Branches) != 1 || job.Branches[0] != "main" {
		t.Errorf("branches = %v, want [main]", job.Branches)
	}
}

func writeJobConfig(t *testing.T, path string, jobConfig prowconfig.JobConfig) {
	t.Helper()
	data, err := yaml.Marshal(jobConfig)
	if err != nil {
		t.Fatalf("failed to marshal job config: %v", err)
	}
	if err := os.WriteFile(path, data, 0o664); err != nil {
		t.Fatalf("failed to write job config: %v", err)
	}
}

func readJobConfig(t *testing.T, path string) prowconfig.JobConfig {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read job config: %v", err)
	}
	var jobConfig prowconfig.JobConfig
	if err := yaml.Unmarshal(data, &jobConfig); err != nil {
		t.Fatalf("failed to unmarshal job config: %v", err)
	}
	return jobConfig
}
