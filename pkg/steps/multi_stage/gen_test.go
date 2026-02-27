package multi_stage

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowdapi "sigs.k8s.io/prow/pkg/pod-utils/downwardapi"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestGeneratePods(t *testing.T) {
	jobSpec := func() api.JobSpec {
		js := api.JobSpec{
			Metadata: api.Metadata{
				Org:     "org",
				Repo:    "repo",
				Branch:  "base ref",
				Variant: "variant",
			},
			Target: "target",
			JobSpec: prowdapi.JobSpec{
				Job:       "job",
				BuildID:   "build id",
				ProwJobID: "prow job id",
				Refs: &prowapi.Refs{
					Org:     "org",
					Repo:    "repo",
					BaseRef: "base ref",
					BaseSHA: "base sha",
				},
				Type: "postsubmit",
				DecorationConfig: &prowapi.DecorationConfig{
					Timeout:     &prowapi.Duration{Duration: time.Minute},
					GracePeriod: &prowapi.Duration{Duration: time.Second},
					UtilityImages: &prowapi.UtilityImages{
						Sidecar:    "sidecar",
						Entrypoint: "entrypoint",
					},
				},
			},
		}
		js.SetNamespace("namespace")
		return js
	}

	resourceRequirements := api.ResourceRequirements{
		Requests: api.ResourceList{api.ShmResource: "2G"},
		Limits:   api.ResourceList{api.ShmResource: "2G"},
	}

	for _, tc := range []struct {
		name                      string
		config                    *api.ReleaseBuildConfiguration
		env                       []coreapi.EnvVar
		secretVolumes             []coreapi.Volume
		secretVolumeMounts        []coreapi.VolumeMount
		leaseProxyServerAvailable bool
	}{
		{
			name: "generate pods",
			config: &api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					As: "test",
					MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
						ClusterProfile: api.ClusterProfileAWS,
						Test: []api.LiteralTestStep{{
							As: "step0", From: "src", Commands: "command0",
							Timeout:     &prowapi.Duration{Duration: time.Hour},
							GracePeriod: &prowapi.Duration{Duration: 20 * time.Second},
						}, {
							As:       "step1",
							From:     "image1",
							Commands: "command1",
						}, {
							As: "step2", From: "stable-initial:installer", Commands: "command2", RunAsScript: ptr.To(true),
						}, {
							As: "step3", From: "src", Commands: "command3", DNSConfig: &api.StepDNSConfig{
								Nameservers: []string{"nameserver1", "nameserver2"},
								Searches:    []string{"my.dns.search1", "my.dns.search2"},
							},
						}, {
							As: "step4", From: "src", Commands: "command4", NodeArchitecture: ptr.To(api.NodeArchitectureARM64),
						}, {
							As: "step5", From: "src", Commands: "command5", NodeArchitecture: ptr.To(api.NodeArchitectureAMD64),
						}},
					}},
				},
			},
			env: []coreapi.EnvVar{
				{Name: "RELEASE_IMAGE_INITIAL", Value: "release:initial"},
				{Name: "RELEASE_IMAGE_LATEST", Value: "release:latest"},
				{Name: "LEASED_RESOURCE", Value: "uuid"},
			},
			secretVolumes: []coreapi.Volume{{
				Name:         "secret",
				VolumeSource: coreapi.VolumeSource{Secret: &coreapi.SecretVolumeSource{SecretName: "k8-secret"}},
			}},
			secretVolumeMounts: []coreapi.VolumeMount{{
				Name:      "secret",
				MountPath: "/secret",
			}},
		},
		{
			name: "enable nested podman",
			config: &api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					As: "run podman",
					MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
						Test: []api.LiteralTestStep{{
							As:           "step0",
							From:         "src",
							NestedPodman: true,
							Commands:     "command0",
							Timeout:      &prowapi.Duration{Duration: time.Hour},
							GracePeriod:  &prowapi.Duration{Duration: 20 * time.Second},
						}},
					},
				}},
			},
		},
		{
			name: "lease proxy server available",
			config: &api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					As: "claim-a-lease",
					MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
						Test: []api.LiteralTestStep{{
							As:           "step0",
							From:         "src",
							NestedPodman: true,
							Commands:     "command0",
							Timeout:      &prowapi.Duration{Duration: time.Hour},
							GracePeriod:  &prowapi.Duration{Duration: 20 * time.Second},
						}},
					},
				}},
			},
			leaseProxyServerAvailable: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			js := jobSpec()
			step := newMultiStageTestStep(tc.config.Tests[0], tc.config, nil, nil, &js, nil, "node-name", "", nil, false, nil, tc.leaseProxyServerAvailable)
			step.test[0].Resources = resourceRequirements

			ret, _, err := step.generatePods(tc.config.Tests[0].MultiStageTestConfigurationLiteral.Test, tc.env, tc.secretVolumes, tc.secretVolumeMounts, nil)
			if err != nil {
				t.Fatal(err)
			}

			testhelper.CompareWithFixture(t, ret)
		})
	}
}

