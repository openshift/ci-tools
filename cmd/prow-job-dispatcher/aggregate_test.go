package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	prowjobv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowconfig "sigs.k8s.io/prow/pkg/config"
	"sigs.k8s.io/prow/pkg/scheduler/strategy"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/dispatcher"
)

func TestLoadAggregateJobs(t *testing.T) {
	for _, tc := range []struct {
		name             string
		content          string
		want             map[string]string
		wantError        string
		missingDirectory bool
		brokenLink       bool
		wantSyntaxError  bool
	}{
		{name: "default and custom templates", content: `{"verify":{"aggregated-aws-ovn-upgrade-5.1-micro-fips":{"prowJob":{"name":"periodic-ci-openshift-release-main-nightly-5.1-e2e-aws-ovn-upgrade-fips"},"aggregatedProwJob":{"analysisJobCount":7}},"custom":{"aggregatedProwJob":{"analysisJobCount":2,"prowJob":{"name":"custom-controller"}}},"ordinary":{"prowJob":{"name":"ordinary"}},"disabled":{"aggregatedProwJob":{"analysisJobCount":0}}}}`, want: map[string]string{"aggregated-aws-ovn-upgrade-5.1-micro-fips-release-openshift-release-analysis-aggregator": "release-openshift-release-analysis-aggregator", "custom-custom-controller": "custom-controller"}},
		{name: "invalid JSON", content: `{`, wantError: "unexpected end of JSON input", wantSyntaxError: true},
		{name: "missing directory", missingDirectory: true, wantError: "load aggregate jobs"},
		{name: "unreadable release file", brokenLink: true, wantError: "read release configuration"},
		{name: "conflicting templates fail closed", content: `{"verify":{"x":{"aggregatedProwJob":{"analysisJobCount":1,"prowJob":{"name":"a-b"}}},"x-a":{"aggregatedProwJob":{"analysisJobCount":1,"prowJob":{"name":"b"}}}}}`, wantError: `aggregate job "x-a-b" references multiple templates`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "release.json")
			if tc.missingDirectory {
				directory = filepath.Join(directory, "missing")
			}
			if tc.brokenLink {
				if err := os.Symlink(filepath.Join(directory, "missing.json"), path); err != nil {
					t.Fatal(err)
				}
			}
			if !tc.missingDirectory && !tc.brokenLink {
				if err := os.WriteFile(path, []byte(tc.content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := loadAggregateJobs(directory)
			if (err != nil) != (tc.wantError != "") || err != nil && !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("loadAggregateJobs() error = %v", err)
			}
			if err != nil && (!strings.Contains(err.Error(), directory) || !tc.missingDirectory && !strings.Contains(err.Error(), path)) {
				t.Errorf("source path missing from error: %v", err)
			}
			var syntaxError *json.SyntaxError
			if tc.wantSyntaxError && !errors.As(err, &syntaxError) {
				t.Errorf("JSON error not wrapped: %v", err)
			}
			if (tc.missingDirectory || tc.brokenLink) && !errors.Is(err, os.ErrNotExist) {
				t.Errorf("filesystem error not wrapped: %v", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("unexpected aliases (-want +got):\n%s", diff)
			}
		})
	}
}

func TestExpandAggregateJobs(t *testing.T) {
	for _, tc := range []struct {
		name      string
		namespace *string
	}{
		{name: "default namespace"},
		{name: "prow job namespace", namespace: new("")},
		{name: "explicit namespace", namespace: new("ci")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := &prowconfig.JobConfig{Periodics: []prowconfig.Periodic{{JobBase: prowconfig.JobBase{Name: "controller", Namespace: tc.namespace, Labels: map[string]string{"capability/rce": "rce"}, Annotations: map[string]string{"description": "controller"}, Spec: &corev1.PodSpec{ServiceAccountName: "ci-operator"}}}}}
			original := config.Periodics[0]
			original.JobBase = *config.Periodics[0].JobBase.DeepCopy()
			aliases := map[string]string{"aggregate-controller": "controller", "unconfigured-controller": "missing-template"}
			expandAggregateJobs(config, aliases)
			expandAggregateJobs(config, aliases)
			if len(config.Periodics) != 2 {
				t.Fatalf("expanded jobs = %d, want 2", len(config.Periodics))
			}
			if diff := cmp.Diff(original, config.Periodics[0], cmpopts.IgnoreUnexported(prowconfig.Periodic{})); diff != "" {
				t.Fatalf("ordinary job changed (-want +got):\n%s", diff)
			}
			aggregate := config.Periodics[1]
			aggregate.JobBase = *config.Periodics[1].JobBase.DeepCopy()
			aggregate.Name = "controller"
			delete(aggregate.Annotations, aggregateTemplateAnnotation)
			if diff := cmp.Diff(original, aggregate, cmpopts.IgnoreUnexported(prowconfig.Periodic{})); diff != "" {
				t.Fatalf("aggregate lost template settings (-want +got):\n%s", diff)
			}
			config.Periodics[1].Labels["capability/rce"] = "other"
			if config.Periodics[0].Labels["capability/rce"] != "rce" {
				t.Fatal("aggregate aliases share template labels")
			}
		})
	}
}

