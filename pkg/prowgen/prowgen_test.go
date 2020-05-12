package prowgen

import (
	"io/ioutil"
	"log"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"

	ciop "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/jobconfig"
)

var unexportedFields = []cmp.Option{
	cmpopts.IgnoreUnexported(prowconfig.Presubmit{}),
	cmpopts.IgnoreUnexported(prowconfig.Periodic{}),
	cmpopts.IgnoreUnexported(prowconfig.Brancher{}),
	cmpopts.IgnoreUnexported(prowconfig.RegexpChangeMatcher{}),
}

func TestGeneratePodSpec(t *testing.T) {
	tests := []struct {
		description    string
		info           *ProwgenInfo
		secrets        []*ciop.Secret
		targets        []string
		additionalArgs []string

		expected *corev1.PodSpec
	}{
		{
			description: "standard use case",
			info:        &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			secrets:     nil,
			targets:     []string{"target"},

			expected: &corev1.PodSpec{
				ServiceAccountName: "ci-operator",
				Containers: []corev1.Container{{
					Image:           "ci-operator:latest",
					ImagePullPolicy: corev1.PullAlways,
					Command:         []string{"ci-operator"},
					Args: []string{
						"--give-pr-author-access-to-namespace=true",
						"--artifact-dir=$(ARTIFACTS)",
						"--kubeconfig=/etc/apici/kubeconfig",
						"--image-import-pull-secret=/etc/pull-secret/.dockerconfigjson",
						"--report-username=ci",
						"--report-password-file=/etc/report/password.txt",
						"--target=target",
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{"cpu": *resource.NewMilliQuantity(10, resource.DecimalSI)},
					},
					VolumeMounts: []corev1.VolumeMount{
						{Name: "apici-ci-operator-credentials", ReadOnly: true, MountPath: "/etc/apici"},
						{Name: "pull-secret", ReadOnly: true, MountPath: "/etc/pull-secret"},
						{Name: "result-aggregator", ReadOnly: true, MountPath: "/etc/report"},
					},
				}},
				Volumes: []corev1.Volume{
					{
						Name: "apici-ci-operator-credentials",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "apici-ci-operator-credentials", Items: []corev1.KeyToPath{{Key: "sa.ci-operator.apici.config", Path: "kubeconfig"}}},
						},
					},
					{
						Name: "pull-secret",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "regcred"},
						},
					},
					{
						Name: "result-aggregator",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "result-aggregator"},
						},
					},
				},
			},
		},
		{
			description:    "additional args are included in podspec",
			info:           &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			secrets:        nil,
			targets:        []string{"target"},
			additionalArgs: []string{"--promote", "--some=thing"},

			expected: &corev1.PodSpec{
				ServiceAccountName: "ci-operator",
				Containers: []corev1.Container{{
					Image:           "ci-operator:latest",
					ImagePullPolicy: corev1.PullAlways,
					Command:         []string{"ci-operator"},
					Args: []string{
						"--give-pr-author-access-to-namespace=true",
						"--artifact-dir=$(ARTIFACTS)",
						"--kubeconfig=/etc/apici/kubeconfig",
						"--image-import-pull-secret=/etc/pull-secret/.dockerconfigjson",
						"--report-username=ci",
						"--report-password-file=/etc/report/password.txt",
						"--promote",
						"--some=thing",
						"--target=target",
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{"cpu": *resource.NewMilliQuantity(10, resource.DecimalSI)},
					},
					VolumeMounts: []corev1.VolumeMount{
						{Name: "apici-ci-operator-credentials", ReadOnly: true, MountPath: "/etc/apici"},
						{Name: "pull-secret", ReadOnly: true, MountPath: "/etc/pull-secret"},
						{Name: "result-aggregator", ReadOnly: true, MountPath: "/etc/report"},
					},
				}},
				Volumes: []corev1.Volume{
					{
						Name: "apici-ci-operator-credentials",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "apici-ci-operator-credentials", Items: []corev1.KeyToPath{{Key: "sa.ci-operator.apici.config", Path: "kubeconfig"}}},
						},
					},
					{
						Name: "pull-secret",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "regcred"},
						},
					},
					{
						Name: "result-aggregator",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "result-aggregator"},
						},
					},
				},
			},
		},
		{
			description:    "additional args and secret are included in podspec",
			info:           &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			secrets:        []*ciop.Secret{{Name: "secret-name", MountPath: "/usr/local/test-secret"}},
			targets:        []string{"target"},
			additionalArgs: []string{"--promote", "--some=thing"},

			expected: &corev1.PodSpec{
				ServiceAccountName: "ci-operator",
				Containers: []corev1.Container{{
					Image:           "ci-operator:latest",
					ImagePullPolicy: corev1.PullAlways,
					Command:         []string{"ci-operator"},
					Args: []string{
						"--give-pr-author-access-to-namespace=true",
						"--artifact-dir=$(ARTIFACTS)",
						"--kubeconfig=/etc/apici/kubeconfig",
						"--image-import-pull-secret=/etc/pull-secret/.dockerconfigjson",
						"--report-username=ci",
						"--report-password-file=/etc/report/password.txt",
						"--promote",
						"--some=thing",
						"--target=target",
						"--secret-dir=/secrets/secret-name",
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{"cpu": *resource.NewMilliQuantity(10, resource.DecimalSI)},
					},
					VolumeMounts: []corev1.VolumeMount{

						{Name: "apici-ci-operator-credentials", ReadOnly: true, MountPath: "/etc/apici"},
						{Name: "pull-secret", ReadOnly: true, MountPath: "/etc/pull-secret"},
						{Name: "result-aggregator", ReadOnly: true, MountPath: "/etc/report"},
						{Name: "secret-name", MountPath: "/secrets/secret-name", ReadOnly: true},
					},
				}},
				Volumes: []corev1.Volume{
					{
						Name: "apici-ci-operator-credentials",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "apici-ci-operator-credentials", Items: []corev1.KeyToPath{{Key: "sa.ci-operator.apici.config", Path: "kubeconfig"}}},
						},
					},
					{
						Name: "pull-secret",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "regcred"},
						},
					},
					{
						Name: "result-aggregator",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "result-aggregator"},
						},
					},
					{
						Name: "secret-name",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "secret-name"},
						},
					},
				},
			},
		},
		{
			description: "multiple targets",
			info:        &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			secrets:     nil,
			targets:     []string{"target", "more", "and-more"},

			expected: &corev1.PodSpec{
				ServiceAccountName: "ci-operator",
				Containers: []corev1.Container{{
					Image:           "ci-operator:latest",
					ImagePullPolicy: corev1.PullAlways,
					Command:         []string{"ci-operator"},
					Args: []string{
						"--give-pr-author-access-to-namespace=true",
						"--artifact-dir=$(ARTIFACTS)",
						"--kubeconfig=/etc/apici/kubeconfig",
						"--image-import-pull-secret=/etc/pull-secret/.dockerconfigjson",
						"--report-username=ci",
						"--report-password-file=/etc/report/password.txt",
						"--target=target",
						"--target=more",
						"--target=and-more",
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{"cpu": *resource.NewMilliQuantity(10, resource.DecimalSI)},
					},
					VolumeMounts: []corev1.VolumeMount{
						{Name: "apici-ci-operator-credentials", ReadOnly: true, MountPath: "/etc/apici"},
						{Name: "pull-secret", ReadOnly: true, MountPath: "/etc/pull-secret"},
						{Name: "result-aggregator", ReadOnly: true, MountPath: "/etc/report"},
					},
				}},
				Volumes: []corev1.Volume{
					{
						Name: "apici-ci-operator-credentials",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "apici-ci-operator-credentials", Items: []corev1.KeyToPath{{Key: "sa.ci-operator.apici.config", Path: "kubeconfig"}}},
						},
					},
					{
						Name: "pull-secret",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "regcred"},
						},
					},
					{
						Name: "result-aggregator",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "result-aggregator"},
						},
					},
				},
			},
		},
		{
			description: "private job",
			info: &ProwgenInfo{
				Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
				Config:   config.Prowgen{Private: true},
			},
			secrets: nil,
			targets: []string{"target"},

			expected: &corev1.PodSpec{
				ServiceAccountName: "ci-operator",
				Containers: []corev1.Container{
					{
						Image:           "ci-operator:latest",
						ImagePullPolicy: corev1.PullAlways,
						Command:         []string{"ci-operator"},
						Args: []string{
							"--give-pr-author-access-to-namespace=true",
							"--artifact-dir=$(ARTIFACTS)",
							"--kubeconfig=/etc/apici/kubeconfig",
							"--image-import-pull-secret=/etc/pull-secret/.dockerconfigjson",
							"--report-username=ci",
							"--report-password-file=/etc/report/password.txt",
							"--target=target",
							"--oauth-token-path=/usr/local/github-credentials/oauth",
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{"cpu": *resource.NewMilliQuantity(10, resource.DecimalSI)},
						},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "apici-ci-operator-credentials", ReadOnly: true, MountPath: "/etc/apici"},
							{Name: "pull-secret", ReadOnly: true, MountPath: "/etc/pull-secret"},
							{Name: "result-aggregator", ReadOnly: true, MountPath: "/etc/report"},
							{
								Name:      "github-credentials-openshift-ci-robot-private-git-cloner",
								MountPath: "/usr/local/github-credentials",
								ReadOnly:  true,
							},
						},
					},
				},
				Volumes: []corev1.Volume{
					{
						Name: "apici-ci-operator-credentials",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "apici-ci-operator-credentials", Items: []corev1.KeyToPath{{Key: "sa.ci-operator.apici.config", Path: "kubeconfig"}}},
						},
					},
					{
						Name: "pull-secret",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "regcred"},
						},
					},
					{
						Name: "result-aggregator",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "result-aggregator"},
						},
					},
					{
						Name: "github-credentials-openshift-ci-robot-private-git-cloner",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "github-credentials-openshift-ci-robot-private-git-cloner"},
						},
					},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			podSpec := generateCiOperatorPodSpec(tc.info, tc.secrets, tc.targets, tc.additionalArgs...)
			if !equality.Semantic.DeepEqual(podSpec, tc.expected) {
				t.Errorf("%s: expected PodSpec diff:\n%s", tc.description, cmp.Diff(tc.expected, podSpec, unexportedFields...))
			}
		})
	}
}

