package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/hashicorp/vault/api"

	coreapi "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	"github.com/openshift/ci-tools/pkg/api/secretbootstrap"
	"github.com/openshift/ci-tools/pkg/api/secretgenerator"
	"github.com/openshift/ci-tools/pkg/secrets"
	"github.com/openshift/ci-tools/pkg/testhelper"
	"github.com/openshift/ci-tools/pkg/vaultclient"
)

func TestValidateOptions(t *testing.T) {
	testCases := []struct {
		name     string
		given    options
		expected error
	}{
		{
			name: "empty config path",
			given: options{
				logLevel: "info",
				secrets: secrets.CLIOptions{
					VaultAddr:      "https://vault.test",
					VaultPrefix:    "prefix",
					VaultTokenFile: "/tmp/vault-token",
				},
			},
			expected: fmt.Errorf("--config is required"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := tc.given.validateOptions()
			equalError(t, tc.expected, actual)
		})
	}
}

const (
	configContent = `---
secret_configs:
- from:
    key-name-1:
      item: item-name-1
      field: field-name-1
    key-name-2:
      item: item-name-1
      field: field-name-2
    key-name-3:
      item: item-name-1
      field: field-name-3
    key-name-4:
      item: item-name-2
      field: field-name-1
    key-name-5:
      item: item-name-2
      field: field-name-2
    key-name-6:
      item: item-name-3
      field: field-name-1
    key-name-7:
      item: item-name-2
      field: field-name-2
  to:
    - cluster: default
      namespace: namespace-1
      name: prod-secret-1
    - cluster: build01
      namespace: namespace-2
      name: prod-secret-2
- from:
    .dockerconfigjson:
      item: quay.io
      field: pull-credentials
  to:
    - cluster: default
      namespace: ci
      name: ci-pull-credentials
      type: kubernetes.io/dockerconfigjson
`
	configContentWithTypo = `---
secret_configs:
- from:
    key-name-1:
      item: item-name-1
      field: field-name-1
    key-name-2:
      item: item-name-1
      field: field-name-2
    key-name-3:
      item: item-name-1
      field: attachment-name-1
    key-name-4:
      item: item-name-2
      field: field-name-1
    key-name-5:
      item: item-name-2
      field: attachment-name-1
    key-name-6:
      item: item-name-3
      field: attachment-name-2
  to:
    - cluster: default
      namespace: namespace-1
      name: prod-secret-1
    - cluster: bla
      namespace: namespace-2
      name: prod-secret-2
`
	configContentWithNonPasswordAttribute = `---
secret_configs:
- from:
    key-name-1:
      item: item-name-1
      field: field-name-1
    key-name-2:
      item: item-name-1
      field: not-password
  to:
    - cluster: default
      namespace: namespace-1
      name: prod-secret-1
    - cluster: build01
      namespace: namespace-2
      name: prod-secret-2
`

	configWithGroups = `
cluster_groups:
  group-a:
  - default
secret_configs:
- from:
    key-name-1:
      item: item-name-1
      field: field-name-1
  to:
  - cluster_groups:
    - group-a
    namespace: ns
    name: name
`
)

var (
	configDefault = rest.Config{
		Host:        "https://api.ci.openshift.org:443",
		BearerToken: "token1",
	}
	configBuild01 = rest.Config{
		Host:        "https://api.build01.ci.devcluster.openshift.com:6443",
		BearerToken: "token2",
	}

	defaultConfig = secretbootstrap.Config{
		Secrets: []secretbootstrap.SecretConfig{
			{
				From: map[string]secretbootstrap.ItemContext{
					"key-name-1": {
						Item:  "item-name-1",
						Field: "field-name-1",
					},
					"key-name-2": {
						Item:  "item-name-1",
						Field: "field-name-2",
					},
					"key-name-3": {
						Item:  "item-name-1",
						Field: "field-name-3",
					},
					"key-name-4": {
						Item:  "item-name-2",
						Field: "field-name-1",
					},
					"key-name-5": {
						Item:  "item-name-2",
						Field: "field-name-2",
					},
					"key-name-6": {
						Item:  "item-name-3",
						Field: "field-name-1",
					},
					"key-name-7": {
						Item:  "item-name-2",
						Field: "field-name-2",
					},
				},
				To: []secretbootstrap.SecretContext{
					{
						Cluster:   "default",
						Namespace: "namespace-1",
						Name:      "prod-secret-1",
					},
					{
						Cluster:   "build01",
						Namespace: "namespace-2",
						Name:      "prod-secret-2",
					},
				},
			},
			{
				From: map[string]secretbootstrap.ItemContext{
					".dockerconfigjson": {
						Item:  "quay.io",
						Field: "pull-credentials",
					},
				},
				To: []secretbootstrap.SecretContext{
					{
						Cluster:   "default",
						Namespace: "ci",
						Name:      "ci-pull-credentials",
						Type:      "kubernetes.io/dockerconfigjson",
					},
				},
			},
		},
	}
	defaultConfigWithoutDefaultCluster = secretbootstrap.Config{
		Secrets: []secretbootstrap.SecretConfig{
			{
				From: map[string]secretbootstrap.ItemContext{
					"key-name-1": {
						Item:  "item-name-1",
						Field: "field-name-1",
					},
					"key-name-2": {
						Item:  "item-name-1",
						Field: "field-name-2",
					},
					"key-name-3": {
						Item:  "item-name-1",
						Field: "field-name-3",
					},
					"key-name-4": {
						Item:  "item-name-2",
						Field: "field-name-1",
					},
					"key-name-5": {
						Item:  "item-name-2",
						Field: "field-name-2",
					},
					"key-name-6": {
						Item:  "item-name-3",
						Field: "field-name-1",
					},
					"key-name-7": {
						Item:  "item-name-2",
						Field: "field-name-2",
					},
				},
				To: []secretbootstrap.SecretContext{
					{
						Cluster:   "build01",
						Namespace: "namespace-2",
						Name:      "prod-secret-2",
					},
				},
			},
		},
	}
)

func TestCompleteOptions(t *testing.T) {
	dir, err := ioutil.TempDir("", "test")
	if err != nil {
		t.Errorf("Failed to create temp dir")
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("Failed to remove temp dir")
		}
	}()

	bwPasswordPath := filepath.Join(dir, "bwPasswordPath")
	configPath := filepath.Join(dir, "configPath")
	configWithTypoPath := filepath.Join(dir, "configWithTypoPath")
	configWithGroupsPath := filepath.Join(dir, "configWithGroups")
	configWithNonPasswordAttributePath := filepath.Join(dir, "configContentWithNonPasswordAttribute")

	fileMap := map[string][]byte{
		bwPasswordPath:                     []byte("topSecret"),
		configPath:                         []byte(configContent),
		configWithTypoPath:                 []byte(configContentWithTypo),
		configWithGroupsPath:               []byte(configWithGroups),
		configWithNonPasswordAttributePath: []byte(configContentWithNonPasswordAttribute),
	}

	for k, v := range fileMap {
		if err := ioutil.WriteFile(k, v, 0755); err != nil {
			t.Errorf("Failed to remove temp dir")
		}
	}

	kubeconfigs := map[string]rest.Config{
		"default": {},
		"build01": {},
	}

	testCases := []struct {
		name             string
		given            options
		expectedError    error
		expectedConfig   secretbootstrap.Config
		expectedClusters []string
	}{
		{
			name: "basic case",
			given: options{
				logLevel:   "info",
				configPath: configPath,
			},
			expectedConfig:   defaultConfig,
			expectedClusters: []string{"build01", "default"},
		},
		{
			name: "missing context in kubeconfig",
			given: options{
				logLevel:   "info",
				configPath: configWithTypoPath,
			},
			expectedConfig: defaultConfig,
			expectedError:  fmt.Errorf("config[0].to[1]: failed to find cluster context \"bla\" in the kubeconfig"),
		},
		{
			name: "only configured cluster is used",
			given: options{
				logLevel:   "info",
				configPath: configPath,
				cluster:    "build01",
			},
			expectedConfig:   defaultConfigWithoutDefaultCluster,
			expectedClusters: []string{"build01"},
		},
		{
			name: "group is resolved",
			given: options{
				logLevel:   "info",
				configPath: configWithGroupsPath,
			},
			expectedConfig: secretbootstrap.Config{
				ClusterGroups: map[string][]string{"group-a": {"default"}},
				Secrets: []secretbootstrap.SecretConfig{{
					From: map[string]secretbootstrap.ItemContext{"key-name-1": {Item: "item-name-1", Field: "field-name-1"}},
					To:   []secretbootstrap.SecretContext{{ClusterGroups: []string{"group-a"}, Cluster: "default", Namespace: "ns", Name: "name"}},
				}},
			},
			expectedClusters: []string{"default"},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			censor := secrets.NewDynamicCensor()
			actualError := tc.given.completeOptions(&censor, kubeconfigs)
			equalError(t, tc.expectedError, actualError)
			if tc.expectedError == nil {
				equal(t, "config", tc.expectedConfig, tc.given.config)
				var actualClusters []string
				for k := range tc.given.secretsGetters {
					actualClusters = append(actualClusters, k)
				}
				sort.Strings(actualClusters)
				equal(t, "clusters", tc.expectedClusters, actualClusters)
			}
		})
	}
}

