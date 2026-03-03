package multi_stage

import (
	"context"
	"fmt"
	"path"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"

	coreapi "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openshift/ci-tools/pkg/api"
	gsm "github.com/openshift/ci-tools/pkg/gsm-secrets"
	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
)

// the multiStageTestStep implements the subStepReporter interface
var _ steps.SubStepReporter = &multiStageTestStep{}

func TestRequires(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		config                    api.ReleaseBuildConfiguration
		steps                     api.MultiStageTestConfigurationLiteral
		clusterClaim              *api.ClusterClaim
		leaseProxyServerAvailable bool
		req                       []api.StepLink
	}{{
		name: "step has a cluster profile and requires a release image, should not have ReleaseImagesLink",
		steps: api.MultiStageTestConfigurationLiteral{
			ClusterProfile: api.ClusterProfileAWS,
			Test:           []api.LiteralTestStep{{From: "from-release"}},
		},
		req: []api.StepLink{
			api.ReleasePayloadImageLink(api.LatestReleaseName),
			api.ImagesReadyLink(),
		},
	}, {
		name: "step needs release images, should have ReleaseImagesLink",
		steps: api.MultiStageTestConfigurationLiteral{
			Test: []api.LiteralTestStep{{From: "from-release"}},
		},
		req: []api.StepLink{
			api.ReleaseImagesLink(api.LatestReleaseName),
		},
	}, {
		name: "step needs images, should have InternalImageLink",
		config: api.ReleaseBuildConfiguration{
			Images: []api.ProjectDirectoryImageBuildStepConfiguration{
				{To: "from-images"},
			},
		},
		steps: api.MultiStageTestConfigurationLiteral{
			Test: []api.LiteralTestStep{{From: "from-images"}},
		},
		req: []api.StepLink{
			api.InternalImageLink("from-images"),
		},
	}, {
		name: "step needs pipeline image, should have InternalImageLink",
		steps: api.MultiStageTestConfigurationLiteral{
			Test: []api.LiteralTestStep{{From: "src"}},
		},
		req: []api.StepLink{
			api.InternalImageLink(api.PipelineImageStreamTagReferenceSource),
		},
	}, {
		name: "step needs pipeline image explicitly, should have InternalImageLink",
		steps: api.MultiStageTestConfigurationLiteral{
			Test: []api.LiteralTestStep{{From: "pipeline:src"}},
		},
		req: []api.StepLink{
			api.InternalImageLink(api.PipelineImageStreamTagReferenceSource),
		},
	}, {
		name:                      "step needs the lease proxy server",
		leaseProxyServerAvailable: true,
		req:                       []api.StepLink{api.LeaseProxyServerLink()},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			step := MultiStageTestStep(api.TestStepConfiguration{
				As:                                 "some-e2e",
				ClusterClaim:                       tc.clusterClaim,
				MultiStageTestConfigurationLiteral: &tc.steps,
			}, &tc.config, api.NewDeferredParameters(nil), nil, nil, nil, "node-name", "", nil, false, nil, tc.leaseProxyServerAvailable)
			ret := step.Requires()
			if len(ret) == len(tc.req) {
				matches := true
				for i := range ret {
					if !ret[i].SatisfiedBy(tc.req[i]) {
						matches = false
						break
					}
				}
				if matches {
					return
				}
			}
			t.Errorf("incorrect requirements: %s", cmp.Diff(ret, tc.req, api.Comparer()))
		})
	}
}

