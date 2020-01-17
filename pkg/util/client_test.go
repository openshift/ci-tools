package util

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"k8s.io/client-go/rest"
)

const (
	kubeconfigContent1 = `apiVersion: v1
clusters:
- cluster:
    insecure-skip-tls-verify: true
    server: https://api.build01.ci.devcluster.openshift.com:6443
  name: api-build01-ci-devcluster-openshift-com:6443
contexts:
- context:
    cluster: api-build01-ci-devcluster-openshift-com:6443
    namespace: ci
    user: system:serviceaccount:ci:hook/api-build01-ci-devcluster-openshift-com:6443
  name: ci/api-build01-ci-devcluster-openshift-com:6443
current-context: ci/api-build01-ci-devcluster-openshift-com:6443
kind: Config
preferences: {}
users:
- name: system:serviceaccount:ci:hook/api-build01-ci-devcluster-openshift-com:6443
  user:
    token: TOKEN`
)

func TestLoadKubeConfigs(t *testing.T) {
	dir, err := ioutil.TempDir("", "test")
	if err != nil {
		t.Errorf("Failed to create temp dir: '%v'", err)
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("Failed to delete temp dir '%s': '%v'", dir, err)
		}
	}()
	filename1 := filepath.Join(dir, "kubeconfig1")
	if err := ioutil.WriteFile(filename1, []byte(kubeconfigContent1), 0755); err != nil {
		t.Errorf("Failed to write file '%s': '%v'", filename1, err)
	}

	var testCases = []struct {
		name            string
		kubeconfig      string
		expectedConfigs map[string]rest.Config
		expectedContext string
		expectedError   error
	}{
		{
			name:          "file not exist",
			kubeconfig:    "/tmp/file-not-exists",
			expectedError: fmt.Errorf("stat /tmp/file-not-exists: no such file or directory"),
		},
		{
			name:       "file kubeconfig1",
			kubeconfig: filename1,
			expectedConfigs: map[string]rest.Config{
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
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			configs, context, err := LoadKubeConfigs(tc.kubeconfig)
			if !reflect.DeepEqual(configs, tc.expectedConfigs) {
				t.Errorf("actual configs differ from expected:\n%s", cmp.Diff(configs, tc.expectedConfigs))
			}
			if !reflect.DeepEqual(context, tc.expectedContext) {
				t.Errorf("actual context differs from expected:\n%s", cmp.Diff(context, tc.expectedContext))
			}
			if err == nil && tc.expectedError != nil || err != nil && tc.expectedError == nil {
				t.Errorf("actual error differs from expected:\n%s", cmp.Diff(err, tc.expectedError))
			} else if err != nil && tc.expectedError != nil && !reflect.DeepEqual(err.Error(), tc.expectedError.Error()) {
				t.Errorf("actual error differs from expected:\n%s", cmp.Diff(err.Error(), tc.expectedError.Error()))
			}
		})
	}
}
