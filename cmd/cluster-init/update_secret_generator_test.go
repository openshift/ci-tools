package main

import (
	"fmt"
	"github.com/google/go-cmp/cmp"
	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/api/secretgenerator"
	"github.com/openshift/ci-tools/pkg/testhelper"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
)

func TestFindSecretItem(t *testing.T) {
	secretA := secretgenerator.SecretItem{
		ItemName: BuildUFarm,
		Fields: []secretgenerator.FieldGenerator{{
			Name: "secret-a",
			Cmd:  "oc --context $(cluster) sa create-kubeconfig --namespace ci $(service_account) | sed \"s/$(service_account)/$(cluster)/g\"",
		}},
		Notes: "",
		Params: map[string][]string{
			"cluster": {
				string(api.ClusterAPPCI),
				string(api.ClusterBuild01)}},
	}
	config := SecretGenConfig{
		{
			ItemName: "release-controller",
			Fields: []secretgenerator.FieldGenerator{{
				Name: "secret-0",
				Cmd:  "oc --context $(cluster) sa create-kubeconfig --namespace ci $(service_account) | sed \"s/$(service_account)/$(cluster)/g\"",
			}},
			Notes: "",
			Params: map[string][]string{
				"cluster": {
					string(api.ClusterAPPCI),
					string(api.ClusterBuild01)}},
		},
		secretA,
		{
			ItemName: BuildUFarm,
			Fields: []secretgenerator.FieldGenerator{{
				Name: "secret-b",
				Cmd:  "oc --context $(cluster) sa create-kubeconfig --namespace ci $(service_account) | sed \"s/$(service_account)/$(cluster)/g\"",
			}},
			Notes: "",
			Params: map[string][]string{
				"cluster": {
					string(api.ClusterAPPCI),
					string(api.ClusterBuild02)}},
		},
	}
	type args struct {
		itemName    string
		name        string
		likeCluster string
		c           SecretGenConfig
	}
	testCases := []struct {
		name          string
		args          args
		expected      *secretgenerator.SecretItem
		expectedError error
	}{
		{
			name: "existing",
			args: args{
				itemName:    BuildUFarm,
				name:        "secret-a",
				likeCluster: string(api.ClusterBuild01),
				c:           config,
			},
			expected: &secretA,
		},
		{
			name: "non-existing",
			args: args{
				itemName:    BuildUFarm,
				name:        "secret-c",
				likeCluster: string(api.ClusterBuild01),
				c:           config,
			},
			expected:      &secretgenerator.SecretItem{},
			expectedError: fmt.Errorf("couldn't find SecretItem with item_name: build_farm name: secret-c containing cluster: build01"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			secretItem, err := findSecretItem(tc.args.itemName, tc.args.name, tc.args.likeCluster, tc.args.c)
			if diff := cmp.Diff(tc.expectedError, err, testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("error: %v - expectedError: %v", err, tc.expectedError)
				return
			}
			if diff := cmp.Diff(tc.expected, secretItem); diff != "" {
				t.Fatalf("wrong secretItem returned. Diff: %s", diff)
			}
		})
	}
}

func TestUpdateSecretGenerator(t *testing.T) {
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("couldn't obtain working directory")
	}
	testdata := filepath.Join(workingDir, "testdata")
	testCases := []struct {
		name string
		options
	}{
		{
			name: "basic",
			options: options{
				clusterName: "newcluster",
				releaseRepo: testdata,
			},
		},
	}
	for _, tc := range testCases {
		var tempConfigFile string
		t.Run(tc.name, func(t *testing.T) {
			secretGeneratorDir := filepath.Join(testdata, "core-services", "ci-secret-generator")
			src := filepath.Join(secretGeneratorDir, "config.yaml")
			srcFD, err := os.Open(src)
			if err != nil {
				t.Fatalf("couldn't open config file")
			}
			tempConfigFile = filepath.Join(secretGeneratorDir, "_config.yaml")
			destFD, err := os.Create(tempConfigFile)
			if err != nil {
				t.Fatalf("couldn't create temp config file")
			}
			_, err = io.Copy(destFD, srcFD)
			if err != nil {
				t.Fatalf("couldn't copy to temp config file")
			}
			if err = updateSecretGenerator(tc.options); err != nil {
				t.Fatalf("updateSecretGenerator returned error: %v", err)
			}

			configOut, _ := ioutil.ReadFile(tempConfigFile)
			expectedOut, _ := ioutil.ReadFile(filepath.Join(secretGeneratorDir, "config_expected.yaml"))
			if diff := cmp.Diff(expectedOut, configOut); diff != "" {
				t.Fatalf("expected config does not match generated config: %s", diff)
			}
		})

		t.Cleanup(func() {
			if err := os.Remove(tempConfigFile); err != nil {
				t.Fatalf("error removing output config file: %v", err)
			}
		})
	}
}
