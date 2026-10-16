package main

import (
	"os"
	"os/exec"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestRunSteps(t *testing.T) {
	testCases := []struct {
		name     string
		commands [][]string

		expectNeedsPushing bool
	}{
		{
			name:     "Command changes something",
			commands: [][]string{{"bash", "-c", "echo change >file"}},

			expectNeedsPushing: true,
		},
		{
			name:     "Command doesn't change anything",
			commands: [][]string{{"true", "true"}},

			expectNeedsPushing: false,
		},
		{
			name:               "First command changes, second reverts, no push",
			commands:           [][]string{{"bash", "-c", "echo change >file"}, {"git", "revert", "HEAD"}},
			expectNeedsPushing: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			wd, err := os.Getwd()
			if err != nil {
				t.Fatalf("failed to get current workdir: %v", err)
			}
			t.Cleanup(func() {
				if err := os.Chdir(wd); err != nil {
					t.Errorf("failed to chdir back to original working dir: %v", err)
				}
			})

			tempDir := t.TempDir()
			if err := os.Chdir(tempDir); err != nil {
				t.Fatalf("failed to chdir into tempdir: %v", err)
			}
			for _, cmd := range [][]string{
				{"init"},
				{"config", "user.name", "test"},
				{"config", "user.email", "test@example.com"},
				{"commit", "--allow-empty", "--message", "init"},
			} {
				if out, err := exec.Command("git", cmd...).CombinedOutput(); err != nil {
					t.Fatalf("git command %q failed: %v, out: %s", cmd, err, string(out))
				}
			}
			var steps []step
			for _, command := range tc.commands {
				steps = append(steps, step{command: command[0], arguments: command[1:]})
			}

			needsPushing, err := runSteps(steps, "tests <test@test.com>", os.Stdout, os.Stderr)
			if err != nil {
				t.Fatalf("runSteps failed: %v", err)
			}
			if needsPushing != tc.expectNeedsPushing {
				t.Errorf("expectNeedsPushing: %t, needsPushing: %t", tc.expectNeedsPushing, needsPushing)
			}
		})
	}
}

func TestConfigBrancherArgs(t *testing.T) {
	testCases := []struct {
		name     string
		releases []string
		want     []string
	}{
		{name: "one release", releases: []string{"4.22"}, want: []string{"--config-dir", "./ci-operator/config", "--current-release", "4.21", "--skip-periodics", "--pipeline-controller-config", "./core-services/pipeline-controller/config.yaml", "--future-release", "4.22", "--confirm"}},
		{name: "two releases", releases: []string{"4.21", "4.22"}, want: []string{"--config-dir", "./ci-operator/config", "--current-release", "4.21", "--skip-periodics", "--pipeline-controller-config", "./core-services/pipeline-controller/config.yaml", "--future-release", "4.21", "--future-release", "4.22", "--confirm"}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var opts options
			opts.CurrentRelease = "4.21"
			for _, release := range testCase.releases {
				if err := opts.FutureReleases.Set(release); err != nil {
					t.Fatalf("setting release: %v", err)
				}
			}
			if diff := cmp.Diff(testCase.want, configBrancherArgs(opts)); diff != "" {
				t.Errorf("arguments (-want +got):\n%s", diff)
			}
		})
	}
}