func TestValidateCompletedOptions(t *testing.T) {
	testCases := []struct {
		name        string
		given       options
		kubeConfigs map[string]rest.Config
		expected    error
	}{
		{
			name: "basic case",
			given: options{
				logLevel: "info",
				config:   defaultConfig,
			},
			kubeConfigs: map[string]rest.Config{
				"default": configDefault,
				"build01": configBuild01,
			},
		},
		{
			name: "empty to",
			given: options{
				logLevel: "info",
				config: secretbootstrap.Config{
					Secrets: []secretbootstrap.SecretConfig{
						{
							From: map[string]secretbootstrap.ItemContext{
								"key-name-1": {
									Item:  "item-name-1",
									Field: "field-name-1",
								},
							},
						},
					},
				},
			},
			expected: fmt.Errorf("config[0].to is empty"),
		},
		{
			name: "empty from",
			given: options{
				logLevel: "info",
				config: secretbootstrap.Config{
					Secrets: []secretbootstrap.SecretConfig{{}},
				},
			},
			expected: fmt.Errorf("config[0].from is empty"),
		},
		{
			name: "empty key",
			given: options{
				logLevel: "info",
				config: secretbootstrap.Config{
					Secrets: []secretbootstrap.SecretConfig{
						{
							From: map[string]secretbootstrap.ItemContext{
								"": {
									Item:  "item-name-1",
									Field: "field-name-1",
								},
							},
							To: []secretbootstrap.SecretContext{
								{
									Cluster:   "default",
									Namespace: "namespace-1",
									Name:      "prod-secret-1",
								},
							},
						},
					},
				},
			},
			expected: fmt.Errorf("config[0].from: empty key is not allowed"),
		},
		{
			name: "empty item",
			given: options{
				logLevel: "info",
				config: secretbootstrap.Config{
					Secrets: []secretbootstrap.SecretConfig{
						{
							From: map[string]secretbootstrap.ItemContext{
								"key-name-1": {
									Field: "field-name-1",
								},
							},
							To: []secretbootstrap.SecretContext{
								{
									Cluster:   "default",
									Namespace: "namespace-1",
									Name:      "prod-secret-1",
								},
							},
						},
					},
				},
			},
			expected: fmt.Errorf("config[0].from[key-name-1]: empty value is not allowed"),
		},
		{
			name: "empty field",
			given: options{
				logLevel: "info",
				config: secretbootstrap.Config{
					Secrets: []secretbootstrap.SecretConfig{
						{
							From: map[string]secretbootstrap.ItemContext{
								"key-name-1": {
									Item: "item-name-1",
								},
							},
							To: []secretbootstrap.SecretContext{
								{
									Cluster:   "default",
									Namespace: "namespace-1",
									Name:      "prod-secret-1",
								},
							},
						},
					},
				},
			},
			expected: fmt.Errorf("config[0].from[key-name-1]: field must be set"),
		},
		{
			name: "empty cluster",
			given: options{
				logLevel: "info",
				config: secretbootstrap.Config{
					Secrets: []secretbootstrap.SecretConfig{
						{
							From: map[string]secretbootstrap.ItemContext{
								"key-name-1": {
									Item:  "item-name-1",
									Field: "field-name-1",
								},
							},
							To: []secretbootstrap.SecretContext{
								{
									Namespace: "namespace-1",
									Name:      "prod-secret-1",
								},
							},
						},
					},
				},
			},
			expected: fmt.Errorf("config[0].to[0].cluster: empty value is not allowed"),
		},
		{
			name: "empty namespace",
			given: options{
				logLevel: "info",
				config: secretbootstrap.Config{
					Secrets: []secretbootstrap.SecretConfig{
						{
							From: map[string]secretbootstrap.ItemContext{
								"key-name-1": {
									Item:  "item-name-1",
									Field: "field-name-1",
								},
							},
							To: []secretbootstrap.SecretContext{
								{
									Cluster: "default",
									Name:    "prod-secret-1",
								},
							},
						},
					},
				},
			},
			expected: fmt.Errorf("config[0].to[0].namespace: empty value is not allowed"),
		},
		{
			name: "empty name",
			given: options{
				logLevel: "info",
				config: secretbootstrap.Config{
					Secrets: []secretbootstrap.SecretConfig{
						{
							From: map[string]secretbootstrap.ItemContext{
								"key-name-1": {
									Item:  "item-name-1",
									Field: "field-name-1",
								},
							},
							To: []secretbootstrap.SecretContext{
								{
									Cluster:   "default",
									Namespace: "namespace-1",
								},
							},
						},
					},
				},
			},
			expected: fmt.Errorf("config[0].to[0].name: empty value is not allowed"),
		},
		{
			name: "conflicting secrets in same TO",
			given: options{
				logLevel: "info",
				config: secretbootstrap.Config{
					Secrets: []secretbootstrap.SecretConfig{
						{
							From: map[string]secretbootstrap.ItemContext{
								".dockerconfigjson": {
									Item:  "item-name-1",
									Field: "field-name-1",
								},
							},
							To: []secretbootstrap.SecretContext{
								{
									Cluster:   "default",
									Namespace: "namespace-1",
									Name:      "prod-secret-1",
									Type:      "kubernetes.io/dockerconfigjson",
								},
								{
									Cluster:   "build01",
									Namespace: "namespace-1",
									Name:      "prod-secret-1",
								},
								{
									Cluster:   "default",
									Namespace: "namespace-1",
									Name:      "prod-secret-1",
									Type:      "kubernetes.io/dockerconfigjson",
								},
							},
						},
					},
				},
			},
			kubeConfigs: map[string]rest.Config{
				"default": configDefault,
				"build01": configBuild01,
			},
			expected: errors.New("config[0].to[2]: secret namespace-1/prod-secret-1 in cluster default listed more than once in the config"),
		},
		{
			name: "conflicting secrets in different TOs",
			given: options{
				logLevel: "info",
				config: secretbootstrap.Config{
					Secrets: []secretbootstrap.SecretConfig{
						{
							From: map[string]secretbootstrap.ItemContext{
								"key-name-1": {
									Item:  "item-name-1",
									Field: "field-name-1",
								},
							},
							To: []secretbootstrap.SecretContext{
								{
									Cluster:   "build01",
									Namespace: "namespace-1",
									Name:      "prod-secret-1",
								},
								{
									Cluster:   "default",
									Namespace: "namespace-1",
									Name:      "prod-secret-1",
								},
							},
						},
						{
							From: map[string]secretbootstrap.ItemContext{
								"key-name-1": {
									Item:  "item-name-1",
									Field: "field-name-1",
								},
							},
							To: []secretbootstrap.SecretContext{
								{
									Cluster:   "default",
									Namespace: "namespace-1",
									Name:      "prod-secret-1",
								},
								{
									Cluster:   "build01",
									Namespace: "namespace-1",
									Name:      "prod-secret-1",
								},
							},
						},
					},
				},
			},
			kubeConfigs: map[string]rest.Config{
				"default": configDefault,
				"build01": configBuild01,
			},
			expected: errors.New("config[1].to[0]: secret namespace-1/prod-secret-1 in cluster default listed more than once in the config"),
		},
		{
			name: "happy dockerconfigJSON configuration",
			given: options{
				logLevel: "info",
				config: secretbootstrap.Config{
					Secrets: []secretbootstrap.SecretConfig{
						{
							From: map[string]secretbootstrap.ItemContext{
								".dockerconfigjson": {
									DockerConfigJSONData: []secretbootstrap.DockerConfigJSONData{
										{
											Item:        "item-1",
											RegistryURL: "test.com",
											AuthField:   "auth",
											EmailField:  "email",
										},
										{
											Item:        "item-2",
											RegistryURL: "test.com",
											AuthField:   "auth",
											EmailField:  "email",
										},
									},
								},
							},
							To: []secretbootstrap.SecretContext{
								{
									Cluster:   "default",
									Name:      "docker-config-json-secret",
									Namespace: "namespace-1",
									Type:      "kubernetes.io/dockerconfigjson",
								},
							},
						},
					},
				},
			},
		},
		{
			name: "sad dockerconfigJSON configuration",
			given: options{
				logLevel: "info",
				config: secretbootstrap.Config{
					Secrets: []secretbootstrap.SecretConfig{
						{
							From: map[string]secretbootstrap.ItemContext{
								"key-name-1": {
									DockerConfigJSONData: []secretbootstrap.DockerConfigJSONData{
										{
											Item:        "item-1",
											RegistryURL: "test.com",
										},
										{
											Item:        "item-2",
											RegistryURL: "test.com",
											AuthField:   "auth",
											EmailField:  "email",
										},
									},
								},
							},
							To: []secretbootstrap.SecretContext{
								{
									Cluster:   "default",
									Name:      "docker-config-json-secret",
									Namespace: "namespace-1",
								},
							},
						},
					},
				},
			},
			expected: fmt.Errorf("config[0].from[key-name-1]: auth_field is missing"),
		},
		{
			name: "sad dockerconfigJSON configuration: cannot determine registry URL",
			given: options{
				logLevel: "info",
				config: secretbootstrap.Config{
					Secrets: []secretbootstrap.SecretConfig{
						{
							From: map[string]secretbootstrap.ItemContext{
								"key-name-1": {
									DockerConfigJSONData: []secretbootstrap.DockerConfigJSONData{
										{
											Item:       "bitwarden-item2",
											AuthField:  "auth",
											EmailField: "email",
										},
									},
								},
							},
							To: []secretbootstrap.SecretContext{
								{
									Cluster:   "default",
									Name:      "docker-config-json-secret",
									Namespace: "namespace-1",
								},
							},
						},
					},
				},
			},
			expected: fmt.Errorf("config[0].from[key-name-1]: registry_url must be set"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := tc.given.validateCompletedOptions()
			equalError(t, tc.expected, actual)
		})
	}
}