func TestGenerateObservers(t *testing.T) {
	config := api.ReleaseBuildConfiguration{
		Tests: []api.TestStepConfiguration{{
			As: "test",
			MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
				ClusterProfile: api.ClusterProfileAWS,
				Test: []api.LiteralTestStep{{
					As: "step0", From: "src", Commands: "command0",
				}},
			}},
		},
	}

	observers := []api.Observer{{
		Name:        "observer0",
		From:        "src",
		Commands:    "command0",
		Timeout:     &prowapi.Duration{Duration: 2 * time.Minute},
		GracePeriod: &prowapi.Duration{Duration: 4 * time.Second},
	}, {
		Name:     "observer1",
		From:     "src",
		Commands: "command1",
	}}
	jobSpec := api.JobSpec{
		Metadata: api.Metadata{
			Org:     "org",
			Repo:    "repo",
			Branch:  "base ref",
			Variant: "variant",
		},
		Target: "target",
		JobSpec: prowdapi.JobSpec{
			Job:       "job",
			BuildID:   "build id",
			ProwJobID: "prow job id",
			Refs: &prowapi.Refs{
				Org:     "org",
				Repo:    "repo",
				BaseRef: "base ref",
				BaseSHA: "base sha",
			},
			Type: "postsubmit",
			DecorationConfig: &prowapi.DecorationConfig{
				Timeout:     &prowapi.Duration{Duration: time.Minute},
				GracePeriod: &prowapi.Duration{Duration: time.Second},
				UtilityImages: &prowapi.UtilityImages{
					Sidecar:    "sidecar",
					Entrypoint: "entrypoint",
				},
			},
		},
	}
	jobSpec.SetNamespace("namespace")
	step := newMultiStageTestStep(config.Tests[0], &config, nil, nil, &jobSpec, nil, "node-name", "", nil, false, nil, false)
	ret, err := step.generateObservers(observers, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	testhelper.CompareWithFixture(t, ret)
}

func TestGeneratePodsEnvironment(t *testing.T) {
	value := "test"
	defValue := "default"
	for _, tc := range []struct {
		name     string
		env      api.TestEnvironment
		test     api.LiteralTestStep
		expected *string
	}{{
		name: "test environment is propagated to the step",
		env:  api.TestEnvironment{"TEST": "test"},
		test: api.LiteralTestStep{
			Environment: []api.StepParameter{{Name: "TEST"}},
		},
		expected: &value,
	}, {
		name: "test environment is not propagated to the step",
		env:  api.TestEnvironment{"TEST": "test"},
		test: api.LiteralTestStep{
			Environment: []api.StepParameter{{Name: "NOT_TEST"}},
		},
	}, {
		name: "default value is overwritten",
		env:  api.TestEnvironment{"TEST": "test"},
		test: api.LiteralTestStep{
			Environment: []api.StepParameter{{
				Name:    "TEST",
				Default: &defValue,
			}},
		},
		expected: &value,
	}, {
		name: "default value is applied",
		test: api.LiteralTestStep{
			Environment: []api.StepParameter{{
				Name:    "TEST",
				Default: &defValue,
			}},
		},
		expected: &defValue,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			jobSpec := api.JobSpec{
				JobSpec: prowdapi.JobSpec{
					Job:       "job",
					BuildID:   "build_id",
					ProwJobID: "prow_job_id",
					Type:      prowapi.PeriodicJob,
					DecorationConfig: &prowapi.DecorationConfig{
						Timeout:     &prowapi.Duration{Duration: time.Minute},
						GracePeriod: &prowapi.Duration{Duration: time.Second},
						UtilityImages: &prowapi.UtilityImages{
							Sidecar:    "sidecar",
							Entrypoint: "entrypoint",
						},
					},
				},
			}
			jobSpec.SetNamespace("ns")
			test := []api.LiteralTestStep{tc.test}
			step := MultiStageTestStep(api.TestStepConfiguration{
				MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
					Test:        test,
					Environment: tc.env,
				},
			}, &api.ReleaseBuildConfiguration{}, nil, nil, &jobSpec, nil, "node-name", "", nil, false, nil, false)
			pods, _, err := step.(*multiStageTestStep).generatePods(test, nil, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			var env *string
			for i, v := range pods[0].Spec.Containers[0].Env {
				if v.Name == "TEST" {
					env = &pods[0].Spec.Containers[0].Env[i].Value
				}
			}
			if !reflect.DeepEqual(env, tc.expected) {
				t.Errorf("incorrect environment:\n%s", diff.ObjectReflectDiff(env, tc.expected))
			}
		})
	}
}

