package bumper

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJobNameToConfigFile(t *testing.T) {
	testCases := []struct {
		name             string
		jobName          string
		org              string
		repo             string
		branch           string
		variant          string
		expectedFilename string
		expectError      bool
	}{
		{
			name:             "cluster-control-plane-machine-set-operator periodic job",
			jobName:          "periodic-ci-openshift-cluster-control-plane-machine-set-operator-release-4.21-periodics-e2e-aws",
			org:              "openshift",
			repo:             "cluster-control-plane-machine-set-operator",
			branch:           "release-4.21",
			variant:          "periodics",
			expectedFilename: "openshift-cluster-control-plane-machine-set-operator-release-4.21__periodics.yaml",
			expectError:      false,
		},
		{
			name:             "cluster-control-plane-machine-set-operator periodic job main, 4.21 variant",
			jobName:          "periodic-ci-openshift-cluster-control-plane-machine-set-operator-main-4.21-e2e-aws",
			org:              "openshift",
			repo:             "cluster-control-plane-machine-set-operator",
			branch:           "main",
			variant:          "4.21",
			expectedFilename: "openshift-cluster-control-plane-machine-set-operator-main__4.21.yaml",
			expectError:      false,
		},
		{
			name:             "cluster-control-plane-machine-set-operator periodic job master, 4.21 variant",
			jobName:          "periodic-ci-openshift-cluster-control-plane-machine-set-operator-master-4.21-e2e-aws",
			org:              "openshift",
			repo:             "cluster-control-plane-machine-set-operator",
			branch:           "master",
			variant:          "4.21",
			expectedFilename: "openshift-cluster-control-plane-machine-set-operator-master__4.21.yaml",
			expectError:      false,
		},
		{
			name:             "cluster-control-plane-machine-set-operator periodic job master, release-4.21 variant",
			jobName:          "periodic-ci-openshift-cluster-control-plane-machine-set-operator-master-release-4.21-e2e-aws",
			org:              "openshift",
			repo:             "cluster-control-plane-machine-set-operator",
			branch:           "master",
			variant:          "release-4.21",
			expectedFilename: "openshift-cluster-control-plane-machine-set-operator-master__release-4.21.yaml",
			expectError:      false,
		},
		{
			name:             "release nightly job",
			jobName:          "periodic-ci-openshift-release-master-nightly-4.21-e2e-aws",
			org:              "openshift",
			repo:             "release",
			branch:           "master",
			variant:          "nightly-4.21",
			expectedFilename: "openshift-release-master__nightly-4.21.yaml",
			expectError:      false,
		},
		{
			name:             "release ci job",
			jobName:          "periodic-ci-openshift-release-master-ci-4.21-e2e-aws",
			org:              "openshift",
			repo:             "release",
			branch:           "master",
			variant:          "ci-4.21",
			expectedFilename: "openshift-release-master__ci-4.21.yaml",
			expectError:      false,
		},
		{
			name:        "non-periodic job",
			jobName:     "presubmit-ci-openshift-cluster-control-plane-machine-set-operator-release-4.21-e2e-aws",
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.expectError {
				// Create temporary directory structure
				tmpDir := t.TempDir()

				// Create ci-operator/config directory
				configDir := filepath.Join(tmpDir, "ci-operator", "config")
				if err := os.MkdirAll(filepath.Join(configDir, tc.org, tc.repo), 0755); err != nil {
					t.Fatalf("failed to create config dir: %v", err)
				}

				// Create ci-operator/jobs directory
				jobsDir := filepath.Join(tmpDir, "ci-operator", "jobs")
				if err := os.MkdirAll(filepath.Join(jobsDir, tc.org, tc.repo), 0755); err != nil {
					t.Fatalf("failed to create jobs dir: %v", err)
				}

				// Create mock Prow job file
				jobFileContent := `periodics:
- name: ` + tc.jobName + `
  labels:
    ci-operator.openshift.io/variant: ` + tc.variant + `
  extra_refs:
  - org: ` + tc.org + `
    repo: ` + tc.repo + `
    base_ref: ` + tc.branch + `
`
				jobFilePath := filepath.Join(jobsDir, tc.org, tc.repo, "test-periodics.yaml")
				if err := os.WriteFile(jobFilePath, []byte(jobFileContent), 0644); err != nil {
					t.Fatalf("failed to create job file: %v", err)
				}

				// Create the expected config file
				configFilePath := filepath.Join(configDir, tc.org, tc.repo, tc.expectedFilename)
				if err := os.WriteFile(configFilePath, []byte("test config"), 0644); err != nil {
					t.Fatalf("failed to create config file: %v", err)
				}

				// Test the function
				configFile, err := JobNameToConfigFile(tc.jobName, configDir)
				if err != nil {
					t.Errorf("unexpected error: %v", err)
					return
				}

				if filepath.Base(configFile) != tc.expectedFilename {
					t.Errorf("expected filename %s, got %s", tc.expectedFilename, filepath.Base(configFile))
				}
			} else {
				// Test error cases
				tmpDir := t.TempDir()
				configDir := filepath.Join(tmpDir, "ci-operator", "config")

				_, err := JobNameToConfigFile(tc.jobName, configDir)
				if err == nil {
					t.Errorf("expected error but got none")
				}
			}
		})
	}
}

