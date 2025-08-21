package main

import (
	"io/ioutil"
	"os"
	"testing"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/api/ocplifecycle"
	"github.com/openshift/ci-tools/pkg/config"
)

func TestExtractVersion(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "release branch",
			input:    "release-4.15",
			expected: "4.15",
		},
		{
			name:     "openshift branch",
			input:    "openshift-4.14",
			expected: "4.14",
		},
		{
			name:     "invalid format",
			input:    "main",
			expected: "",
		},
		{
			name:     "wrong prefix",
			input:    "feature-4.15",
			expected: "",
		},
		{
			name:     "no hyphen",
			input:    "release4.15",
			expected: "",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := extractVersion(tc.input)
			if result != tc.expected {
				t.Errorf("extractVersion(%q) = %q, want %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestIsExecutedAtMostOncePerYear(t *testing.T) {
	testCases := []struct {
		name        string
		cronExpr    string
		expected    bool
		expectError bool
	}{
		{
			name:        "yearly",
			cronExpr:    "0 0 1 1 *",
			expected:    true,
			expectError: false,
		},
		{
			name:        "monthly",
			cronExpr:    "0 0 1 * *",
			expected:    false,
			expectError: false,
		},
		{
			name:        "daily",
			cronExpr:    "0 0 * * *",
			expected:    false,
			expectError: false,
		},
		{
			name:        "weekly",
			cronExpr:    "0 0 * * 0",
			expected:    false,
			expectError: false,
		},
		{
			name:        "@yearly",
			cronExpr:    "@yearly",
			expected:    true,
			expectError: false,
		},
		{
			name:        "@annually",
			cronExpr:    "@annually",
			expected:    true,
			expectError: false,
		},
		{
			name:        "@monthly",
			cronExpr:    "@monthly",
			expected:    false,
			expectError: false,
		},
		{
			name:        "@weekly",
			cronExpr:    "@weekly",
			expected:    false,
			expectError: false,
		},
		{
			name:        "@daily",
			cronExpr:    "@daily",
			expected:    false,
			expectError: false,
		},
		{
			name:        "custom yearly march",
			cronExpr:    "30 14 15 3 *",
			expected:    true,
			expectError: false,
		},
		{
			name:        "custom yearly december",
			cronExpr:    "0 0 25 12 *",
			expected:    true,
			expectError: false,
		},
		{
			name:        "invalid",
			cronExpr:    "invalid",
			expected:    false,
			expectError: true,
		},
		{
			name:        "too many fields",
			cronExpr:    "0 0 1 1 * 2024",
			expected:    false,
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := isExecutedAtMostOncePerYear(tc.cronExpr)

			if tc.expectError {
				if err == nil {
					t.Errorf("isExecutedAtMostOncePerYear(%q) expected error, got nil", tc.cronExpr)
				}
				return
			}

			if err != nil {
				t.Errorf("isExecutedAtMostOncePerYear(%q) unexpected error: %v", tc.cronExpr, err)
				return
			}

			if result != tc.expected {
				t.Errorf("isExecutedAtMostOncePerYear(%q) = %v, want %v", tc.cronExpr, result, tc.expected)
			}
		})
	}
}

func TestIsExecutedAtMostXTimesAMonth(t *testing.T) {
	testCases := []struct {
		name        string
		cronExpr    string
		maxTimes    int
		expected    bool
		expectError bool
	}{
		{
			name:        "daily limit 4",
			cronExpr:    "0 0 * * *",
			maxTimes:    4,
			expected:    false,
			expectError: false,
		},
		{
			name:        "weekly limit 4",
			cronExpr:    "0 0 * * 0",
			maxTimes:    4,
			expected:    true,
			expectError: false,
		},
		{
			name:        "monthly limit 1",
			cronExpr:    "0 0 1 * *",
			maxTimes:    1,
			expected:    true,
			expectError: false,
		},
		{
			name:        "monthly limit 0",
			cronExpr:    "0 0 1 * *",
			maxTimes:    0,
			expected:    false,
			expectError: false,
		},
		{
			name:        "bi-weekly limit 2",
			cronExpr:    "0 0 1,15 * *",
			maxTimes:    2,
			expected:    true,
			expectError: false,
		},
		{
			name:        "bi-weekly limit 1",
			cronExpr:    "0 0 1,15 * *",
			maxTimes:    1,
			expected:    false,
			expectError: false,
		},
		{
			name:        "@weekly limit 4",
			cronExpr:    "@weekly",
			maxTimes:    4,
			expected:    true,
			expectError: false,
		},
		{
			name:        "@daily limit 31",
			cronExpr:    "@daily",
			maxTimes:    31,
			expected:    true,
			expectError: false,
		},
		{
			name:        "@monthly limit 1",
			cronExpr:    "@monthly",
			maxTimes:    1,
			expected:    true,
			expectError: false,
		},
		{
			name:        "invalid",
			cronExpr:    "invalid",
			maxTimes:    1,
			expected:    false,
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := isExecutedAtMostXTimesAMonth(tc.cronExpr, tc.maxTimes)

			if tc.expectError {
				if err == nil {
					t.Errorf("isExecutedAtMostXTimesAMonth(%q, %d) expected error, got nil", tc.cronExpr, tc.maxTimes)
				}
				return
			}

			if err != nil {
				t.Errorf("isExecutedAtMostXTimesAMonth(%q, %d) unexpected error: %v", tc.cronExpr, tc.maxTimes, err)
				return
			}

			if result != tc.expected {
				t.Errorf("isExecutedAtMostXTimesAMonth(%q, %d) = %v, want %v", tc.cronExpr, tc.maxTimes, result, tc.expected)
			}
		})
	}
}