func TestGeneratePodSpecMultiStage(t *testing.T) {
	info := ProwgenInfo{Metadata: ciop.Metadata{Org: "organization", Repo: "repo", Branch: "branch"}}
	test := ciop.TestStepConfiguration{
		As: "test",
		MultiStageTestConfiguration: &ciop.MultiStageTestConfiguration{
			ClusterProfile: ciop.ClusterProfileAWS,
		},
	}
	expected := corev1.PodSpec{
		ServiceAccountName: "ci-operator",
		Volumes: []corev1.Volume{{
			Name: "apici-ci-operator-credentials",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: "apici-ci-operator-credentials", Items: []corev1.KeyToPath{{Key: "sa.ci-operator.apici.config", Path: "kubeconfig"}}},
			},
		}, {
			Name: "pull-secret",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: "regcred"},
			},
		}, {
			Name: "result-aggregator",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: "result-aggregator"},
			},
		}, {
			Name: "cluster-profile",
			VolumeSource: corev1.VolumeSource{
				Projected: &corev1.ProjectedVolumeSource{
					Sources: []corev1.VolumeProjection{{
						Secret: &corev1.SecretProjection{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "cluster-secrets-aws",
							},
						},
					}},
				},
			},
		}, {
			Name: "boskos",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: "boskos-credentials",
					Items:      []corev1.KeyToPath{{Key: "password", Path: "password"}},
				},
			},
		}},
		Containers: []corev1.Container{{
			Image:           "ci-operator:latest",
			ImagePullPolicy: corev1.PullAlways,
			Command:         []string{"ci-operator"},
			Args: []string{
				"--give-pr-author-access-to-namespace=true",
				"--artifact-dir=$(ARTIFACTS)",
				"--kubeconfig=/etc/apici/kubeconfig",
				"--image-import-pull-secret=/etc/pull-secret/.dockerconfigjson",
				"--report-username=ci",
				"--report-password-file=/etc/report/password.txt",
				"--target=test",
				"--secret-dir=/usr/local/test-cluster-profile",
				"--lease-server-password-file=/etc/boskos/password",
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{"cpu": *resource.NewMilliQuantity(10, resource.DecimalSI)},
			},
			VolumeMounts: []corev1.VolumeMount{
				{Name: "apici-ci-operator-credentials", ReadOnly: true, MountPath: "/etc/apici"},
				{Name: "pull-secret", ReadOnly: true, MountPath: "/etc/pull-secret"},
				{Name: "result-aggregator", ReadOnly: true, MountPath: "/etc/report"},
				{Name: "cluster-profile", MountPath: "/usr/local/test-cluster-profile"},
				{Name: "boskos", ReadOnly: true, MountPath: "/etc/boskos"},
			},
		}},
	}
	podSpec := *generatePodSpecMultiStage(&info, &test)
	if !equality.Semantic.DeepEqual(&podSpec, &expected) {
		t.Errorf("expected PodSpec diff:\n%s", cmp.Diff(expected, podSpec, unexportedFields...))
	}
}