func TestAggregateJobNameCollisions(t *testing.T) {
	for _, tc := range []struct {
		name     string
		jobs     string
		sameFile bool
	}{
		{name: "periodic", jobs: "periodics:\n- name: verify-controller\n"},
		{name: "presubmit", jobs: "presubmits:\n  org/repo:\n  - name: verify-controller\n"},
		{name: "postsubmit", jobs: "postsubmits:\n  org/repo:\n  - name: verify-controller\n"},
		{name: "same file", jobs: "periodics:\n- name: verify-controller\n- name: controller\n", sameFile: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jobDir, releaseDir := t.TempDir(), t.TempDir()
			template := "periodics:\n- name: controller\n"
			if tc.sameFile {
				template = "periodics: []\n"
			}
			for path, content := range map[string]string{
				filepath.Join(jobDir, "template.yaml"):    template,
				filepath.Join(jobDir, "ordinary.yaml"):    tc.jobs,
				filepath.Join(releaseDir, "release.json"): `{"verify":{"verify":{"aggregatedProwJob":{"analysisJobCount":1,"prowJob":{"name":"controller"}}}}}`,
			} {
				if err := os.WriteFile(path, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := composeFileInfoList(jobDir, releaseDir)
			if err == nil || !strings.Contains(err.Error(), `aggregate job "verify-controller" collides with configured job`) {
				t.Fatalf("expected ambiguous ordinary/aggregate identity to fail closed, got %v", err)
			}
			if _, err := composeFileInfoList(jobDir); err != nil {
				t.Fatalf("ordinary-only configuration changed: %v", err)
			}
			config := &dispatcher.Config{Default: "build01", BuildFarm: map[api.Cloud]map[api.Cluster]*dispatcher.BuildFarmConfig{api.CloudAWS: {"build01": {}}}}
			inventory := dispatcher.ClusterMap{"build01": {Provider: "aws", Capacity: 100}}
			if _, err := dispatchJobsWithPolicy(jobDir, config, nil, sets.New[string](), nil, inventory, nil, 0, -1, 1, releaseDir); err == nil {
				t.Fatal("full dispatch accepted ambiguous identity")
			}
			assignments := map[string]dispatcher.ProwJobData{"verify-controller": {Cluster: "build01", Demand: 37, Group: "ordinary.yaml"}}
			if err := dispatchDeltaJobs(jobDir, config, sets.New[string](), assignments, inventory, 1, releaseDir); err == nil {
				t.Fatal("delta dispatch accepted ambiguous identity")
			}
			if len(assignments) != 1 || assignments["verify-controller"].Demand != 37 {
				t.Fatalf("failed refresh modified assignments: %#v", assignments)
			}
		})
	}
}

func TestAggregateAliasesAcrossDirectories(t *testing.T) {
	for _, tc := range []struct {
		name      string
		second    string
		wantError bool
	}{
		{name: "shared identity deduplicated", second: `{"verify":{"x":{"aggregatedProwJob":{"analysisJobCount":2,"prowJob":{"name":"a-b"}}}}}`},
		{name: "conflicting templates rejected", second: `{"verify":{"x-a":{"aggregatedProwJob":{"analysisJobCount":2,"prowJob":{"name":"b"}}}}}`, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first, second := t.TempDir(), t.TempDir()
			for path, content := range map[string]string{
				filepath.Join(first, "first.json"):   `{"verify":{"x":{"aggregatedProwJob":{"analysisJobCount":1,"prowJob":{"name":"a-b"}}}}}`,
				filepath.Join(second, "second.json"): tc.second,
			} {
				if err := os.WriteFile(path, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := composeFileInfoList(t.TempDir(), first, second)
			if (err != nil) != tc.wantError {
				t.Fatalf("composeFileInfoList() error = %v", err)
			}
		})
	}
}

func TestAggregateDeltaDiscovery(t *testing.T) {
	jobDir, releaseDir := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(jobDir, "infra.yaml"), []byte("periodics:\n- name: controller\n  cluster: build01\n"), 0600); err != nil {
		t.Fatal(err)
	}
	releasePath := filepath.Join(releaseDir, "release.json")
	if err := os.WriteFile(releasePath, []byte(`{"verify":{"new":{"aggregatedProwJob":{"analysisJobCount":7,"prowJob":{"name":"controller"}}}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	config := &dispatcher.Config{Default: "build01", BuildFarm: map[api.Cloud]map[api.Cluster]*dispatcher.BuildFarmConfig{api.CloudAWS: {"build01": {}}}, BuildFarmCloud: map[api.Cloud][]string{api.CloudAWS: {"build01"}}}
	inventory := dispatcher.ClusterMap{"build01": {Provider: "aws", Capacity: 100}}
	assignments := map[string]dispatcher.ProwJobData{"controller": {Cluster: "build01", Demand: 2, Group: "infra.yaml"}}
	if err := dispatchDeltaJobs(jobDir, config, sets.New[string](), assignments, inventory, 1, releaseDir); err != nil {
		t.Fatal(err)
	}
	if assignments["controller"].Demand != 2 || assignments["new-controller"].Demand != 1 || len(assignments) != 2 {
		t.Fatalf("delta discovery lost ordinary demand or aggregate minimum: %#v", assignments)
	}
	volumes := map[string]float64{"controller": 2, "new-controller": 37}
	assignments, err := dispatchJobsWithPolicy(jobDir, config, volumes, sets.New[string](), nil, inventory, assignments, 0, -1, 1, releaseDir)
	if err != nil {
		t.Fatal(err)
	}
	if assignments["controller"].Demand != 2 || assignments["new-controller"].Demand != 37 || len(assignments) != 2 {
		t.Fatalf("full refresh lost separate measured demand: %#v", assignments)
	}
	if err := os.WriteFile(releasePath, []byte(`{"verify":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	assignments, err = dispatchJobsWithPolicy(jobDir, config, volumes, sets.New[string](), nil, inventory, assignments, 0, -1, 1, releaseDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(assignments) != 1 || assignments["controller"].Demand != 2 {
		t.Fatalf("full refresh retained obsolete aggregate: %#v", assignments)
	}
}

func TestDispatchJobsWithPolicy(t *testing.T) {
	const templateName = "release-openshift-release-analysis-aggregator"
	const aliasName = "aggregated-vsphere-ovn-upgrade-5.1-micro-" + templateName
	for _, tc := range []struct {
		name        string
		labels      map[string]string
		manual      bool
		cloud       bool
		blocked     bool
		hysteresis  float64
		budget      float64
		previous    string
		wantCluster string
	}{
		{name: "ordinary and aggregate use capacity formula", wantCluster: "build02"},
		{name: "capability remains mandatory", labels: map[string]string{"capability/rce": "rce"}, wantCluster: "build01"},
		{name: "cloud remains mandatory", labels: map[string]string{api.CloudLabel: "aws"}, cloud: true, wantCluster: "build01"},
		{name: "manual template stays local", manual: true, wantCluster: "app.ci"},
		{name: "blocked cluster is avoided", blocked: true, wantCluster: "build01"},
		{name: "aggregate hysteresis is preserved", hysteresis: 100, budget: 100, previous: "build01", wantCluster: "build01"},
		{name: "aggregate movement budget is preserved", budget: 1, previous: "build01", wantCluster: "build01"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			jobDir, releaseDir := filepath.Join(root, "jobs"), filepath.Join(root, "releases")
			for _, directory := range []string{jobDir, releaseDir} {
				if err := os.Mkdir(directory, 0700); err != nil {
					t.Fatal(err)
				}
			}
			namespace := "test-pods"
			jobConfig := &prowconfig.JobConfig{Periodics: []prowconfig.Periodic{{JobBase: prowconfig.JobBase{Name: templateName, Cluster: "build01", Namespace: &namespace, Labels: tc.labels, Spec: &corev1.PodSpec{ServiceAccountName: "ci-operator"}}}}}
			data, err := yaml.Marshal(jobConfig)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(jobDir, "infra-periodics.yaml"), data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(releaseDir, "release.json"), []byte(`{"verify":{"aggregated-vsphere-ovn-upgrade-5.1-micro":{"prowJob":{"name":"periodic-ci-workload-vsphere"},"aggregatedProwJob":{"analysisJobCount":7}}}}`), 0600); err != nil {
				t.Fatal(err)
			}
			config := &dispatcher.Config{Default: "build01", DetermineE2EByJob: tc.cloud, BuildFarm: map[api.Cloud]map[api.Cluster]*dispatcher.BuildFarmConfig{api.CloudAWS: {"build01": {}}, api.CloudGCP: {"build02": {}}}, BuildFarmCloud: map[api.Cloud][]string{api.CloudAWS: {"build01"}, api.CloudGCP: {"build02"}}}
			if tc.manual {
				config.Groups = dispatcher.JobGroups{"app.ci": {Jobs: []string{templateName}}}
				config.ManualClusters = []api.Cluster{"app.ci"}
			}
			inventory := dispatcher.ClusterMap{"build01": {Provider: "aws", Capacity: 100, Capabilities: []string{"rce"}}, "build02": {Provider: "gcp", Capacity: 200}}
			blocked := sets.New[string]()
			if tc.blocked {
				blocked.Insert("build02")
			}
			volumes := map[string]float64{templateName: 2, aliasName: 37}
			previous := map[string]dispatcher.ProwJobData{}
			budget := float64(-1)
			if tc.previous != "" {
				budget = tc.budget
				for name, demand := range volumes {
					previous[name] = dispatcher.ProwJobData{Cluster: tc.previous, Demand: demand, Group: "infra-periodics.yaml"}
				}
			}
			assignments, err := dispatchJobsWithPolicy(jobDir, config, volumes, blocked, nil, inventory, previous, tc.hysteresis, budget, 1, releaseDir)
			if err != nil {
				t.Fatal(err)
			}
			if len(assignments) != 2 {
				t.Fatalf("expected template and aggregate, got %#v", assignments)
			}
			for name, demand := range volumes {
				assignment := assignments[name]
				if assignment.Cluster != tc.wantCluster || assignment.Demand != demand || assignment.Group != "infra-periodics.yaml" {
					t.Fatalf("%s assignment = %#v", name, assignment)
				}
			}
			aliases, err := loadAggregateJobs(releaseDir)
			if err != nil {
				t.Fatal(err)
			}
			expandAggregateJobs(jobConfig, aliases)
			expandAggregateJobs(jobConfig, aliases)
			if len(jobConfig.Periodics) != 2 || jobConfig.Periodics[1].Namespace == nil || *jobConfig.Periodics[1].Namespace != namespace || jobConfig.Periodics[1].Spec.ServiceAccountName != "ci-operator" {
				t.Fatalf("aggregate lost template locality: %#v", jobConfig.Periodics)
			}
			if got := placementJobBase(jobConfig.Periodics[1].JobBase).Name; got != templateName {
				t.Fatalf("aggregate placement identity = %q", got)
			}
			if got := placementJobBase(jobConfig.Periodics[0].JobBase).Name; got != templateName {
				t.Fatalf("ordinary placement identity changed to %q", got)
			}
			files, err := composeFileInfoList(jobDir, releaseDir)
			if err != nil {
				t.Fatal(err)
			}
			movementDemand, err := movementBudgetForConfiguredJobFiles(files, volumes, 100, 1)
			if err != nil {
				t.Fatal(err)
			}
			if movementDemand != 39 {
				t.Fatalf("aggregate demand counted incorrectly: %v", movementDemand)
			}
			if err := dispatchDeltaJobs(jobDir, config, blocked, assignments, inventory, 1, releaseDir); err != nil {
				t.Fatal(err)
			}
			if assignments[aliasName].Demand != 37 || assignments[aliasName].Cluster != tc.wantCluster {
				t.Fatalf("delta changed aggregate demand or eligibility: %#v", assignments[aliasName])
			}
			manager := dispatcher.NewSnapshotManager("")
			snapshot, _, err := dispatcher.CompileSnapshot(dispatcher.CompileInput{Baseline: assignments, Inventory: inventory, Generation: 1, Now: time.Unix(100, 0)})
			if err != nil {
				t.Fatal(err)
			}
			if err := manager.Publish(snapshot); err != nil {
				t.Fatal(err)
			}
			server := dispatcher.NewSnapshotServer(dispatcher.NewProwjobs(""), dispatcher.NewEphemeralClusterDispatcher(nil), nil, manager)
			body, err := json.Marshal(dispatcher.SchedulingRequest{Job: aliasName})
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			server.RequestHandler(response, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body)))
			var decision dispatcher.SchedulingResponse
			if err := json.Unmarshal(response.Body.Bytes(), &decision); err != nil {
				t.Fatalf("aggregate lookup returned %d: %s", response.Code, response.Body.String())
			}
			if response.Code != http.StatusOK || decision.Source != "baseline" || decision.Cluster != tc.wantCluster {
				t.Fatalf("aggregate snapshot lookup = %#v", decision)
			}
			httpServer := httptest.NewServer(http.HandlerFunc(server.RequestHandler))
			defer httpServer.Close()
			scheduler := strategy.NewExternal(prowconfig.ExternalScheduling{URL: httpServer.URL, Cache: prowconfig.ExternalSchedulingCache{CleanupInterval: &metav1.Duration{Duration: time.Minute}, EntryTimeoutInterval: &metav1.Duration{Duration: time.Minute}}}, logrus.NewEntry(logrus.New()))
			result, err := scheduler.Schedule(t.Context(), &prowjobv1.ProwJob{Spec: prowjobv1.ProwJobSpec{Job: aliasName, Cluster: "fallback-only"}})
			if err != nil || result.Cluster != tc.wantCluster {
				t.Fatalf("external scheduler failed aggregate lookup: result=%#v error=%v", result, err)
			}
		})
	}
}

func TestAggregateSchedulingFallback(t *testing.T) {
	for _, tc := range []struct {
		name        string
		job         string
		wantCluster string
	}{
		{name: "ordinary template recognized", job: "release-openshift-release-analysis-aggregator", wantCluster: "build01"},
		{name: "generated aggregate name falls back to checked-in cluster", job: "aggregated-aws-ovn-upgrade-5.1-micro-fips-release-openshift-release-analysis-aggregator", wantCluster: "build02"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := dispatcher.NewSnapshotManager("")
			snapshot, _, err := dispatcher.CompileSnapshot(dispatcher.CompileInput{Baseline: map[string]dispatcher.ProwJobData{"release-openshift-release-analysis-aggregator": {Cluster: "build01", Demand: 2, Group: "infra-periodics.yaml"}}, Inventory: dispatcher.ClusterMap{"build01": {Provider: "aws", Capacity: 100}}, Generation: 1, Now: time.Unix(100, 0)})
			if err != nil {
				t.Fatal(err)
			}
			if err := manager.Publish(snapshot); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(dispatcher.NewSnapshotServer(dispatcher.NewProwjobs(""), dispatcher.NewEphemeralClusterDispatcher(nil), nil, manager).RequestHandler))
			defer server.Close()
			scheduler := strategy.NewExternal(prowconfig.ExternalScheduling{URL: server.URL, Cache: prowconfig.ExternalSchedulingCache{CleanupInterval: &metav1.Duration{Duration: time.Minute}, EntryTimeoutInterval: &metav1.Duration{Duration: time.Minute}}}, logrus.NewEntry(logrus.New()))
			result, err := scheduler.Schedule(t.Context(), &prowjobv1.ProwJob{Spec: prowjobv1.ProwJobSpec{Job: tc.job, Cluster: "build02"}})
			if err != nil {
				t.Fatal(err)
			}
			if result.Cluster != tc.wantCluster {
				t.Fatalf("scheduled cluster = %q, want %q", result.Cluster, tc.wantCluster)
			}
		})
	}
}