func TestGeneratePodBestEffort(t *testing.T) {
	yes := true
	no := false
	config := api.ReleaseBuildConfiguration{
		Tests: []api.TestStepConfiguration{{
			As: "test",
			MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
				AllowBestEffortPostSteps: &yes,
				Test: []api.LiteralTestStep{{
					As:       "step0",
					From:     "src",
					Commands: "command0",
				}},
				Post: []api.LiteralTestStep{{
					As:         "step1",
					From:       "src",
					Commands:   "command1",
					BestEffort: &yes,
				}, {
					As:         "step2",
					From:       "src",
					Commands:   "command2",
					BestEffort: &no,
				}},
			},
		}},
	}
	jobSpec := api.JobSpec{
		JobSpec: prowdapi.JobSpec{
			Job:       "job",
			BuildID:   "build id",
			ProwJobID: "prow job id",
			Refs: &prowapi.Refs{
				Org:     "org",
				Repo:    "repo",
				BaseRef: "base ref",
				BaseSHA: "base sha",
			},
			Type: "postsubmit",
			DecorationConfig: &prowapi.DecorationConfig{
				Timeout:     &prowapi.Duration{Duration: time.Minute},
				GracePeriod: &prowapi.Duration{Duration: time.Second},
				UtilityImages: &prowapi.UtilityImages{
					Sidecar:    "sidecar",
					Entrypoint: "entrypoint",
				},
			},
		},
	}
	jobSpec.SetNamespace("namespace")
	step := newMultiStageTestStep(config.Tests[0], &config, nil, nil, &jobSpec, nil, "node-name", "", nil, false, nil, false)
	_, bestEffortSteps, err := step.generatePods(config.Tests[0].MultiStageTestConfigurationLiteral.Post, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for pod, bestEffort := range map[string]bool{
		"test-step0": false,
		"test-step1": true,
		"test-step2": false,
	} {
		if actual, expected := bestEffortSteps.Has(pod), bestEffort; actual != expected {
			t.Errorf("didn't check best-effort status of Pod %s correctly, expected %v", pod, bestEffort)
		}
	}
}