func TestGeneratePodSpecTemplate(t *testing.T) {
	tests := []struct {
		info    *ProwgenInfo
		release string
		test    ciop.TestStepConfiguration

		expected *corev1.PodSpec
	}{
		{
			info:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "organization", Repo: "repo", Branch: "branch"}},
			release: "origin-v4.0",
			test: ciop.TestStepConfiguration{
				As:       "test",
				Commands: "commands",
				OpenshiftAnsibleClusterTestConfiguration: &ciop.OpenshiftAnsibleClusterTestConfiguration{
					ClusterTestConfiguration: ciop.ClusterTestConfiguration{ClusterProfile: "gcp"},
				},
			},

			expected: &corev1.PodSpec{
				ServiceAccountName: "ci-operator",
				Volumes: []corev1.Volume{
					{
						Name: "apici-ci-operator-credentials",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "apici-ci-operator-credentials", Items: []corev1.KeyToPath{{Key: "sa.ci-operator.apici.config", Path: "kubeconfig"}}},
						},
					},
					{
						Name: "pull-secret",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "regcred"},
						},
					},
					{
						Name: "result-aggregator",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "result-aggregator"},
						},
					},
					{
						Name: "job-definition",
						VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "prow-job-cluster-launch-e2e",
								},
							},
						},
					},
					{
						Name: "cluster-profile",
						VolumeSource: corev1.VolumeSource{
							Projected: &corev1.ProjectedVolumeSource{
								Sources: []corev1.VolumeProjection{
									{
										Secret: &corev1.SecretProjection{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: "cluster-secrets-gcp",
											},
										},
									},
									{
										ConfigMap: &corev1.ConfigMapProjection{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: "cluster-profile-gcp",
											},
										},
									},
								},
							},
						},
					},
				},
				Containers: []corev1.Container{{
					Image:           "ci-operator:latest",
					ImagePullPolicy: corev1.PullAlways,
					Command:         []string{"ci-operator"},
					Args: []string{
						"--give-pr-author-access-to-namespace=true",
						"--artifact-dir=$(ARTIFACTS)",
						"--kubeconfig=/etc/apici/kubeconfig",
						"--image-import-pull-secret=/etc/pull-secret/.dockerconfigjson",
						"--report-username=ci",
						"--report-password-file=/etc/report/password.txt",
						"--target=test",
						"--secret-dir=/usr/local/test-cluster-profile",
						"--template=/usr/local/test"},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{"cpu": *resource.NewMilliQuantity(10, resource.DecimalSI)},
					},
					Env: []corev1.EnvVar{
						{Name: "CLUSTER_TYPE", Value: "gcp"},
						{Name: "JOB_NAME_SAFE", Value: "test"},
						{Name: "TEST_COMMAND", Value: "commands"},
						{Name: "RPM_REPO_OPENSHIFT_ORIGIN", Value: "https://rpms.svc.ci.openshift.org/openshift-origin-v4.0/"},
					},
					VolumeMounts: []corev1.VolumeMount{

						{Name: "apici-ci-operator-credentials", ReadOnly: true, MountPath: "/etc/apici"},
						{Name: "pull-secret", ReadOnly: true, MountPath: "/etc/pull-secret"},
						{Name: "result-aggregator", ReadOnly: true, MountPath: "/etc/report"},
						{Name: "cluster-profile", MountPath: "/usr/local/test-cluster-profile"},
						{Name: "job-definition", MountPath: "/usr/local/test", SubPath: "cluster-launch-e2e.yaml"},
					},
				}},
			},
		},
		{
			info:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "organization", Repo: "repo", Branch: "branch"}},
			release: "origin-v4.0",
			test: ciop.TestStepConfiguration{
				As:       "test",
				Commands: "commands",
				OpenshiftInstallerClusterTestConfiguration: &ciop.OpenshiftInstallerClusterTestConfiguration{
					ClusterTestConfiguration: ciop.ClusterTestConfiguration{ClusterProfile: "aws"},
				},
			},

			expected: &corev1.PodSpec{
				ServiceAccountName: "ci-operator",
				Volumes: []corev1.Volume{
					{
						Name: "apici-ci-operator-credentials",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "apici-ci-operator-credentials", Items: []corev1.KeyToPath{{Key: "sa.ci-operator.apici.config", Path: "kubeconfig"}}},
						},
					},
					{
						Name: "pull-secret",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "regcred"},
						},
					},
					{
						Name: "result-aggregator",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "result-aggregator"},
						},
					},
					{
						Name: "job-definition",
						VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "prow-job-cluster-launch-installer-e2e",
								},
							},
						},
					},
					{
						Name: "cluster-profile",
						VolumeSource: corev1.VolumeSource{
							Projected: &corev1.ProjectedVolumeSource{
								Sources: []corev1.VolumeProjection{
									{
										Secret: &corev1.SecretProjection{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: "cluster-secrets-aws",
											},
										},
									},
								},
							},
						},
					},
					{
						Name: "boskos",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "boskos-credentials", Items: []corev1.KeyToPath{{Key: "password", Path: "password"}}},
						},
					},
				},
				Containers: []corev1.Container{{
					Image:           "ci-operator:latest",
					ImagePullPolicy: corev1.PullAlways,
					Command:         []string{"ci-operator"},
					Args: []string{
						"--give-pr-author-access-to-namespace=true",
						"--artifact-dir=$(ARTIFACTS)",
						"--kubeconfig=/etc/apici/kubeconfig",
						"--image-import-pull-secret=/etc/pull-secret/.dockerconfigjson",
						"--report-username=ci",
						"--report-password-file=/etc/report/password.txt",
						"--target=test",
						"--secret-dir=/usr/local/test-cluster-profile",
						"--template=/usr/local/test",
						"--lease-server-password-file=/etc/boskos/password",
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{"cpu": *resource.NewMilliQuantity(10, resource.DecimalSI)},
					},
					Env: []corev1.EnvVar{
						{Name: "CLUSTER_TYPE", Value: "aws"},
						{Name: "JOB_NAME_SAFE", Value: "test"},
						{Name: "TEST_COMMAND", Value: "commands"},
					},
					VolumeMounts: []corev1.VolumeMount{

						{Name: "apici-ci-operator-credentials", ReadOnly: true, MountPath: "/etc/apici"},
						{Name: "pull-secret", ReadOnly: true, MountPath: "/etc/pull-secret"},
						{Name: "result-aggregator", ReadOnly: true, MountPath: "/etc/report"},
						{Name: "boskos", ReadOnly: true, MountPath: "/etc/boskos"},
						{Name: "cluster-profile", MountPath: "/usr/local/test-cluster-profile"},
						{Name: "job-definition", MountPath: "/usr/local/test", SubPath: "cluster-launch-installer-e2e.yaml"},
					},
				}},
			},
		},
		{
			info:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "organization", Repo: "repo", Branch: "branch"}},
			release: "origin-v4.0",
			test: ciop.TestStepConfiguration{
				As:       "test",
				Commands: "commands",
				OpenshiftInstallerCustomTestImageClusterTestConfiguration: &ciop.OpenshiftInstallerCustomTestImageClusterTestConfiguration{
					ClusterTestConfiguration: ciop.ClusterTestConfiguration{ClusterProfile: "gcp"},
					From:                     "pipeline:kubevirt-test",
					EnableNestedVirt:         true,
					NestedVirtImage:          "nested-virt-image-name",
				},
			},

			expected: &corev1.PodSpec{
				ServiceAccountName: "ci-operator",
				Volumes: []corev1.Volume{
					{
						Name: "apici-ci-operator-credentials",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "apici-ci-operator-credentials", Items: []corev1.KeyToPath{{Key: "sa.ci-operator.apici.config", Path: "kubeconfig"}}},
						},
					},
					{
						Name: "pull-secret",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "regcred"},
						},
					},
					{
						Name: "result-aggregator",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "result-aggregator"},
						},
					},
					{
						Name: "job-definition",
						VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "prow-job-cluster-launch-installer-custom-test-image",
								},
							},
						},
					},
					{
						Name: "cluster-profile",
						VolumeSource: corev1.VolumeSource{
							Projected: &corev1.ProjectedVolumeSource{
								Sources: []corev1.VolumeProjection{
									{
										Secret: &corev1.SecretProjection{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: "cluster-secrets-gcp",
											},
										},
									},
									{
										ConfigMap: &corev1.ConfigMapProjection{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: "cluster-profile-gcp",
											},
										},
									},
								},
							},
						},
					},
					{
						Name: "boskos",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "boskos-credentials", Items: []corev1.KeyToPath{{Key: "password", Path: "password"}}},
						},
					},
				},
				Containers: []corev1.Container{{
					Image:           "ci-operator:latest",
					ImagePullPolicy: corev1.PullAlways,
					Command:         []string{"ci-operator"},
					Args: []string{
						"--give-pr-author-access-to-namespace=true",
						"--artifact-dir=$(ARTIFACTS)",
						"--kubeconfig=/etc/apici/kubeconfig",
						"--image-import-pull-secret=/etc/pull-secret/.dockerconfigjson",
						"--report-username=ci",
						"--report-password-file=/etc/report/password.txt",
						"--target=test",
						"--secret-dir=/usr/local/test-cluster-profile",
						"--template=/usr/local/test",
						"--lease-server-password-file=/etc/boskos/password",
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{"cpu": *resource.NewMilliQuantity(10, resource.DecimalSI)},
					},
					Env: []corev1.EnvVar{
						{Name: "CLUSTER_TYPE", Value: "gcp"},
						{Name: "JOB_NAME_SAFE", Value: "test"},
						{Name: "TEST_COMMAND", Value: "commands"},
						{Name: "TEST_IMAGESTREAM_TAG", Value: "pipeline:kubevirt-test"},
						{Name: "CLUSTER_ENABLE_NESTED_VIRT", Value: "true"},
						{Name: "CLUSTER_NESTED_VIRT_IMAGE", Value: "nested-virt-image-name"},
					},
					VolumeMounts: []corev1.VolumeMount{

						{Name: "apici-ci-operator-credentials", ReadOnly: true, MountPath: "/etc/apici"},
						{Name: "pull-secret", ReadOnly: true, MountPath: "/etc/pull-secret"},
						{Name: "result-aggregator", ReadOnly: true, MountPath: "/etc/report"},
						{Name: "boskos", ReadOnly: true, MountPath: "/etc/boskos"},
						{Name: "cluster-profile", MountPath: "/usr/local/test-cluster-profile"},
						{Name: "job-definition", MountPath: "/usr/local/test", SubPath: "cluster-launch-installer-custom-test-image.yaml"},
					},
				}},
			},
		},
		{
			info:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "organization", Repo: "repo", Branch: "branch"}},
			release: "origin-v4.0",
			test: ciop.TestStepConfiguration{
				As:       "test",
				Commands: "commands",
				OpenshiftInstallerCustomTestImageClusterTestConfiguration: &ciop.OpenshiftInstallerCustomTestImageClusterTestConfiguration{
					ClusterTestConfiguration: ciop.ClusterTestConfiguration{ClusterProfile: "gcp"},
					From:                     "pipeline:kubevirt-test",
					EnableNestedVirt:         true,
				},
			},

			expected: &corev1.PodSpec{
				ServiceAccountName: "ci-operator",
				Volumes: []corev1.Volume{
					{
						Name: "apici-ci-operator-credentials",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "apici-ci-operator-credentials", Items: []corev1.KeyToPath{{Key: "sa.ci-operator.apici.config", Path: "kubeconfig"}}},
						},
					},
					{
						Name: "pull-secret",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "regcred"},
						},
					},
					{
						Name: "result-aggregator",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "result-aggregator"},
						},
					},
					{
						Name: "job-definition",
						VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "prow-job-cluster-launch-installer-custom-test-image",
								},
							},
						},
					},
					{
						Name: "cluster-profile",
						VolumeSource: corev1.VolumeSource{
							Projected: &corev1.ProjectedVolumeSource{
								Sources: []corev1.VolumeProjection{
									{
										Secret: &corev1.SecretProjection{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: "cluster-secrets-gcp",
											},
										},
									},
									{
										ConfigMap: &corev1.ConfigMapProjection{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: "cluster-profile-gcp",
											},
										},
									},
								},
							},
						},
					},
					{
						Name: "boskos",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "boskos-credentials", Items: []corev1.KeyToPath{{Key: "password", Path: "password"}}},
						},
					},
				},
				Containers: []corev1.Container{{
					Image:           "ci-operator:latest",
					ImagePullPolicy: corev1.PullAlways,
					Command:         []string{"ci-operator"},
					Args: []string{
						"--give-pr-author-access-to-namespace=true",
						"--artifact-dir=$(ARTIFACTS)",
						"--kubeconfig=/etc/apici/kubeconfig",
						"--image-import-pull-secret=/etc/pull-secret/.dockerconfigjson",
						"--report-username=ci",
						"--report-password-file=/etc/report/password.txt",
						"--target=test",
						"--secret-dir=/usr/local/test-cluster-profile",
						"--template=/usr/local/test",
						"--lease-server-password-file=/etc/boskos/password",
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{"cpu": *resource.NewMilliQuantity(10, resource.DecimalSI)},
					},
					Env: []corev1.EnvVar{
						{Name: "CLUSTER_TYPE", Value: "gcp"},
						{Name: "JOB_NAME_SAFE", Value: "test"},
						{Name: "TEST_COMMAND", Value: "commands"},
						{Name: "TEST_IMAGESTREAM_TAG", Value: "pipeline:kubevirt-test"},
						{Name: "CLUSTER_ENABLE_NESTED_VIRT", Value: "true"},
					},
					VolumeMounts: []corev1.VolumeMount{

						{Name: "apici-ci-operator-credentials", ReadOnly: true, MountPath: "/etc/apici"},
						{Name: "pull-secret", ReadOnly: true, MountPath: "/etc/pull-secret"},
						{Name: "result-aggregator", ReadOnly: true, MountPath: "/etc/report"},
						{Name: "boskos", ReadOnly: true, MountPath: "/etc/boskos"},
						{Name: "cluster-profile", MountPath: "/usr/local/test-cluster-profile"},
						{Name: "job-definition", MountPath: "/usr/local/test", SubPath: "cluster-launch-installer-custom-test-image.yaml"},
					},
				}},
			},
		},
		{
			info:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "organization", Repo: "repo", Branch: "branch"}},
			release: "origin-v4.0",
			test: ciop.TestStepConfiguration{
				As:       "test",
				Commands: "commands",
				OpenshiftInstallerCustomTestImageClusterTestConfiguration: &ciop.OpenshiftInstallerCustomTestImageClusterTestConfiguration{
					ClusterTestConfiguration: ciop.ClusterTestConfiguration{ClusterProfile: "gcp"},
					From:                     "pipeline:kubevirt-test",
					NestedVirtImage:          "",
					EnableNestedVirt:         false,
				},
			},

			expected: &corev1.PodSpec{
				ServiceAccountName: "ci-operator",
				Volumes: []corev1.Volume{
					{
						Name: "apici-ci-operator-credentials",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "apici-ci-operator-credentials", Items: []corev1.KeyToPath{{Key: "sa.ci-operator.apici.config", Path: "kubeconfig"}}},
						},
					},
					{
						Name: "pull-secret",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "regcred"},
						},
					},
					{
						Name: "result-aggregator",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "result-aggregator"},
						},
					},
					{
						Name: "job-definition",
						VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "prow-job-cluster-launch-installer-custom-test-image",
								},
							},
						},
					},
					{
						Name: "cluster-profile",
						VolumeSource: corev1.VolumeSource{
							Projected: &corev1.ProjectedVolumeSource{
								Sources: []corev1.VolumeProjection{
									{
										Secret: &corev1.SecretProjection{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: "cluster-secrets-gcp",
											},
										},
									},
									{
										ConfigMap: &corev1.ConfigMapProjection{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: "cluster-profile-gcp",
											},
										},
									},
								},
							},
						},
					},
					{
						Name: "boskos",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "boskos-credentials", Items: []corev1.KeyToPath{{Key: "password", Path: "password"}}},
						},
					},
				},
				Containers: []corev1.Container{{
					Image:           "ci-operator:latest",
					ImagePullPolicy: corev1.PullAlways,
					Command:         []string{"ci-operator"},
					Args: []string{
						"--give-pr-author-access-to-namespace=true",
						"--artifact-dir=$(ARTIFACTS)",
						"--kubeconfig=/etc/apici/kubeconfig",
						"--image-import-pull-secret=/etc/pull-secret/.dockerconfigjson",
						"--report-username=ci",
						"--report-password-file=/etc/report/password.txt",
						"--target=test",
						"--secret-dir=/usr/local/test-cluster-profile",
						"--template=/usr/local/test",
						"--lease-server-password-file=/etc/boskos/password",
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{"cpu": *resource.NewMilliQuantity(10, resource.DecimalSI)},
					},
					Env: []corev1.EnvVar{
						{Name: "CLUSTER_TYPE", Value: "gcp"},
						{Name: "JOB_NAME_SAFE", Value: "test"},
						{Name: "TEST_COMMAND", Value: "commands"},
						{Name: "TEST_IMAGESTREAM_TAG", Value: "pipeline:kubevirt-test"},
					},
					VolumeMounts: []corev1.VolumeMount{

						{Name: "apici-ci-operator-credentials", ReadOnly: true, MountPath: "/etc/apici"},
						{Name: "pull-secret", ReadOnly: true, MountPath: "/etc/pull-secret"},
						{Name: "result-aggregator", ReadOnly: true, MountPath: "/etc/report"},
						{Name: "boskos", ReadOnly: true, MountPath: "/etc/boskos"},
						{Name: "cluster-profile", MountPath: "/usr/local/test-cluster-profile"},
						{Name: "job-definition", MountPath: "/usr/local/test", SubPath: "cluster-launch-installer-custom-test-image.yaml"},
					},
				}},
			},
		},
		{
			info:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "organization", Repo: "repo", Branch: "branch"}},
			release: "origin-v4.0",
			test: ciop.TestStepConfiguration{
				As:       "test",
				Commands: "commands",
				OpenshiftInstallerCustomTestImageClusterTestConfiguration: &ciop.OpenshiftInstallerCustomTestImageClusterTestConfiguration{
					ClusterTestConfiguration: ciop.ClusterTestConfiguration{ClusterProfile: "gcp"},
					From:                     "pipeline:kubevirt-test",
				},
			},

			expected: &corev1.PodSpec{
				ServiceAccountName: "ci-operator",
				Volumes: []corev1.Volume{
					{
						Name: "apici-ci-operator-credentials",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "apici-ci-operator-credentials", Items: []corev1.KeyToPath{{Key: "sa.ci-operator.apici.config", Path: "kubeconfig"}}},
						},
					},
					{
						Name: "pull-secret",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "regcred"},
						},
					},
					{
						Name: "result-aggregator",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "result-aggregator"},
						},
					},
					{
						Name: "job-definition",
						VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "prow-job-cluster-launch-installer-custom-test-image",
								},
							},
						},
					},
					{
						Name: "cluster-profile",
						VolumeSource: corev1.VolumeSource{
							Projected: &corev1.ProjectedVolumeSource{
								Sources: []corev1.VolumeProjection{
									{
										Secret: &corev1.SecretProjection{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: "cluster-secrets-gcp",
											},
										},
									},
									{
										ConfigMap: &corev1.ConfigMapProjection{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: "cluster-profile-gcp",
											},
										},
									},
								},
							},
						},
					},
					{
						Name: "boskos",
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{SecretName: "boskos-credentials", Items: []corev1.KeyToPath{{Key: "password", Path: "password"}}},
						},
					},
				},
				Containers: []corev1.Container{{
					Image:           "ci-operator:latest",
					ImagePullPolicy: corev1.PullAlways,
					Command:         []string{"ci-operator"},
					Args: []string{
						"--give-pr-author-access-to-namespace=true",
						"--artifact-dir=$(ARTIFACTS)",
						"--kubeconfig=/etc/apici/kubeconfig",
						"--image-import-pull-secret=/etc/pull-secret/.dockerconfigjson",
						"--report-username=ci",
						"--report-password-file=/etc/report/password.txt",
						"--target=test",
						"--secret-dir=/usr/local/test-cluster-profile",
						"--template=/usr/local/test",
						"--lease-server-password-file=/etc/boskos/password",
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{"cpu": *resource.NewMilliQuantity(10, resource.DecimalSI)},
					},
					Env: []corev1.EnvVar{
						{Name: "CLUSTER_TYPE", Value: "gcp"},
						{Name: "JOB_NAME_SAFE", Value: "test"},
						{Name: "TEST_COMMAND", Value: "commands"},
						{Name: "TEST_IMAGESTREAM_TAG", Value: "pipeline:kubevirt-test"},
					},
					VolumeMounts: []corev1.VolumeMount{

						{Name: "apici-ci-operator-credentials", ReadOnly: true, MountPath: "/etc/apici"},
						{Name: "pull-secret", ReadOnly: true, MountPath: "/etc/pull-secret"},
						{Name: "result-aggregator", ReadOnly: true, MountPath: "/etc/report"},
						{Name: "boskos", ReadOnly: true, MountPath: "/etc/boskos"},
						{Name: "cluster-profile", MountPath: "/usr/local/test-cluster-profile"},
						{Name: "job-definition", MountPath: "/usr/local/test", SubPath: "cluster-launch-installer-custom-test-image.yaml"},
					},
				}},
			},
		},
	}

	for _, tc := range tests {
		podSpec := generatePodSpecTemplate(tc.info, tc.release, &tc.test)
		if !equality.Semantic.DeepEqual(podSpec, tc.expected) {
			t.Errorf("expected PodSpec diff:\n%s", cmp.Diff(tc.expected, podSpec, unexportedFields...))
		}
	}
}