func TestConstructSecrets(t *testing.T) {
	testCases := []struct {
		name          string
		config        secretbootstrap.Config
		items         map[string]vaultclient.KVData
		expected      map[string][]*coreapi.Secret
		expectedError string
	}{
		{
			name:   "basic case",
			config: defaultConfig,
			items: map[string]vaultclient.KVData{
				"item-name-1": {
					Data: map[string]string{
						"field-name-1": "value1",
						"field-name-2": "value2",
						"field-name-3": "value3",
						"field-name-4": "value4",
					},
				},
				"item-name-2": {
					Data: map[string]string{
						"field-name-1": "value1",
						"field-name-2": "value2",
						"field-name-3": "value3",
						"field-name-4": "value4",
					},
				},
				"item-name-3": {
					Data: map[string]string{
						"field-name-1": "value1",
					},
				},
				"quay.io": {
					Data: map[string]string{
						"pull-credentials": "pullToken",
					},
				},
			},
			expected: map[string][]*coreapi.Secret{
				"default": {
					{
						TypeMeta: metav1.TypeMeta{Kind: "Secret", APIVersion: "v1"},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "prod-secret-1",
							Namespace: "namespace-1",
							Labels:    map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"},
						},
						Data: map[string][]byte{
							"key-name-1": []byte("value1"),
							"key-name-2": []byte("value2"),
							"key-name-3": []byte("value3"),
							"key-name-4": []byte("value1"),
							"key-name-5": []byte("value2"),
							"key-name-6": []byte("value1"),
							"key-name-7": []byte("value2"),
						},
						Type: "Opaque",
					},
					{
						TypeMeta: metav1.TypeMeta{Kind: "Secret", APIVersion: "v1"},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "ci-pull-credentials",
							Namespace: "ci",
							Labels:    map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"},
						},
						Data: map[string][]byte{
							".dockerconfigjson": []byte("pullToken"),
						},
						Type: "kubernetes.io/dockerconfigjson",
					},
				},
				"build01": {
					{
						TypeMeta: metav1.TypeMeta{Kind: "Secret", APIVersion: "v1"},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "prod-secret-2",
							Namespace: "namespace-2",
							Labels:    map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"},
						},
						Data: map[string][]byte{
							"key-name-1": []byte("value1"),
							"key-name-2": []byte("value2"),
							"key-name-3": []byte("value3"),
							"key-name-4": []byte("value1"),
							"key-name-5": []byte("value2"),
							"key-name-6": []byte("value1"),
							"key-name-7": []byte("value2"),
						},
						Type: "Opaque",
					},
				},
			},
		},
		{
			name:   "error: no such field",
			config: defaultConfig,
			items: map[string]vaultclient.KVData{
				"item-name-1": {
					Data: map[string]string{
						"field-name-2": "value2",
						"field-name-3": "value3",
						"field-name-4": "value4",
					},
				},
				"item-name-2": {
					Data: map[string]string{
						"field-name-1": "value1",
						"field-name-2": "value2",
						"field-name-3": "value3",
						"field-name-4": "value4",
					},
				},

				"item-name-3": {
					Data: map[string]string{
						"field-name-1": "value1",
					},
				},
			},
			expectedError: `[config.0."key-name-1": item at path "prefix/item-name-1" has no key "field-name-1", config.1.".dockerconfigjson": Error making API request.

URL: GET fakeVaultClient.GetKV
Code: 404. Errors:

* no data at path prefix/quay.io]`,
		},
		{
			name: "Usersecret, simple happy case",
			items: map[string]vaultclient.KVData{
				"my/vault/secret": {
					Data: map[string]string{
						"secretsync/target-namespace": "some-namespace",
						"secretsync/target-name":      "some-name",
						"some-data-key":               "a-secret",
					},
				},
			},
			config: secretbootstrap.Config{UserSecretsTargetClusters: []string{"a", "b"}},
			expected: map[string][]*coreapi.Secret{
				"a": {{
					ObjectMeta: metav1.ObjectMeta{Namespace: "some-namespace", Name: "some-name", Labels: map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"}},
					Type:       coreapi.SecretTypeOpaque,
					Data: map[string][]byte{
						"some-data-key":                []byte("a-secret"),
						"secretsync-vault-source-path": []byte("prefix/my/vault/secret"),
					},
				}},
				"b": {{
					ObjectMeta: metav1.ObjectMeta{Namespace: "some-namespace", Name: "some-name", Labels: map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"}},
					Type:       coreapi.SecretTypeOpaque,
					Data: map[string][]byte{
						"some-data-key":                []byte("a-secret"),
						"secretsync-vault-source-path": []byte("prefix/my/vault/secret"),
					},
				}},
			},
		},
		{
			name: "Usersecret only for one cluster",
			items: map[string]vaultclient.KVData{
				"my/vault/secret": {
					Data: map[string]string{
						"secretsync/target-namespace": "some-namespace",
						"secretsync/target-name":      "some-name",
						"secretsync/target-clusters":  "a",
						"some-data-key":               "a-secret",
					},
				},
			},
			config: secretbootstrap.Config{UserSecretsTargetClusters: []string{"a", "b"}},
			expected: map[string][]*coreapi.Secret{
				"a": {{
					ObjectMeta: metav1.ObjectMeta{Namespace: "some-namespace", Name: "some-name", Labels: map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"}},
					Type:       coreapi.SecretTypeOpaque,
					Data: map[string][]byte{
						"some-data-key":                []byte("a-secret"),
						"secretsync-vault-source-path": []byte("prefix/my/vault/secret"),
					},
				}},
			},
		},
		{
			name: "Usersecret for multiple but not all clusters",
			items: map[string]vaultclient.KVData{
				"my/vault/secret": {
					Data: map[string]string{
						"secretsync/target-namespace": "some-namespace",
						"secretsync/target-name":      "some-name",
						"secretsync/target-clusters":  "a,b",
						"some-data-key":               "a-secret",
					},
				},
			},
			config: secretbootstrap.Config{UserSecretsTargetClusters: []string{"a", "b", "c", "d"}},
			expected: map[string][]*coreapi.Secret{
				"a": {{
					ObjectMeta: metav1.ObjectMeta{Namespace: "some-namespace", Name: "some-name", Labels: map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"}},
					Type:       coreapi.SecretTypeOpaque,
					Data: map[string][]byte{
						"some-data-key":                []byte("a-secret"),
						"secretsync-vault-source-path": []byte("prefix/my/vault/secret"),
					},
				}},
				"b": {{
					ObjectMeta: metav1.ObjectMeta{Namespace: "some-namespace", Name: "some-name", Labels: map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"}},
					Type:       coreapi.SecretTypeOpaque,
					Data: map[string][]byte{
						"some-data-key":                []byte("a-secret"),
						"secretsync-vault-source-path": []byte("prefix/my/vault/secret"),
					},
				}},
			},
		},
		{
			name: "Secret for multiple namespaces",
			items: map[string]vaultclient.KVData{
				"my/vault/secret": {
					Data: map[string]string{
						"secretsync/target-namespace": "some-namespace,another-namespace",
						"secretsync/target-name":      "some-name",
						"some-data-key":               "a-secret",
					},
				},
			},
			config: secretbootstrap.Config{UserSecretsTargetClusters: []string{"cluster"}},
			expected: map[string][]*coreapi.Secret{
				"cluster": {
					{
						ObjectMeta: metav1.ObjectMeta{Namespace: "another-namespace", Name: "some-name", Labels: map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"}},
						Type:       coreapi.SecretTypeOpaque,
						Data: map[string][]byte{
							"some-data-key":                []byte("a-secret"),
							"secretsync-vault-source-path": []byte("prefix/my/vault/secret"),
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Namespace: "some-namespace", Name: "some-name", Labels: map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"}},
						Type:       coreapi.SecretTypeOpaque,
						Data: map[string][]byte{
							"some-data-key":                []byte("a-secret"),
							"secretsync-vault-source-path": []byte("prefix/my/vault/secret"),
						},
					},
				},
			},
		},
		{
			name: "Secret gets combined from user- and dptp secret ",
			items: map[string]vaultclient.KVData{
				"my/vault/secret": {
					Data: map[string]string{
						"secretsync/target-namespace": "some-namespace",
						"secretsync/target-name":      "some-name",
						"some-data-key":               "a-secret",
					},
				},
				"dptp-item": {
					Data: map[string]string{
						"dptp-key": "dptp-secret",
					},
				},
			},
			config: secretbootstrap.Config{
				UserSecretsTargetClusters: []string{"a", "b"},
				Secrets: []secretbootstrap.SecretConfig{{
					From: map[string]secretbootstrap.ItemContext{"dptp-key": {Item: "dptp-item", Field: "dptp-key"}},
					To: []secretbootstrap.SecretContext{
						{Cluster: "a", Namespace: "some-namespace", Name: "some-name"},
						{Cluster: "b", Namespace: "some-namespace", Name: "some-name"},
					},
				}},
			},
			expected: map[string][]*coreapi.Secret{
				"a": {{
					ObjectMeta: metav1.ObjectMeta{Namespace: "some-namespace", Name: "some-name", Labels: map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"}},
					Type:       coreapi.SecretTypeOpaque,
					Data: map[string][]byte{
						"dptp-key":                     []byte("dptp-secret"),
						"some-data-key":                []byte("a-secret"),
						"secretsync-vault-source-path": []byte("prefix/my/vault/secret"),
					},
				}},
				"b": {{
					ObjectMeta: metav1.ObjectMeta{Namespace: "some-namespace", Name: "some-name", Labels: map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"}},
					Type:       coreapi.SecretTypeOpaque,
					Data: map[string][]byte{
						"dptp-key":                     []byte("dptp-secret"),
						"some-data-key":                []byte("a-secret"),
						"secretsync-vault-source-path": []byte("prefix/my/vault/secret"),
					},
				}},
			},
		},
		{
			name: "Secret gets base64-decoded when requested",
			items: map[string]vaultclient.KVData{
				"item": {
					Data: map[string]string{
						"key": "dmFsdWUx",
					},
				},
			},
			config: secretbootstrap.Config{
				UserSecretsTargetClusters: []string{"a", "b"},
				Secrets: []secretbootstrap.SecretConfig{{
					From: map[string]secretbootstrap.ItemContext{"secret-key": {Item: "item", Field: "key", Base64Decode: true}},
					To: []secretbootstrap.SecretContext{
						{Cluster: "a", Namespace: "some-namespace", Name: "some-name"},
					},
				}},
			},
			expected: map[string][]*coreapi.Secret{
				"a": {{
					ObjectMeta: metav1.ObjectMeta{Namespace: "some-namespace", Name: "some-name", Labels: map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"}},
					Type:       coreapi.SecretTypeOpaque,
					Data: map[string][]byte{
						"secret-key": []byte("value1"),
					},
				}},
			},
		},
		{
			name: "Secret fails when base64 decoding is requsted on invalid data",
			items: map[string]vaultclient.KVData{
				"item": {
					Data: map[string]string{
						"key": "value",
					},
				},
			},
			config: secretbootstrap.Config{
				UserSecretsTargetClusters: []string{"a", "b"},
				Secrets: []secretbootstrap.SecretConfig{{
					From: map[string]secretbootstrap.ItemContext{"secret-key": {Item: "item", Field: "key", Base64Decode: true}},
					To: []secretbootstrap.SecretContext{
						{Cluster: "a", Namespace: "some-namespace", Name: "some-name"},
					},
				}},
			},
			expectedError: `failed to base64-decode config.0."secret-key": illegal base64 data at input byte 4`,
		},
		{
			name: "Usersecret would override dptp key, error",
			items: map[string]vaultclient.KVData{
				"my/vault/secret": {
					Data: map[string]string{
						"secretsync/target-namespace": "some-namespace",
						"secretsync/target-name":      "some-name",
						"dptp-key":                    "user-value",
					},
				},
				"dptp-item": {
					Data: map[string]string{
						"dptp-key": "dptp-secret",
					},
				},
			},
			config: secretbootstrap.Config{
				UserSecretsTargetClusters: []string{"a", "b"},
				Secrets: []secretbootstrap.SecretConfig{{
					From: map[string]secretbootstrap.ItemContext{"dptp-key": {Item: "dptp-item", Field: "dptp-key"}},
					To: []secretbootstrap.SecretContext{
						{Cluster: "a", Namespace: "some-namespace", Name: "some-name"},
						{Cluster: "b", Namespace: "some-namespace", Name: "some-name"},
					},
				}},
			},
			expectedError: `[key dptp-key in secret some-namespace/some-name in cluster a is targeted by ci-secret-bootstrap config and by vault item in path prefix/my/vault/secret, key dptp-key in secret some-namespace/some-name in cluster b is targeted by ci-secret-bootstrap config and by vault item in path prefix/my/vault/secret]`,
		},
		{
			name: "dptp secret isn't of opaque type, error",
			items: map[string]vaultclient.KVData{
				"my/vault/secret": {
					Data: map[string]string{
						"secretsync/target-namespace": "some-namespace",
						"secretsync/target-name":      "some-name",
						"dptp-key":                    "user-value",
					},
				},
				"dptp-item": {
					Data: map[string]string{
						"dptp-key": "dptp-secret",
					},
				},
			},
			config: secretbootstrap.Config{
				UserSecretsTargetClusters: []string{"a", "b"},
				Secrets: []secretbootstrap.SecretConfig{{
					From: map[string]secretbootstrap.ItemContext{"dptp-key": {Item: "dptp-item", Field: "dptp-key"}},
					To: []secretbootstrap.SecretContext{
						{Cluster: "a", Namespace: "some-namespace", Name: "some-name", Type: coreapi.SecretTypeBasicAuth},
						{Cluster: "b", Namespace: "some-namespace", Name: "some-name", Type: coreapi.SecretTypeBasicAuth},
					},
				}},
			},
			expectedError: `[secret some-namespace/some-name in cluster a has ci-secret-bootstrap config as non-opaque type and is targeted by user sync from key prefix/my/vault/secret, secret some-namespace/some-name in cluster b has ci-secret-bootstrap config as non-opaque type and is targeted by user sync from key prefix/my/vault/secret]`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Run(tc.name, func(t *testing.T) {
				client := vaultClientFromTestItems(tc.items)

				var actualErrorMsg string
				actual, actualError := constructSecrets(tc.config, client)
				if actualError != nil {
					actualErrorMsg = actualError.Error()
				}
				if actualErrorMsg != tc.expectedError {
					t.Fatalf("expected error message %s, got %s", tc.expectedError, actualErrorMsg)
				}
				if actualError != nil {
					return
				}
				for key := range actual {
					sort.Slice(actual[key], func(i, j int) bool {
						return actual[key][i].Namespace+actual[key][i].Name < actual[key][j].Namespace+actual[key][j].Name
					})
				}
				for key := range tc.expected {
					sort.Slice(tc.expected[key], func(i, j int) bool {
						return tc.expected[key][i].Name < tc.expected[key][j].Name
					})
				}
				equal(t, "secrets", tc.expected, actual)
			})
		})
	}
}

func TestUpdateSecrets(t *testing.T) {
	testCases := []struct {
		name                     string
		existSecretsOnDefault    []runtime.Object
		existSecretsOnBuild01    []runtime.Object
		secretsMap               map[string][]*coreapi.Secret
		force                    bool
		expected                 error
		expectedSecretsOnDefault []coreapi.Secret
		expectedSecretsOnBuild01 []coreapi.Secret
	}{
		{
			name: "namespace is created when it does not exist",
			secretsMap: map[string][]*coreapi.Secret{
				"default": {
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "prod-secret-1",
							Namespace: "create-this-namespace",
							Labels:    map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"},
						},
						Data: map[string][]byte{"secret": []byte("value")},
					},
				},
			},
			force: true,
			expectedSecretsOnDefault: []coreapi.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prod-secret-1",
						Namespace: "create-this-namespace",
						Labels:    map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"},
					},
					Data: map[string][]byte{"secret": []byte("value")},
				},
			},
		},
		{
			name: "basic case with force",
			existSecretsOnDefault: []runtime.Object{
				&coreapi.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prod-secret-1",
						Namespace: "namespace-1",
					},
					Data: map[string][]byte{
						"key-name-1": []byte("abc"),
					},
				},
			},
			secretsMap: map[string][]*coreapi.Secret{
				"default": {
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "prod-secret-1",
							Namespace: "namespace-1",
							Labels:    map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"},
						},
						Data: map[string][]byte{
							"key-name-1": []byte("value1"),
							"key-name-2": []byte("value2"),
							"key-name-3": []byte("attachment-name-1-1-value"),
							"key-name-4": []byte("value3"),
							"key-name-5": []byte("attachment-name-2-1-value"),
							"key-name-6": []byte("attachment-name-3-2-value"),
							"key-name-7": []byte("yyy"),
						},
					},
				},
				"build01": {
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "prod-secret-2",
							Namespace: "namespace-2",
							Labels:    map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"},
						},
						Data: map[string][]byte{
							"key-name-1": []byte("value1"),
							"key-name-2": []byte("value2"),
							"key-name-3": []byte("attachment-name-1-1-value"),
							"key-name-4": []byte("value3"),
							"key-name-5": []byte("attachment-name-2-1-value"),
							"key-name-6": []byte("attachment-name-3-2-value"),
							"key-name-7": []byte("yyy"),
						},
					},
				},
			},
			force: true,
			expectedSecretsOnDefault: []coreapi.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prod-secret-1",
						Namespace: "namespace-1",
						Labels:    map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"},
					},
					Data: map[string][]byte{
						"key-name-1": []byte("value1"),
						"key-name-2": []byte("value2"),
						"key-name-3": []byte("attachment-name-1-1-value"),
						"key-name-4": []byte("value3"),
						"key-name-5": []byte("attachment-name-2-1-value"),
						"key-name-6": []byte("attachment-name-3-2-value"),
						"key-name-7": []byte("yyy"),
					},
				},
			},
			expectedSecretsOnBuild01: []coreapi.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prod-secret-2",
						Namespace: "namespace-2",
						Labels:    map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"},
					},
					Data: map[string][]byte{
						"key-name-1": []byte("value1"),
						"key-name-2": []byte("value2"),
						"key-name-3": []byte("attachment-name-1-1-value"),
						"key-name-4": []byte("value3"),
						"key-name-5": []byte("attachment-name-2-1-value"),
						"key-name-6": []byte("attachment-name-3-2-value"),
						"key-name-7": []byte("yyy"),
					},
				},
			},
		},
		{
			name: "basic case without force: not semantically equal",
			existSecretsOnDefault: []runtime.Object{
				&coreapi.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prod-secret-1",
						Namespace: "namespace-1",
						Labels:    map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"},
					},
					Data: map[string][]byte{
						"key-name-1": []byte("abc"),
					},
				},
			},
			secretsMap: map[string][]*coreapi.Secret{
				"default": {
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "prod-secret-1",
							Namespace: "namespace-1",
							Labels:    map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"},
						},
						Data: map[string][]byte{
							"key-name-1": []byte("value1"),
						},
					},
				},
			},
			expected: fmt.Errorf("secret default:namespace-1/prod-secret-1 needs updating in place, use --force to do so"),
			expectedSecretsOnDefault: []coreapi.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prod-secret-1",
						Namespace: "namespace-1",
						Labels:    map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"},
					},
					Data: map[string][]byte{
						"key-name-1": []byte("abc"),
					},
				},
			},
		},
		{
			name: "basic case without force: semantically equal",
			existSecretsOnDefault: []runtime.Object{
				&coreapi.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "prod-secret-1",
						Namespace:         "namespace-1",
						Labels:            map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"},
						CreationTimestamp: metav1.NewTime(time.Now()),
					},
					Data: map[string][]byte{
						"key-name-1": []byte("abc"),
					},
				},
			},
			secretsMap: map[string][]*coreapi.Secret{
				"default": {
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "prod-secret-1",
							Namespace: "namespace-1",
							Labels:    map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"},
						},
						Data: map[string][]byte{
							"key-name-1": []byte("abc"),
						},
					},
				},
			},
			expectedSecretsOnDefault: []coreapi.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prod-secret-1",
						Namespace: "namespace-1",
						Labels:    map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"},
					},
					Data: map[string][]byte{
						"key-name-1": []byte("abc"),
					},
				},
			},
		},
		{
			name: "change secret type with force",
			existSecretsOnDefault: []runtime.Object{
				&coreapi.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prod-secret-2",
						Namespace: "namespace-2",
					},
					Data: map[string][]byte{
						"key-name-1": []byte(`{
  "auths": {
    "quay.io": {
      "auth": "aaa",
      "email": ""
    }
  }
}`),
					},
					Type: coreapi.SecretTypeDockerConfigJson,
				},
			},
			secretsMap: map[string][]*coreapi.Secret{
				"default": {
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "prod-secret-2",
							Namespace: "namespace-2",
						},
						Data: map[string][]byte{
							"key-name-1": []byte(`{
  "auths": {
    "quay.io": {
      "auth": "aaa",
      "email": ""
    }
  }
}`),
						},
						Type: coreapi.SecretTypeOpaque,
					},
				},
			},
			force: true,
			expectedSecretsOnDefault: []coreapi.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prod-secret-2",
						Namespace: "namespace-2",
					},
					Data: map[string][]byte{
						"key-name-1": []byte(`{
  "auths": {
    "quay.io": {
      "auth": "aaa",
      "email": ""
    }
  }
}`),
					},
					Type: coreapi.SecretTypeOpaque,
				},
			},
		},
		{
			name: "change secret type without force",
			existSecretsOnDefault: []runtime.Object{
				&coreapi.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prod-secret-2",
						Namespace: "namespace-2",
					},
					Data: map[string][]byte{
						"key-name-1": []byte(`{
  "auths": {
    "quay.io": {
      "auth": "aaa",
      "email": ""
    }
  }
}`),
					},
					Type: coreapi.SecretTypeDockerConfigJson,
				},
			},
			secretsMap: map[string][]*coreapi.Secret{
				"default": {
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "prod-secret-2",
							Namespace: "namespace-2",
						},
						Data: map[string][]byte{
							"key-name-1": []byte(`{
  "auths": {
    "quay.io": {
      "auth": "aaa",
      "email": ""
    }
  }
}`),
						},
					},
				},
			},
			expected: fmt.Errorf("cannot change secret type from \"kubernetes.io/dockerconfigjson\" to \"\" (immutable field): default:namespace-2/prod-secret-2"),
			expectedSecretsOnDefault: []coreapi.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prod-secret-2",
						Namespace: "namespace-2",
					},
					Data: map[string][]byte{
						"key-name-1": []byte(`{
  "auths": {
    "quay.io": {
      "auth": "aaa",
      "email": ""
    }
  }
}`),
					},
					Type: coreapi.SecretTypeDockerConfigJson,
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fkcDefault := fake.NewSimpleClientset(tc.existSecretsOnDefault...)
			fkcBuild01 := fake.NewSimpleClientset(tc.existSecretsOnBuild01...)
			clients := map[string]Getter{
				"default": fkcDefault.CoreV1(),
				"build01": fkcBuild01.CoreV1(),
			}

			actual := updateSecrets(clients, tc.secretsMap, tc.force, true)
			equalError(t, tc.expected, actual)

			actualSecretsOnDefault, err := fkcDefault.CoreV1().Secrets("").List(context.TODO(), metav1.ListOptions{})
			equalError(t, nil, err)
			equal(t, "secrets in default cluster", tc.expectedSecretsOnDefault, actualSecretsOnDefault.Items)

			actualSecretsOnBuild01, err := fkcBuild01.CoreV1().Secrets("").List(context.TODO(), metav1.ListOptions{})
			equalError(t, nil, err)
			equal(t, "secrets in build01 cluster", tc.expectedSecretsOnBuild01, actualSecretsOnBuild01.Items)
		})
	}
}