func TestGenerateCronFunctions(t *testing.T) {
	t.Run("generateYearlyCron", func(t *testing.T) {
		for i := 0; i < 10; i++ {
			cron := generateYearlyCron()
			isYearly, err := isExecutedAtMostOncePerYear(cron)
			if err != nil {
				t.Errorf("generateYearlyCron() produced invalid cron: %q, error: %v", cron, err)
			}
			if !isYearly {
				t.Errorf("generateYearlyCron() produced non-yearly cron: %q", cron)
			}
		}
	})

	t.Run("generateMonthlyCron", func(t *testing.T) {
		for i := 0; i < 10; i++ {
			cron := generateMonthlyCron()
			isMonthly, err := isExecutedAtMostXTimesAMonth(cron, 1)
			if err != nil {
				t.Errorf("generateMonthlyCron() produced invalid cron: %q, error: %v", cron, err)
			}
			if !isMonthly {
				t.Errorf("generateMonthlyCron() produced non-monthly cron: %q", cron)
			}
		}
	})

	t.Run("generateBiWeeklyCron", func(t *testing.T) {
		for i := 0; i < 10; i++ {
			cron := generateBiWeeklyCron()
			isBiWeekly, err := isExecutedAtMostXTimesAMonth(cron, 2)
			if err != nil {
				t.Errorf("generateBiWeeklyCron() produced invalid cron: %q, error: %v", cron, err)
			}
			if !isBiWeekly {
				t.Errorf("generateBiWeeklyCron() produced non-bi-weekly cron: %q", cron)
			}
		}
	})

	t.Run("generateWeeklyWeekendCron", func(t *testing.T) {
		for i := 0; i < 10; i++ {
			cron := generateWeeklyWeekendCron()
			isWeekly, err := isExecutedAtMostXTimesAMonth(cron, 5)
			if err != nil {
				t.Errorf("generateWeeklyWeekendCron() produced invalid cron: %q, error: %v", cron, err)
			}
			if !isWeekly {
				t.Errorf("generateWeeklyWeekendCron() produced invalid weekly cron: %q", cron)
			}
		}
	})
}