func TestGeneratePresubmitForTest(t *testing.T) {
	newTrue := true

	tests := []struct {
		description string

		test     string
		repoInfo *ProwgenInfo
		expected *prowconfig.Presubmit
	}{{
		description: "presubmit for standard test",
		test:        "testname",
		repoInfo:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},

		expected: &prowconfig.Presubmit{
			JobBase: prowconfig.JobBase{
				Agent: "kubernetes",
				Labels: map[string]string{
					"ci-operator.openshift.io/prowgen-controlled": "true",
					"pj-rehearse.openshift.io/can-be-rehearsed":   "true",
				},
				Name: "pull-ci-org-repo-branch-testname",
				UtilityConfig: prowconfig.UtilityConfig{
					DecorationConfig: &prowv1.DecorationConfig{SkipCloning: &newTrue},
					Decorate:         true,
				},
			},
			AlwaysRun: true,
			Brancher:  prowconfig.Brancher{Branches: []string{"branch"}},
			Reporter: prowconfig.Reporter{
				Context: "ci/prow/testname",
			},
			RerunCommand: "/test testname",
			Trigger:      `(?m)^/test( | .* )testname,?($|\s.*)`,
		},
	},
		{
			description: "presubmit for a test in a variant config",
			test:        "testname",
			repoInfo:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch", Variant: "also"}},

			expected: &prowconfig.Presubmit{
				JobBase: prowconfig.JobBase{
					Agent: "kubernetes",
					Labels: map[string]string{
						"ci-operator.openshift.io/prowgen-controlled": "true",
						"ci-operator.openshift.io/variant":            "also",
						"pj-rehearse.openshift.io/can-be-rehearsed":   "true",
					},
					Name: "pull-ci-org-repo-branch-also-testname",
					UtilityConfig: prowconfig.UtilityConfig{
						DecorationConfig: &prowv1.DecorationConfig{SkipCloning: &newTrue},
						Decorate:         true,
					},
				},
				AlwaysRun: true,
				Brancher:  prowconfig.Brancher{Branches: []string{"branch"}},
				Reporter: prowconfig.Reporter{
					Context: "ci/prow/also-testname",
				},
				RerunCommand: "/test also-testname",
				Trigger:      `(?m)^/test( | .* )also-testname,?($|\s.*)`,
			},
		},
	}
	for _, tc := range tests {
		presubmit := generatePresubmitForTest(tc.test, tc.repoInfo, jobconfig.Generated, nil, true, nil) // podSpec tested in generatePodSpec
		if !equality.Semantic.DeepEqual(presubmit, tc.expected) {
			t.Errorf("expected presubmit diff:\n%s", cmp.Diff(tc.expected, presubmit, unexportedFields...))
		}
	}
}

