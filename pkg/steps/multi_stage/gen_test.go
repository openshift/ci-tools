package multi_stage

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/diff"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	prowdapi "sigs.k8s.io/prow/pkg/pod-utils/downwardapi"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/steps/csi_secrets"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	"github.com/openshift/ci-tools/pkg/testhelper"
	testhelper_kube "github.com/openshift/ci-tools/pkg/testhelper/kubernetes"
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
		paramsFunc                func() api.Parameters
		stsHomeRoleARN            string
		stsHubRoleARN             string
		stsTargetRoleARN          string
	}{
		{
			name: "generate pods",
			config: &api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					As: "test",
					MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
						ClusterProfileLiteral: &api.ClusterProfileLiteral{
							Name:        "aws",
							ClusterType: "aws",
							LeaseType:   "aws-quota-slice",
							Secret:      "cluster-secrets-aws",
						},
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
			paramsFunc: func() api.Parameters {
				params := api.NewDeferredParameters(nil)
				params.Add(api.ClusterProfileParam, func() (any, error) {
					return &api.ClusterProfile{Secret: "cluster-secrets-aws-5"}, nil
				})
				return params
			},
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
		{
			name: "STS volumes",
			config: &api.ReleaseBuildConfiguration{
				Tests: []api.TestStepConfiguration{{
					As: "e2e-aws",
					MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
						ClusterProfileLiteral: &api.ClusterProfileLiteral{
							Name:        "aws",
							ClusterType: "aws",
							LeaseType:   "aws-quota-slice",
							Secret:      "cluster-secrets-aws",
						},
						Test: []api.LiteralTestStep{{
							As: "step0", From: "src", Commands: "command0",
							Timeout:     &prowapi.Duration{Duration: time.Hour},
							GracePeriod: &prowapi.Duration{Duration: 20 * time.Second},
						}},
					}},
				},
			},
			env: []coreapi.EnvVar{
				{Name: "RELEASE_IMAGE_INITIAL", Value: "release:initial"},
				{Name: "RELEASE_IMAGE_LATEST", Value: "release:latest"},
				{Name: "LEASED_RESOURCE", Value: "uuid"},
			},
			paramsFunc: func() api.Parameters {
				params := api.NewDeferredParameters(nil)
				params.Add(api.ClusterProfileParam, func() (any, error) {
					return &api.ClusterProfile{Secret: "cluster-secrets-aws-5"}, nil
				})
				return params
			},
			stsHomeRoleARN:   "arn:aws:iam::000000000000:role/ci-step-runner",
			stsHubRoleARN:    "arn:aws:iam::111111111111:role/ci-step-runner-hub",
			stsTargetRoleARN: "arn:aws:iam::222222222222:role/ci-step-runner-target",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var params api.Parameters = api.NewDeferredParameters(nil)
			if tc.paramsFunc != nil {
				params = tc.paramsFunc()
			}

			js := jobSpec()
			step := newMultiStageTestStep(tc.config.Tests[0], tc.config, params, nil, &js, nil, "node-name", "", nil, false, nil, tc.leaseProxyServerAvailable, wait.Backoff{})
			step.stsHomeRoleARN = tc.stsHomeRoleARN
			step.stsHubRoleARN = tc.stsHubRoleARN
			step.stsTargetRoleARN = tc.stsTargetRoleARN
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
				ClusterProfileLiteral: &api.ClusterProfileLiteral{
					Name:        "aws",
					ClusterType: "aws",
					LeaseType:   "aws-quota-slice",
					Secret:      "cluster-secrets-aws",
				},
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
	step := newMultiStageTestStep(config.Tests[0], &config, nil, nil, &jobSpec, nil, "node-name", "", nil, false, nil, false, wait.Backoff{})
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
			}, &api.ReleaseBuildConfiguration{}, fakeStepParams{}, nil, &jobSpec, nil, "node-name", "", nil, false, nil, false, wait.Backoff{})
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
				t.Errorf("incorrect environment:\n%s", diff.Diff(env, tc.expected))
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
	step := newMultiStageTestStep(config.Tests[0], &config, nil, nil, &jobSpec, nil, "node-name", "", nil, false, nil, false, wait.Backoff{})
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
			credentials: []api.CredentialReference{{Collection: "ns", Group: "default", Field: "field1", MountPath: "/tmp"}},
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
									Name: csi_secrets.GetCSIVolumeName("pod-ns", []api.CredentialReference{{Collection: "ns", Group: "default", Field: "field1", MountPath: "/tmp"}}), MountPath: "/tmp",
								},
							},
						},
					},
					Volumes: []coreapi.Volume{
						{
							Name: csi_secrets.GetCSIVolumeName("pod-ns", []api.CredentialReference{{Collection: "ns", Group: "default", Field: "field1", MountPath: "/tmp"}}),
							VolumeSource: coreapi.VolumeSource{
								CSI: &coreapi.CSIVolumeSource{
									Driver:   "secrets-store.csi.k8s.io",
									ReadOnly: &readOnly,
									VolumeAttributes: map[string]string{
										"secretProviderClass": csi_secrets.GetSPCName("pod-ns", []api.CredentialReference{{Collection: "ns", Group: "default", Field: "field1", MountPath: "/tmp"}}),
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
				{Collection: "ns", Group: "default", Field: "field1", MountPath: "/tmp"},
				{Collection: "other", Group: "default", Field: "field2", MountPath: "/tamp"},
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
									Name: csi_secrets.GetCSIVolumeName("pod-ns", []api.CredentialReference{{Collection: "ns", Group: "default", Field: "field1", MountPath: "/tmp"}}), MountPath: "/tmp"},
								{
									Name: csi_secrets.GetCSIVolumeName("pod-ns", []api.CredentialReference{{Collection: "other", Group: "default", Field: "field2", MountPath: "/tamp"}}), MountPath: "/tamp"},
							},
						},
					},
					Volumes: []coreapi.Volume{
						{
							Name: csi_secrets.GetCSIVolumeName("pod-ns", []api.CredentialReference{{Collection: "ns", Group: "default", Field: "field1", MountPath: "/tmp"}}),
							VolumeSource: coreapi.VolumeSource{
								CSI: &coreapi.CSIVolumeSource{
									Driver:   "secrets-store.csi.k8s.io",
									ReadOnly: &readOnly,
									VolumeAttributes: map[string]string{
										"secretProviderClass": csi_secrets.GetSPCName("pod-ns", []api.CredentialReference{{Collection: "ns", Group: "default", Field: "field1", MountPath: "/tmp"}}),
									},
								},
							},
						},
						{
							Name: csi_secrets.GetCSIVolumeName("pod-ns", []api.CredentialReference{{Collection: "other", Group: "default", Field: "field2", MountPath: "/tamp"}}),
							VolumeSource: coreapi.VolumeSource{
								CSI: &coreapi.CSIVolumeSource{
									Driver:   "secrets-store.csi.k8s.io",
									ReadOnly: &readOnly,
									VolumeAttributes: map[string]string{
										"secretProviderClass": csi_secrets.GetSPCName("pod-ns", []api.CredentialReference{{Collection: "other", Group: "default", Field: "field2", MountPath: "/tamp"}}),
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
				{Collection: "test-ns", Group: "default", Field: "hive-hive-credentials", MountPath: "/tmp"},
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
						{Name: csi_secrets.GetCSIVolumeName("pod-ns", []api.CredentialReference{{Collection: "test-ns", Group: "default", Field: "hive-hive-credentials", MountPath: "/tmp"}}), MountPath: "/tmp"},
					}}},
					Volumes: []coreapi.Volume{
						{
							Name: csi_secrets.GetCSIVolumeName("pod-ns", []api.CredentialReference{{Collection: "test-ns", Group: "default", Field: "hive-hive-credentials", MountPath: "/tmp"}}),
							VolumeSource: coreapi.VolumeSource{
								CSI: &coreapi.CSIVolumeSource{
									Driver:   "secrets-store.csi.k8s.io",
									ReadOnly: &readOnly,
									VolumeAttributes: map[string]string{
										"secretProviderClass": csi_secrets.GetSPCName("pod-ns", []api.CredentialReference{{Collection: "test-ns", Group: "default", Field: "hive-hive-credentials", MountPath: "/tmp"}}),
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

func TestDependencyVolumeNames(t *testing.T) {
	for _, names := range [][]string{
		{"installer", "installer"},
		{"stable:installer", "stable-installer"},
		{"My_Image", "release.latest"},
		{strings.Repeat("a", 100) + "-one", strings.Repeat("a", 100) + "-two"},
	} {
		t.Run(strings.Join(names, ","), func(t *testing.T) {
			s := &multiStageTestStep{}
			_, volumes, mounts, errs := s.resourcesForDependencies(api.LiteralTestStep{Dependencies: []api.StepDependency{
				{Name: names[0], Env: "FIRST", PullSpec: "registry.example.com/first:latest", MountPath: "/first"},
				{Name: names[1], Env: "SECOND", PullSpec: "registry.example.com/second:latest", MountPath: "/second"},
			}})
			if len(errs) != 0 {
				t.Fatal(errs)
			}
			pod := &coreapi.Pod{Spec: coreapi.PodSpec{Containers: []coreapi.Container{{Name: containerName}}}}
			if err := addDependencyVolumes(pod, &pod.Spec.Containers[0], volumes, mounts); err != nil {
				t.Fatal(err)
			}
			if len(pod.Spec.Volumes) != 2 || len(pod.Spec.Containers[0].VolumeMounts) != 2 {
				t.Fatalf("expected two volumes and mounts, got %+v", pod.Spec)
			}
			if pod.Spec.Volumes[0].Name == pod.Spec.Volumes[1].Name {
				t.Fatal("dependency volumes have duplicate names")
			}
			for i, volume := range pod.Spec.Volumes {
				if errs := validation.IsDNS1123Label(volume.Name); len(errs) != 0 {
					t.Errorf("invalid volume name %q: %v", volume.Name, errs)
				}
				if mount := pod.Spec.Containers[0].VolumeMounts[i]; mount.Name != volume.Name || mount.MountPath != mounts[i].MountPath {
					t.Errorf("volume %q has incorrect mount %+v", volume.Name, mount)
				}
			}
		})
	}
}

func TestDependencyImageVolumes(t *testing.T) {
	testCases := []struct {
		name            string
		dependencies    []api.StepDependency
		imageStream     *imagev1.ImageStream
		credentials     []api.CredentialReference
		expectedEnv     []coreapi.EnvVar
		expectedVolumes []coreapi.Volume
		expectedMounts  []coreapi.VolumeMount
		expectedError   string
	}{
		{
			name: "no mount_path produces no volumes",
			dependencies: []api.StepDependency{
				{Name: "src", Env: "SOURCE", PullSpec: "registry.example.com/src@sha256:abc123"},
			},
			expectedEnv: []coreapi.EnvVar{{Name: "SOURCE", Value: "registry.example.com/src@sha256:abc123"}},
		},
		{
			name: "mount_path produces image volume",
			dependencies: []api.StepDependency{
				{Name: "installer", Env: "INSTALLER_IMAGE", PullSpec: "registry.example.com/installer@sha256:abc123", MountPath: "/var/run/images/installer"},
			},
			expectedEnv: []coreapi.EnvVar{{Name: "INSTALLER_IMAGE", Value: "registry.example.com/installer@sha256:abc123"}},
			expectedVolumes: []coreapi.Volume{{
				Name: "dependency-0",
				VolumeSource: coreapi.VolumeSource{
					Image: &coreapi.ImageVolumeSource{
						Reference:  "registry.example.com/installer@sha256:abc123",
						PullPolicy: coreapi.PullIfNotPresent,
					},
				},
			}},
			expectedMounts: []coreapi.VolumeMount{{
				Name:      "dependency-0",
				MountPath: "/var/run/images/installer",
				ReadOnly:  true,
			}},
		},
		{
			name: "mixed dependencies with and without mount_path",
			dependencies: []api.StepDependency{
				{Name: "src", Env: "SOURCE", PullSpec: "registry.example.com/src@sha256:abc123"},
				{Name: "stable:installer", Env: "INSTALLER", PullSpec: "registry.example.com/installer@sha256:def456", MountPath: "/opt/installer"},
			},
			expectedEnv: []coreapi.EnvVar{
				{Name: "SOURCE", Value: "registry.example.com/src@sha256:abc123"},
				{Name: "INSTALLER", Value: "registry.example.com/installer@sha256:def456"},
			},
			expectedVolumes: []coreapi.Volume{{
				Name: "dependency-0",
				VolumeSource: coreapi.VolumeSource{
					Image: &coreapi.ImageVolumeSource{
						Reference:  "registry.example.com/installer@sha256:def456",
						PullPolicy: coreapi.PullIfNotPresent,
					},
				},
			}},
			expectedMounts: []coreapi.VolumeMount{{
				Name:      "dependency-0",
				MountPath: "/opt/installer",
				ReadOnly:  true,
			}},
		},
		{
			name:         "image stream dependency resolves the same digest for env and volume",
			dependencies: []api.StepDependency{{Name: "stable:installer", Env: "INSTALLER", MountPath: "/opt/installer"}},
			imageStream: &imagev1.ImageStream{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "stable"},
				Status: imagev1.ImageStreamStatus{
					PublicDockerImageRepository: "registry.example.com/stable",
					Tags:                        []imagev1.NamedTagEventList{{Tag: "installer", Items: []imagev1.TagEvent{{Image: "sha256:def456"}}}},
				},
			},
			expectedEnv: []coreapi.EnvVar{{Name: "INSTALLER", Value: "registry.example.com/stable@sha256:def456"}},
			expectedVolumes: []coreapi.Volume{{
				Name: "dependency-0",
				VolumeSource: coreapi.VolumeSource{Image: &coreapi.ImageVolumeSource{
					Reference: "registry.example.com/stable@sha256:def456", PullPolicy: coreapi.PullIfNotPresent,
				}},
			}},
			expectedMounts: []coreapi.VolumeMount{{Name: "dependency-0", MountPath: "/opt/installer", ReadOnly: true}},
		},
		{
			name:         "mutable pull spec is refreshed and existing credential volume name is avoided",
			dependencies: []api.StepDependency{{Name: "installer", Env: "INSTALLER", PullSpec: "registry.example.com/installer:latest", MountPath: "/opt/installer"}},
			credentials:  []api.CredentialReference{{Namespace: "dependency", Name: "0", MountPath: "/credentials"}},
			expectedEnv:  []coreapi.EnvVar{{Name: "INSTALLER", Value: "registry.example.com/installer:latest"}},
			expectedVolumes: []coreapi.Volume{{
				Name: "dependency-1",
				VolumeSource: coreapi.VolumeSource{Image: &coreapi.ImageVolumeSource{
					Reference: "registry.example.com/installer:latest", PullPolicy: coreapi.PullAlways,
				}},
			}},
			expectedMounts: []coreapi.VolumeMount{{Name: "dependency-1", MountPath: "/opt/installer", ReadOnly: true}},
		},
		{
			name:         "existing built-in environment precedence is preserved",
			dependencies: []api.StepDependency{{Name: "src", Env: SecretMountEnv, PullSpec: "registry.example.com/src:latest"}},
			expectedEnv: []coreapi.EnvVar{
				{Name: SecretMountEnv, Value: "registry.example.com/src:latest"},
				{Name: SecretMountEnv, Value: SecretMountPath},
			},
		},
		{
			name:          "image resolution error retains the underlying cause",
			dependencies:  []api.StepDependency{{Name: "stable:installer", Env: "INSTALLER", MountPath: "/opt/installer"}},
			expectedError: `could not retrieve output imagestream: imagestreams.image.openshift.io "stable" not found`,
		},
		{
			name:          "dependency cannot hide a built-in mount",
			dependencies:  []api.StepDependency{{Name: "installer", Env: "INSTALLER", PullSpec: "registry.example.com/installer:latest", MountPath: "/alabama/"}},
			expectedError: `overlaps volume "home" mounted at "/alabama"`,
		},
		{
			name:          "dependency cannot nest under a credential mount",
			dependencies:  []api.StepDependency{{Name: "installer", Env: "INSTALLER", PullSpec: "registry.example.com/installer:latest", MountPath: "/credentials/a..b"}},
			credentials:   []api.CredentialReference{{Namespace: "ns", Name: "secret", MountPath: "/credentials"}},
			expectedError: `overlaps volume "ns-secret" mounted at "/credentials"`,
		},
		{
			name:          "dependency cannot hide a credential mount under its path",
			dependencies:  []api.StepDependency{{Name: "installer", Env: "INSTALLER", PullSpec: "registry.example.com/installer:latest", MountPath: "/credentials"}},
			credentials:   []api.CredentialReference{{Namespace: "ns", Name: "secret", MountPath: "/credentials/token"}},
			expectedError: `overlaps volume "ns-secret" mounted at "/credentials/token"`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			step := api.LiteralTestStep{
				As:           "test",
				From:         "src",
				Dependencies: tc.dependencies,
				Credentials:  tc.credentials,
			}
			jobSpec := api.JobSpec{JobSpec: prowdapi.JobSpec{
				Job: "job", Type: prowapi.PeriodicJob,
				DecorationConfig: &prowapi.DecorationConfig{
					UtilityImages: &prowapi.UtilityImages{Entrypoint: "entrypoint", Sidecar: "sidecar"},
				},
			}}
			jobSpec.SetNamespace("ns")
			scheme := runtime.NewScheme()
			if err := imagev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			clientBuilder := fakectrlruntimeclient.NewClientBuilder().WithScheme(scheme)
			if tc.imageStream != nil {
				clientBuilder.WithObjects(tc.imageStream)
			}
			client := &testhelper_kube.FakePodClient{FakePodExecutor: &testhelper_kube.FakePodExecutor{
				LoggingClient: loggingclient.New(clientBuilder.Build(), nil),
			}}
			s := &multiStageTestStep{name: "test", config: &api.ReleaseBuildConfiguration{}, jobSpec: &jobSpec, client: client}
			pods, _, err := s.generatePods([]api.LiteralTestStep{step}, nil, nil, nil, nil)
			if tc.expectedError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.expectedError) {
					t.Fatalf("expected error containing %q, got %v", tc.expectedError, err)
				}
				if len(pods) != 0 {
					t.Fatal("generated a pod for an invalid dependency")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(pods) != 1 {
				t.Fatalf("expected one pod, got %d", len(pods))
			}
			pod := &pods[0]
			var volumes []coreapi.Volume
			volumeNames := sets.New[string]()
			for _, volume := range pod.Spec.Volumes {
				if volumeNames.Has(volume.Name) {
					t.Errorf("duplicate volume name %q", volume.Name)
				}
				volumeNames.Insert(volume.Name)
				if volume.Image != nil {
					volumes = append(volumes, volume)
				}
			}
			var mounts []coreapi.VolumeMount
			for _, mount := range pod.Spec.Containers[0].VolumeMounts {
				for _, volume := range volumes {
					if volume.Name == mount.Name {
						mounts = append(mounts, mount)
					}
				}
			}
			var env []coreapi.EnvVar
			expectedEnvNames := sets.New[string]()
			for _, expected := range tc.expectedEnv {
				expectedEnvNames.Insert(expected.Name)
			}
			for _, actual := range pod.Spec.Containers[0].Env {
				if expectedEnvNames.Has(actual.Name) {
					env = append(env, actual)
				}
			}
			if diff := cmp.Diff(tc.expectedEnv, env); diff != "" {
				t.Errorf("unexpected dependency env (-want +got): %s", diff)
			}
			if !equality.Semantic.DeepEqual(volumes, tc.expectedVolumes) {
				t.Errorf("unexpected volumes: %s", cmp.Diff(tc.expectedVolumes, volumes))
			}
			if !equality.Semantic.DeepEqual(mounts, tc.expectedMounts) {
				t.Errorf("unexpected volume mounts: %s", cmp.Diff(tc.expectedMounts, mounts))
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