func TestUpdateIntervalFieldsForMatchedSteps(t *testing.T) {
	currentVersion := ocplifecycle.MajorMinor{Major: 4, Minor: 17}

	testCases := []struct {
		name                 string
		testVersion          string
		org                  string
		testName             string
		initialCron          *string
		initialInterval      *string
		expectCronChange     bool
		expectIntervalChange bool
		expectYearlyCron     bool
	}{
		{
			name:                 "n-3 daily to yearly",
			testVersion:          "4.14",
			org:                  "openshift",
			testName:             "e2e-test",
			initialCron:          stringPtr("0 0 * * *"),
			initialInterval:      nil,
			expectCronChange:     true,
			expectIntervalChange: false,
			expectYearlyCron:     true,
		},
		{
			name:                 "n-3 yearly unchanged",
			testVersion:          "4.14",
			org:                  "openshift",
			testName:             "e2e-test",
			initialCron:          stringPtr("0 0 1 6 *"),
			initialInterval:      nil,
			expectCronChange:     false,
			expectIntervalChange: false,
			expectYearlyCron:     false,
		},
		{
			name:                 "n-2 daily to bi-weekly",
			testVersion:          "4.15",
			org:                  "openshift",
			testName:             "e2e-test",
			initialCron:          stringPtr("0 0 * * *"),
			initialInterval:      nil,
			expectCronChange:     true,
			expectIntervalChange: false,
			expectYearlyCron:     false,
		},
		{
			name:                 "n-1 daily to weekly",
			testVersion:          "4.16",
			org:                  "openshift",
			testName:             "e2e-test",
			initialCron:          stringPtr("0 0 * * *"),
			initialInterval:      nil,
			expectCronChange:     true,
			expectIntervalChange: false,
			expectYearlyCron:     false,
		},
		{
			name:                 "current unchanged",
			testVersion:          "4.17",
			org:                  "openshift",
			testName:             "e2e-test",
			initialCron:          stringPtr("0 0 * * *"),
			initialInterval:      nil,
			expectCronChange:     false,
			expectIntervalChange: false,
			expectYearlyCron:     false,
		},
		{
			name:                 "non-openshift unchanged",
			testVersion:          "4.14",
			org:                  "other-org",
			testName:             "e2e-test",
			initialCron:          stringPtr("0 0 * * *"),
			initialInterval:      nil,
			expectCronChange:     false,
			expectIntervalChange: false,
			expectYearlyCron:     false,
		},
		{
			name:                 "openshift-priv org",
			testVersion:          "4.14",
			org:                  "openshift-priv",
			testName:             "e2e-test",
			initialCron:          stringPtr("0 0 * * *"),
			initialInterval:      nil,
			expectCronChange:     true,
			expectIntervalChange: false,
			expectYearlyCron:     true,
		},
		{
			name:                 "mirror test unchanged",
			testVersion:          "4.14",
			org:                  "openshift",
			testName:             "mirror-nightly-image-test",
			initialCron:          stringPtr("0 0 * * *"),
			initialInterval:      nil,
			expectCronChange:     false,
			expectIntervalChange: false,
			expectYearlyCron:     false,
		},
		{
			name:                 "promote test unchanged",
			testVersion:          "4.14",
			org:                  "openshift",
			testName:             "promote-test",
			initialCron:          stringPtr("0 0 * * *"),
			initialInterval:      nil,
			expectCronChange:     false,
			expectIntervalChange: false,
			expectYearlyCron:     false,
		},
		{
			name:                 "n-3 interval to yearly",
			testVersion:          "4.14",
			org:                  "openshift",
			testName:             "e2e-test",
			initialCron:          nil,
			initialInterval:      stringPtr("24h"),
			expectCronChange:     true,
			expectIntervalChange: true,
			expectYearlyCron:     true,
		},
		{
			name:                 "n-3 long interval unchanged",
			testVersion:          "4.14",
			org:                  "openshift",
			testName:             "e2e-test",
			initialCron:          nil,
			initialInterval:      stringPtr("8760h"),
			expectCronChange:     false,
			expectIntervalChange: false,
			expectYearlyCron:     false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := &config.DataWithInfo{
				Configuration: api.ReleaseBuildConfiguration{
					Tests: []api.TestStepConfiguration{
						{
							As:       tc.testName,
							Cron:     tc.initialCron,
							Interval: tc.initialInterval,
						},
					},
				},
				Info: config.Info{
					Metadata: api.Metadata{
						Org:    tc.org,
						Branch: "release-" + tc.testVersion,
					},
				},
			}

			var originalCronValue string
			if config.Configuration.Tests[0].Cron != nil {
				originalCronValue = *config.Configuration.Tests[0].Cron
			}
			var originalIntervalValue string
			if config.Configuration.Tests[0].Interval != nil {
				originalIntervalValue = *config.Configuration.Tests[0].Interval
			}

			updateIntervalFieldsForMatchedSteps(config, currentVersion, nil) // nil = no cluster profile filtering

			if tc.expectCronChange {
				var currentCronValue string
				if config.Configuration.Tests[0].Cron != nil {
					currentCronValue = *config.Configuration.Tests[0].Cron
				}
				if currentCronValue == originalCronValue {
					t.Errorf("Expected cron to change, but it remained: %v", originalCronValue)
				}
				if tc.expectYearlyCron && config.Configuration.Tests[0].Cron != nil {
					isYearly, err := isExecutedAtMostOncePerYear(*config.Configuration.Tests[0].Cron)
					if err != nil {
						t.Errorf("Generated cron is invalid: %v", err)
					}
					if !isYearly {
						t.Errorf("Expected yearly cron, got: %s", *config.Configuration.Tests[0].Cron)
					}
				}
			} else {
				var currentCronValue string
				if config.Configuration.Tests[0].Cron != nil {
					currentCronValue = *config.Configuration.Tests[0].Cron
				}
				if currentCronValue != originalCronValue {
					t.Errorf("Expected cron to remain unchanged, but it changed from %v to %v", originalCronValue, currentCronValue)
				}
			}

			if tc.expectIntervalChange {
				var currentIntervalValue string
				if config.Configuration.Tests[0].Interval != nil {
					currentIntervalValue = *config.Configuration.Tests[0].Interval
				}
				if currentIntervalValue == originalIntervalValue {
					t.Errorf("Expected interval to change, but it remained: %v", originalIntervalValue)
				}
				if config.Configuration.Tests[0].Interval != nil {
					t.Errorf("Expected interval to be nil after conversion, but got: %v", config.Configuration.Tests[0].Interval)
				}
				if config.Configuration.Tests[0].Cron == nil {
					t.Errorf("Expected cron to be set after interval conversion, but it's nil")
				}
			} else if originalIntervalValue != "" {
				var currentIntervalValue string
				if config.Configuration.Tests[0].Interval != nil {
					currentIntervalValue = *config.Configuration.Tests[0].Interval
				}
				if currentIntervalValue != originalIntervalValue {
					t.Errorf("Expected interval to remain unchanged, but it changed from %v to %v", originalIntervalValue, currentIntervalValue)
				}
			}
		})
	}
}