func TestAddCredentials(t *testing.T) {
	var testCases = []struct {
		name        string
		credentials []api.CredentialReference
		pod         coreapi.Pod
		expected    coreapi.Pod
	}{
		{
			name:        "none to add",
			credentials: []api.CredentialReference{},
			pod:         coreapi.Pod{},
			expected:    coreapi.Pod{},
		},
		{
			name:        "one to add",
			credentials: []api.CredentialReference{{Namespace: "ns", Name: "name", MountPath: "/tmp"}},
			pod: coreapi.Pod{Spec: coreapi.PodSpec{
				Containers: []coreapi.Container{{VolumeMounts: []coreapi.VolumeMount{}}},
				Volumes:    []coreapi.Volume{},
			}},
			expected: coreapi.Pod{Spec: coreapi.PodSpec{
				Containers: []coreapi.Container{{VolumeMounts: []coreapi.VolumeMount{{Name: "ns-name", MountPath: "/tmp"}}}},
				Volumes:    []coreapi.Volume{{Name: "ns-name", VolumeSource: coreapi.VolumeSource{Secret: &coreapi.SecretVolumeSource{SecretName: "ns-name"}}}},
			}},
		},
		{
			name: "many to add and disambiguate",
			credentials: []api.CredentialReference{
				{Namespace: "ns", Name: "name", MountPath: "/tmp"},
				{Namespace: "other", Name: "name", MountPath: "/tamp"},
			},
			pod: coreapi.Pod{Spec: coreapi.PodSpec{
				Containers: []coreapi.Container{{VolumeMounts: []coreapi.VolumeMount{}}},
				Volumes:    []coreapi.Volume{},
			}},
			expected: coreapi.Pod{Spec: coreapi.PodSpec{
				Containers: []coreapi.Container{{VolumeMounts: []coreapi.VolumeMount{
					{Name: "ns-name", MountPath: "/tmp"},
					{Name: "other-name", MountPath: "/tamp"},
				}}},
				Volumes: []coreapi.Volume{
					{Name: "ns-name", VolumeSource: coreapi.VolumeSource{Secret: &coreapi.SecretVolumeSource{SecretName: "ns-name"}}},
					{Name: "other-name", VolumeSource: coreapi.VolumeSource{Secret: &coreapi.SecretVolumeSource{SecretName: "other-name"}}},
				},
			}},
		},
		{
			name: "dots in volume name are replaced",
			credentials: []api.CredentialReference{
				{Namespace: "ns", Name: "hive-hive-credentials", MountPath: "/tmp"},
			},
			pod: coreapi.Pod{Spec: coreapi.PodSpec{
				Containers: []coreapi.Container{{VolumeMounts: []coreapi.VolumeMount{}}},
				Volumes:    []coreapi.Volume{},
			}},
			expected: coreapi.Pod{Spec: coreapi.PodSpec{
				Containers: []coreapi.Container{{VolumeMounts: []coreapi.VolumeMount{
					{Name: "ns-hive-hive-credentials", MountPath: "/tmp"},
				}}},
				Volumes: []coreapi.Volume{
					{Name: "ns-hive-hive-credentials", VolumeSource: coreapi.VolumeSource{Secret: &coreapi.SecretVolumeSource{SecretName: "ns-hive-hive-credentials"}}},
				},
			}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			addCredentials(testCase.credentials, &testCase.pod, false)
			if !equality.Semantic.DeepEqual(testCase.pod, testCase.expected) {
				t.Errorf("%s: got incorrect Pod: %s", testCase.name, cmp.Diff(testCase.pod, testCase.expected))
			}
		})
	}
}

// sortPodVolumesAndMounts sorts volumes and volume mounts in a pod for deterministic comparison
func sortPodVolumesAndMounts(pod *coreapi.Pod) {
	// Sort volumes by name
	sort.Slice(pod.Spec.Volumes, func(i, j int) bool {
		return pod.Spec.Volumes[i].Name < pod.Spec.Volumes[j].Name
	})

	// Sort volume mounts by name in each container
	for i := range pod.Spec.Containers {
		sort.Slice(pod.Spec.Containers[i].VolumeMounts, func(j, k int) bool {
			return pod.Spec.Containers[i].VolumeMounts[j].Name < pod.Spec.Containers[i].VolumeMounts[k].Name
		})
	}
}

