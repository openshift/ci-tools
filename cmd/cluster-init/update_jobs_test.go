package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	v1 "k8s.io/api/core/v1"
	prowconfig "k8s.io/test-infra/prow/config"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestPeriodicExistsFor(t *testing.T) {
	testCases := []struct {
		name string
		options
		expected bool
	}{
		{
			name: "exists",
			options: options{
				clusterName: "existingCluster",
				releaseRepo: "testdata",
			},
			expected: true,
		},
		{
			name: "does not exist",
			options: options{
				clusterName: "newCluster",
				releaseRepo: "testdata",
			},
			expected: false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			exists, err := periodicExistsFor(tc.options)
			if err != nil {
				t.Fatalf("unexpected error occurred while running periodicExistsFor: %v", err)
			}
			if tc.expected != exists {
				t.Fatalf("result: %v does not match expected: %v", exists, tc.expected)
			}
		})
	}
}

func TestAppendNewClustersConfigUpdaterToKubeconfig(t *testing.T) {
	testCases := []struct {
		name          string
		containerName string
		clusterName   string
		input         prowconfig.Periodic
		expected      prowconfig.Periodic
	}{
		{
			name:          "basic",
			containerName: "container-1",
			clusterName:   "newcluster",
			input: prowconfig.Periodic{
				JobBase: prowconfig.JobBase{
					Name: "periodic-rotate-serviceaccount-secrets",
					Spec: &v1.PodSpec{
						Containers: []v1.Container{
							{
								Name: "container-1",
								Env: []v1.EnvVar{
									{
										Name: Kubeconfig,
										Value: fmt.Sprintf(":/etc/build-farm-credentials/%s",
											serviceAccountKubeconfigPath(ConfigUpdater, string(api.ClusterBuild01))),
									},
								}}}}}},
			expected: prowconfig.Periodic{
				JobBase: prowconfig.JobBase{
					Name: "periodic-rotate-serviceaccount-secrets",
					Spec: &v1.PodSpec{
						Containers: []v1.Container{
							{
								Name: "container-1",
								Env: []v1.EnvVar{
									{
										Name: Kubeconfig,
										Value: fmt.Sprintf(":/etc/build-farm-credentials/%s:/etc/build-farm-credentials/%s",
											serviceAccountKubeconfigPath(ConfigUpdater, string(api.ClusterBuild01)),
											serviceAccountKubeconfigPath(ConfigUpdater, "newcluster")),
									},
								}}}}}},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if err := appendNewClustersConfigUpdaterToKubeconfig(&tc.input, tc.containerName, tc.clusterName); err != nil {
				t.Fatalf("unexpected error encountered: %v", err)
			}
			if diff := cmp.Diff(tc.expected, tc.input, cmp.AllowUnexported(prowconfig.Periodic{})); diff != "" {
				t.Fatalf("expected periodic was different than result: %s", diff)
			}
		})
	}
}

func TestAppendBuildFarmCredentialSecret(t *testing.T) {
	testCases := []struct {
		name        string
		clusterName string
		input       prowconfig.Periodic
		expected    prowconfig.Periodic
	}{
		{
			name:        "basic",
			clusterName: "newcluster",
			input: prowconfig.Periodic{
				JobBase: prowconfig.JobBase{
					Name: "periodic-ci-secret-generator",
					Spec: &v1.PodSpec{
						Volumes: []v1.Volume{
							{
								Name: "build-farm-credentials",
								VolumeSource: v1.VolumeSource{
									Secret: &v1.SecretVolumeSource{
										SecretName: "secret",
										Items: []v1.KeyToPath{
											{
												Key:  serviceAccountKubeconfigPath(ConfigUpdater, string(api.ClusterBuild01)),
												Path: serviceAccountKubeconfigPath(ConfigUpdater, string(api.ClusterBuild01)),
											},
										}}}}}}}},
			expected: prowconfig.Periodic{
				JobBase: prowconfig.JobBase{
					Name: "periodic-ci-secret-generator",
					Spec: &v1.PodSpec{
						Volumes: []v1.Volume{
							{
								Name: "build-farm-credentials",
								VolumeSource: v1.VolumeSource{
									Secret: &v1.SecretVolumeSource{
										SecretName: "secret",
										Items: []v1.KeyToPath{
											{
												Key:  serviceAccountKubeconfigPath(ConfigUpdater, string(api.ClusterBuild01)),
												Path: serviceAccountKubeconfigPath(ConfigUpdater, string(api.ClusterBuild01)),
											},
											{
												Key:  serviceAccountKubeconfigPath(ConfigUpdater, "newcluster"),
												Path: serviceAccountKubeconfigPath(ConfigUpdater, "newcluster"),
											},
										}}}}}}}},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if err := appendBuildFarmCredentialSecret(&tc.input, tc.clusterName); err != nil {
				t.Fatalf("unexpected error encountered: %v", err)
			}
			if diff := cmp.Diff(tc.expected, tc.input, cmp.AllowUnexported(prowconfig.Periodic{})); diff != "" {
				t.Fatalf("expected periodic was different than result: %s", diff)
			}
		})
	}
}