func TestOptionsValidation(t *testing.T) {
	testCases := []struct {
		name        string
		options     options
		expectError bool
	}{
		{
			name: "valid options",
			options: options{
				ConfirmableOptions: config.ConfirmableOptions{
					Options: config.Options{
						ConfigDir: "/tmp/test",
						LogLevel:  "info",
					},
				},
				maxThreads: 4,
			},
			expectError: false,
		},
		{
			name: "zero threads",
			options: options{
				ConfirmableOptions: config.ConfirmableOptions{
					Options: config.Options{
						ConfigDir: "/tmp/test",
						LogLevel:  "info",
					},
				},
				maxThreads: 0,
			},
			expectError: true,
		},
		{
			name: "negative threads",
			options: options{
				ConfirmableOptions: config.ConfirmableOptions{
					Options: config.Options{
						ConfigDir: "/tmp/test",
						LogLevel:  "info",
					},
				},
				maxThreads: -1,
			},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.options.validate()

			if tc.expectError && err == nil {
				t.Errorf("Expected validation error, but got none")
			}

			if !tc.expectError && err != nil {
				t.Errorf("Unexpected validation error: %v", err)
			}
		})
	}
}

func TestGatherOptions(t *testing.T) {
	originalArgs := os.Args
	defer func() { os.Args = originalArgs }()

	os.Args = []string{"frequency-reducer", "-max-threads", "8", "-current-release", "4.17"}

	opts := gatherOptions()

	if opts.maxThreads != 8 {
		t.Errorf("Expected maxThreads to be 8, got %d", opts.maxThreads)
	}

	if opts.currentOCPVersion != "4.17" {
		t.Errorf("Expected currentOCPVersion to be '4.17', got %q", opts.currentOCPVersion)
	}
}

func TestShouldProcessTest(t *testing.T) {
	tests := []struct {
		name                   string
		testConfig             *api.TestStepConfiguration
		allowedClusterProfiles map[string]bool
		expected               bool
	}{
		{
			name: "test with allowed cluster profile",
			testConfig: &api.TestStepConfiguration{
				As: "test-with-aws",
				MultiStageTestConfiguration: &api.MultiStageTestConfiguration{
					ClusterProfile: api.ClusterProfileAWS,
				},
			},
			allowedClusterProfiles: map[string]bool{
				"aws": true,
				"gcp": true,
			},
			expected: true,
		},
		{
			name: "test with disallowed cluster profile",
			testConfig: &api.TestStepConfiguration{
				As: "test-with-azure",
				MultiStageTestConfiguration: &api.MultiStageTestConfiguration{
					ClusterProfile: api.ClusterProfileAzure4,
				},
			},
			allowedClusterProfiles: map[string]bool{
				"aws": true,
				"gcp": true,
			},
			expected: false,
		},
		{
			name: "test without cluster profile should be processed",
			testConfig: &api.TestStepConfiguration{
				As: "test-without-cluster-profile",
			},
			allowedClusterProfiles: map[string]bool{
				"aws": true,
			},
			expected: true,
		},
		{
			name: "test without cluster profile with nil filter",
			testConfig: &api.TestStepConfiguration{
				As: "test-without-cluster-profile",
			},
			allowedClusterProfiles: nil,
			expected:               true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := shouldProcessTest(tc.testConfig, tc.allowedClusterProfiles)
			if result != tc.expected {
				t.Errorf("Expected shouldProcessTest to return %v, got %v", tc.expected, result)
			}
		})
	}
}