func TestAddCSICredentials(t *testing.T) {
	readOnly := true
	var testCases = []struct {
		name        string
		credentials []api.CredentialReference
		pod         coreapi.Pod
		expected    coreapi.Pod
	}{
		{
			name:        "none to add",
			credentials: []api.CredentialReference{},
			pod:         coreapi.Pod{},
			expected:    coreapi.Pod{},
		},
		{
			name:        "one to add",
			credentials: []api.CredentialReference{{Collection: "ns", Group: "default", Name: "name", MountPath: "/tmp"}},
			pod: coreapi.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "pod-ns",
				},
				Spec: coreapi.PodSpec{
					Containers: []coreapi.Container{{VolumeMounts: []coreapi.VolumeMount{}}},
					Volumes:    []coreapi.Volume{},
				},
			},
			expected: coreapi.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "pod-ns",
				},
				Spec: coreapi.PodSpec{
					Containers: []coreapi.Container{
						{
							VolumeMounts: []coreapi.VolumeMount{
								{
									Name: getCSIVolumeName("pod-ns", []api.CredentialReference{{Collection: "ns", Group: "default", Name: "name", MountPath: "/tmp"}}), MountPath: "/tmp",
								},
							},
						},
					},
					Volumes: []coreapi.Volume{
						{
							Name: getCSIVolumeName("pod-ns", []api.CredentialReference{{Collection: "ns", Group: "default", Name: "name", MountPath: "/tmp"}}),
							VolumeSource: coreapi.VolumeSource{
								CSI: &coreapi.CSIVolumeSource{
									Driver:   "secrets-store.csi.k8s.io",
									ReadOnly: &readOnly,
									VolumeAttributes: map[string]string{
										"secretProviderClass": getSPCName("pod-ns", []api.CredentialReference{{Collection: "ns", Group: "default", Name: "name", MountPath: "/tmp"}}),
									},
								},
							},
						},
					},
				}},
		},
		{
			name: "many to add and disambiguate",
			credentials: []api.CredentialReference{
				{Collection: "ns", Group: "default", Name: "name1", MountPath: "/tmp"},
				{Collection: "other", Group: "default", Name: "name2", MountPath: "/tamp"},
			},
			pod: coreapi.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "pod-ns",
				},
				Spec: coreapi.PodSpec{
					Containers: []coreapi.Container{{VolumeMounts: []coreapi.VolumeMount{}}},
					Volumes:    []coreapi.Volume{},
				},
			},
			expected: coreapi.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "pod-ns",
				},
				Spec: coreapi.PodSpec{
					Containers: []coreapi.Container{
						{
							VolumeMounts: []coreapi.VolumeMount{
								{
									Name: getCSIVolumeName("pod-ns", []api.CredentialReference{{Collection: "ns", Group: "default", Name: "name1", MountPath: "/tmp"}}), MountPath: "/tmp"},
								{
									Name: getCSIVolumeName("pod-ns", []api.CredentialReference{{Collection: "other", Group: "default", Name: "name2", MountPath: "/tamp"}}), MountPath: "/tamp"},
							},
						},
					},
					Volumes: []coreapi.Volume{
						{
							Name: getCSIVolumeName("pod-ns", []api.CredentialReference{{Collection: "ns", Group: "default", Name: "name1", MountPath: "/tmp"}}),
							VolumeSource: coreapi.VolumeSource{
								CSI: &coreapi.CSIVolumeSource{
									Driver:   "secrets-store.csi.k8s.io",
									ReadOnly: &readOnly,
									VolumeAttributes: map[string]string{
										"secretProviderClass": getSPCName("pod-ns", []api.CredentialReference{{Collection: "ns", Group: "default", Name: "name1", MountPath: "/tmp"}}),
									},
								},
							},
						},
						{
							Name: getCSIVolumeName("pod-ns", []api.CredentialReference{{Collection: "other", Group: "default", Name: "name2", MountPath: "/tamp"}}),
							VolumeSource: coreapi.VolumeSource{
								CSI: &coreapi.CSIVolumeSource{
									Driver:   "secrets-store.csi.k8s.io",
									ReadOnly: &readOnly,
									VolumeAttributes: map[string]string{
										"secretProviderClass": getSPCName("pod-ns", []api.CredentialReference{{Collection: "other", Group: "default", Name: "name2", MountPath: "/tamp"}}),
									},
								},
							},
						},
					},
				}},
		},
		{
			name: "dots in volume name are replaced",
			credentials: []api.CredentialReference{
				{Collection: "test-ns", Group: "default", Name: "hive-hive-credentials", MountPath: "/tmp"},
			},
			pod: coreapi.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "pod-ns",
				},
				Spec: coreapi.PodSpec{
					Containers: []coreapi.Container{{VolumeMounts: []coreapi.VolumeMount{}}},
					Volumes:    []coreapi.Volume{},
				},
			},
			expected: coreapi.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "pod-ns",
				},
				Spec: coreapi.PodSpec{
					Containers: []coreapi.Container{{VolumeMounts: []coreapi.VolumeMount{
						{Name: getCSIVolumeName("pod-ns", []api.CredentialReference{{Collection: "test-ns", Group: "default", Name: "hive-hive-credentials", MountPath: "/tmp"}}), MountPath: "/tmp"},
					}}},
					Volumes: []coreapi.Volume{
						{
							Name: getCSIVolumeName("pod-ns", []api.CredentialReference{{Collection: "test-ns", Group: "default", Name: "hive-hive-credentials", MountPath: "/tmp"}}),
							VolumeSource: coreapi.VolumeSource{
								CSI: &coreapi.CSIVolumeSource{
									Driver:   "secrets-store.csi.k8s.io",
									ReadOnly: &readOnly,
									VolumeAttributes: map[string]string{
										"secretProviderClass": getSPCName("pod-ns", []api.CredentialReference{{Collection: "test-ns", Group: "default", Name: "hive-hive-credentials", MountPath: "/tmp"}}),
									},
								},
							},
						},
					},
				}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			addCredentials(testCase.credentials, &testCase.pod, true)

			// Sort volumes and mounts for deterministic comparison
			sortPodVolumesAndMounts(&testCase.pod)
			sortPodVolumesAndMounts(&testCase.expected)

			if !equality.Semantic.DeepEqual(testCase.pod, testCase.expected) {
				t.Errorf("%s: got incorrect Pod: %s", testCase.name, cmp.Diff(testCase.pod, testCase.expected))
			}
		})
	}
}