func TestGeneratePeriodicForTest(t *testing.T) {
	newTrue := true

	tests := []struct {
		description string

		test     string
		repoInfo *ProwgenInfo
		expected *prowconfig.Periodic
	}{{
		description: "periodic for standard test",
		test:        "testname",
		repoInfo:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},

		expected: &prowconfig.Periodic{
			Cron: "@yearly",
			JobBase: prowconfig.JobBase{
				Agent: "kubernetes",
				Labels: map[string]string{
					"ci-operator.openshift.io/prowgen-controlled": "true",
					"pj-rehearse.openshift.io/can-be-rehearsed":   "true",
				},
				Name: "periodic-ci-org-repo-branch-testname",
				UtilityConfig: prowconfig.UtilityConfig{
					DecorationConfig: &prowv1.DecorationConfig{SkipCloning: &newTrue},
					Decorate:         true,
					ExtraRefs: []prowv1.Refs{{
						Org:     "org",
						Repo:    "repo",
						BaseRef: "branch",
					}},
				},
			},
		},
	},
		{
			description: "periodic for a test in a variant config",
			test:        "testname",
			repoInfo:    &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch", Variant: "also"}},
			expected: &prowconfig.Periodic{
				Cron: "@yearly",
				JobBase: prowconfig.JobBase{
					Agent: "kubernetes",
					Labels: map[string]string{
						"ci-operator.openshift.io/prowgen-controlled": "true",
						"ci-operator.openshift.io/variant":            "also",
						"pj-rehearse.openshift.io/can-be-rehearsed":   "true",
					},
					Name: "periodic-ci-org-repo-branch-also-testname",
					UtilityConfig: prowconfig.UtilityConfig{
						DecorationConfig: &prowv1.DecorationConfig{SkipCloning: &newTrue},
						Decorate:         true,
						ExtraRefs: []prowv1.Refs{{
							Org:     "org",
							Repo:    "repo",
							BaseRef: "branch",
						}},
					},
				},
			},
		},
	}
	for _, tc := range tests {
		periodic := generatePeriodicForTest(tc.test, tc.repoInfo, jobconfig.Generated, nil, true, "@yearly", nil) // podSpec tested in generatePodSpec
		// Periodic has unexported fields on which DeepEqual panics, so we need to compare member-wise
		if !equality.Semantic.DeepEqual(periodic.JobBase, tc.expected.JobBase) {
			t.Errorf("expected periodic diff:\n%s", cmp.Diff(tc.expected.JobBase, periodic.JobBase, unexportedFields...))
		}
		if periodic.Cron != tc.expected.Cron {
			t.Errorf("expected periodic cron diff:\n%s", cmp.Diff(tc.expected.Cron, periodic.Cron, unexportedFields...))
		}
	}
}