func TestWriteSecrets(t *testing.T) {
	testCases := []struct {
		name          string
		secrets       []*coreapi.Secret
		w             *bytes.Buffer
		expected      string
		expectedError error
	}{
		{
			name: "basic case",
			secrets: []*coreapi.Secret{
				{
					TypeMeta: metav1.TypeMeta{Kind: "Secret", APIVersion: "v1"},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prod-secret-1",
						Namespace: "namespace-1",
						Labels:    map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"},
					},
					Data: map[string][]byte{
						"key-name-1": []byte("value1"),
						"key-name-2": []byte("value2"),
						"key-name-3": []byte("attachment-name-1-1-value"),
						"key-name-4": []byte("value3"),
						"key-name-5": []byte("attachment-name-2-1-value"),
						"key-name-6": []byte("attachment-name-3-2-value"),
					},
				},
				{
					TypeMeta: metav1.TypeMeta{Kind: "Secret", APIVersion: "v1"},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prod-secret-2",
						Namespace: "namespace-2",
						Labels:    map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"},
					},
					Data: map[string][]byte{
						"key-name-1": []byte("value1"),
						"key-name-2": []byte("value2"),
						"key-name-3": []byte("attachment-name-1-1-value"),
						"key-name-4": []byte("value3"),
						"key-name-5": []byte("attachment-name-2-1-value"),
						"key-name-6": []byte("attachment-name-3-2-value"),
					},
				},
			},
			w: &bytes.Buffer{},
			expected: `apiVersion: v1
data:
  key-name-1: dmFsdWUx
  key-name-2: dmFsdWUy
  key-name-3: YXR0YWNobWVudC1uYW1lLTEtMS12YWx1ZQ==
  key-name-4: dmFsdWUz
  key-name-5: YXR0YWNobWVudC1uYW1lLTItMS12YWx1ZQ==
  key-name-6: YXR0YWNobWVudC1uYW1lLTMtMi12YWx1ZQ==
kind: Secret
metadata:
  creationTimestamp: null
  labels:
    dptp.openshift.io/requester: ci-secret-bootstrap
  name: prod-secret-1
  namespace: namespace-1
---
apiVersion: v1
data:
  key-name-1: dmFsdWUx
  key-name-2: dmFsdWUy
  key-name-3: YXR0YWNobWVudC1uYW1lLTEtMS12YWx1ZQ==
  key-name-4: dmFsdWUz
  key-name-5: YXR0YWNobWVudC1uYW1lLTItMS12YWx1ZQ==
  key-name-6: YXR0YWNobWVudC1uYW1lLTMtMi12YWx1ZQ==
kind: Secret
metadata:
  creationTimestamp: null
  labels:
    dptp.openshift.io/requester: ci-secret-bootstrap
  name: prod-secret-2
  namespace: namespace-2
---
`,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualError := writeSecretsToFile(tc.secrets, tc.w)
			equalError(t, tc.expectedError, actualError)
			equal(t, "result", tc.expected, tc.w.String())
		})
	}
}

func equalError(t *testing.T, expected, actual error) {
	t.Helper()
	if expected != nil && actual == nil || expected == nil && actual != nil {
		t.Errorf("expecting error \"%v\", got \"%v\"", expected, actual)
	}
	if expected != nil && actual != nil && expected.Error() != actual.Error() {
		t.Errorf("expecting error msg %q, got %q", expected.Error(), actual.Error())
	}
}

func equal(t *testing.T, what string, expected, actual interface{}) {
	t.Helper()
	if diff := cmp.Diff(expected, actual, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
		t.Errorf("%s differs from expected:\n%s", what, diff)
	}
}