func TestGetClusterClaimPodParams(t *testing.T) {
	var testCases = []struct {
		name               string
		secretVolumeMounts []coreapi.VolumeMount
		expectedEnv        []coreapi.EnvVar
		expectedMount      []coreapi.VolumeMount
		expectedError      error
	}{
		{
			name: "basic case",
			secretVolumeMounts: []coreapi.VolumeMount{
				{
					Name:      "censor-as-hive-admin-kubeconfig",
					MountPath: "/secrets/as-hive-admin-kubeconfig",
				},
				{
					Name:      "censor-as-hive-admin-password",
					MountPath: "/secrets/as-hive-admin-password",
				},
			},
			expectedEnv: []coreapi.EnvVar{
				{Name: "KUBECONFIG", Value: "/secrets/as-hive-admin-kubeconfig/kubeconfig"},
				{Name: "KUBEADMIN_PASSWORD_FILE", Value: "/secrets/as-hive-admin-password/password"},
			},
			expectedMount: []coreapi.VolumeMount{
				{Name: "censor-as-hive-admin-kubeconfig", MountPath: "/secrets/as-hive-admin-kubeconfig"},
				{Name: "censor-as-hive-admin-password", MountPath: "/secrets/as-hive-admin-password"},
			},
		},
		{
			name: "missing a secretVolumeMount",
			secretVolumeMounts: []coreapi.VolumeMount{
				{
					Name:      "censor-as-hive-admin-kubeconfig",
					MountPath: "/secrets/as-hive-admin-kubeconfig",
				},
			},
			expectedError: utilerrors.NewAggregate([]error{fmt.Errorf("failed to find foundMountPath /secrets/as-hive-admin-password to create secret as-hive-admin-password")}),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualEnv, actualMount, actualError := getClusterClaimPodParams(tc.secretVolumeMounts, "as")
			if diff := cmp.Diff(tc.expectedEnv, actualEnv); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expectedMount, actualMount); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expectedError, actualError, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
		})
	}
}

func TestSetSecurityContexts(t *testing.T) {
	for _, tc := range []struct {
		name, root string
		pod        coreapi.Pod
		expected   sets.Set[string]
	}{{
		name: "empty",
	}, {
		name: "no match",
		pod: coreapi.Pod{
			Spec: coreapi.PodSpec{
				InitContainers: []coreapi.Container{{Name: "i0"}, {Name: "i1"}},
				Containers:     []coreapi.Container{{Name: "c0"}, {Name: "c1"}},
			},
		},
	}, {
		name: "match",
		pod: coreapi.Pod{
			Spec: coreapi.PodSpec{
				InitContainers: []coreapi.Container{{Name: "i0"}, {Name: "i1"}},
				Containers:     []coreapi.Container{{Name: "c0"}, {Name: "c1"}},
			},
		},
		root: "c1",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			const uid = 1007160000
			var capabilities coreapi.Capabilities
			var seLinuxOpts coreapi.SELinuxOptions
			pod := &tc.pod
			setSecurityContexts(pod, tc.root, uid, &capabilities, &seLinuxOpts)
			testhelper.CompareWithFixture(t, pod)
		})
	}
}