func TestSecretsForCensoring(t *testing.T) {
	// this ends up returning based on alphanumeric sort of names, so name things accordingly
	client := loggingclient.New(
		fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(
			&coreapi.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "target-namespace",
					Name:      "1first",
				},
			},
			&coreapi.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "target-namespace",
					Name:      "2second",
				},
			},
			&coreapi.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "target-namespace",
					Name:      "3third",
				},
			},
			&coreapi.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "target-namespace",
					Name:      "4skipped",
					Labels:    map[string]string{"ci.openshift.io/skip-censoring": "true"},
				},
			},
			&coreapi.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:   "target-namespace",
					Name:        "5sa-secret",
					Annotations: map[string]string{"kubernetes.io/service-account.name": "foo"},
				},
			},
		).Build(), nil,
	)

	volumes, mounts, err := secretsForCensoring(client, "target-namespace", context.Background())
	if err != nil {
		t.Fatalf("got error when listing secrets: %v", err)
	}
	expectedVolumes := []coreapi.Volume{
		{
			Name: "censor-0",
			VolumeSource: coreapi.VolumeSource{
				Secret: &coreapi.SecretVolumeSource{
					SecretName: "1first",
				},
			},
		},
		{
			Name: "censor-1",
			VolumeSource: coreapi.VolumeSource{
				Secret: &coreapi.SecretVolumeSource{
					SecretName: "2second",
				},
			},
		},
		{
			Name: "censor-2",
			VolumeSource: coreapi.VolumeSource{
				Secret: &coreapi.SecretVolumeSource{
					SecretName: "3third",
				},
			},
		},
	}
	if diff := cmp.Diff(volumes, expectedVolumes); diff != "" {
		t.Errorf("got incorrect volumes: %v", diff)
	}

	expectedMounts := []coreapi.VolumeMount{
		{
			Name:      "censor-0",
			MountPath: path.Join("/secrets", "1first"),
		},
		{
			Name:      "censor-1",
			MountPath: path.Join("/secrets", "2second"),
		},
		{
			Name:      "censor-2",
			MountPath: path.Join("/secrets", "3third"),
		},
	}
	if diff := cmp.Diff(mounts, expectedMounts); diff != "" {
		t.Errorf("got incorrect mounts: %v", diff)
	}
}

func TestAddCredentialsToCensoring(t *testing.T) {
	secretVolumes := []coreapi.Volume{
		{
			Name: "censor-0",
		},
	}
	secretVolumeMounts := []coreapi.VolumeMount{
		{
			Name:      "censor-0",
			MountPath: path.Join("/secrets", "1first"),
		},
	}
	credential1 := api.CredentialReference{Collection: "test-collection", Group: "test-group", Field: "key"}
	credential2 := api.CredentialReference{Collection: "another-collection", Group: "another-group", Field: "key"}

	// Helper function to create a volume
	newVolume := func(index int, collection, group, field string) coreapi.Volume {
		readOnly := true
		fullSecretName := gsm.GetGSMSecretName(collection, group, field)
		censorMountPath := getCensorMountPath(fullSecretName)
		individualCredentials := []api.CredentialReference{
			{
				Collection: collection,
				Group:      group,
				Field:      field,
				MountPath:  censorMountPath,
			},
		}

		return coreapi.Volume{
			Name: fmt.Sprintf("censor-cred-%d", index),
			VolumeSource: coreapi.VolumeSource{
				CSI: &coreapi.CSIVolumeSource{
					Driver:   "secrets-store.csi.k8s.io",
					ReadOnly: &readOnly,
					VolumeAttributes: map[string]string{
						"secretProviderClass": getSPCName("test", individualCredentials),
					},
				},
			},
		}
	}
	for _, tc := range []struct {
		name                 string
		credentials          []api.CredentialReference
		pre                  []api.LiteralTestStep
		test                 []api.LiteralTestStep
		post                 []api.LiteralTestStep
		expectedVolumes      []coreapi.Volume
		expectedVolumeMounts []coreapi.VolumeMount
	}{
		{
			name:                 "no credentials",
			expectedVolumes:      secretVolumes,
			expectedVolumeMounts: secretVolumeMounts,
		},
		{
			name:        "one credential",
			credentials: []api.CredentialReference{credential1},
			pre: []api.LiteralTestStep{
				{
					Credentials: []api.CredentialReference{
						credential1,
					},
				},
			},
			expectedVolumes: append(secretVolumes, newVolume(0, credential1.Collection, credential1.Group, credential1.Field)),
			expectedVolumeMounts: append(secretVolumeMounts, coreapi.VolumeMount{
				Name:      "censor-cred-0",
				MountPath: getMountPath(gsm.GetGSMSecretName(credential1.Collection, credential1.Group, credential1.Field)),
			}),
		},
		{
			name: "multiple duplicated credentials",
			test: []api.LiteralTestStep{
				{
					Credentials: []api.CredentialReference{
						credential1,
						credential2,
					},
				},
			},
			post: []api.LiteralTestStep{
				{
					Credentials: []api.CredentialReference{
						credential1,
					},
				},
			},
			expectedVolumes: append(
				secretVolumes,
				newVolume(0, credential1.Collection, credential1.Group, credential1.Field),
				newVolume(1, credential2.Collection, credential2.Group, credential2.Field),
			),
			expectedVolumeMounts: append(secretVolumeMounts, coreapi.VolumeMount{
				Name:      "censor-cred-0",
				MountPath: getMountPath(gsm.GetGSMSecretName(credential1.Collection, credential1.Group, credential1.Field)),
			}, coreapi.VolumeMount{
				Name:      "censor-cred-1",
				MountPath: getMountPath(gsm.GetGSMSecretName(credential2.Collection, credential2.Group, credential2.Field)),
			}),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step := &multiStageTestStep{
				pre:     tc.pre,
				test:    tc.test,
				post:    tc.post,
				jobSpec: &api.JobSpec{},
			}
			secretVolumes := []coreapi.Volume{{Name: "censor-0"}}
			secretVolumeMounts := []coreapi.VolumeMount{{
				Name:      "censor-0",
				MountPath: path.Join("/secrets", "1first"),
			}}
			step.jobSpec.SetNamespace("test")
			secretVolumes, secretVolumeMounts = step.addCredentialsToCensoring(secretVolumes, secretVolumeMounts)
			if diff := cmp.Diff(secretVolumes, tc.expectedVolumes); diff != "" {
				t.Errorf("got incorrect volumes: %v", diff)
			}
			if diff := cmp.Diff(secretVolumeMounts, tc.expectedVolumeMounts); diff != "" {
				t.Errorf("got incorrect mounts: %v", diff)
			}
		})
	}
}