func TestLoadClusterProfilesConfig(t *testing.T) {
	tests := []struct {
		name        string
		fileContent string
		expectError bool
		expected    map[string]bool
	}{
		{
			name: "valid config file",
			fileContent: `cluster_profiles:
  - aws
  - gcp
  - azure4`,
			expectError: false,
			expected: map[string]bool{
				"aws":    true,
				"gcp":    true,
				"azure4": true,
			},
		},
		{
			name:        "empty cluster profiles",
			fileContent: `cluster_profiles: []`,
			expectError: true,
			expected:    nil,
		},
		{
			name: "invalid YAML",
			fileContent: `cluster_profiles:
  - aws
  - gcp
invalid: yaml: content`,
			expectError: true,
			expected:    nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Create a temporary file
			tmpFile, err := ioutil.TempFile("", "cluster-profiles-*.yaml")
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer os.Remove(tmpFile.Name())

			// Write content to file
			if _, err := tmpFile.WriteString(tc.fileContent); err != nil {
				t.Fatalf("Failed to write to temp file: %v", err)
			}
			tmpFile.Close()

			// Test the function
			result, err := loadClusterProfilesConfig(tmpFile.Name())

			if tc.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if len(result) != len(tc.expected) {
				t.Errorf("Expected %d cluster profiles, got %d", len(tc.expected), len(result))
				return
			}

			for profile := range tc.expected {
				if !result[profile] {
					t.Errorf("Expected cluster profile %s to be allowed", profile)
				}
			}
		})
	}
}

func TestUpdateIntervalFieldsWithClusterProfileFiltering(t *testing.T) {
	currentVersion := ocplifecycle.MajorMinor{Major: 4, Minor: 17}

	tests := []struct {
		name                   string
		testConfig             *api.TestStepConfiguration
		allowedClusterProfiles map[string]bool
		expectChange           bool
	}{
		{
			name: "n-3 test with allowed cluster profile should be modified",
			testConfig: &api.TestStepConfiguration{
				As:   "test-allowed",
				Cron: stringPtr("0 0 * * *"), // daily cron for n-3 should change to yearly
				MultiStageTestConfiguration: &api.MultiStageTestConfiguration{
					ClusterProfile: api.ClusterProfileAWS,
				},
			},
			allowedClusterProfiles: map[string]bool{
				"aws": true,
			},
			expectChange: true,
		},
		{
			name: "n-3 test with disallowed cluster profile should not be modified",
			testConfig: &api.TestStepConfiguration{
				As:   "test-disallowed",
				Cron: stringPtr("0 0 * * *"), // daily cron for n-3 should NOT change
				MultiStageTestConfiguration: &api.MultiStageTestConfiguration{
					ClusterProfile: api.ClusterProfileGCP,
				},
			},
			allowedClusterProfiles: map[string]bool{
				"aws": true,
			},
			expectChange: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			config := &config.DataWithInfo{
				Configuration: api.ReleaseBuildConfiguration{
					Tests: []api.TestStepConfiguration{*tc.testConfig},
				},
				Info: config.Info{
					Metadata: api.Metadata{
						Org:    "openshift",
						Repo:   "test-repo",
						Branch: "release-4.14", // n-3 for 4.17
					},
				},
			}

			originalCron := *config.Configuration.Tests[0].Cron
			updateIntervalFieldsForMatchedSteps(config, currentVersion, tc.allowedClusterProfiles)

			cronChanged := *config.Configuration.Tests[0].Cron != originalCron

			if tc.expectChange && !cronChanged {
				t.Errorf("Expected cron to change for %s, but it remained: %s", tc.name, *config.Configuration.Tests[0].Cron)
			} else if !tc.expectChange && cronChanged {
				t.Errorf("Expected cron NOT to change for %s, but it changed from %s to %s", tc.name, originalCron, *config.Configuration.Tests[0].Cron)
			}
		})
	}
}

func stringPtr(s string) *string {
	return &s
}