func TestGeneratePostSubmitForTest(t *testing.T) {
	newTrue := true
	standardJobLabels := map[string]string{"ci-operator.openshift.io/prowgen-controlled": "true"}
	tests := []struct {
		name     string
		repoInfo *ProwgenInfo

		expected *prowconfig.Postsubmit
	}{
		{
			name: "name",
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			}},

			expected: &prowconfig.Postsubmit{
				JobBase: prowconfig.JobBase{
					Agent:  "kubernetes",
					Labels: standardJobLabels,
					Name:   "branch-ci-organization-repository-branch-name",
					UtilityConfig: prowconfig.UtilityConfig{
						DecorationConfig: &prowv1.DecorationConfig{SkipCloning: &newTrue},
						Decorate:         true,
					},
				},

				Brancher: prowconfig.Brancher{Branches: []string{"^branch$"}},
			},
		},
		{
			name: "Name",
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "Organization",
				Repo:   "Repository",
				Branch: "Branch",
			}},

			expected: &prowconfig.Postsubmit{
				JobBase: prowconfig.JobBase{
					Agent:  "kubernetes",
					Name:   "branch-ci-Organization-Repository-Branch-Name",
					Labels: map[string]string{"ci-operator.openshift.io/prowgen-controlled": "true"},
					UtilityConfig: prowconfig.UtilityConfig{
						DecorationConfig: &prowv1.DecorationConfig{SkipCloning: &newTrue},
						Decorate:         true,
					}},
				Brancher: prowconfig.Brancher{Branches: []string{"^Branch$"}},
			},
		},
		{
			name: "name",
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "Organization",
				Repo:   "Repository",
				Branch: "Branch",
			}},

			expected: &prowconfig.Postsubmit{
				JobBase: prowconfig.JobBase{
					Agent:  "kubernetes",
					Name:   "branch-ci-Organization-Repository-Branch-name",
					Labels: map[string]string{"ci-operator.openshift.io/prowgen-controlled": "true"},
					UtilityConfig: prowconfig.UtilityConfig{
						DecorationConfig: &prowv1.DecorationConfig{SkipCloning: &newTrue},
						Decorate:         true,
					}},
				Brancher: prowconfig.Brancher{Branches: []string{"^Branch$"}},
			},
		},
	}
	for _, tc := range tests {
		postsubmit := generatePostsubmitForTest(tc.name, tc.repoInfo, jobconfig.Generated, nil, nil) // podSpec tested in TestGeneratePodSpec
		if !equality.Semantic.DeepEqual(postsubmit, tc.expected) {
			t.Errorf("expected postsubmit diff:\n%s", cmp.Diff(tc.expected, postsubmit, unexportedFields...))
		}
	}
}