func TestConstructDockerConfigJSON(t *testing.T) {
	testCases := []struct {
		id                   string
		items                map[string]vaultclient.KVData
		dockerConfigJSONData []secretbootstrap.DockerConfigJSONData
		expectedJSON         []byte
		expectedError        string
	}{
		{
			id: "happy case",
			dockerConfigJSONData: []secretbootstrap.DockerConfigJSONData{
				{
					Item:        "item-name-1",
					RegistryURL: "quay.io",
					AuthField:   "auth",
					EmailField:  "email",
				},
			},
			items: map[string]vaultclient.KVData{
				"item-name-1": {
					Data: map[string]string{
						"auth":  "c2VydmljZWFjY291bnQ6ZXlKaGJHY2lPaUpTVXpJMU5pSXNJbXRwWkNJNklrRndTekF0YjBaNGJXMUZURXRHTVMwMFVEa3djbEEwUTJWQlRUZERNMGRXUkZwdmJGOVllaTFEUW5NaWZRLmV5SnBjM01pT2lKcmRXSmxjbTVsZEdWekwzTmxjblpwWTJWaFkyTnZkVzUwSWl3aWEzVmlaWEp1WlhSbGN5NXBieTl6WlhKMmFXTmxZV05qYjNWdWRDOXVZVzFsYzNCaFkyVWlPaUpoYkhaaGNtOHRkR1Z6ZENJc0ltdDFZbVZ5Ym1WMFpYTXVhVzh2YzJWeWRtbGpaV0ZqWTI5MWJuUXZjMlZqY21WMExtNWhiV1VpT2lKa1pXWmhkV3gwTFhSdmEyVnVMV1EwT1d4aUlpd2lhM1ZpWlhKdVpYUmxjeTVwYnk5elpYSjJhV05sWVdOamIzVnVkQzl6WlhKMmFXTmxMV0ZqWTI5MWJuUXVibUZ0WlNJNkltUmxabUYxYkhRaUxDSnJkV0psY201bGRHVnpMbWx2TDNObGNuWnBZMlZoWTJOdmRXNTBMM05sY25acFkyVXRZV05qYjNWdWRDNTFhV1FpT2lJM05tVTRZMlpsTmkxbU1HWXhMVFF5WlRNdFlqUm1NQzFoTXpjM1pUbGhOemxrWWpRaUxDSnpkV0lpT2lKemVYTjBaVzA2YzJWeWRtbGpaV0ZqWTI5MWJuUTZZV3gyWVhKdkxYUmxjM1E2WkdWbVlYVnNkQ0o5LnMyajh6X2JfT3NMOHY5UGlLR1NUQmFuZDE0MHExMHc3VTlMdU9JWmZlUG1SeF9OMHdKRkZPcVN0MGNjdmtVaUVGV0x5QWNSU2k2cUt3T1FSVzE2MVUzSU52UEY4Q0pDZ2d2R3JHUnMzeHp6N3hjSmgzTWRpcXhzWGViTmNmQmlmWWxXUTU2U1RTZDlUeUh1RkN6c1poNXBlSHVzS3hOa2hJRTNyWHp5ZHNoMkhCaTZMYTlYZ1l4R1VjM0x3NWh4RnB5bXFyajFJNzExbWZLcUV2bUN0a0J4blJtMlhIZmFKalNVRkswWWdoY0lMbkhuWGhMOEx2MUl0bnU4SzlvWFRfWVZIQWY1R3hlaERjZ3FBMmw1NUZyYkJMTGVfNi1DV2V2N2RQZU5PbFlaWE5xbEtkUG5KbW9BREdsOEktTlhKN2x5ZXl2a2hfZ3JkanhXdVVqQ3lQUQ==",
						"email": "test@test.com",
					},
				},
			},
			expectedJSON: []byte(`{"auths":{"quay.io":{"auth":"c2VydmljZWFjY291bnQ6ZXlKaGJHY2lPaUpTVXpJMU5pSXNJbXRwWkNJNklrRndTekF0YjBaNGJXMUZURXRHTVMwMFVEa3djbEEwUTJWQlRUZERNMGRXUkZwdmJGOVllaTFEUW5NaWZRLmV5SnBjM01pT2lKcmRXSmxjbTVsZEdWekwzTmxjblpwWTJWaFkyTnZkVzUwSWl3aWEzVmlaWEp1WlhSbGN5NXBieTl6WlhKMmFXTmxZV05qYjNWdWRDOXVZVzFsYzNCaFkyVWlPaUpoYkhaaGNtOHRkR1Z6ZENJc0ltdDFZbVZ5Ym1WMFpYTXVhVzh2YzJWeWRtbGpaV0ZqWTI5MWJuUXZjMlZqY21WMExtNWhiV1VpT2lKa1pXWmhkV3gwTFhSdmEyVnVMV1EwT1d4aUlpd2lhM1ZpWlhKdVpYUmxjeTVwYnk5elpYSjJhV05sWVdOamIzVnVkQzl6WlhKMmFXTmxMV0ZqWTI5MWJuUXVibUZ0WlNJNkltUmxabUYxYkhRaUxDSnJkV0psY201bGRHVnpMbWx2TDNObGNuWnBZMlZoWTJOdmRXNTBMM05sY25acFkyVXRZV05qYjNWdWRDNTFhV1FpT2lJM05tVTRZMlpsTmkxbU1HWXhMVFF5WlRNdFlqUm1NQzFoTXpjM1pUbGhOemxrWWpRaUxDSnpkV0lpT2lKemVYTjBaVzA2YzJWeWRtbGpaV0ZqWTI5MWJuUTZZV3gyWVhKdkxYUmxjM1E2WkdWbVlYVnNkQ0o5LnMyajh6X2JfT3NMOHY5UGlLR1NUQmFuZDE0MHExMHc3VTlMdU9JWmZlUG1SeF9OMHdKRkZPcVN0MGNjdmtVaUVGV0x5QWNSU2k2cUt3T1FSVzE2MVUzSU52UEY4Q0pDZ2d2R3JHUnMzeHp6N3hjSmgzTWRpcXhzWGViTmNmQmlmWWxXUTU2U1RTZDlUeUh1RkN6c1poNXBlSHVzS3hOa2hJRTNyWHp5ZHNoMkhCaTZMYTlYZ1l4R1VjM0x3NWh4RnB5bXFyajFJNzExbWZLcUV2bUN0a0J4blJtMlhIZmFKalNVRkswWWdoY0lMbkhuWGhMOEx2MUl0bnU4SzlvWFRfWVZIQWY1R3hlaERjZ3FBMmw1NUZyYkJMTGVfNi1DV2V2N2RQZU5PbFlaWE5xbEtkUG5KbW9BREdsOEktTlhKN2x5ZXl2a2hfZ3JkanhXdVVqQ3lQUQ==","email":"test@test.com"}}}`),
		},
		{
			id: "invalid conents, parsing fails",
			dockerConfigJSONData: []secretbootstrap.DockerConfigJSONData{
				{
					Item:        "item-name-1",
					RegistryURL: "quay.io",
					AuthField:   "auth",
					EmailField:  "email",
				},
			},
			items: map[string]vaultclient.KVData{
				"item-name-1": {
					Data: map[string]string{
						"auth":        "123456789",
						"registryURL": "quay.io",
						"email":       "test@test.com",
					},
				},
			},
			expectedJSON:  []byte(`{"auths":{"quay.io":{"auth":"123456789","email":"test@test.com"}}}`),
			expectedError: "the constructed dockerconfigJSON doesn't parse: illegal base64 data at input byte 8",
		},
		{
			id: "happy multiple case",
			dockerConfigJSONData: []secretbootstrap.DockerConfigJSONData{
				{
					Item:        "item-name-1",
					RegistryURL: "quay.io",
					AuthField:   "auth",
					EmailField:  "email",
				},
				{
					Item:        "item-name-2",
					RegistryURL: "cloud.redhat.com",
					AuthField:   "auth",
					EmailField:  "email",
				},
			},
			items: map[string]vaultclient.KVData{
				"item-name-1": {
					Data: map[string]string{
						"auth":        "c2VydmljZWFjY291bnQ6ZXlKaGJHY2lPaUpTVXpJMU5pSXNJbXRwWkNJNklrRndTekF0YjBaNGJXMUZURXRHTVMwMFVEa3djbEEwUTJWQlRUZERNMGRXUkZwdmJGOVllaTFEUW5NaWZRLmV5SnBjM01pT2lKcmRXSmxjbTVsZEdWekwzTmxjblpwWTJWaFkyTnZkVzUwSWl3aWEzVmlaWEp1WlhSbGN5NXBieTl6WlhKMmFXTmxZV05qYjNWdWRDOXVZVzFsYzNCaFkyVWlPaUpoYkhaaGNtOHRkR1Z6ZENJc0ltdDFZbVZ5Ym1WMFpYTXVhVzh2YzJWeWRtbGpaV0ZqWTI5MWJuUXZjMlZqY21WMExtNWhiV1VpT2lKa1pXWmhkV3gwTFhSdmEyVnVMV1EwT1d4aUlpd2lhM1ZpWlhKdVpYUmxjeTVwYnk5elpYSjJhV05sWVdOamIzVnVkQzl6WlhKMmFXTmxMV0ZqWTI5MWJuUXVibUZ0WlNJNkltUmxabUYxYkhRaUxDSnJkV0psY201bGRHVnpMbWx2TDNObGNuWnBZMlZoWTJOdmRXNTBMM05sY25acFkyVXRZV05qYjNWdWRDNTFhV1FpT2lJM05tVTRZMlpsTmkxbU1HWXhMVFF5WlRNdFlqUm1NQzFoTXpjM1pUbGhOemxrWWpRaUxDSnpkV0lpT2lKemVYTjBaVzA2YzJWeWRtbGpaV0ZqWTI5MWJuUTZZV3gyWVhKdkxYUmxjM1E2WkdWbVlYVnNkQ0o5LnMyajh6X2JfT3NMOHY5UGlLR1NUQmFuZDE0MHExMHc3VTlMdU9JWmZlUG1SeF9OMHdKRkZPcVN0MGNjdmtVaUVGV0x5QWNSU2k2cUt3T1FSVzE2MVUzSU52UEY4Q0pDZ2d2R3JHUnMzeHp6N3hjSmgzTWRpcXhzWGViTmNmQmlmWWxXUTU2U1RTZDlUeUh1RkN6c1poNXBlSHVzS3hOa2hJRTNyWHp5ZHNoMkhCaTZMYTlYZ1l4R1VjM0x3NWh4RnB5bXFyajFJNzExbWZLcUV2bUN0a0J4blJtMlhIZmFKalNVRkswWWdoY0lMbkhuWGhMOEx2MUl0bnU4SzlvWFRfWVZIQWY1R3hlaERjZ3FBMmw1NUZyYkJMTGVfNi1DV2V2N2RQZU5PbFlaWE5xbEtkUG5KbW9BREdsOEktTlhKN2x5ZXl2a2hfZ3JkanhXdVVqQ3lQUQ==",
						"registryURL": "quay.io",
						"email":       "test@test.com",
					},
				},
				"item-name-2": {
					Data: map[string]string{
						"auth":        "c2VydmljZWFjY291bnQ6ZXlKaGJHY2lPaUpTVXpJMU5pSXNJbXRwWkNJNklrRndTekF0YjBaNGJXMUZURXRHTVMwMFVEa3djbEEwUTJWQlRUZERNMGRXUkZwdmJGOVllaTFEUW5NaWZRLmV5SnBjM01pT2lKcmRXSmxjbTVsZEdWekwzTmxjblpwWTJWaFkyTnZkVzUwSWl3aWEzVmlaWEp1WlhSbGN5NXBieTl6WlhKMmFXTmxZV05qYjNWdWRDOXVZVzFsYzNCaFkyVWlPaUpoYkhaaGNtOHRkR1Z6ZENJc0ltdDFZbVZ5Ym1WMFpYTXVhVzh2YzJWeWRtbGpaV0ZqWTI5MWJuUXZjMlZqY21WMExtNWhiV1VpT2lKa1pXWmhkV3gwTFhSdmEyVnVMV1EwT1d4aUlpd2lhM1ZpWlhKdVpYUmxjeTVwYnk5elpYSjJhV05sWVdOamIzVnVkQzl6WlhKMmFXTmxMV0ZqWTI5MWJuUXVibUZ0WlNJNkltUmxabUYxYkhRaUxDSnJkV0psY201bGRHVnpMbWx2TDNObGNuWnBZMlZoWTJOdmRXNTBMM05sY25acFkyVXRZV05qYjNWdWRDNTFhV1FpT2lJM05tVTRZMlpsTmkxbU1HWXhMVFF5WlRNdFlqUm1NQzFoTXpjM1pUbGhOemxrWWpRaUxDSnpkV0lpT2lKemVYTjBaVzA2YzJWeWRtbGpaV0ZqWTI5MWJuUTZZV3gyWVhKdkxYUmxjM1E2WkdWbVlYVnNkQ0o5LnMyajh6X2JfT3NMOHY5UGlLR1NUQmFuZDE0MHExMHc3VTlMdU9JWmZlUG1SeF9OMHdKRkZPcVN0MGNjdmtVaUVGV0x5QWNSU2k2cUt3T1FSVzE2MVUzSU52UEY4Q0pDZ2d2R3JHUnMzeHp6N3hjSmgzTWRpcXhzWGViTmNmQmlmWWxXUTU2U1RTZDlUeUh1RkN6c1poNXBlSHVzS3hOa2hJRTNyWHp5ZHNoMkhCaTZMYTlYZ1l4R1VjM0x3NWh4RnB5bXFyajFJNzExbWZLcUV2bUN0a0J4blJtMlhIZmFKalNVRkswWWdoY0lMbkhuWGhMOEx2MUl0bnU4SzlvWFRfWVZIQWY1R3hlaERjZ3FBMmw1NUZyYkJMTGVfNi1DV2V2N2RQZU5PbFlaWE5xbEtkUG5KbW9BREdsOEktTlhKN2x5ZXl2a2hfZ3JkanhXdVVqQ3lQUQ==",
						"registryURL": "cloud.redhat.com",
						"email":       "foo@bar.com",
					},
				},
			},
			expectedJSON: []byte(`{"auths":{"cloud.redhat.com":{"auth":"c2VydmljZWFjY291bnQ6ZXlKaGJHY2lPaUpTVXpJMU5pSXNJbXRwWkNJNklrRndTekF0YjBaNGJXMUZURXRHTVMwMFVEa3djbEEwUTJWQlRUZERNMGRXUkZwdmJGOVllaTFEUW5NaWZRLmV5SnBjM01pT2lKcmRXSmxjbTVsZEdWekwzTmxjblpwWTJWaFkyTnZkVzUwSWl3aWEzVmlaWEp1WlhSbGN5NXBieTl6WlhKMmFXTmxZV05qYjNWdWRDOXVZVzFsYzNCaFkyVWlPaUpoYkhaaGNtOHRkR1Z6ZENJc0ltdDFZbVZ5Ym1WMFpYTXVhVzh2YzJWeWRtbGpaV0ZqWTI5MWJuUXZjMlZqY21WMExtNWhiV1VpT2lKa1pXWmhkV3gwTFhSdmEyVnVMV1EwT1d4aUlpd2lhM1ZpWlhKdVpYUmxjeTVwYnk5elpYSjJhV05sWVdOamIzVnVkQzl6WlhKMmFXTmxMV0ZqWTI5MWJuUXVibUZ0WlNJNkltUmxabUYxYkhRaUxDSnJkV0psY201bGRHVnpMbWx2TDNObGNuWnBZMlZoWTJOdmRXNTBMM05sY25acFkyVXRZV05qYjNWdWRDNTFhV1FpT2lJM05tVTRZMlpsTmkxbU1HWXhMVFF5WlRNdFlqUm1NQzFoTXpjM1pUbGhOemxrWWpRaUxDSnpkV0lpT2lKemVYTjBaVzA2YzJWeWRtbGpaV0ZqWTI5MWJuUTZZV3gyWVhKdkxYUmxjM1E2WkdWbVlYVnNkQ0o5LnMyajh6X2JfT3NMOHY5UGlLR1NUQmFuZDE0MHExMHc3VTlMdU9JWmZlUG1SeF9OMHdKRkZPcVN0MGNjdmtVaUVGV0x5QWNSU2k2cUt3T1FSVzE2MVUzSU52UEY4Q0pDZ2d2R3JHUnMzeHp6N3hjSmgzTWRpcXhzWGViTmNmQmlmWWxXUTU2U1RTZDlUeUh1RkN6c1poNXBlSHVzS3hOa2hJRTNyWHp5ZHNoMkhCaTZMYTlYZ1l4R1VjM0x3NWh4RnB5bXFyajFJNzExbWZLcUV2bUN0a0J4blJtMlhIZmFKalNVRkswWWdoY0lMbkhuWGhMOEx2MUl0bnU4SzlvWFRfWVZIQWY1R3hlaERjZ3FBMmw1NUZyYkJMTGVfNi1DV2V2N2RQZU5PbFlaWE5xbEtkUG5KbW9BREdsOEktTlhKN2x5ZXl2a2hfZ3JkanhXdVVqQ3lQUQ==","email":"foo@bar.com"},"quay.io":{"auth":"c2VydmljZWFjY291bnQ6ZXlKaGJHY2lPaUpTVXpJMU5pSXNJbXRwWkNJNklrRndTekF0YjBaNGJXMUZURXRHTVMwMFVEa3djbEEwUTJWQlRUZERNMGRXUkZwdmJGOVllaTFEUW5NaWZRLmV5SnBjM01pT2lKcmRXSmxjbTVsZEdWekwzTmxjblpwWTJWaFkyTnZkVzUwSWl3aWEzVmlaWEp1WlhSbGN5NXBieTl6WlhKMmFXTmxZV05qYjNWdWRDOXVZVzFsYzNCaFkyVWlPaUpoYkhaaGNtOHRkR1Z6ZENJc0ltdDFZbVZ5Ym1WMFpYTXVhVzh2YzJWeWRtbGpaV0ZqWTI5MWJuUXZjMlZqY21WMExtNWhiV1VpT2lKa1pXWmhkV3gwTFhSdmEyVnVMV1EwT1d4aUlpd2lhM1ZpWlhKdVpYUmxjeTVwYnk5elpYSjJhV05sWVdOamIzVnVkQzl6WlhKMmFXTmxMV0ZqWTI5MWJuUXVibUZ0WlNJNkltUmxabUYxYkhRaUxDSnJkV0psY201bGRHVnpMbWx2TDNObGNuWnBZMlZoWTJOdmRXNTBMM05sY25acFkyVXRZV05qYjNWdWRDNTFhV1FpT2lJM05tVTRZMlpsTmkxbU1HWXhMVFF5WlRNdFlqUm1NQzFoTXpjM1pUbGhOemxrWWpRaUxDSnpkV0lpT2lKemVYTjBaVzA2YzJWeWRtbGpaV0ZqWTI5MWJuUTZZV3gyWVhKdkxYUmxjM1E2WkdWbVlYVnNkQ0o5LnMyajh6X2JfT3NMOHY5UGlLR1NUQmFuZDE0MHExMHc3VTlMdU9JWmZlUG1SeF9OMHdKRkZPcVN0MGNjdmtVaUVGV0x5QWNSU2k2cUt3T1FSVzE2MVUzSU52UEY4Q0pDZ2d2R3JHUnMzeHp6N3hjSmgzTWRpcXhzWGViTmNmQmlmWWxXUTU2U1RTZDlUeUh1RkN6c1poNXBlSHVzS3hOa2hJRTNyWHp5ZHNoMkhCaTZMYTlYZ1l4R1VjM0x3NWh4RnB5bXFyajFJNzExbWZLcUV2bUN0a0J4blJtMlhIZmFKalNVRkswWWdoY0lMbkhuWGhMOEx2MUl0bnU4SzlvWFRfWVZIQWY1R3hlaERjZ3FBMmw1NUZyYkJMTGVfNi1DV2V2N2RQZU5PbFlaWE5xbEtkUG5KbW9BREdsOEktTlhKN2x5ZXl2a2hfZ3JkanhXdVVqQ3lQUQ==","email":"test@test.com"}}}`),
		},
		{
			id: "sad case, field is missing",
			dockerConfigJSONData: []secretbootstrap.DockerConfigJSONData{
				{
					Item:        "item-name-1",
					RegistryURL: "quay.io",
					AuthField:   "auth",
					EmailField:  "email",
				},
			},
			items: map[string]vaultclient.KVData{
				"item-name-1": {
					Data: map[string]string{
						"registryURL": "quay.io",
						"email":       "test@test.com",
					},
				},
			},
			expectedError: `couldn't get auth field 'auth' from item item-name-1: item at path "prefix/item-name-1" has no key "auth"`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			client := vaultClientFromTestItems(tc.items)
			actual, err := constructDockerConfigJSON(client, tc.dockerConfigJSONData)
			if tc.expectedError != "" && err != nil {
				if !reflect.DeepEqual(err.Error(), tc.expectedError) {
					t.Fatal(cmp.Diff(err.Error(), tc.expectedError))
				}
			} else if tc.expectedError == "" && err != nil {
				t.Fatalf("Error not expected: %v", err)
			} else {
				if !reflect.DeepEqual(actual, tc.expectedJSON) {
					t.Fatal(cmp.Diff(actual, tc.expectedJSON))
				}
			}
		})
	}
}