func TestLoadSippyConfig(t *testing.T) {
	// Create a temporary Sippy config file for testing
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "sippy-config.yaml")

	configContent := `releases:
  "4.21":
    informingJobs:
      - periodic-ci-openshift-release-master-nightly-4.21-e2e-aws
      - periodic-ci-openshift-release-master-ci-4.21-e2e-gcp
  "4.20":
    informingJobs:
      - periodic-ci-openshift-release-master-nightly-4.20-e2e-aws
`

	if err := os.WriteFile(configFile, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to create test config: %v", err)
	}

	config, err := LoadSippyConfig(configFile)
	if err != nil {
		t.Fatalf("failed to load sippy config: %v", err)
	}

	// Test getting informing jobs for 4.21
	jobs, err := config.GetInformingJobsForRelease("4.21")
	if err != nil {
		t.Errorf("failed to get informing jobs for 4.21: %v", err)
	}

	if len(jobs) != 2 {
		t.Errorf("expected 2 jobs for 4.21, got %d", len(jobs))
	}

	expectedJob := "periodic-ci-openshift-release-master-nightly-4.21-e2e-aws"
	found := false
	for _, job := range jobs {
		if job == expectedJob {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected to find job %s in informing jobs", expectedJob)
	}

	// Test non-existent release
	_, err = config.GetInformingJobsForRelease("4.99")
	if err == nil {
		t.Errorf("expected error for non-existent release, got nil")
	}
}

func TestBuildConfigFilename(t *testing.T) {
	testCases := []struct {
		name     string
		metadata *ProwJobMetadata
		expected string
	}{
		{
			name: "with variant",
			metadata: &ProwJobMetadata{
				Org:     "openshift",
				Repo:    "cluster-control-plane-machine-set-operator",
				Branch:  "release-4.21",
				Variant: "periodics",
			},
			expected: "openshift-cluster-control-plane-machine-set-operator-release-4.21__periodics.yaml",
		},
		{
			name: "without variant",
			metadata: &ProwJobMetadata{
				Org:     "openshift",
				Repo:    "installer",
				Branch:  "master",
				Variant: "",
			},
			expected: "openshift-installer-master.yaml",
		},
		{
			name: "with complex variant",
			metadata: &ProwJobMetadata{
				Org:     "openshift",
				Repo:    "release",
				Branch:  "master",
				Variant: "nightly-4.21",
			},
			expected: "openshift-release-master__nightly-4.21.yaml",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := buildConfigFilename(tc.metadata)
			if result != tc.expected {
				t.Errorf("expected %s, got %s", tc.expected, result)
			}
		})
	}
}

func TestFindRelatedReleases(t *testing.T) {
	config := &SippyConfig{
		Releases: map[string]SippyRelease{
			"4.21":     {InformingJobs: []string{"job1"}},
			"4.21-okd": {InformingJobs: []string{"job2"}},
			"4.20":     {InformingJobs: []string{"job3"}},
			"4.22":     {InformingJobs: []string{"job4"}},
			"4.21-foo": {InformingJobs: []string{"job5"}},
		},
	}

	testCases := []struct {
		name            string
		version         string
		expectedCount   int
		expectedInclude []string
		expectedExclude []string
	}{
		{
			name:            "finds exact match and related",
			version:         "4.21",
			expectedCount:   3,
			expectedInclude: []string{"4.21", "4.21-okd", "4.21-foo"},
			expectedExclude: []string{"4.20", "4.22"},
		},
		{
			name:            "finds only exact match when no related",
			version:         "4.20",
			expectedCount:   1,
			expectedInclude: []string{"4.20"},
			expectedExclude: []string{"4.21", "4.22"},
		},
		{
			name:            "finds specific variant",
			version:         "4.21-okd",
			expectedCount:   1,
			expectedInclude: []string{"4.21-okd"},
			expectedExclude: []string{"4.21", "4.21-foo"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := findRelatedReleases(config, tc.version)

			if len(result) != tc.expectedCount {
				t.Errorf("expected %d releases, got %d: %v", tc.expectedCount, len(result), result)
			}

			for _, expected := range tc.expectedInclude {
				found := false
				for _, r := range result {
					if r == expected {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected to find %s in results %v", expected, result)
				}
			}

			for _, notExpected := range tc.expectedExclude {
				for _, r := range result {
					if r == notExpected {
						t.Errorf("did not expect to find %s in results %v", notExpected, result)
					}
				}
			}
		})
	}
}
