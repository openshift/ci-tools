package util

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"k8s.io/client-go/rest"
)

func TestLoadKubeConfigs(t *testing.T) {
	var testCases = []struct {
		name             string
		kubeconfig       string
		kubeconfigDir    string
		kubeconfigEnvVar string
		expectedConfigs  map[string]*rest.Config
		expectedContext  string
		expectedError    error
	}{
		{
			name:          "file not exist",
			kubeconfig:    "/tmp/file-not-exists",
			expectedError: fmt.Errorf(`failed to load cluster configs: fail to load kubecfg from "/tmp/file-not-exists": failed to load: stat /tmp/file-not-exists: no such file or directory`),
		},
		{
			name:       "load from kubeconfig",
			kubeconfig: filepath.Join(filepath.Join("testdata", "load_from_kubeconfig"), "kubeconfig"),
			expectedConfigs: map[string]*rest.Config{
				"ci/api-build01-ci-devcluster-openshift-com:6443": {
					Host:        "https://api.build01.ci.devcluster.openshift.com:6443",
					BearerToken: "TOKEN",
					TLSClientConfig: rest.TLSClientConfig{
						Insecure: true,
					},
				},
			},
			expectedContext: "ci/api-build01-ci-devcluster-openshift-com:6443",
		},
		{
			name:             "env kubeconfig that does not exist",
			kubeconfigEnvVar: "/tmp/does-not-exist",
			expectedError:    fmt.Errorf("KUBECONFIG env var with value /tmp/does-not-exist had 1 elements but only got 0 kubeconfigs"),
		},
		{
			name:             "env kubeconfig exists",
			kubeconfigEnvVar: filepath.Join(filepath.Join("testdata", "load_from_kubeconfig"), "kubeconfig"),
			expectedConfigs: map[string]*rest.Config{
				"ci/api-build01-ci-devcluster-openshift-com:6443": {
					Host:        "https://api.build01.ci.devcluster.openshift.com:6443",
					BearerToken: "TOKEN",
					TLSClientConfig: rest.TLSClientConfig{
						Insecure: true,
					},
				},
			},
			expectedContext: "ci/api-build01-ci-devcluster-openshift-com:6443",
		},
		{
			name:          "load from kubeconfigDir",
			kubeconfigDir: filepath.Join("testdata", "load_from_kubeconfigDir"),
			expectedConfigs: map[string]*rest.Config{
				"app.ci": {
					Host:        "https://api.ci.l2s4.p1.openshiftapps.com:6443",
					BearerToken: "REDACTED",
				},
				"build01": {
					Host:        "https://api.build01.ci.devcluster.openshift.com:6443",
					BearerToken: "REDACTED",
				},
				"build02": {
					Host:        "https://api.build02.gcp.ci.openshift.org:6443",
					BearerToken: "REDACTED",
				},
				"hive": {
					Host:        "https://api.hive.9xw5.p1.openshiftapps.com:6443",
					BearerToken: "REDACTED",
				},
			},
			expectedContext: "ci/api-build01-ci-devcluster-openshift-com:6443",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			os.Setenv("KUBECONFIG", tc.kubeconfigEnvVar)
			configs, err := LoadKubeConfigs(tc.kubeconfig, tc.kubeconfigDir, nil)
			if err == nil && tc.expectedError != nil || err != nil && tc.expectedError == nil {
				t.Errorf("actual error differs from expected:\n%s", cmp.Diff(err, tc.expectedError))
			} else if err != nil && tc.expectedError != nil && !reflect.DeepEqual(err.Error(), tc.expectedError.Error()) {
				t.Errorf("actual error differs from expected:\n%s", cmp.Diff(err.Error(), tc.expectedError.Error()))
			}
			if tc.expectedError != nil {
				return
			}
			if diff := cmp.Diff(tc.expectedConfigs, configs, cmpopts.IgnoreFields(rest.Config{}, "UserAgent")); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
		})
	}
}
