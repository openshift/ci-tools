package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"k8s.io/test-infra/prow/plugins"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api/ocplifecycle"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestRun(t *testing.T) {
	files, err := os.ReadDir("./testdata")
	if err != nil {
		t.Fatalf("failed to list testdata dir files: %v", err)
	}
	for _, file := range files {
		if !strings.HasPrefix(file.Name(), "lifecycle_") {
			continue
		}
		t.Run(file.Name(), func(t *testing.T) {
			file := file
			t.Parallel()

			path := filepath.Join("testdata", file.Name())
			lifecycleConfigRaw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("failed to read %s: %v", path, err)
			}

			var lifecycleConfig ocplifecycle.Config
			if err := yaml.Unmarshal(lifecycleConfigRaw, &lifecycleConfig); err != nil {
				t.Fatalf("failed to unmarshal lifecycleConfig: %v", err)
			}

			newConfig, err := run(lifecycleConfig, plugins.Bugzilla{}, time.Now())
			if err != nil {
				t.Fatalf("failed to run: %v", err)
			}

			testhelper.CompareWithFixture(t, newConfig)
		})
	}
}