type fakeStepParams map[string]string

func (f fakeStepParams) Has(key string) bool {
	_, ok := f[key]
	return ok
}

func (f fakeStepParams) HasInput(_ string) bool {
	panic("This should not be used")
}

func (f fakeStepParams) Get(key string) (string, error) {
	return f[key], nil
}

func TestEnvironment(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		params    api.Parameters
		leases    []api.StepLease
		expected  []coreapi.EnvVar
		expectErr bool
	}{
		{
			name:     "leases are exposed in environment",
			params:   fakeStepParams{"LEASE_ONE": "ONE", "LEASE_TWO": "TWO"},
			leases:   []api.StepLease{{Env: "LEASE_ONE"}, {Env: "LEASE_TWO"}},
			expected: []coreapi.EnvVar{{Name: "LEASE_ONE", Value: "ONE"}, {Name: "LEASE_TWO", Value: "TWO"}},
		},
		{
			name:     "leases proxy server URL is exposed in environment",
			params:   fakeStepParams{api.LeaseProxyServerURLEnvVarName: "http://192.168.1.1"},
			expected: []coreapi.EnvVar{{Name: api.LeaseProxyServerURLEnvVarName, Value: "http://192.168.1.1"}},
		},
		{
			name: "arbitrary variables are not exposed in environment",
			params: fakeStepParams{
				"OO_IMSMART":     "nope",
				"IM_A_POWERUSER": "nope you are not",
			},
		},
		{
			name: "ORIGINAL_* variables are exposed in environment",
			params: fakeStepParams{
				"ORIGINAL_RELEASE_IMAGE_INITIAL": "initial",
				"ORIGINAL_RELEASE_IMAGE_LATEST":  "latest",
			},
			expected: []coreapi.EnvVar{
				{Name: "ORIGINAL_RELEASE_IMAGE_INITIAL", Value: "initial"},
				{Name: "ORIGINAL_RELEASE_IMAGE_LATEST", Value: "latest"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &multiStageTestStep{
				params: tc.params,
				leases: tc.leases,
			}
			got, err := s.environment()
			if (err != nil) != tc.expectErr {
				t.Errorf("environment() error = %v, wantErr %v", err, tc.expectErr)
				return
			}
			sort.Slice(tc.expected, func(i, j int) bool {
				return tc.expected[i].Name < tc.expected[j].Name
			})
			sort.Slice(got, func(i, j int) bool {
				return got[i].Name < got[j].Name
			})
			if diff := cmp.Diff(tc.expected, got); diff != "" {
				t.Errorf("%s: result differs from expected:\n %s", tc.name, diff)
			}
		})
	}
}

func TestProfileSecretName(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name             string
		stepName         string
		additionalSuffix string
		expected         string
	}{
		{
			name:     "no additional suffix",
			stepName: "step",
			expected: "step-cluster-profile",
		},
		{
			name:             "additional suffix",
			stepName:         "step-0",
			additionalSuffix: "0",
			expected:         "step-cluster-profile",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			step := multiStageTestStep{name: tc.stepName, additionalSuffix: tc.additionalSuffix}
			result := step.profileSecretName()
			if diff := cmp.Diff(tc.expected, result); diff != "" {
				t.Fatalf("result does not match expected, diff: %s", diff)
			}
		})
	}
}
