package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestManageCLI(t *testing.T) {
	testCases := []struct {
		testName string
		cliDir   string
		expected string
	}{
		{
			testName: "CLI Directory Set",
			cliDir:   "/usr/local/bin/oc",
			expected: os.Getenv("PATH") + ":/usr/local/bin/oc",
		},
		{
			testName: "CLI Directory Not Set",
			expected: os.Getenv("PATH"),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.testName, func(t *testing.T) {
			originalPath := os.Getenv("PATH")

			defer os.Setenv("PATH", originalPath)

			cmd := exec.Command("echo")
			cmd.Env = os.Environ()

			if testCase.cliDir != "" {
				os.Setenv(api.CliEnv, testCase.cliDir)
				defer os.Unsetenv(api.CliEnv)
			}

			manageCLI(cmd)

			actualPath := ""
			for _, env := range cmd.Env {
				if strings.HasPrefix(env, "PATH=") {
					actualPath = strings.TrimPrefix(env, "PATH=")
				}
			}

			if actualPath != testCase.expected {
				t.Fatalf("expected PATH to be %q, got %q", testCase.expected, actualPath)
			}
		})
	}
}

const NonWritableMode os.FileMode = 0555

func TestManageHome(t *testing.T) {
	fileModePtr := func(fm os.FileMode) *os.FileMode { return &fm }

	testCases := []struct {
		testName    string
		homeSet     bool
		expectedEnv []string
		fileMode    *os.FileMode
	}{
		{
			testName: "Home Set",
			homeSet:  true,
		},
		{
			testName:    "Home Not Set",
			homeSet:     false,
			expectedEnv: []string{"HOME=/alabama"},
		},
		{
			testName:    "Home Set Not Writable",
			homeSet:     true,
			expectedEnv: []string{"HOME=/alabama"},
			fileMode:    fileModePtr(NonWritableMode),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.testName, func(t *testing.T) {
			cmd := exec.Command("fake")
			dir := t.TempDir()

			expectedHome := dir
			if len(testCase.expectedEnv) > 0 {
				for _, env := range testCase.expectedEnv {
					if strings.HasPrefix(env, "HOME=") {
						expectedHome = strings.TrimPrefix(env, "HOME=")
					}
				}
			}

			if testCase.homeSet {
				os.Setenv("HOME", dir)
			}

			if testCase.fileMode != nil {
				if err := os.Chmod(dir, *testCase.fileMode); err != nil {
					t.Fatalf("Failed to set permission %d on %s: %v", *testCase.fileMode, dir, err)
				}
			}

			home := manageHome(cmd)

			if home != expectedHome {
				t.Fatalf("expected home %q but got %q\n", expectedHome, home)
			}

			if diff := cmp.Diff(cmd.Env, testCase.expectedEnv); diff != "" {
				t.Fatalf("unexpected env: %q\n", diff)
			}
		})
	}
}