func TestGenerateJobs(t *testing.T) {
	standardPresubmitJobLabels := map[string]string{
		"ci-operator.openshift.io/prowgen-controlled": "true",
		"pj-rehearse.openshift.io/can-be-rehearsed":   "true"}
	standardPostsubmitJobLabels := map[string]string{"ci-operator.openshift.io/prowgen-controlled": "true", "ci-operator.openshift.io/is-promotion": "true"}

	tests := []struct {
		id       string
		config   *ciop.ReleaseBuildConfiguration
		repoInfo *ProwgenInfo
		expected *prowconfig.JobConfig
	}{
		{
			id: "two tests and empty Images so only two test presubmits are generated",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{
					{As: "derTest", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "from"}},
					{As: "leTest", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "from"}}},
			},
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			}},
			expected: &prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{"organization/repository": {{
					JobBase: prowconfig.JobBase{
						Name:   "pull-ci-organization-repository-branch-derTest",
						Labels: standardPresubmitJobLabels,
					}}, {
					JobBase: prowconfig.JobBase{
						Name:   "pull-ci-organization-repository-branch-leTest",
						Labels: standardPresubmitJobLabels,
					}},
				}},
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{},
			},
		}, {
			id: "two tests and nonempty Images so two test presubmits and images pre/postsubmits are generated ",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{
					{As: "derTest", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "from"}},
					{As: "leTest", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "from"}}},
				Images:                 []ciop.ProjectDirectoryImageBuildStepConfiguration{{}},
				PromotionConfiguration: &ciop.PromotionConfiguration{},
			},
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			}},
			expected: &prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{"organization/repository": {{
					JobBase: prowconfig.JobBase{
						Name:   "pull-ci-organization-repository-branch-derTest",
						Labels: standardPresubmitJobLabels,
					}}, {
					JobBase: prowconfig.JobBase{
						Name:   "pull-ci-organization-repository-branch-leTest",
						Labels: standardPresubmitJobLabels,
					}}, {
					JobBase: prowconfig.JobBase{
						Name:   "pull-ci-organization-repository-branch-images",
						Labels: standardPresubmitJobLabels,
					}},
				}},
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{"organization/repository": {{
					JobBase: prowconfig.JobBase{
						Name:           "branch-ci-organization-repository-branch-images",
						Labels:         standardPostsubmitJobLabels,
						MaxConcurrency: 1,
					}},
				}},
			},
		}, {
			id: "template test",
			config: &ciop.ReleaseBuildConfiguration{
				InputConfiguration: ciop.InputConfiguration{
					ReleaseTagConfiguration: &ciop.ReleaseTagConfiguration{Name: "origin-v4.0"}},
				Tests: []ciop.TestStepConfiguration{
					{
						As: "oTeste",
						OpenshiftAnsibleClusterTestConfiguration: &ciop.OpenshiftAnsibleClusterTestConfiguration{
							ClusterTestConfiguration: ciop.ClusterTestConfiguration{ClusterProfile: "gcp"},
						},
					},
				},
			},
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			}},
			expected: &prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{"organization/repository": {{
					JobBase: prowconfig.JobBase{
						Name:   "pull-ci-organization-repository-branch-oTeste",
						Labels: standardPresubmitJobLabels,
					}},
				}},
			},
		}, {
			id: "template test which doesn't require `tag_specification`",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{{
					As: "oTeste",
					OpenshiftInstallerClusterTestConfiguration: &ciop.OpenshiftInstallerClusterTestConfiguration{
						ClusterTestConfiguration: ciop.ClusterTestConfiguration{ClusterProfile: "gcp"},
					},
				}},
			},
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			}},
			expected: &prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{"organization/repository": {{
					JobBase: prowconfig.JobBase{
						Name:   "pull-ci-organization-repository-branch-oTeste",
						Labels: standardPresubmitJobLabels,
					}},
				}},
			},
		}, {
			id: "Promotion configuration causes --promote job",
			config: &ciop.ReleaseBuildConfiguration{
				Tests:                  []ciop.TestStepConfiguration{},
				Images:                 []ciop.ProjectDirectoryImageBuildStepConfiguration{{}},
				PromotionConfiguration: &ciop.PromotionConfiguration{Namespace: "ci"},
			},
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			}},
			expected: &prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{"organization/repository": {{
					JobBase: prowconfig.JobBase{
						Name:   "pull-ci-organization-repository-branch-images",
						Labels: standardPresubmitJobLabels,
					}},
				}},
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{"organization/repository": {{
					JobBase: prowconfig.JobBase{
						Name:           "branch-ci-organization-repository-branch-images",
						Labels:         standardPostsubmitJobLabels,
						MaxConcurrency: 1,
					}},
				}},
			},
		}, {
			id: "no Promotion configuration has no branch job",
			config: &ciop.ReleaseBuildConfiguration{
				Tests:  []ciop.TestStepConfiguration{},
				Images: []ciop.ProjectDirectoryImageBuildStepConfiguration{{}},
				InputConfiguration: ciop.InputConfiguration{
					ReleaseTagConfiguration: &ciop.ReleaseTagConfiguration{Namespace: "openshift"},
				},
			},
			repoInfo: &ProwgenInfo{Metadata: ciop.Metadata{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			}},
			expected: &prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{"organization/repository": {{
					JobBase: prowconfig.JobBase{
						Name:   "pull-ci-organization-repository-branch-images",
						Labels: standardPresubmitJobLabels,
					}},
				}},
			},
		},
	}

	log.SetOutput(ioutil.Discard)
	for _, tc := range tests {
		jobConfig := GenerateJobs(tc.config, tc.repoInfo, jobconfig.Generated)

		pruneForTests(jobConfig) // prune the fields that are tested in TestGeneratePre/PostsubmitForTest

		if !equality.Semantic.DeepEqual(jobConfig, tc.expected) {
			t.Errorf("testcase: %s\nexpected job config diff:\n%s", tc.id, cmp.Diff(tc.expected, jobConfig, unexportedFields...))
		}
	}
}