func TestGetUnusedItems(t *testing.T) {
	threshold := time.Now()
	dayAfter := threshold.AddDate(0, 0, 1)
	dayBefore := threshold.AddDate(0, 0, -1)

	testCases := []struct {
		id            string
		config        secretbootstrap.Config
		items         map[string]vaultclient.KVData
		allowItems    sets.String
		expectedError string
	}{
		{
			id:         "all used, no unused items expected",
			allowItems: sets.NewString(),
			items: map[string]vaultclient.KVData{
				"item-name-1": {
					Data: map[string]string{
						"field-name-1": "testdata",
						"field-name-2": "testdata",
					},
				},
				"item-name-2": {
					Data: map[string]string{
						"field-name-1": "testdata",
						"field-name-2": "testdata",
					},
				},
			},
			config: secretbootstrap.Config{
				Secrets: []secretbootstrap.SecretConfig{
					{
						From: map[string]secretbootstrap.ItemContext{
							"1": {Item: "item-name-1", Field: "field-name-1"},
							"2": {Item: "item-name-1", Field: "field-name-2"},
							"3": {Item: "item-name-2", Field: "field-name-1"},
							"4": {Item: "item-name-2", Field: "field-name-2"},
						},
					},
				},
			},
		},
		{
			id:         "partly used, unused items expected",
			allowItems: sets.NewString(),
			items: map[string]vaultclient.KVData{
				"item-name-1": {
					Data: map[string]string{
						"field-name-1": "testdata",
						"field-name-2": "testdata",
					},
				},
				"item-name-2": {
					Data: map[string]string{
						"field-name-1": "testdata",
						"field-name-2": "testdata",
					},
				},
			},
			config: secretbootstrap.Config{
				Secrets: []secretbootstrap.SecretConfig{
					{
						From: map[string]secretbootstrap.ItemContext{
							"1": {Item: "item-name-1", Field: "field-name-1"},
							"2": {Item: "item-name-2", Field: "field-name-1"},
						},
					},
				},
			},
			expectedError: "[Unused item: 'item-name-1' with  SuperfluousFields: [field-name-2], Unused item: 'item-name-2' with  SuperfluousFields: [field-name-2]]",
		},
		{
			id:         "partly used with docker json config, unused items expected",
			allowItems: sets.NewString(),
			items: map[string]vaultclient.KVData{
				"item-name-1": {
					Data: map[string]string{
						"field-name-1": "testdata",
						"field-name-2": "testdata",
					},
				},
				"item-name-2": {
					Data: map[string]string{
						"field-name-1": "testdata",
						"field-name-2": "testdata",
					},
				},
				"item-name-3": {
					Data: map[string]string{
						"email": "test@test.com",
						"auth":  "authToken",
					},
				},
			},
			config: secretbootstrap.Config{
				Secrets: []secretbootstrap.SecretConfig{
					{
						From: map[string]secretbootstrap.ItemContext{
							"1": {Item: "item-name-1", Field: "field-name-1"},
							"2": {Item: "item-name-2", Field: "field-name-1"},
							"3": {DockerConfigJSONData: []secretbootstrap.DockerConfigJSONData{{Item: "item-name-3"}}},
						},
					},
				},
			},
			expectedError: "[Unused item: 'item-name-1' with  SuperfluousFields: [field-name-2], Unused item: 'item-name-2' with  SuperfluousFields: [field-name-2], Unused item: 'item-name-3' with  SuperfluousFields: [auth email]]",
		},
		{
			id:         "partly used with an allow list, no unused items expected",
			allowItems: sets.NewString([]string{"item-name-2"}...),
			items: map[string]vaultclient.KVData{
				"item-name-1": {
					Data: map[string]string{
						"field-name-1": "testdata",
					},
				},
				"item-name-2": {
					Data: map[string]string{
						"unused-1": "testdata",
						"unused-2": "testdata",
						"unused-3": "testdata",
					},
				},
				"item-name-3": {
					Data: map[string]string{
						"auth": "authToken",
					},
				},
			},
			config: secretbootstrap.Config{
				Secrets: []secretbootstrap.SecretConfig{
					{
						From: map[string]secretbootstrap.ItemContext{
							"1": {Item: "item-name-1", Field: "field-name-1"},
							"2": {DockerConfigJSONData: []secretbootstrap.DockerConfigJSONData{{Item: "item-name-3", RegistryURL: "test.com", AuthField: "auth"}}},
						},
					},
				},
			},
		},
		{
			id: "unused item last modified after threshold is not reported",
			items: map[string]vaultclient.KVData{
				"item-name-1": {
					Metadata: vaultclient.KVMetadata{CreatedTime: dayAfter},
					Data: map[string]string{
						"field-name-1": "testdata",
					},
				},
			},
		},
		{
			id: "unused item last modified before threshold is reported",
			items: map[string]vaultclient.KVData{
				"item-name-1": {
					Metadata: vaultclient.KVMetadata{CreatedTime: dayBefore},
					Data: map[string]string{
						"field-name-1": "testdata",
					},
				},
			},
			expectedError: "Unused item: 'item-name-1'",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			client := vaultClientFromTestItems(tc.items)
			var actualErrMsg string
			actualErr := getUnusedItems(tc.config, client, tc.allowItems, threshold)
			if actualErr != nil {
				actualErrMsg = actualErr.Error()
			}

			if actualErrMsg != tc.expectedError {
				t.Errorf("expected error: %s\ngot error: %s", tc.expectedError, actualErr)
			}

		})
	}
}

func vaultClientFromTestItems(items map[string]vaultclient.KVData) secrets.Client {
	const prefix = "prefix"
	data := make(map[string]*vaultclient.KVData, len(items))

	for name, item := range items {
		kvItem := &vaultclient.KVData{Data: map[string]string{}}

		for k, v := range item.Data {
			kvItem.Data[k] = v
		}

		kvItem.Metadata.CreatedTime = item.Metadata.CreatedTime
		data[prefix+"/"+name] = kvItem
	}

	censor := secrets.NewDynamicCensor()
	return secrets.NewVaultClient(&fakeVaultClient{items: data}, prefix, &censor)
}

func TestValidateItems(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name         string
		cfg          secretbootstrap.Config
		generatorCfg secretgenerator.Config
		items        map[string]*vaultclient.KVData

		expectedErrorMsg string
	}{
		{
			name:  "Item exists, no error",
			cfg:   secretbootstrap.Config{Secrets: []secretbootstrap.SecretConfig{{From: map[string]secretbootstrap.ItemContext{"": {Item: "foo", Field: "bar"}}}}},
			items: map[string]*vaultclient.KVData{"/foo": {Data: map[string]string{"bar": "some-value"}}},
		},
		{
			name: "Item doesn't exist,error",
			cfg:  secretbootstrap.Config{Secrets: []secretbootstrap.SecretConfig{{From: map[string]secretbootstrap.ItemContext{"": {Item: "foo", Field: "bar"}}}}},

			expectedErrorMsg: "item foo doesn't exist",
		},
		{
			name:         "Item doesn't exist but is in generator config, success",
			cfg:          secretbootstrap.Config{Secrets: []secretbootstrap.SecretConfig{{From: map[string]secretbootstrap.ItemContext{"": {Item: "foo", Field: "bar"}}}}},
			generatorCfg: secretgenerator.Config{{ItemName: "foo", Fields: []secretgenerator.FieldGenerator{{Name: "bar"}}}},
		},
		{
			name:         "Prefix, item doesn't exist but is in generator config, success",
			cfg:          secretbootstrap.Config{Secrets: []secretbootstrap.SecretConfig{{From: map[string]secretbootstrap.ItemContext{"": {Item: "dptp/foo", Field: "bar"}}}}, VaultDPTPPrefix: "dptp"},
			generatorCfg: secretgenerator.Config{{ItemName: "foo", Fields: []secretgenerator.FieldGenerator{{Name: "bar"}}}},
		},
		{
			name:         "Item doesn't exist, generator only generates different field on item, error",
			cfg:          secretbootstrap.Config{Secrets: []secretbootstrap.SecretConfig{{From: map[string]secretbootstrap.ItemContext{"": {Item: "foo", Field: "bar"}}}}},
			generatorCfg: secretgenerator.Config{{ItemName: "foo", Fields: []secretgenerator.FieldGenerator{{Name: "baz"}}}},

			expectedErrorMsg: "field bar in item foo doesn't exist",
		},
		{
			name:         "Prefix, item doesn't exist, generator only generates different field on item, error",
			cfg:          secretbootstrap.Config{Secrets: []secretbootstrap.SecretConfig{{From: map[string]secretbootstrap.ItemContext{"": {Item: "dptp/foo", Field: "bar"}}}}, VaultDPTPPrefix: "dptp"},
			generatorCfg: secretgenerator.Config{{ItemName: "foo", Fields: []secretgenerator.FieldGenerator{{Name: "baz"}}}},

			expectedErrorMsg: "field bar in item dptp/foo doesn't exist",
		},
		{
			name:         "Item exists, field doesn't but is in generator config, success",
			cfg:          secretbootstrap.Config{Secrets: []secretbootstrap.SecretConfig{{From: map[string]secretbootstrap.ItemContext{"": {Item: "foo", Field: "bar"}}}}},
			generatorCfg: secretgenerator.Config{{ItemName: "foo", Fields: []secretgenerator.FieldGenerator{{Name: "bar"}}}},
			items:        map[string]*vaultclient.KVData{"/foo": {Data: map[string]string{"baz": "some-value"}}},
		},
		{
			name:         "prefix Item exists, field doesn't but is in generator config, success",
			cfg:          secretbootstrap.Config{Secrets: []secretbootstrap.SecretConfig{{From: map[string]secretbootstrap.ItemContext{"": {Item: "dptp/foo", Field: "bar"}}}}, VaultDPTPPrefix: "dptp"},
			generatorCfg: secretgenerator.Config{{ItemName: "foo", Fields: []secretgenerator.FieldGenerator{{Name: "bar"}}}},
			items:        map[string]*vaultclient.KVData{"/foo": {Data: map[string]string{"baz": "some-value"}}},
		},
		{
			name:         "item exists, field from DockerConfigJSONData doesn't but is in generator config, success",
			cfg:          secretbootstrap.Config{Secrets: []secretbootstrap.SecretConfig{{From: map[string]secretbootstrap.ItemContext{"": {DockerConfigJSONData: []secretbootstrap.DockerConfigJSONData{{Item: "foo", AuthField: "bar"}}}}}}},
			generatorCfg: secretgenerator.Config{{ItemName: "foo", Fields: []secretgenerator.FieldGenerator{{Name: "bar"}}}},
			items:        map[string]*vaultclient.KVData{"/foo": {Data: map[string]string{"baz": "some-value"}}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			o := &options{
				config:          tc.cfg,
				generatorConfig: tc.generatorCfg,
			}
			censor := secrets.NewDynamicCensor()
			var errMsg string
			err := o.validateItems(secrets.NewVaultClient(&fakeVaultClient{items: tc.items}, "", &censor))
			if err != nil {
				errMsg = err.Error()
			}
			if tc.expectedErrorMsg != errMsg {
				t.Fatalf("actual error %v differs from expected %s", err, tc.expectedErrorMsg)
			}
		})
	}
}

type fakeVaultClient struct {
	items map[string]*vaultclient.KVData
}

func (f *fakeVaultClient) GetKV(path string) (*vaultclient.KVData, error) {
	if item, ok := f.items[path]; ok {
		return item, nil
	}

	return nil, &api.ResponseError{
		HTTPMethod: "GET",
		StatusCode: 404,
		URL:        "fakeVaultClient.GetKV",
		Errors:     []string{"no data at path " + path}}
}

func (f *fakeVaultClient) ListKVRecursively(prefix string) ([]string, error) {
	var result []string
	for key := range f.items {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		result = append(result, key)
	}
	return result, nil
}

func (f *fakeVaultClient) UpsertKV(_ string, _ map[string]string) error {
	return nil
}