func TestFindPeriodic(t *testing.T) {
	testCases := []struct {
		name             string
		ip               InfraPeriodics
		periodicName     string
		expectedPeriodic *prowconfig.Periodic
		expectedError    error
	}{
		{
			name: "exists",
			ip: InfraPeriodics{
				Periodics: []prowconfig.Periodic{
					{
						JobBase: prowconfig.JobBase{Name: "per-0"},
					},
					{
						JobBase: prowconfig.JobBase{Name: "per-a"},
					},
					{
						JobBase: prowconfig.JobBase{Name: "per-b"},
					},
				},
			},
			periodicName: "per-a",
			expectedPeriodic: &prowconfig.Periodic{
				JobBase: prowconfig.JobBase{Name: "per-a"},
			},
		},
		{
			name: "does not exist",
			ip: InfraPeriodics{
				Periodics: []prowconfig.Periodic{
					{
						JobBase: prowconfig.JobBase{Name: "per-0"},
					},
					{
						JobBase: prowconfig.JobBase{Name: "per-a"},
					},
					{
						JobBase: prowconfig.JobBase{Name: "per-b"},
					},
				},
			},
			periodicName:  "per-c",
			expectedError: errors.New("couldn't find periodic with name: per-c"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			periodic, err := findPeriodic(&tc.ip, tc.periodicName)
			if diff := cmp.Diff(tc.expectedError, err, testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("expectedError doesn't match err, diff: %s", diff)
			}
			if diff := cmp.Diff(tc.expectedPeriodic, periodic, cmp.AllowUnexported(prowconfig.Periodic{})); diff != "" {
				t.Fatalf("expectedPeriodic doesn't match periodic, diff: %s", diff)
			}
		})
	}
}

func TestFindContainer(t *testing.T) {
	testCases := []struct {
		name              string
		ps                v1.PodSpec
		containerName     string
		expectedContainer *v1.Container
		expectedError     error
	}{
		{
			name: "exists",
			ps: v1.PodSpec{
				Containers: []v1.Container{
					{Name: "container-0"},
					{Name: "container-a"},
					{Name: "container-z"},
				},
			},
			containerName:     "container-a",
			expectedContainer: &v1.Container{Name: "container-a"},
		},
		{
			name: "does not exist",
			ps: v1.PodSpec{
				Containers: []v1.Container{
					{Name: "container-0"},
					{Name: "container-a"},
					{Name: "container-z"},
				},
			},
			containerName: "container-c",
			expectedError: errors.New("couldn't find Container with name: container-c"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			container, err := findContainer(&tc.ps, tc.containerName)
			if diff := cmp.Diff(tc.expectedError, err, testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("expectedError doesn't match err, diff: %s", diff)
			}
			if diff := cmp.Diff(tc.expectedContainer, container); diff != "" {
				t.Fatalf("expectedContainer doesn't match container, diff: %s", diff)
			}
		})
	}
}

func TestFindEnv(t *testing.T) {
	testCases := []struct {
		name          string
		container     v1.Container
		envName       string
		expectedEnv   *v1.EnvVar
		expectedError error
	}{
		{
			name: "exists",
			container: v1.Container{Env: []v1.EnvVar{
				{Name: "env-0"},
				{Name: "env-1"},
				{Name: "env-a"},
			}},
			envName:     "env-1",
			expectedEnv: &v1.EnvVar{Name: "env-1"},
		},
		{
			name: "does not exist",
			container: v1.Container{Env: []v1.EnvVar{
				{Name: "env-0"},
				{Name: "env-1"},
				{Name: "env-a"},
			}},
			envName:       "env-c",
			expectedError: errors.New("couldn't find Env with name: env-c"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			env, err := findEnv(&tc.container, tc.envName)
			if diff := cmp.Diff(tc.expectedError, err, testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("expectedError doesn't match err, diff: %s", diff)
			}
			if diff := cmp.Diff(tc.expectedEnv, env); diff != "" {
				t.Fatalf("expectedEnv doesn't match env, diff: %s", diff)
			}
		})
	}
}

func TestFindVolume(t *testing.T) {
	testCases := []struct {
		name           string
		ps             v1.PodSpec
		volumeName     string
		expectedVolume *v1.Volume
		expectedError  error
	}{
		{
			name: "exists",
			ps: v1.PodSpec{
				Volumes: []v1.Volume{
					{Name: "vol-a"},
					{Name: "vol-2"},
					{Name: "vol-z"},
				},
			},
			volumeName:     "vol-2",
			expectedVolume: &v1.Volume{Name: "vol-2"},
		},
		{
			name: "does not exist",
			ps: v1.PodSpec{
				Volumes: []v1.Volume{
					{Name: "vol-a"},
					{Name: "vol-2"},
					{Name: "vol-z"},
				},
			},
			volumeName:    "vol-c",
			expectedError: errors.New("couldn't find Volume with name: vol-c"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			volume, err := findVolume(&tc.ps, tc.volumeName)
			if diff := cmp.Diff(tc.expectedError, err, testhelper.EquateErrorMessage); diff != "" {
				t.Fatalf("expectedError doesn't match err, diff: %s", diff)
			}
			if diff := cmp.Diff(tc.expectedVolume, volume); diff != "" {
				t.Fatalf("expectedVolume doesn't match volume, diff: %s", diff)
			}
		})
	}
}
