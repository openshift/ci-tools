package main

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestLoadConfig(t *testing.T) {
	var testCases = []struct {
		name       string
		expected   error
		configPath string
	}{
		{
			name:       "Valid config file",
			configPath: "testdata/ok-case.yaml",
		},
		{
			name:       "Config file with invalid formatting",
			configPath: "testdata/invalid-formatting.yaml",
			expected:   fmt.Errorf("failed to unmarshall file testdata/invalid-formatting.yaml: error converting YAML to JSON: yaml: line 2: mapping values are not allowed in this context"),
		},
		{
			name:       "Duplicated profile in config file",
			configPath: "testdata/duplicated-profile.yaml",
			expected:   fmt.Errorf("cluster profile 'aws' already exists in the configuration file"),
		},
	}

	validator := newValidator(fakectrlruntimeclient.NewFakeClient())

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validator.loadConfig(tc.configPath)
			if diff := cmp.Diff(tc.expected, err, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("error differs from expected:\n%v", diff)
			}
		})
	}
}