func TestIntegration(t *testing.T) {
	testCases := []struct {
		id              string
		initialData     map[string][]coreapi.Secret
		force           bool
		config          secretbootstrap.Config
		secretGetters   map[string]Getter
		vaultData       map[string]map[string][]byte
		expectedSecrets map[string][]coreapi.Secret
		expectedErrors  bool
		expectedError   error
	}{
		{
			id:    "Successfully create secret from config",
			force: true,
			config: secretbootstrap.Config{
				Secrets: []secretbootstrap.SecretConfig{
					{
						From: map[string]secretbootstrap.ItemContext{
							"key-name-1": {
								Item:  "item-name-1",
								Field: "field-name-1",
							},
						},
						To: []secretbootstrap.SecretContext{
							{
								Cluster:   "cluster-1",
								Namespace: "namespace-1",
								Name:      "prod-secret-1",
							},
						},
					},
				},
			},
			secretGetters: map[string]Getter{
				"cluster-1": fake.NewSimpleClientset().CoreV1(),
			},
			vaultData: map[string]map[string][]byte{"item-name-1": {"field-name-1": []byte("secret-data")}},
			expectedSecrets: map[string][]coreapi.Secret{
				"cluster-1": {
					{
						ObjectMeta: metav1.ObjectMeta{Name: "prod-secret-1", Namespace: "namespace-1", Labels: map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"}},
						Data:       map[string][]byte{"key-name-1": []byte("secret-data")},
						Type:       coreapi.SecretTypeOpaque,
					},
				},
			},
		},
		{
			id:    "Successfully creating secret from config in multiple clusters",
			force: true,
			config: secretbootstrap.Config{
				Secrets: []secretbootstrap.SecretConfig{
					{
						From: map[string]secretbootstrap.ItemContext{
							"key-name-1": {
								Item:  "item-name-1",
								Field: "field-name-1",
							},
						},
						To: []secretbootstrap.SecretContext{
							{
								Cluster:   "cluster-1",
								Namespace: "namespace-1",
								Name:      "prod-secret-1",
							},
							{
								Cluster:   "cluster-2",
								Namespace: "namespace-1",
								Name:      "prod-secret-1",
							},
						},
					},
				},
			},
			secretGetters: map[string]Getter{
				"cluster-1": fake.NewSimpleClientset().CoreV1(),
				"cluster-2": fake.NewSimpleClientset().CoreV1(),
			},
			vaultData: map[string]map[string][]byte{"item-name-1": {"field-name-1": []byte("secret-data")}},
			expectedSecrets: map[string][]coreapi.Secret{
				"cluster-1": {
					{
						ObjectMeta: metav1.ObjectMeta{Name: "prod-secret-1", Namespace: "namespace-1", Labels: map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"}},
						Data:       map[string][]byte{"key-name-1": []byte("secret-data")},
						Type:       coreapi.SecretTypeOpaque,
					},
				},
				"cluster-2": {
					{
						ObjectMeta: metav1.ObjectMeta{Name: "prod-secret-1", Namespace: "namespace-1", Labels: map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"}},
						Data:       map[string][]byte{"key-name-1": []byte("secret-data")},
						Type:       coreapi.SecretTypeOpaque,
					},
				},
			},
		},
		{
			id:    "Successfully create secret from config and vault user secret",
			force: true,
			config: secretbootstrap.Config{
				UserSecretsTargetClusters: []string{"cluster-1"},
				Secrets: []secretbootstrap.SecretConfig{
					{
						From: map[string]secretbootstrap.ItemContext{
							"key-name-1": {
								Item:  "item-name-1",
								Field: "field-name-1",
							},
						},
						To: []secretbootstrap.SecretContext{
							{
								Cluster:   "cluster-1",
								Namespace: "namespace-1",
								Name:      "prod-secret-1",
							},
						},
					},
				},
			},
			secretGetters: map[string]Getter{
				"cluster-1": fake.NewSimpleClientset().CoreV1(),
			},
			vaultData: map[string]map[string][]byte{
				"item-name-1": {"field-name-1": []byte("secret-data")},
				"user-item-1": {
					"some-data-key":               []byte("a-secret"),
					"secretsync/target-namespace": []byte("namespace-1"),
					"secretsync/target-name":      []byte("some-name"),
				},
			},
			expectedSecrets: map[string][]coreapi.Secret{
				"cluster-1": {
					{
						ObjectMeta: metav1.ObjectMeta{Name: "prod-secret-1", Namespace: "namespace-1", Labels: map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"}},
						Data:       map[string][]byte{"key-name-1": []byte("secret-data")},
						Type:       coreapi.SecretTypeOpaque,
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "some-name", Namespace: "namespace-1", Labels: map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"}},
						Data: map[string][]byte{
							"some-data-key":                []byte("a-secret"),
							"secretsync-vault-source-path": []byte("secret/user-item-1"),
						},
						Type: coreapi.SecretTypeOpaque,
					},
				},
			},
		},
		{
			id:    "Successfully create secret from config and vault user secret in multiple clusters",
			force: true,
			config: secretbootstrap.Config{
				UserSecretsTargetClusters: []string{"cluster-1", "cluster-2"},
				Secrets: []secretbootstrap.SecretConfig{
					{
						From: map[string]secretbootstrap.ItemContext{
							"key-name-1": {
								Item:  "item-name-1",
								Field: "field-name-1",
							},
						},
						To: []secretbootstrap.SecretContext{
							{
								Cluster:   "cluster-1",
								Namespace: "namespace-1",
								Name:      "prod-secret-1",
							},
						},
					},
				},
			},
			secretGetters: map[string]Getter{
				"cluster-1": fake.NewSimpleClientset().CoreV1(),
				"cluster-2": fake.NewSimpleClientset().CoreV1(),
			},
			vaultData: map[string]map[string][]byte{
				"item-name-1": {"field-name-1": []byte("secret-data")},
				"user-item-1": {
					"some-data-key":               []byte("a-secret"),
					"secretsync/target-namespace": []byte("namespace-1"),
					"secretsync/target-name":      []byte("some-name"),
				},
			},
			expectedSecrets: map[string][]coreapi.Secret{
				"cluster-1": {
					{
						ObjectMeta: metav1.ObjectMeta{Name: "prod-secret-1", Namespace: "namespace-1", Labels: map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"}},
						Data:       map[string][]byte{"key-name-1": []byte("secret-data")},
						Type:       coreapi.SecretTypeOpaque,
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "some-name", Namespace: "namespace-1", Labels: map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"}},
						Data: map[string][]byte{
							"some-data-key":                []byte("a-secret"),
							"secretsync-vault-source-path": []byte("secret/user-item-1"),
						},
						Type: coreapi.SecretTypeOpaque,
					},
				},
				"cluster-2": {
					{
						ObjectMeta: metav1.ObjectMeta{Name: "some-name", Namespace: "namespace-1", Labels: map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"}},
						Data: map[string][]byte{
							"some-data-key":                []byte("a-secret"),
							"secretsync-vault-source-path": []byte("secret/user-item-1"),
						},
						Type: coreapi.SecretTypeOpaque,
					},
				},
			},
		},
		{
			id:    "Successfully create secret from config and vault user secret in multiple selected clusters",
			force: true,
			config: secretbootstrap.Config{
				UserSecretsTargetClusters: []string{"cluster-1", "cluster-2", "cluster-3"},
				Secrets: []secretbootstrap.SecretConfig{
					{
						From: map[string]secretbootstrap.ItemContext{
							"key-name-1": {
								Item:  "item-name-1",
								Field: "field-name-1",
							},
						},
						To: []secretbootstrap.SecretContext{
							{
								Cluster:   "cluster-1",
								Namespace: "namespace-1",
								Name:      "prod-secret-1",
							},
						},
					},
				},
			},
			secretGetters: map[string]Getter{
				"cluster-1": fake.NewSimpleClientset().CoreV1(),
				"cluster-2": fake.NewSimpleClientset().CoreV1(),
				"cluster-3": fake.NewSimpleClientset().CoreV1(),
			},

			vaultData: map[string]map[string][]byte{
				"item-name-1": {"field-name-1": []byte("secret-data")},
				"user-item-1": {
					"some-data-key":               []byte("a-secret"),
					"secretsync/target-namespace": []byte("namespace-1"),
					"secretsync/target-name":      []byte("some-name"),
					"secretsync/target-clusters":  []byte("cluster-1,cluster-3"),
				},
			},
			expectedSecrets: map[string][]coreapi.Secret{
				"cluster-1": {
					{
						ObjectMeta: metav1.ObjectMeta{Name: "prod-secret-1", Namespace: "namespace-1", Labels: map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"}},
						Data:       map[string][]byte{"key-name-1": []byte("secret-data")},
						Type:       coreapi.SecretTypeOpaque,
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "some-name", Namespace: "namespace-1", Labels: map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"}},
						Data: map[string][]byte{
							"some-data-key":                []byte("a-secret"),
							"secretsync-vault-source-path": []byte("secret/user-item-1"),
						},
						Type: coreapi.SecretTypeOpaque,
					},
				},
				"cluster-3": {
					{
						ObjectMeta: metav1.ObjectMeta{Name: "some-name", Namespace: "namespace-1", Labels: map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"}},
						Data: map[string][]byte{
							"some-data-key":                []byte("a-secret"),
							"secretsync-vault-source-path": []byte("secret/user-item-1"),
						},
						Type: coreapi.SecretTypeOpaque,
					},
				},
			},
		},
		{
			id: "fails to find secret in vault, error is expected",
			config: secretbootstrap.Config{
				Secrets: []secretbootstrap.SecretConfig{
					{
						From: map[string]secretbootstrap.ItemContext{
							"key-name-1": {
								Item:  "item-name-1",
								Field: "field-name-1",
							},
						},
						To: []secretbootstrap.SecretContext{
							{
								Cluster:   "cluster-1",
								Namespace: "namespace-1",
								Name:      "prod-secret-1",
							},
						},
					},
				},
			},
			secretGetters: map[string]Getter{
				"cluster-1": fake.NewSimpleClientset().CoreV1(),
			},
			vaultData: map[string]map[string][]byte{
				"no-one-wants-me": {"field-name-1": []byte("secret-data")},
			},
			expectedError: utilerrors.NewAggregate([]error{errors.New("config.0.\"key-name-1\": failed to get item at path \"secret/item-name-1\": Error making API request.\n\nURL:  \nCode: 404. Errors:\n\n")}),
		},
		{
			id: "fails to update secret from config and vault user secret",
			config: secretbootstrap.Config{
				UserSecretsTargetClusters: []string{"cluster-1", "cluster-2"},
				Secrets: []secretbootstrap.SecretConfig{
					{
						From: map[string]secretbootstrap.ItemContext{
							"key-name-1": {
								Item:  "item-name-1",
								Field: "field-name-1",
							},
						},
						To: []secretbootstrap.SecretContext{
							{
								Cluster:   "cluster-1",
								Namespace: "namespace-1",
								Name:      "prod-secret-1",
							},
						},
					},
				},
			},
			secretGetters: map[string]Getter{
				"cluster-1": fake.NewSimpleClientset(
					[]runtime.Object{
						&coreapi.Secret{
							Type: coreapi.SecretTypeOpaque,
							ObjectMeta: metav1.ObjectMeta{
								Name:      "prod-secret-1",
								Namespace: "namespace-1",
							},
							Data: map[string][]byte{
								"key-name-1": []byte("update-me"),
							},
						},
					}...,
				).CoreV1(),
				"cluster-2": fake.NewSimpleClientset().CoreV1(),
			},
			vaultData: map[string]map[string][]byte{
				"item-name-1": {"field-name-1": []byte("secret-data")},
				"user-item-1": {
					"some-data-key":               []byte("a-secret"),
					"secretsync/target-namespace": []byte("namespace-1"),
					"secretsync/target-name":      []byte("some-name"),
				},
			},
			expectedError: utilerrors.NewAggregate([]error{errors.New("failed to update secrets: secret cluster-1:namespace-1/prod-secret-1 needs updating in place, use --force to do so")}),
		},
		{
			id:    "Two items reference the same secret but different keys, success",
			force: true,
			config: secretbootstrap.Config{
				UserSecretsTargetClusters: []string{"cluster-1"},
			},
			secretGetters: map[string]Getter{"cluster-1": fake.NewSimpleClientset().CoreV1()},
			vaultData: map[string]map[string][]byte{
				"user-item-1": {
					"some-data-key":               []byte("a-secret"),
					"secretsync/target-namespace": []byte("namespace-1"),
					"secretsync/target-name":      []byte("some-name"),
				},
				"user-item-2": {
					"some-different-data-key":     []byte("a-secret"),
					"secretsync/target-namespace": []byte("namespace-1"),
					"secretsync/target-name":      []byte("some-name"),
				},
			},
			expectedSecrets: map[string][]coreapi.Secret{
				"cluster-1": {
					{
						ObjectMeta: metav1.ObjectMeta{Name: "some-name", Namespace: "namespace-1", Labels: map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"}},
						Data: map[string][]byte{
							"some-data-key":                []byte("a-secret"),
							"some-different-data-key":      []byte("a-secret"),
							"secretsync-vault-source-path": []byte("secret/user-item-1,secret/user-item-2"),
						},
						Type: coreapi.SecretTypeOpaque,
					},
				},
			},
		},
		{
			id:    "Two items reference the same key in the same secret, failure",
			force: true,
			config: secretbootstrap.Config{
				UserSecretsTargetClusters: []string{"cluster-1"},
			},
			secretGetters: map[string]Getter{"cluster-1": fake.NewSimpleClientset().CoreV1()},
			vaultData: map[string]map[string][]byte{
				"user-item-1": {
					"some-data-key":               []byte("a-secret"),
					"secretsync/target-namespace": []byte("namespace-3"),
					"secretsync/target-name":      []byte("some-name"),
				},
				"user-item-2": {
					"some-data-key":               []byte("a-secret"),
					"secretsync/target-namespace": []byte("namespace-3"),
					"secretsync/target-name":      []byte("some-name"),
				},
			},
			expectedError: utilerrors.NewAggregate([]error{errors.New("the some-data-key key in secret namespace-3/some-name is referenced by multiple vault items: secret/user-item-1,secret/user-item-2")}),
		},
		{
			id:    "Successfully create secret from config and vault user secret in multiple selected clusters",
			force: true,
			config: secretbootstrap.Config{
				UserSecretsTargetClusters: []string{"cluster-1", "cluster-2", "cluster-3"},
				Secrets: []secretbootstrap.SecretConfig{
					{
						From: map[string]secretbootstrap.ItemContext{
							"key-name-1": {
								Item:  "item-name-1",
								Field: "field-name-1",
							},
						},
						To: []secretbootstrap.SecretContext{
							{
								Cluster:   "cluster-1",
								Namespace: "namespace-1",
								Name:      "prod-secret-1",
							},
						},
					},
				},
			},
			secretGetters: map[string]Getter{
				"cluster-1": fake.NewSimpleClientset().CoreV1(),
				"cluster-2": fake.NewSimpleClientset().CoreV1(),
				"cluster-3": fake.NewSimpleClientset().CoreV1(),
			},

			vaultData: map[string]map[string][]byte{
				"item-name-1": {"field-name-1": []byte("secret-data")},
				"user-item-1": {
					"some-data-key":               []byte("a-secret"),
					"secretsync/target-namespace": []byte("namespace-1"),
					"secretsync/target-name":      []byte("prod-secret-1"),
					"secretsync/target-clusters":  []byte("cluster-1,cluster-3"),
				},
			},
			expectedSecrets: map[string][]coreapi.Secret{
				"cluster-1": {
					{
						ObjectMeta: metav1.ObjectMeta{Name: "prod-secret-1", Namespace: "namespace-1", Labels: map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"}},
						Data: map[string][]byte{
							"key-name-1":                   []byte("secret-data"),
							"some-data-key":                []byte("a-secret"),
							"secretsync-vault-source-path": []byte("secret/user-item-1"),
						},
						Type: coreapi.SecretTypeOpaque,
					},
				},
				"cluster-3": {
					{
						ObjectMeta: metav1.ObjectMeta{Name: "prod-secret-1", Namespace: "namespace-1", Labels: map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"}},
						Data: map[string][]byte{
							"some-data-key":                []byte("a-secret"),
							"secretsync-vault-source-path": []byte("secret/user-item-1"),
						},
						Type: coreapi.SecretTypeOpaque,
					},
				},
			},
		},
		{
			id:    "Existing config secret with stale keys should be removed",
			force: true,
			config: secretbootstrap.Config{
				Secrets: []secretbootstrap.SecretConfig{
					{
						From: map[string]secretbootstrap.ItemContext{
							"key-name-1": {
								Item:  "item-name-1",
								Field: "field-name-1",
							},
						},
						To: []secretbootstrap.SecretContext{
							{
								Cluster:   "cluster-1",
								Namespace: "namespace-1",
								Name:      "prod-secret-1",
							},
						},
					},
				},
			},
			secretGetters: map[string]Getter{
				"cluster-1": fake.NewSimpleClientset([]runtime.Object{
					&coreapi.Secret{
						ObjectMeta: metav1.ObjectMeta{Name: "prod-secret-1", Namespace: "namespace-1", Labels: map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"}},
						Data:       map[string][]byte{"old-key-1": []byte("old-value"), "old-key-2": []byte("old-value")},
						Type:       coreapi.SecretTypeOpaque,
					},
				}...).CoreV1(),
			},

			vaultData: map[string]map[string][]byte{
				"item-name-1": {"field-name-1": []byte("secret-data")},
			},
			expectedSecrets: map[string][]coreapi.Secret{
				"cluster-1": {
					{
						ObjectMeta: metav1.ObjectMeta{Name: "prod-secret-1", Namespace: "namespace-1", Labels: map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"}},
						Data: map[string][]byte{
							"key-name-1": []byte("secret-data"),
						},
						Type: coreapi.SecretTypeOpaque,
					},
				},
			},
		},
		{
			id:    "Existing user secret with stale keys should be removed",
			force: true,
			config: secretbootstrap.Config{
				UserSecretsTargetClusters: []string{"cluster-1"},
			},
			secretGetters: map[string]Getter{
				"cluster-1": fake.NewSimpleClientset([]runtime.Object{
					&coreapi.Secret{
						ObjectMeta: metav1.ObjectMeta{Name: "prod-secret-1", Namespace: "namespace-1", Labels: map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"}},
						Data:       map[string][]byte{"old-key-1": []byte("old-value"), "old-key-2": []byte("old-value")},
						Type:       coreapi.SecretTypeOpaque,
					},
				}...).CoreV1(),
			},

			vaultData: map[string]map[string][]byte{
				"user-item-1": {
					"some-data-key":               []byte("a-secret"),
					"secretsync/target-namespace": []byte("namespace-1"),
					"secretsync/target-name":      []byte("prod-secret-1"),
					"secretsync/target-clusters":  []byte("cluster-1"),
				},
			},
			expectedSecrets: map[string][]coreapi.Secret{
				"cluster-1": {
					{
						ObjectMeta: metav1.ObjectMeta{Name: "prod-secret-1", Namespace: "namespace-1", Labels: map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"}},
						Data: map[string][]byte{
							"some-data-key":                []byte("a-secret"),
							"secretsync-vault-source-path": []byte("secret/user-item-1"),
						},
						Type: coreapi.SecretTypeOpaque,
					},
				},
			},
		},
		{
			id:    "Combined config and user secret with stale keys should be removed",
			force: true,
			config: secretbootstrap.Config{
				UserSecretsTargetClusters: []string{"cluster-1"},
				Secrets: []secretbootstrap.SecretConfig{
					{
						From: map[string]secretbootstrap.ItemContext{
							"key-name-1": {
								Item:  "item-name-1",
								Field: "field-name-1",
							},
						},
						To: []secretbootstrap.SecretContext{
							{
								Cluster:   "cluster-1",
								Namespace: "namespace-1",
								Name:      "prod-secret-1",
							},
						},
					},
				},
			},
			secretGetters: map[string]Getter{
				"cluster-1": fake.NewSimpleClientset([]runtime.Object{
					&coreapi.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "prod-secret-1",
							Namespace: "namespace-1",
						},
						Data: map[string][]byte{"old-key-1": []byte("old-value"), "old-key-2": []byte("old-value")},
						Type: coreapi.SecretTypeOpaque,
					},
				}...).CoreV1(),
			},

			vaultData: map[string]map[string][]byte{
				"item-name-1": {"field-name-1": []byte("secret-data")},
				"user-item-1": {
					"some-data-key":               []byte("a-secret"),
					"secretsync/target-namespace": []byte("namespace-1"),
					"secretsync/target-name":      []byte("prod-secret-1"),
					"secretsync/target-clusters":  []byte("cluster-1"),
				},
			},
			expectedSecrets: map[string][]coreapi.Secret{
				"cluster-1": {
					{
						ObjectMeta: metav1.ObjectMeta{Name: "prod-secret-1", Namespace: "namespace-1", Labels: map[string]string{"dptp.openshift.io/requester": "ci-secret-bootstrap"}},
						Data: map[string][]byte{
							"key-name-1":                   []byte("secret-data"),
							"some-data-key":                []byte("a-secret"),
							"secretsync-vault-source-path": []byte("secret/user-item-1"),
						},
						Type: coreapi.SecretTypeOpaque,
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			vaultAddr := testhelper.Vault(t)

			o := options{
				force:          tc.force,
				config:         tc.config,
				secretsGetters: tc.secretGetters,
				secrets: secrets.CLIOptions{
					VaultPrefix: "secret",
					VaultAddr:   "http://" + vaultAddr,
					VaultToken:  testhelper.VaultTestingRootToken,
				},
			}

			censor := secrets.NewDynamicCensor()
			c, err := o.secrets.NewClient(&censor)
			if err != nil {
				t.Fatalf("Failed to create client: %v", err)
			}

			for item, data := range tc.vaultData {
				for name, value := range data {
					if err := c.SetFieldOnItem(item, name, value); err != nil {
						t.Fatalf("couldn't populate vault item %s: %v", item, err)
					}
				}
			}

			readOnlyClient, err := o.secrets.NewReadOnlyClient(&censor)
			if err != nil {
				t.Fatal("Failed to create a read only client.")
			}

			actualSecretsByCluster := make(map[string][]coreapi.Secret)

			// Create Case
			errs := reconcileSecrets(o, readOnlyClient)
			if tc.expectedError != nil {
				if len(errs) == 0 {
					t.Fatal("expected errors but got nothing")
				}
				if diff := cmp.Diff(utilerrors.NewAggregate(errs).Error(), tc.expectedError.Error()); diff != "" {
					t.Fatal(diff)
				}
			} else {
				if len(errs) > 0 {
					t.Fatalf("errors weren't expected but got: %v", utilerrors.NewAggregate(errs))
				}

				for cluster, secretGetter := range tc.secretGetters {
					actualSecrets, err := secretGetter.Secrets("").List(context.Background(), metav1.ListOptions{})
					if err != nil {
						t.Fatal(err)
					}
					if len(actualSecrets.Items) > 0 {
						actualSecretsByCluster[cluster] = append(actualSecretsByCluster[cluster], actualSecrets.Items...)
					}
				}

				if diff := cmp.Diff(actualSecretsByCluster, tc.expectedSecrets, cmpopts.EquateEmpty()); diff != "" {
					t.Errorf("secrets by cluster differ from expected after initial create: %s", diff)
				}
			}

			// Update Case
			for cluster, secrets := range actualSecretsByCluster {
				for _, s := range secrets {

					for key := range s.Data {
						s.Data[key] = []byte("old-value")
					}
					if _, err := tc.secretGetters[cluster].Secrets(s.Namespace).Update(context.Background(), &s, metav1.UpdateOptions{}); err != nil {
						t.Fatalf("couldn't update secret %s/%s: %v", s.Namespace, s.Name, err)
					}
				}
			}

			errs = reconcileSecrets(o, readOnlyClient)
			if tc.expectedError != nil {
				if len(errs) == 0 {
					t.Fatal("expected errors but got nothing")
				}
				if diff := cmp.Diff(utilerrors.NewAggregate(errs).Error(), tc.expectedError.Error()); diff != "" {
					t.Fatal(diff)
				}
			} else {
				if len(errs) > 0 {
					t.Fatalf("errors weren't expected but got: %v", utilerrors.NewAggregate(errs))
				}

				actualUpdatedSecretsByCluster := make(map[string][]coreapi.Secret)
				for cluster, secretGetter := range tc.secretGetters {
					actualSecrets, err := secretGetter.Secrets("").List(context.Background(), metav1.ListOptions{})
					if err != nil {
						t.Fatalf("failed to list secrets: %v", err)
					}
					if len(actualSecrets.Items) > 0 {
						actualUpdatedSecretsByCluster[cluster] = append(actualUpdatedSecretsByCluster[cluster], actualSecrets.Items...)
					}
				}

				if diff := cmp.Diff(actualUpdatedSecretsByCluster, tc.expectedSecrets, cmpopts.EquateEmpty()); diff != "" {
					t.Fatalf("secrets by cluster differ from expected after in update case: %s", diff)
				}
			}
		})
	}
}

func TestPruneIrrelevantConfiguration(t *testing.T) {
	testCases := []struct {
		name     string
		given    *secretbootstrap.Config
		expected *secretbootstrap.Config
	}{
		{
			name: "base case",
			given: &secretbootstrap.Config{
				Secrets: []secretbootstrap.SecretConfig{
					{
						From: map[string]secretbootstrap.ItemContext{
							"sa.config-updater.app.ci.config":  {Field: "sa.config-updater.app.ci.config", Item: "build_farm"},
							"sa.config-updater.build01.config": {Field: "sa.config-updater.build01.config", Item: "build_farm"},
						},
						To: []secretbootstrap.SecretContext{
							{Namespace: "ci", Name: "config-updater", Cluster: "app.ci"},
							{Namespace: "vault", Name: "config-updater", Cluster: "app.ci"},
						},
					},
					{
						From: map[string]secretbootstrap.ItemContext{
							"a": {Field: "b", Item: "c"},
						},
						To: []secretbootstrap.SecretContext{
							{Namespace: "ci", Name: "some", Cluster: "build03"},
						},
					},
				},
				UserSecretsTargetClusters: []string{"b01"},
			},
			expected: &secretbootstrap.Config{
				Secrets: []secretbootstrap.SecretConfig{
					{
						From: map[string]secretbootstrap.ItemContext{
							"sa.config-updater.app.ci.config":  {Field: "sa.config-updater.app.ci.config", Item: "build_farm"},
							"sa.config-updater.build01.config": {Field: "sa.config-updater.build01.config", Item: "build_farm"},
						},
						To: []secretbootstrap.SecretContext{
							{Namespace: "ci", Name: "config-updater", Cluster: "app.ci"},
							{Namespace: "vault", Name: "config-updater", Cluster: "app.ci"},
						},
					},
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pruneIrrelevantConfiguration(tc.given, sets.NewString("config-updater"))
			if diff := cmp.Diff(tc.given, tc.expected); diff != "" {
				t.Errorf("%s: actual differs from expected: %s", tc.name, diff)
			}
		})
	}
}