func TestGenerateJobBase(t *testing.T) {
	yes := true
	path := "/some/where"
	var testCases = []struct {
		testName    string
		name        string
		prefix      string
		info        *ProwgenInfo
		label       jobconfig.ProwgenLabel
		podSpec     *corev1.PodSpec
		rehearsable bool
		pathAlias   *string
		expected    prowconfig.JobBase
	}{
		{
			testName: "no special options",
			name:     "test",
			prefix:   "pull",
			info:     &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			label:    jobconfig.Generated,
			podSpec:  &corev1.PodSpec{Containers: []corev1.Container{{Name: "test"}}},
			expected: prowconfig.JobBase{
				Name:  "pull-ci-org-repo-branch-test",
				Agent: "kubernetes",
				Labels: map[string]string{
					"ci-operator.openshift.io/prowgen-controlled": "true",
				},
				Spec:          &corev1.PodSpec{Containers: []corev1.Container{{Name: "test"}}},
				UtilityConfig: prowconfig.UtilityConfig{Decorate: true, DecorationConfig: &prowv1.DecorationConfig{SkipCloning: &yes}},
			},
		},
		{
			testName:    "rehearsable",
			name:        "test",
			prefix:      "pull",
			info:        &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"}},
			label:       jobconfig.Generated,
			podSpec:     &corev1.PodSpec{Containers: []corev1.Container{{Name: "test"}}},
			rehearsable: true,
			expected: prowconfig.JobBase{
				Name:  "pull-ci-org-repo-branch-test",
				Agent: "kubernetes",
				Labels: map[string]string{
					"ci-operator.openshift.io/prowgen-controlled": "true",
					"pj-rehearse.openshift.io/can-be-rehearsed":   "true",
				},
				Spec:          &corev1.PodSpec{Containers: []corev1.Container{{Name: "test"}}},
				UtilityConfig: prowconfig.UtilityConfig{Decorate: true, DecorationConfig: &prowv1.DecorationConfig{SkipCloning: &yes}},
			},
		},
		{
			testName: "config variant",
			name:     "test",
			prefix:   "pull",
			info:     &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch", Variant: "whatever"}},
			label:    jobconfig.Generated,
			podSpec:  &corev1.PodSpec{Containers: []corev1.Container{{Name: "test"}}},
			expected: prowconfig.JobBase{
				Name:  "pull-ci-org-repo-branch-whatever-test",
				Agent: "kubernetes",
				Labels: map[string]string{
					"ci-operator.openshift.io/prowgen-controlled": "true",
					"ci-operator.openshift.io/variant":            "whatever",
				},
				Spec:          &corev1.PodSpec{Containers: []corev1.Container{{Name: "test"}}},
				UtilityConfig: prowconfig.UtilityConfig{Decorate: true, DecorationConfig: &prowv1.DecorationConfig{SkipCloning: &yes}},
			},
		},
		{
			testName:  "path alias",
			name:      "test",
			prefix:    "pull",
			info:      &ProwgenInfo{Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch", Variant: "whatever"}},
			label:     jobconfig.Generated,
			podSpec:   &corev1.PodSpec{Containers: []corev1.Container{{Name: "test"}}},
			pathAlias: &path,
			expected: prowconfig.JobBase{
				Name:  "pull-ci-org-repo-branch-whatever-test",
				Agent: "kubernetes",
				Labels: map[string]string{
					"ci-operator.openshift.io/prowgen-controlled": "true",
					"ci-operator.openshift.io/variant":            "whatever",
				},
				Spec:          &corev1.PodSpec{Containers: []corev1.Container{{Name: "test"}}},
				UtilityConfig: prowconfig.UtilityConfig{Decorate: true, DecorationConfig: &prowv1.DecorationConfig{SkipCloning: &yes}, PathAlias: "/some/where"},
			},
		},
		{
			testName: "hidden job for private repos",
			name:     "test",
			prefix:   "pull",
			info: &ProwgenInfo{
				Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
				Config:   config.Prowgen{Private: true},
			},
			label:   jobconfig.Generated,
			podSpec: &corev1.PodSpec{Containers: []corev1.Container{{Name: "test"}}},
			expected: prowconfig.JobBase{
				Name:  "pull-ci-org-repo-branch-test",
				Agent: "kubernetes",
				Labels: map[string]string{
					"ci-operator.openshift.io/prowgen-controlled": "true",
				},
				Spec:          &corev1.PodSpec{Containers: []corev1.Container{{Name: "test"}}},
				UtilityConfig: prowconfig.UtilityConfig{Decorate: true, DecorationConfig: &prowv1.DecorationConfig{SkipCloning: &yes}},
				Hidden:        true,
			},
		},
		{
			testName: "expose job for private repos with public results",
			name:     "test",
			prefix:   "pull",
			info: &ProwgenInfo{
				Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
				Config:   config.Prowgen{Private: true, Expose: true},
			},
			label:   jobconfig.Generated,
			podSpec: &corev1.PodSpec{Containers: []corev1.Container{{Name: "test"}}},
			expected: prowconfig.JobBase{
				Name:  "pull-ci-org-repo-branch-test",
				Agent: "kubernetes",
				Labels: map[string]string{
					"ci-operator.openshift.io/prowgen-controlled": "true",
				},
				Spec:          &corev1.PodSpec{Containers: []corev1.Container{{Name: "test"}}},
				UtilityConfig: prowconfig.UtilityConfig{Decorate: true, DecorationConfig: &prowv1.DecorationConfig{SkipCloning: &yes}},
				Hidden:        false,
			},
		},
		{
			testName: "expose option set but not private",
			name:     "test",
			prefix:   "pull",
			info: &ProwgenInfo{
				Metadata: ciop.Metadata{Org: "org", Repo: "repo", Branch: "branch"},
				Config:   config.Prowgen{Private: false, Expose: true},
			},
			label:   jobconfig.Generated,
			podSpec: &corev1.PodSpec{Containers: []corev1.Container{{Name: "test"}}},
			expected: prowconfig.JobBase{
				Name:  "pull-ci-org-repo-branch-test",
				Agent: "kubernetes",
				Labels: map[string]string{
					"ci-operator.openshift.io/prowgen-controlled": "true",
				},
				Spec:          &corev1.PodSpec{Containers: []corev1.Container{{Name: "test"}}},
				UtilityConfig: prowconfig.UtilityConfig{Decorate: true, DecorationConfig: &prowv1.DecorationConfig{SkipCloning: &yes}},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.testName, func(t *testing.T) {
			if actual, expected := generateJobBase(testCase.name, testCase.prefix, testCase.info, testCase.label, testCase.podSpec, testCase.rehearsable, testCase.pathAlias), testCase.expected; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect job base: %v", testCase.testName, cmp.Diff(actual, expected, unexportedFields...))
			}
		})
	}
}

func pruneForTests(jobConfig *prowconfig.JobConfig) {
	for repo := range jobConfig.PresubmitsStatic {
		for i := range jobConfig.PresubmitsStatic[repo] {
			jobConfig.PresubmitsStatic[repo][i].AlwaysRun = false
			jobConfig.PresubmitsStatic[repo][i].Context = ""
			jobConfig.PresubmitsStatic[repo][i].Trigger = ""
			jobConfig.PresubmitsStatic[repo][i].RerunCommand = ""
			jobConfig.PresubmitsStatic[repo][i].Agent = ""
			jobConfig.PresubmitsStatic[repo][i].Spec = nil
			jobConfig.PresubmitsStatic[repo][i].Brancher = prowconfig.Brancher{}
			jobConfig.PresubmitsStatic[repo][i].UtilityConfig = prowconfig.UtilityConfig{}
		}
	}
	for repo := range jobConfig.PostsubmitsStatic {
		for i := range jobConfig.PostsubmitsStatic[repo] {
			jobConfig.PostsubmitsStatic[repo][i].Agent = ""
			jobConfig.PostsubmitsStatic[repo][i].Spec = nil
			jobConfig.PostsubmitsStatic[repo][i].Brancher = prowconfig.Brancher{}
			jobConfig.PostsubmitsStatic[repo][i].UtilityConfig = prowconfig.UtilityConfig{}
		}
	}
}
