package main

import (
	"bytes"
	"fmt"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	kubeapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/diff"
	v1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
	prowconfig "k8s.io/test-infra/prow/config"

	ciop "github.com/openshift/ci-tools/pkg/api"

	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/jobconfig"
)

func TestGeneratePodSpec(t *testing.T) {
	testSecret := &ciop.Secret{Name: "test-secret", MountPath: "/usr/local/test-secret"}
	tests := []struct {
		description    string
		info           *prowgenInfo
		secrets        []*ciop.Secret
		targets        []string
		additionalArgs []string

		expected *kubeapi.PodSpec
	}{
		{
			description: "standard use case",
			info:        &prowgenInfo{Info: config.Info{Org: "org", Repo: "repo", Branch: "branch"}},
			secrets:     nil,
			targets:     []string{"target"},

			expected: &kubeapi.PodSpec{
				ServiceAccountName: "ci-operator",
				Containers: []kubeapi.Container{{
					Image:           "ci-operator:latest",
					ImagePullPolicy: kubeapi.PullAlways,
					Command:         []string{"ci-operator"},
					Args: []string{
						"--give-pr-author-access-to-namespace=true",
						"--artifact-dir=$(ARTIFACTS)",
						"--sentry-dsn-path=/etc/sentry-dsn/ci-operator",
						"--resolver-address=http://ci-operator-configresolver-ci.svc.ci.openshift.org",
						"--org=org",
						"--repo=repo",
						"--branch=branch",
						"--kubeconfig=/etc/apici/kubeconfig",
						"--image-import-pull-secret=/etc/pull-secret/.dockerconfigjson",
						"--target=target",
					},
					Resources: kubeapi.ResourceRequirements{
						Requests: kubeapi.ResourceList{"cpu": *resource.NewMilliQuantity(10, resource.DecimalSI)},
					},
					Env: []kubeapi.EnvVar{{
						Name: "CONFIG_SPEC",
						ValueFrom: &kubeapi.EnvVarSource{
							ConfigMapKeyRef: &kubeapi.ConfigMapKeySelector{
								LocalObjectReference: kubeapi.LocalObjectReference{
									Name: "ci-operator-misc-configs",
								},
								Key: "org-repo-branch.yaml",
							},
						},
					}},
					VolumeMounts: []kubeapi.VolumeMount{{Name: "sentry-dsn", MountPath: "/etc/sentry-dsn", ReadOnly: true},
						{Name: "apici-ci-operator-credentials", ReadOnly: true, MountPath: "/etc/apici"},
						{Name: "pull-secret", ReadOnly: true, MountPath: "/etc/pull-secret"}},
				}},
				Volumes: []kubeapi.Volume{{
					Name: "sentry-dsn",
					VolumeSource: kubeapi.VolumeSource{
						Secret: &kubeapi.SecretVolumeSource{SecretName: "sentry-dsn"},
					},
				},
					{
						Name: "apici-ci-operator-credentials",
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "apici-ci-operator-credentials", Items: []kubeapi.KeyToPath{{Key: "sa.ci-operator.apici.config", Path: "kubeconfig"}}},
						},
					},
					{
						Name: "pull-secret",
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "regcred"},
						},
					},
				},
			},
		},
		{
			description:    "additional args are included in podspec",
			info:           &prowgenInfo{Info: config.Info{Org: "org", Repo: "repo", Branch: "branch"}},
			secrets:        nil,
			targets:        []string{"target"},
			additionalArgs: []string{"--promote", "--some=thing"},

			expected: &kubeapi.PodSpec{
				ServiceAccountName: "ci-operator",
				Containers: []kubeapi.Container{{
					Image:           "ci-operator:latest",
					ImagePullPolicy: kubeapi.PullAlways,
					Command:         []string{"ci-operator"},
					Args: []string{
						"--give-pr-author-access-to-namespace=true",
						"--artifact-dir=$(ARTIFACTS)",
						"--sentry-dsn-path=/etc/sentry-dsn/ci-operator",
						"--resolver-address=http://ci-operator-configresolver-ci.svc.ci.openshift.org",
						"--org=org",
						"--repo=repo",
						"--branch=branch",
						"--kubeconfig=/etc/apici/kubeconfig",
						"--image-import-pull-secret=/etc/pull-secret/.dockerconfigjson",
						"--promote",
						"--some=thing",
						"--target=target",
					},
					Resources: kubeapi.ResourceRequirements{
						Requests: kubeapi.ResourceList{"cpu": *resource.NewMilliQuantity(10, resource.DecimalSI)},
					},
					Env: []kubeapi.EnvVar{{
						Name: "CONFIG_SPEC",
						ValueFrom: &kubeapi.EnvVarSource{
							ConfigMapKeyRef: &kubeapi.ConfigMapKeySelector{
								LocalObjectReference: kubeapi.LocalObjectReference{
									Name: "ci-operator-misc-configs",
								},
								Key: "org-repo-branch.yaml",
							},
						},
					}},
					VolumeMounts: []kubeapi.VolumeMount{{Name: "sentry-dsn", MountPath: "/etc/sentry-dsn", ReadOnly: true},
						{Name: "apici-ci-operator-credentials", ReadOnly: true, MountPath: "/etc/apici"},
						{Name: "pull-secret", ReadOnly: true, MountPath: "/etc/pull-secret"}},
				}},
				Volumes: []kubeapi.Volume{{
					Name: "sentry-dsn",
					VolumeSource: kubeapi.VolumeSource{
						Secret: &kubeapi.SecretVolumeSource{SecretName: "sentry-dsn"},
					},
				},
					{
						Name: "apici-ci-operator-credentials",
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "apici-ci-operator-credentials", Items: []kubeapi.KeyToPath{{Key: "sa.ci-operator.apici.config", Path: "kubeconfig"}}},
						},
					},
					{
						Name: "pull-secret",
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "regcred"},
						},
					},
				},
			},
		},
		{
			info:           &prowgenInfo{Info: config.Info{Org: "org", Repo: "repo", Branch: "branch"}},
			secrets:        []*ciop.Secret{testSecret},
			targets:        []string{"target"},
			additionalArgs: []string{"--promote", "--some=thing"},

			expected: &kubeapi.PodSpec{
				ServiceAccountName: "ci-operator",
				Containers: []kubeapi.Container{{
					Image:           "ci-operator:latest",
					ImagePullPolicy: kubeapi.PullAlways,
					Command:         []string{"ci-operator"},
					Args: []string{
						"--give-pr-author-access-to-namespace=true",
						"--artifact-dir=$(ARTIFACTS)",
						"--sentry-dsn-path=/etc/sentry-dsn/ci-operator",
						"--resolver-address=http://ci-operator-configresolver-ci.svc.ci.openshift.org",
						"--org=org",
						"--repo=repo",
						"--branch=branch",
						"--kubeconfig=/etc/apici/kubeconfig",
						"--image-import-pull-secret=/etc/pull-secret/.dockerconfigjson",
						"--promote",
						"--some=thing",
						"--target=target",
						fmt.Sprintf("--secret-dir=%s", testSecret.MountPath),
					},
					Resources: kubeapi.ResourceRequirements{
						Requests: kubeapi.ResourceList{"cpu": *resource.NewMilliQuantity(10, resource.DecimalSI)},
					},
					Env: []kubeapi.EnvVar{{
						Name: "CONFIG_SPEC",
						ValueFrom: &kubeapi.EnvVarSource{
							ConfigMapKeyRef: &kubeapi.ConfigMapKeySelector{
								LocalObjectReference: kubeapi.LocalObjectReference{
									Name: "ci-operator-misc-configs",
								},
								Key: "org-repo-branch.yaml",
							},
						},
					}},
					VolumeMounts: []kubeapi.VolumeMount{
						{Name: "sentry-dsn", MountPath: "/etc/sentry-dsn", ReadOnly: true},
						{Name: "apici-ci-operator-credentials", ReadOnly: true, MountPath: "/etc/apici"},
						{Name: "pull-secret", ReadOnly: true, MountPath: "/etc/pull-secret"},
						{Name: testSecret.Name, MountPath: testSecret.MountPath, ReadOnly: true},
					},
				}},
				Volumes: []kubeapi.Volume{
					{
						Name: "sentry-dsn",
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "sentry-dsn"},
						},
					},
					{
						Name: "apici-ci-operator-credentials",
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "apici-ci-operator-credentials", Items: []kubeapi.KeyToPath{{Key: "sa.ci-operator.apici.config", Path: "kubeconfig"}}},
						},
					},
					{
						Name: "pull-secret",
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "regcred"},
						},
					},
					{
						Name: testSecret.Name,
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: testSecret.Name},
						},
					},
				},
			},
		},
		{
			description: "multiple targets",
			info:        &prowgenInfo{Info: config.Info{Org: "org", Repo: "repo", Branch: "branch"}},
			secrets:     nil,
			targets:     []string{"target", "more", "and-more"},

			expected: &kubeapi.PodSpec{
				ServiceAccountName: "ci-operator",
				Containers: []kubeapi.Container{{
					Image:           "ci-operator:latest",
					ImagePullPolicy: kubeapi.PullAlways,
					Command:         []string{"ci-operator"},
					Args: []string{
						"--give-pr-author-access-to-namespace=true",
						"--artifact-dir=$(ARTIFACTS)",
						"--sentry-dsn-path=/etc/sentry-dsn/ci-operator",
						"--resolver-address=http://ci-operator-configresolver-ci.svc.ci.openshift.org",
						"--org=org",
						"--repo=repo",
						"--branch=branch",
						"--kubeconfig=/etc/apici/kubeconfig",
						"--image-import-pull-secret=/etc/pull-secret/.dockerconfigjson",
						"--target=target",
						"--target=more",
						"--target=and-more",
					},
					Resources: kubeapi.ResourceRequirements{
						Requests: kubeapi.ResourceList{"cpu": *resource.NewMilliQuantity(10, resource.DecimalSI)},
					},
					Env: []kubeapi.EnvVar{{
						Name: "CONFIG_SPEC",
						ValueFrom: &kubeapi.EnvVarSource{
							ConfigMapKeyRef: &kubeapi.ConfigMapKeySelector{
								LocalObjectReference: kubeapi.LocalObjectReference{
									Name: "ci-operator-misc-configs",
								},
								Key: "org-repo-branch.yaml",
							},
						},
					}},
					VolumeMounts: []kubeapi.VolumeMount{{Name: "sentry-dsn", MountPath: "/etc/sentry-dsn", ReadOnly: true},
						{Name: "apici-ci-operator-credentials", ReadOnly: true, MountPath: "/etc/apici"},
						{Name: "pull-secret", ReadOnly: true, MountPath: "/etc/pull-secret"}},
				}},
				Volumes: []kubeapi.Volume{{
					Name: "sentry-dsn",
					VolumeSource: kubeapi.VolumeSource{
						Secret: &kubeapi.SecretVolumeSource{SecretName: "sentry-dsn"},
					},
				}, {
					Name: "apici-ci-operator-credentials",
					VolumeSource: kubeapi.VolumeSource{
						Secret: &kubeapi.SecretVolumeSource{SecretName: "apici-ci-operator-credentials", Items: []kubeapi.KeyToPath{{Key: "sa.ci-operator.apici.config", Path: "kubeconfig"}}},
					},
				},
					{
						Name: "pull-secret",
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "regcred"},
						},
					}},
			},
		},
		{
			description: "private job",
			info: &prowgenInfo{
				Info:   config.Info{Org: "org", Repo: "repo", Branch: "branch"},
				config: config.Prowgen{Private: true},
			},
			secrets: nil,
			targets: []string{"target"},

			expected: &kubeapi.PodSpec{
				ServiceAccountName: "ci-operator",
				Containers: []kubeapi.Container{
					{
						Image:           "ci-operator:latest",
						ImagePullPolicy: kubeapi.PullAlways,
						Command:         []string{"ci-operator"},
						Args: []string{
							"--give-pr-author-access-to-namespace=true",
							"--artifact-dir=$(ARTIFACTS)",
							"--sentry-dsn-path=/etc/sentry-dsn/ci-operator",
							"--resolver-address=http://ci-operator-configresolver-ci.svc.ci.openshift.org",
							"--org=org",
							"--repo=repo",
							"--branch=branch",
							"--kubeconfig=/etc/apici/kubeconfig",
							"--image-import-pull-secret=/etc/pull-secret/.dockerconfigjson",
							"--target=target",
							"--oauth-token-path=/usr/local/github-credentials/oauth",
						},
						Resources: kubeapi.ResourceRequirements{
							Requests: kubeapi.ResourceList{"cpu": *resource.NewMilliQuantity(10, resource.DecimalSI)},
						},
						Env: []kubeapi.EnvVar{{
							Name: "CONFIG_SPEC",
							ValueFrom: &kubeapi.EnvVarSource{
								ConfigMapKeyRef: &kubeapi.ConfigMapKeySelector{
									LocalObjectReference: kubeapi.LocalObjectReference{
										Name: "ci-operator-misc-configs",
									},
									Key: "org-repo-branch.yaml",
								},
							},
						}},
						VolumeMounts: []kubeapi.VolumeMount{
							{
								Name: "sentry-dsn", MountPath: "/etc/sentry-dsn", ReadOnly: true,
							},
							{Name: "apici-ci-operator-credentials", ReadOnly: true, MountPath: "/etc/apici"},
							{Name: "pull-secret", ReadOnly: true, MountPath: "/etc/pull-secret"},
							{
								Name:      "github-credentials-openshift-ci-robot-private-git-cloner",
								MountPath: "/usr/local/github-credentials",
								ReadOnly:  true,
							},
						},
					},
				},
				Volumes: []kubeapi.Volume{
					{
						Name: "sentry-dsn",
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "sentry-dsn"},
						},
					},
					{
						Name: "apici-ci-operator-credentials",
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "apici-ci-operator-credentials", Items: []kubeapi.KeyToPath{{Key: "sa.ci-operator.apici.config", Path: "kubeconfig"}}},
						},
					},
					{
						Name: "pull-secret",
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "regcred"},
						},
					},
					{
						Name: "github-credentials-openshift-ci-robot-private-git-cloner",
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "github-credentials-openshift-ci-robot-private-git-cloner"},
						},
					},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			var podSpec *kubeapi.PodSpec
			podSpec = generateCiOperatorPodSpec(tc.info, tc.secrets, tc.targets, tc.additionalArgs...)
			if !equality.Semantic.DeepEqual(podSpec, tc.expected) {
				t.Errorf("%s: expected PodSpec diff:\n%s", tc.description, diff.ObjectDiff(tc.expected, podSpec))
			}
		})
	}
}

func TestGeneratePodSpecTemplate(t *testing.T) {
	tests := []struct {
		info    *prowgenInfo
		release string
		test    ciop.TestStepConfiguration

		expected *kubeapi.PodSpec
	}{
		{
			info:    &prowgenInfo{Info: config.Info{Org: "organization", Repo: "repo", Branch: "branch"}},
			release: "origin-v4.0",
			test: ciop.TestStepConfiguration{
				As:       "test",
				Commands: "commands",
				OpenshiftAnsibleClusterTestConfiguration: &ciop.OpenshiftAnsibleClusterTestConfiguration{
					ClusterTestConfiguration: ciop.ClusterTestConfiguration{ClusterProfile: "gcp"},
				},
			},

			expected: &kubeapi.PodSpec{
				ServiceAccountName: "ci-operator",
				Volumes: []kubeapi.Volume{
					{
						Name: "sentry-dsn",
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "sentry-dsn"},
						},
					},
					{
						Name: "apici-ci-operator-credentials",
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "apici-ci-operator-credentials", Items: []kubeapi.KeyToPath{{Key: "sa.ci-operator.apici.config", Path: "kubeconfig"}}},
						},
					},
					{
						Name: "pull-secret",
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "regcred"},
						},
					},
					{
						Name: "job-definition",
						VolumeSource: kubeapi.VolumeSource{
							ConfigMap: &kubeapi.ConfigMapVolumeSource{
								LocalObjectReference: kubeapi.LocalObjectReference{
									Name: "prow-job-cluster-launch-e2e",
								},
							},
						},
					},
					{
						Name: "cluster-profile",
						VolumeSource: kubeapi.VolumeSource{
							Projected: &kubeapi.ProjectedVolumeSource{
								Sources: []kubeapi.VolumeProjection{
									{
										Secret: &kubeapi.SecretProjection{
											LocalObjectReference: kubeapi.LocalObjectReference{
												Name: "cluster-secrets-gcp",
											},
										},
									},
									{
										ConfigMap: &kubeapi.ConfigMapProjection{
											LocalObjectReference: kubeapi.LocalObjectReference{
												Name: "cluster-profile-gcp",
											},
										},
									},
								},
							},
						},
					},
				},
				Containers: []kubeapi.Container{{
					Image:           "ci-operator:latest",
					ImagePullPolicy: kubeapi.PullAlways,
					Command:         []string{"ci-operator"},
					Args: []string{
						"--give-pr-author-access-to-namespace=true",
						"--artifact-dir=$(ARTIFACTS)",
						"--sentry-dsn-path=/etc/sentry-dsn/ci-operator",
						"--resolver-address=http://ci-operator-configresolver-ci.svc.ci.openshift.org",
						"--org=organization",
						"--repo=repo",
						"--branch=branch",
						"--kubeconfig=/etc/apici/kubeconfig",
						"--image-import-pull-secret=/etc/pull-secret/.dockerconfigjson",
						"--target=test",
						"--secret-dir=/usr/local/test-cluster-profile",
						"--template=/usr/local/test"},
					Resources: kubeapi.ResourceRequirements{
						Requests: kubeapi.ResourceList{"cpu": *resource.NewMilliQuantity(10, resource.DecimalSI)},
					},
					Env: []kubeapi.EnvVar{
						{
							Name: "CONFIG_SPEC",
							ValueFrom: &kubeapi.EnvVarSource{
								ConfigMapKeyRef: &kubeapi.ConfigMapKeySelector{
									LocalObjectReference: kubeapi.LocalObjectReference{
										Name: "ci-operator-misc-configs",
									},
									Key: "organization-repo-branch.yaml",
								},
							},
						},
						{Name: "CLUSTER_TYPE", Value: "gcp"},
						{Name: "JOB_NAME_SAFE", Value: "test"},
						{Name: "TEST_COMMAND", Value: "commands"},
						{Name: "RPM_REPO_OPENSHIFT_ORIGIN", Value: "https://rpms.svc.ci.openshift.org/openshift-origin-v4.0/"},
					},
					VolumeMounts: []kubeapi.VolumeMount{
						{Name: "sentry-dsn", MountPath: "/etc/sentry-dsn", ReadOnly: true},
						{Name: "apici-ci-operator-credentials", ReadOnly: true, MountPath: "/etc/apici"},
						{Name: "pull-secret", ReadOnly: true, MountPath: "/etc/pull-secret"},
						{Name: "cluster-profile", MountPath: "/usr/local/test-cluster-profile"},
						{Name: "job-definition", MountPath: "/usr/local/test", SubPath: "cluster-launch-e2e.yaml"},
					},
				}},
			},
		},
		{
			info:    &prowgenInfo{Info: config.Info{Org: "organization", Repo: "repo", Branch: "branch"}},
			release: "origin-v4.0",
			test: ciop.TestStepConfiguration{
				As:       "test",
				Commands: "commands",
				OpenshiftInstallerClusterTestConfiguration: &ciop.OpenshiftInstallerClusterTestConfiguration{
					ClusterTestConfiguration: ciop.ClusterTestConfiguration{ClusterProfile: "aws"},
				},
			},

			expected: &kubeapi.PodSpec{
				ServiceAccountName: "ci-operator",
				Volumes: []kubeapi.Volume{
					{
						Name: "sentry-dsn",
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "sentry-dsn"},
						},
					},
					{
						Name: "apici-ci-operator-credentials",
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "apici-ci-operator-credentials", Items: []kubeapi.KeyToPath{{Key: "sa.ci-operator.apici.config", Path: "kubeconfig"}}},
						},
					},
					{
						Name: "pull-secret",
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "regcred"},
						},
					},
					{
						Name: "job-definition",
						VolumeSource: kubeapi.VolumeSource{
							ConfigMap: &kubeapi.ConfigMapVolumeSource{
								LocalObjectReference: kubeapi.LocalObjectReference{
									Name: "prow-job-cluster-launch-installer-e2e",
								},
							},
						},
					},
					{
						Name: "cluster-profile",
						VolumeSource: kubeapi.VolumeSource{
							Projected: &kubeapi.ProjectedVolumeSource{
								Sources: []kubeapi.VolumeProjection{
									{
										Secret: &kubeapi.SecretProjection{
											LocalObjectReference: kubeapi.LocalObjectReference{
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
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "boskos-credentials", Items: []kubeapi.KeyToPath{{Key: "password", Path: "password"}}},
						},
					},
				},
				Containers: []kubeapi.Container{{
					Image:           "ci-operator:latest",
					ImagePullPolicy: kubeapi.PullAlways,
					Command:         []string{"ci-operator"},
					Args: []string{
						"--give-pr-author-access-to-namespace=true",
						"--artifact-dir=$(ARTIFACTS)",
						"--sentry-dsn-path=/etc/sentry-dsn/ci-operator",
						"--resolver-address=http://ci-operator-configresolver-ci.svc.ci.openshift.org",
						"--org=organization",
						"--repo=repo",
						"--branch=branch",
						"--kubeconfig=/etc/apici/kubeconfig",
						"--image-import-pull-secret=/etc/pull-secret/.dockerconfigjson",
						"--target=test",
						"--secret-dir=/usr/local/test-cluster-profile",
						"--template=/usr/local/test",
						"--lease-server=https://boskos-ci.svc.ci.openshift.org",
						"--lease-server-username=ci",
						"--lease-server-password-file=/etc/boskos/password",
					},
					Resources: kubeapi.ResourceRequirements{
						Requests: kubeapi.ResourceList{"cpu": *resource.NewMilliQuantity(10, resource.DecimalSI)},
					},
					Env: []kubeapi.EnvVar{
						{
							Name: "CONFIG_SPEC",
							ValueFrom: &kubeapi.EnvVarSource{
								ConfigMapKeyRef: &kubeapi.ConfigMapKeySelector{
									LocalObjectReference: kubeapi.LocalObjectReference{
										Name: "ci-operator-misc-configs",
									},
									Key: "organization-repo-branch.yaml",
								},
							},
						},
						{Name: "CLUSTER_TYPE", Value: "aws"},
						{Name: "JOB_NAME_SAFE", Value: "test"},
						{Name: "TEST_COMMAND", Value: "commands"},
					},
					VolumeMounts: []kubeapi.VolumeMount{
						{Name: "sentry-dsn", MountPath: "/etc/sentry-dsn", ReadOnly: true},
						{Name: "apici-ci-operator-credentials", ReadOnly: true, MountPath: "/etc/apici"},
						{Name: "pull-secret", ReadOnly: true, MountPath: "/etc/pull-secret"},
						{Name: "boskos", ReadOnly: true, MountPath: "/etc/boskos"},
						{Name: "cluster-profile", MountPath: "/usr/local/test-cluster-profile"},
						{Name: "job-definition", MountPath: "/usr/local/test", SubPath: "cluster-launch-installer-e2e.yaml"},
					},
				}},
			},
		},
		{
			info:    &prowgenInfo{Info: config.Info{Org: "organization", Repo: "repo", Branch: "branch"}},
			release: "origin-v4.0",
			test: ciop.TestStepConfiguration{
				As: "test",
				MultiStageTestConfiguration: &ciop.MultiStageTestConfiguration{
					ClusterProfile: ciop.ClusterProfileAWS,
				},
			},
			expected: &kubeapi.PodSpec{
				ServiceAccountName: "ci-operator",
				Volumes: []kubeapi.Volume{
					{
						Name: "sentry-dsn",
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "sentry-dsn"},
						},
					},
					{
						Name: "apici-ci-operator-credentials",
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "apici-ci-operator-credentials", Items: []kubeapi.KeyToPath{{Key: "sa.ci-operator.apici.config", Path: "kubeconfig"}}},
						},
					},
					{
						Name: "pull-secret",
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "regcred"},
						},
					},
					{
						Name: "cluster-profile",
						VolumeSource: kubeapi.VolumeSource{
							Projected: &kubeapi.ProjectedVolumeSource{
								Sources: []kubeapi.VolumeProjection{
									{
										Secret: &kubeapi.SecretProjection{
											LocalObjectReference: kubeapi.LocalObjectReference{
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
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "boskos-credentials", Items: []kubeapi.KeyToPath{{Key: "password", Path: "password"}}},
						},
					},
				},
				Containers: []kubeapi.Container{{
					Image:           "ci-operator:latest",
					ImagePullPolicy: kubeapi.PullAlways,
					Command:         []string{"ci-operator"},
					Args: []string{
						"--give-pr-author-access-to-namespace=true",
						"--artifact-dir=$(ARTIFACTS)",
						"--sentry-dsn-path=/etc/sentry-dsn/ci-operator",
						"--resolver-address=http://ci-operator-configresolver-ci.svc.ci.openshift.org",
						"--org=organization",
						"--repo=repo",
						"--branch=branch",
						"--kubeconfig=/etc/apici/kubeconfig",
						"--image-import-pull-secret=/etc/pull-secret/.dockerconfigjson",
						"--target=test",
						"--secret-dir=/usr/local/test-cluster-profile",
						"--lease-server=https://boskos-ci.svc.ci.openshift.org",
						"--lease-server-username=ci",
						"--lease-server-password-file=/etc/boskos/password",
					},
					Resources: kubeapi.ResourceRequirements{
						Requests: kubeapi.ResourceList{"cpu": *resource.NewMilliQuantity(10, resource.DecimalSI)},
					},
					Env: []kubeapi.EnvVar{
						{
							Name: "CONFIG_SPEC",
							ValueFrom: &kubeapi.EnvVarSource{
								ConfigMapKeyRef: &kubeapi.ConfigMapKeySelector{
									LocalObjectReference: kubeapi.LocalObjectReference{
										Name: "ci-operator-misc-configs",
									},
									Key: "organization-repo-branch.yaml",
								},
							},
						},
					},
					VolumeMounts: []kubeapi.VolumeMount{
						{Name: "sentry-dsn", MountPath: "/etc/sentry-dsn", ReadOnly: true},
						{Name: "apici-ci-operator-credentials", ReadOnly: true, MountPath: "/etc/apici"},
						{Name: "pull-secret", ReadOnly: true, MountPath: "/etc/pull-secret"},
						{Name: "boskos", ReadOnly: true, MountPath: "/etc/boskos"},
						{Name: "cluster-profile", MountPath: "/usr/local/test-cluster-profile"},
					},
				}},
			},
		},
		{
			info:    &prowgenInfo{Info: config.Info{Org: "organization", Repo: "repo", Branch: "branch"}},
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

			expected: &kubeapi.PodSpec{
				ServiceAccountName: "ci-operator",
				Volumes: []kubeapi.Volume{
					{
						Name: "sentry-dsn",
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "sentry-dsn"},
						},
					},
					{
						Name: "apici-ci-operator-credentials",
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "apici-ci-operator-credentials", Items: []kubeapi.KeyToPath{{Key: "sa.ci-operator.apici.config", Path: "kubeconfig"}}},
						},
					},
					{
						Name: "pull-secret",
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "regcred"},
						},
					},
					{
						Name: "job-definition",
						VolumeSource: kubeapi.VolumeSource{
							ConfigMap: &kubeapi.ConfigMapVolumeSource{
								LocalObjectReference: kubeapi.LocalObjectReference{
									Name: "prow-job-cluster-launch-installer-custom-test-image",
								},
							},
						},
					},
					{
						Name: "cluster-profile",
						VolumeSource: kubeapi.VolumeSource{
							Projected: &kubeapi.ProjectedVolumeSource{
								Sources: []kubeapi.VolumeProjection{
									{
										Secret: &kubeapi.SecretProjection{
											LocalObjectReference: kubeapi.LocalObjectReference{
												Name: "cluster-secrets-gcp",
											},
										},
									},
									{
										ConfigMap: &kubeapi.ConfigMapProjection{
											LocalObjectReference: kubeapi.LocalObjectReference{
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
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "boskos-credentials", Items: []kubeapi.KeyToPath{{Key: "password", Path: "password"}}},
						},
					},
				},
				Containers: []kubeapi.Container{{
					Image:           "ci-operator:latest",
					ImagePullPolicy: kubeapi.PullAlways,
					Command:         []string{"ci-operator"},
					Args: []string{
						"--give-pr-author-access-to-namespace=true",
						"--artifact-dir=$(ARTIFACTS)",
						"--sentry-dsn-path=/etc/sentry-dsn/ci-operator",
						"--resolver-address=http://ci-operator-configresolver-ci.svc.ci.openshift.org",
						"--org=organization",
						"--repo=repo",
						"--branch=branch",
						"--kubeconfig=/etc/apici/kubeconfig",
						"--image-import-pull-secret=/etc/pull-secret/.dockerconfigjson",
						"--target=test",
						"--secret-dir=/usr/local/test-cluster-profile",
						"--template=/usr/local/test",
						"--lease-server=https://boskos-ci.svc.ci.openshift.org",
						"--lease-server-username=ci",
						"--lease-server-password-file=/etc/boskos/password",
					},
					Resources: kubeapi.ResourceRequirements{
						Requests: kubeapi.ResourceList{"cpu": *resource.NewMilliQuantity(10, resource.DecimalSI)},
					},
					Env: []kubeapi.EnvVar{
						{
							Name: "CONFIG_SPEC",
							ValueFrom: &kubeapi.EnvVarSource{
								ConfigMapKeyRef: &kubeapi.ConfigMapKeySelector{
									LocalObjectReference: kubeapi.LocalObjectReference{
										Name: "ci-operator-misc-configs",
									},
									Key: "organization-repo-branch.yaml",
								},
							},
						},
						{Name: "CLUSTER_TYPE", Value: "gcp"},
						{Name: "JOB_NAME_SAFE", Value: "test"},
						{Name: "TEST_COMMAND", Value: "commands"},
						{Name: "TEST_IMAGESTREAM_TAG", Value: "pipeline:kubevirt-test"},
						{Name: "CLUSTER_ENABLE_NESTED_VIRT", Value: "true"},
						{Name: "CLUSTER_NESTED_VIRT_IMAGE", Value: "nested-virt-image-name"},
					},
					VolumeMounts: []kubeapi.VolumeMount{
						{Name: "sentry-dsn", MountPath: "/etc/sentry-dsn", ReadOnly: true},
						{Name: "apici-ci-operator-credentials", ReadOnly: true, MountPath: "/etc/apici"},
						{Name: "pull-secret", ReadOnly: true, MountPath: "/etc/pull-secret"},
						{Name: "boskos", ReadOnly: true, MountPath: "/etc/boskos"},
						{Name: "cluster-profile", MountPath: "/usr/local/test-cluster-profile"},
						{Name: "job-definition", MountPath: "/usr/local/test", SubPath: "cluster-launch-installer-custom-test-image.yaml"},
					},
				}},
			},
		},
		{
			info:    &prowgenInfo{Info: config.Info{Org: "organization", Repo: "repo", Branch: "branch"}},
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

			expected: &kubeapi.PodSpec{
				ServiceAccountName: "ci-operator",
				Volumes: []kubeapi.Volume{
					{
						Name: "sentry-dsn",
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "sentry-dsn"},
						},
					},
					{
						Name: "apici-ci-operator-credentials",
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "apici-ci-operator-credentials", Items: []kubeapi.KeyToPath{{Key: "sa.ci-operator.apici.config", Path: "kubeconfig"}}},
						},
					},
					{
						Name: "pull-secret",
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "regcred"},
						},
					},
					{
						Name: "job-definition",
						VolumeSource: kubeapi.VolumeSource{
							ConfigMap: &kubeapi.ConfigMapVolumeSource{
								LocalObjectReference: kubeapi.LocalObjectReference{
									Name: "prow-job-cluster-launch-installer-custom-test-image",
								},
							},
						},
					},
					{
						Name: "cluster-profile",
						VolumeSource: kubeapi.VolumeSource{
							Projected: &kubeapi.ProjectedVolumeSource{
								Sources: []kubeapi.VolumeProjection{
									{
										Secret: &kubeapi.SecretProjection{
											LocalObjectReference: kubeapi.LocalObjectReference{
												Name: "cluster-secrets-gcp",
											},
										},
									},
									{
										ConfigMap: &kubeapi.ConfigMapProjection{
											LocalObjectReference: kubeapi.LocalObjectReference{
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
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "boskos-credentials", Items: []kubeapi.KeyToPath{{Key: "password", Path: "password"}}},
						},
					},
				},
				Containers: []kubeapi.Container{{
					Image:           "ci-operator:latest",
					ImagePullPolicy: kubeapi.PullAlways,
					Command:         []string{"ci-operator"},
					Args: []string{
						"--give-pr-author-access-to-namespace=true",
						"--artifact-dir=$(ARTIFACTS)",
						"--sentry-dsn-path=/etc/sentry-dsn/ci-operator",
						"--resolver-address=http://ci-operator-configresolver-ci.svc.ci.openshift.org",
						"--org=organization",
						"--repo=repo",
						"--branch=branch",
						"--kubeconfig=/etc/apici/kubeconfig",
						"--image-import-pull-secret=/etc/pull-secret/.dockerconfigjson",
						"--target=test",
						"--secret-dir=/usr/local/test-cluster-profile",
						"--template=/usr/local/test",
						"--lease-server=https://boskos-ci.svc.ci.openshift.org",
						"--lease-server-username=ci",
						"--lease-server-password-file=/etc/boskos/password",
					},
					Resources: kubeapi.ResourceRequirements{
						Requests: kubeapi.ResourceList{"cpu": *resource.NewMilliQuantity(10, resource.DecimalSI)},
					},
					Env: []kubeapi.EnvVar{
						{
							Name: "CONFIG_SPEC",
							ValueFrom: &kubeapi.EnvVarSource{
								ConfigMapKeyRef: &kubeapi.ConfigMapKeySelector{
									LocalObjectReference: kubeapi.LocalObjectReference{
										Name: "ci-operator-misc-configs",
									},
									Key: "organization-repo-branch.yaml",
								},
							},
						},
						{Name: "CLUSTER_TYPE", Value: "gcp"},
						{Name: "JOB_NAME_SAFE", Value: "test"},
						{Name: "TEST_COMMAND", Value: "commands"},
						{Name: "TEST_IMAGESTREAM_TAG", Value: "pipeline:kubevirt-test"},
						{Name: "CLUSTER_ENABLE_NESTED_VIRT", Value: "true"},
					},
					VolumeMounts: []kubeapi.VolumeMount{
						{Name: "sentry-dsn", MountPath: "/etc/sentry-dsn", ReadOnly: true},
						{Name: "apici-ci-operator-credentials", ReadOnly: true, MountPath: "/etc/apici"},
						{Name: "pull-secret", ReadOnly: true, MountPath: "/etc/pull-secret"},
						{Name: "boskos", ReadOnly: true, MountPath: "/etc/boskos"},
						{Name: "cluster-profile", MountPath: "/usr/local/test-cluster-profile"},
						{Name: "job-definition", MountPath: "/usr/local/test", SubPath: "cluster-launch-installer-custom-test-image.yaml"},
					},
				}},
			},
		},
		{
			info:    &prowgenInfo{Info: config.Info{Org: "organization", Repo: "repo", Branch: "branch"}},
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

			expected: &kubeapi.PodSpec{
				ServiceAccountName: "ci-operator",
				Volumes: []kubeapi.Volume{
					{
						Name: "sentry-dsn",
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "sentry-dsn"},
						},
					},
					{
						Name: "apici-ci-operator-credentials",
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "apici-ci-operator-credentials", Items: []kubeapi.KeyToPath{{Key: "sa.ci-operator.apici.config", Path: "kubeconfig"}}},
						},
					},
					{
						Name: "pull-secret",
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "regcred"},
						},
					},
					{
						Name: "job-definition",
						VolumeSource: kubeapi.VolumeSource{
							ConfigMap: &kubeapi.ConfigMapVolumeSource{
								LocalObjectReference: kubeapi.LocalObjectReference{
									Name: "prow-job-cluster-launch-installer-custom-test-image",
								},
							},
						},
					},
					{
						Name: "cluster-profile",
						VolumeSource: kubeapi.VolumeSource{
							Projected: &kubeapi.ProjectedVolumeSource{
								Sources: []kubeapi.VolumeProjection{
									{
										Secret: &kubeapi.SecretProjection{
											LocalObjectReference: kubeapi.LocalObjectReference{
												Name: "cluster-secrets-gcp",
											},
										},
									},
									{
										ConfigMap: &kubeapi.ConfigMapProjection{
											LocalObjectReference: kubeapi.LocalObjectReference{
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
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "boskos-credentials", Items: []kubeapi.KeyToPath{{Key: "password", Path: "password"}}},
						},
					},
				},
				Containers: []kubeapi.Container{{
					Image:           "ci-operator:latest",
					ImagePullPolicy: kubeapi.PullAlways,
					Command:         []string{"ci-operator"},
					Args: []string{
						"--give-pr-author-access-to-namespace=true",
						"--artifact-dir=$(ARTIFACTS)",
						"--sentry-dsn-path=/etc/sentry-dsn/ci-operator",
						"--resolver-address=http://ci-operator-configresolver-ci.svc.ci.openshift.org",
						"--org=organization",
						"--repo=repo",
						"--branch=branch",
						"--kubeconfig=/etc/apici/kubeconfig",
						"--image-import-pull-secret=/etc/pull-secret/.dockerconfigjson",
						"--target=test",
						"--secret-dir=/usr/local/test-cluster-profile",
						"--template=/usr/local/test",
						"--lease-server=https://boskos-ci.svc.ci.openshift.org",
						"--lease-server-username=ci",
						"--lease-server-password-file=/etc/boskos/password",
					},
					Resources: kubeapi.ResourceRequirements{
						Requests: kubeapi.ResourceList{"cpu": *resource.NewMilliQuantity(10, resource.DecimalSI)},
					},
					Env: []kubeapi.EnvVar{
						{
							Name: "CONFIG_SPEC",
							ValueFrom: &kubeapi.EnvVarSource{
								ConfigMapKeyRef: &kubeapi.ConfigMapKeySelector{
									LocalObjectReference: kubeapi.LocalObjectReference{
										Name: "ci-operator-misc-configs",
									},
									Key: "organization-repo-branch.yaml",
								},
							},
						},
						{Name: "CLUSTER_TYPE", Value: "gcp"},
						{Name: "JOB_NAME_SAFE", Value: "test"},
						{Name: "TEST_COMMAND", Value: "commands"},
						{Name: "TEST_IMAGESTREAM_TAG", Value: "pipeline:kubevirt-test"},
					},
					VolumeMounts: []kubeapi.VolumeMount{
						{Name: "sentry-dsn", MountPath: "/etc/sentry-dsn", ReadOnly: true},
						{Name: "apici-ci-operator-credentials", ReadOnly: true, MountPath: "/etc/apici"},
						{Name: "pull-secret", ReadOnly: true, MountPath: "/etc/pull-secret"},
						{Name: "boskos", ReadOnly: true, MountPath: "/etc/boskos"},
						{Name: "cluster-profile", MountPath: "/usr/local/test-cluster-profile"},
						{Name: "job-definition", MountPath: "/usr/local/test", SubPath: "cluster-launch-installer-custom-test-image.yaml"},
					},
				}},
			},
		},
		{
			info:    &prowgenInfo{Info: config.Info{Org: "organization", Repo: "repo", Branch: "branch"}},
			release: "origin-v4.0",
			test: ciop.TestStepConfiguration{
				As:       "test",
				Commands: "commands",
				OpenshiftInstallerCustomTestImageClusterTestConfiguration: &ciop.OpenshiftInstallerCustomTestImageClusterTestConfiguration{
					ClusterTestConfiguration: ciop.ClusterTestConfiguration{ClusterProfile: "gcp"},
					From:                     "pipeline:kubevirt-test",
				},
			},

			expected: &kubeapi.PodSpec{
				ServiceAccountName: "ci-operator",
				Volumes: []kubeapi.Volume{
					{
						Name: "sentry-dsn",
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "sentry-dsn"},
						},
					},
					{
						Name: "apici-ci-operator-credentials",
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "apici-ci-operator-credentials", Items: []kubeapi.KeyToPath{{Key: "sa.ci-operator.apici.config", Path: "kubeconfig"}}},
						},
					},
					{
						Name: "pull-secret",
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "regcred"},
						},
					},
					{
						Name: "job-definition",
						VolumeSource: kubeapi.VolumeSource{
							ConfigMap: &kubeapi.ConfigMapVolumeSource{
								LocalObjectReference: kubeapi.LocalObjectReference{
									Name: "prow-job-cluster-launch-installer-custom-test-image",
								},
							},
						},
					},
					{
						Name: "cluster-profile",
						VolumeSource: kubeapi.VolumeSource{
							Projected: &kubeapi.ProjectedVolumeSource{
								Sources: []kubeapi.VolumeProjection{
									{
										Secret: &kubeapi.SecretProjection{
											LocalObjectReference: kubeapi.LocalObjectReference{
												Name: "cluster-secrets-gcp",
											},
										},
									},
									{
										ConfigMap: &kubeapi.ConfigMapProjection{
											LocalObjectReference: kubeapi.LocalObjectReference{
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
						VolumeSource: kubeapi.VolumeSource{
							Secret: &kubeapi.SecretVolumeSource{SecretName: "boskos-credentials", Items: []kubeapi.KeyToPath{{Key: "password", Path: "password"}}},
						},
					},
				},
				Containers: []kubeapi.Container{{
					Image:           "ci-operator:latest",
					ImagePullPolicy: kubeapi.PullAlways,
					Command:         []string{"ci-operator"},
					Args: []string{
						"--give-pr-author-access-to-namespace=true",
						"--artifact-dir=$(ARTIFACTS)",
						"--sentry-dsn-path=/etc/sentry-dsn/ci-operator",
						"--resolver-address=http://ci-operator-configresolver-ci.svc.ci.openshift.org",
						"--org=organization",
						"--repo=repo",
						"--branch=branch",
						"--kubeconfig=/etc/apici/kubeconfig",
						"--image-import-pull-secret=/etc/pull-secret/.dockerconfigjson",
						"--target=test",
						"--secret-dir=/usr/local/test-cluster-profile",
						"--template=/usr/local/test",
						"--lease-server=https://boskos-ci.svc.ci.openshift.org",
						"--lease-server-username=ci",
						"--lease-server-password-file=/etc/boskos/password",
					},
					Resources: kubeapi.ResourceRequirements{
						Requests: kubeapi.ResourceList{"cpu": *resource.NewMilliQuantity(10, resource.DecimalSI)},
					},
					Env: []kubeapi.EnvVar{
						{
							Name: "CONFIG_SPEC",
							ValueFrom: &kubeapi.EnvVarSource{
								ConfigMapKeyRef: &kubeapi.ConfigMapKeySelector{
									LocalObjectReference: kubeapi.LocalObjectReference{
										Name: "ci-operator-misc-configs",
									},
									Key: "organization-repo-branch.yaml",
								},
							},
						},
						{Name: "CLUSTER_TYPE", Value: "gcp"},
						{Name: "JOB_NAME_SAFE", Value: "test"},
						{Name: "TEST_COMMAND", Value: "commands"},
						{Name: "TEST_IMAGESTREAM_TAG", Value: "pipeline:kubevirt-test"},
					},
					VolumeMounts: []kubeapi.VolumeMount{
						{Name: "sentry-dsn", MountPath: "/etc/sentry-dsn", ReadOnly: true},
						{Name: "apici-ci-operator-credentials", ReadOnly: true, MountPath: "/etc/apici"},
						{Name: "pull-secret", ReadOnly: true, MountPath: "/etc/pull-secret"},
						{Name: "boskos", ReadOnly: true, MountPath: "/etc/boskos"},
						{Name: "cluster-profile", MountPath: "/usr/local/test-cluster-profile"},
						{Name: "job-definition", MountPath: "/usr/local/test", SubPath: "cluster-launch-installer-custom-test-image.yaml"},
					},
				}},
			},
		},
	}

	for _, tc := range tests {
		podSpec := generatePodSpecOthers(tc.info, tc.release, &tc.test)
		if !equality.Semantic.DeepEqual(podSpec, tc.expected) {
			t.Errorf("expected PodSpec diff:\n%s", diff.ObjectDiff(tc.expected, podSpec))
		}
	}
}

func TestGeneratePresubmitForTest(t *testing.T) {
	newTrue := true
	standardJobLabels := map[string]string{
		"ci-operator.openshift.io/prowgen-controlled": "true",
		"pj-rehearse.openshift.io/can-be-rehearsed":   "true"}

	tests := []struct {
		name     string
		repoInfo *prowgenInfo
		expected *prowconfig.Presubmit
	}{{
		name:     "testname",
		repoInfo: &prowgenInfo{Info: config.Info{Org: "org", Repo: "repo", Branch: "branch"}},

		expected: &prowconfig.Presubmit{
			JobBase: prowconfig.JobBase{
				Agent:  "kubernetes",
				Labels: standardJobLabels,
				Name:   "pull-ci-org-repo-branch-testname",
				UtilityConfig: prowconfig.UtilityConfig{
					DecorationConfig: &v1.DecorationConfig{SkipCloning: &newTrue},
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
	}}
	for _, tc := range tests {
		presubmit := generatePresubmitForTest(tc.name, tc.repoInfo, jobconfig.Generated, nil, true, nil) // podSpec tested in generatePodSpec
		if !equality.Semantic.DeepEqual(presubmit, tc.expected) {
			t.Errorf("expected presubmit diff:\n%s", diff.ObjectDiff(tc.expected, presubmit))
		}
	}
}

func TestGeneratePostSubmitForTest(t *testing.T) {
	newTrue := true
	standardJobLabels := map[string]string{"ci-operator.openshift.io/prowgen-controlled": "true"}
	tests := []struct {
		name     string
		repoInfo *prowgenInfo

		expected *prowconfig.Postsubmit
	}{
		{
			name: "name",
			repoInfo: &prowgenInfo{Info: config.Info{
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
						DecorationConfig: &v1.DecorationConfig{SkipCloning: &newTrue},
						Decorate:         true,
					},
				},

				Brancher: prowconfig.Brancher{Branches: []string{"^branch$"}},
			},
		},
		{
			name: "Name",
			repoInfo: &prowgenInfo{Info: config.Info{
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
						DecorationConfig: &v1.DecorationConfig{SkipCloning: &newTrue},
						Decorate:         true,
					}},
				Brancher: prowconfig.Brancher{Branches: []string{"^Branch$"}},
			},
		},
		{
			name: "name",
			repoInfo: &prowgenInfo{Info: config.Info{
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
						DecorationConfig: &v1.DecorationConfig{SkipCloning: &newTrue},
						Decorate:         true,
					}},
				Brancher: prowconfig.Brancher{Branches: []string{"^Branch$"}},
			},
		},
	}
	for _, tc := range tests {
		postsubmit := generatePostsubmitForTest(tc.name, tc.repoInfo, jobconfig.Generated, nil, nil) // podSpec tested in TestGeneratePodSpec
		if !equality.Semantic.DeepEqual(postsubmit, tc.expected) {
			t.Errorf("expected postsubmit diff:\n%s", diff.ObjectDiff(tc.expected, postsubmit))
		}
	}
}

func TestGenerateJobs(t *testing.T) {
	standardPresubmitJobLabels := map[string]string{
		"ci-operator.openshift.io/prowgen-controlled": "true",
		"pj-rehearse.openshift.io/can-be-rehearsed":   "true"}
	standardPostsubmitJobLabels := map[string]string{"ci-operator.openshift.io/prowgen-controlled": "true"}

	tests := []struct {
		id       string
		config   *ciop.ReleaseBuildConfiguration
		repoInfo *prowgenInfo
		expected *prowconfig.JobConfig
	}{
		{
			id: "two tests and empty Images so only two test presubmits are generated",
			config: &ciop.ReleaseBuildConfiguration{
				Tests: []ciop.TestStepConfiguration{
					{As: "derTest", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "from"}},
					{As: "leTest", ContainerTestConfiguration: &ciop.ContainerTestConfiguration{From: "from"}}},
			},
			repoInfo: &prowgenInfo{Info: config.Info{
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
					Optional: true,
					JobBase: prowconfig.JobBase{
						Name:    "pull-ci-organization-repository-branch-derTest-build01",
						Labels:  standardPresubmitJobLabels,
						Cluster: "ci/api-build01-ci-devcluster-openshift-com:6443",
					}, Reporter: prowconfig.Reporter{SkipReport: true}}, {
					JobBase: prowconfig.JobBase{
						Name:   "pull-ci-organization-repository-branch-leTest",
						Labels: standardPresubmitJobLabels,
					}}, {
					Optional: true,
					JobBase: prowconfig.JobBase{
						Name:    "pull-ci-organization-repository-branch-leTest-build01",
						Labels:  standardPresubmitJobLabels,
						Cluster: "ci/api-build01-ci-devcluster-openshift-com:6443",
					}, Reporter: prowconfig.Reporter{SkipReport: true}},
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
			repoInfo: &prowgenInfo{Info: config.Info{
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
					Optional: true,
					JobBase: prowconfig.JobBase{
						Name:    "pull-ci-organization-repository-branch-derTest-build01",
						Labels:  standardPresubmitJobLabels,
						Cluster: "ci/api-build01-ci-devcluster-openshift-com:6443",
					}, Reporter: prowconfig.Reporter{SkipReport: true}}, {
					JobBase: prowconfig.JobBase{
						Name:   "pull-ci-organization-repository-branch-leTest",
						Labels: standardPresubmitJobLabels,
					}}, {
					Optional: true,
					JobBase: prowconfig.JobBase{
						Name:    "pull-ci-organization-repository-branch-leTest-build01",
						Labels:  standardPresubmitJobLabels,
						Cluster: "ci/api-build01-ci-devcluster-openshift-com:6443",
					}, Reporter: prowconfig.Reporter{SkipReport: true}}, {
					JobBase: prowconfig.JobBase{
						Name:   "pull-ci-organization-repository-branch-images",
						Labels: standardPresubmitJobLabels,
					}}, {
					Optional: true,
					JobBase: prowconfig.JobBase{
						Name:    "pull-ci-organization-repository-branch-images-build01",
						Labels:  standardPresubmitJobLabels,
						Cluster: "ci/api-build01-ci-devcluster-openshift-com:6443",
					}, Reporter: prowconfig.Reporter{SkipReport: true}},
				}},
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{"organization/repository": {{
					JobBase: prowconfig.JobBase{
						Name:   "branch-ci-organization-repository-branch-images",
						Labels: standardPostsubmitJobLabels,
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
			repoInfo: &prowgenInfo{Info: config.Info{
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
			repoInfo: &prowgenInfo{Info: config.Info{
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
			repoInfo: &prowgenInfo{Info: config.Info{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			}},
			expected: &prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{"organization/repository": {{
					JobBase: prowconfig.JobBase{
						Name:   "pull-ci-organization-repository-branch-images",
						Labels: standardPresubmitJobLabels,
					}}, {
					Optional: true,
					JobBase: prowconfig.JobBase{
						Name:    "pull-ci-organization-repository-branch-images-build01",
						Labels:  standardPresubmitJobLabels,
						Cluster: "ci/api-build01-ci-devcluster-openshift-com:6443",
					}, Reporter: prowconfig.Reporter{SkipReport: true}},
				}},
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{"organization/repository": {{
					JobBase: prowconfig.JobBase{
						Name:   "branch-ci-organization-repository-branch-images",
						Labels: standardPostsubmitJobLabels,
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
			repoInfo: &prowgenInfo{Info: config.Info{
				Org:    "organization",
				Repo:   "repository",
				Branch: "branch",
			}},
			expected: &prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{"organization/repository": {{
					JobBase: prowconfig.JobBase{
						Name:   "pull-ci-organization-repository-branch-images",
						Labels: standardPresubmitJobLabels,
					}}, {
					Optional: true,
					JobBase: prowconfig.JobBase{
						Name:    "pull-ci-organization-repository-branch-images-build01",
						Labels:  standardPresubmitJobLabels,
						Cluster: "ci/api-build01-ci-devcluster-openshift-com:6443",
					}, Reporter: prowconfig.Reporter{SkipReport: true}},
				}},
			},
		},
	}

	log.SetOutput(ioutil.Discard)
	for _, tc := range tests {
		jobConfig := generateJobs(tc.config, tc.repoInfo, jobconfig.Generated)

		pruneForTests(jobConfig) // prune the fields that are tested in TestGeneratePre/PostsubmitForTest

		if !equality.Semantic.DeepEqual(jobConfig, tc.expected) {
			t.Errorf("testcase: %s\nexpected job config diff:\n%s", tc.id, compare(tc.expected, jobConfig))
		}
	}
}

func compare(a, b *prowconfig.JobConfig) string {
	return cmp.Diff(
		a, b,
		cmpopts.IgnoreUnexported(prowconfig.Presubmit{}),
		cmpopts.IgnoreUnexported(prowconfig.Brancher{}),
		cmpopts.IgnoreUnexported(prowconfig.RegexpChangeMatcher{}),
	)
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

func TestFromCIOperatorConfigToProwYaml(t *testing.T) {
	tests := []struct {
		id                         string
		org                        string
		component                  string
		branch                     string
		variant                    string
		configYAML                 []byte
		prowOldPresubmitYAML       []byte
		prowOldPostsubmitYAML      []byte
		prowExpectedPresubmitYAML  []byte
		prowExpectedPostsubmitYAML []byte
	}{
		{
			id:        "one test and images, no previous jobs. Expect test presubmit + pre/post submit images jobs",
			org:       "super",
			component: "duper",
			branch:    "branch",
			configYAML: []byte(`base_images:
  base:
    cluster: https://api.ci.openshift.org
    name: origin-v3.11
    namespace: openshift
    tag: base
build_root:
  image_stream_tag:
    cluster: https://api.ci.openshift.org
    name: release
    namespace: openshift
    tag: golang-1.10
images:
- from: base
  to: service-serving-cert-signer
resources:
  '*':
    limits:
      cpu: 500Mi
    requests:
      cpu: 10Mi
tag_specification:
  cluster: https://api.ci.openshift.org
  name: origin-v3.11
  namespace: openshift
  tag: ''
promotion:
  namespace: ci
  name: other
tests:
- as: unit
  commands: make test-unit
  container:
    from: src`),
			prowOldPresubmitYAML:  []byte(""),
			prowOldPostsubmitYAML: []byte(""),
			prowExpectedPostsubmitYAML: []byte(`postsubmits:
  super/duper:
  - agent: kubernetes
    branches:
    - ^branch$
    decorate: true
    decoration_config:
      skip_cloning: true
    labels:
      ci-operator.openshift.io/prowgen-controlled: "true"
    name: branch-ci-super-duper-branch-images
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --branch=branch
        - --give-pr-author-access-to-namespace=true
        - --image-import-pull-secret=/etc/pull-secret/.dockerconfigjson
        - --kubeconfig=/etc/apici/kubeconfig
        - --org=super
        - --promote
        - --repo=duper
        - --resolver-address=http://ci-operator-configresolver-ci.svc.ci.openshift.org
        - --sentry-dsn-path=/etc/sentry-dsn/ci-operator
        - --target=[images]
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: super-duper-branch.yaml
              name: ci-operator-misc-configs
        image: ci-operator:latest
        imagePullPolicy: Always
        name: ""
        resources:
          requests:
            cpu: 10m
        volumeMounts:
        - mountPath: /etc/apici
          name: apici-ci-operator-credentials
          readOnly: true
        - mountPath: /etc/pull-secret
          name: pull-secret
          readOnly: true
        - mountPath: /etc/sentry-dsn
          name: sentry-dsn
          readOnly: true
      serviceAccountName: ci-operator
      volumes:
      - name: apici-ci-operator-credentials
        secret:
          items:
          - key: sa.ci-operator.apici.config
            path: kubeconfig
          secretName: apici-ci-operator-credentials
      - name: pull-secret
        secret:
          secretName: regcred
      - name: sentry-dsn
        secret:
          secretName: sentry-dsn
`),
			prowExpectedPresubmitYAML: []byte(`presubmits:
  super/duper:
  - agent: kubernetes
    always_run: true
    branches:
    - branch
    context: ci/prow/images
    decorate: true
    decoration_config:
      skip_cloning: true
    labels:
      ci-operator.openshift.io/prowgen-controlled: "true"
      pj-rehearse.openshift.io/can-be-rehearsed: "true"
    name: pull-ci-super-duper-branch-images
    rerun_command: /test images
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --branch=branch
        - --give-pr-author-access-to-namespace=true
        - --image-import-pull-secret=/etc/pull-secret/.dockerconfigjson
        - --kubeconfig=/etc/apici/kubeconfig
        - --org=super
        - --repo=duper
        - --resolver-address=http://ci-operator-configresolver-ci.svc.ci.openshift.org
        - --sentry-dsn-path=/etc/sentry-dsn/ci-operator
        - --target=[images]
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: super-duper-branch.yaml
              name: ci-operator-misc-configs
        image: ci-operator:latest
        imagePullPolicy: Always
        name: ""
        resources:
          requests:
            cpu: 10m
        volumeMounts:
        - mountPath: /etc/apici
          name: apici-ci-operator-credentials
          readOnly: true
        - mountPath: /etc/pull-secret
          name: pull-secret
          readOnly: true
        - mountPath: /etc/sentry-dsn
          name: sentry-dsn
          readOnly: true
      serviceAccountName: ci-operator
      volumes:
      - name: apici-ci-operator-credentials
        secret:
          items:
          - key: sa.ci-operator.apici.config
            path: kubeconfig
          secretName: apici-ci-operator-credentials
      - name: pull-secret
        secret:
          secretName: regcred
      - name: sentry-dsn
        secret:
          secretName: sentry-dsn
    trigger: (?m)^/test( | .* )images,?($|\s.*)
  - agent: kubernetes
    always_run: true
    branches:
    - branch
    cluster: ci/api-build01-ci-devcluster-openshift-com:6443
    context: ci/prow/images-build01
    decorate: true
    decoration_config:
      skip_cloning: true
    labels:
      ci-operator.openshift.io/prowgen-controlled: "true"
      pj-rehearse.openshift.io/can-be-rehearsed: "true"
    name: pull-ci-super-duper-branch-images-build01
    optional: true
    rerun_command: /test images-build01
    skip_report: true
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --branch=branch
        - --give-pr-author-access-to-namespace=true
        - --image-import-pull-secret=/etc/pull-secret/.dockerconfigjson
        - --kubeconfig=/etc/apici/kubeconfig
        - --org=super
        - --repo=duper
        - --resolver-address=http://ci-operator-configresolver-ci.svc.ci.openshift.org
        - --sentry-dsn-path=/etc/sentry-dsn/ci-operator
        - --target=[images]
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: super-duper-branch.yaml
              name: ci-operator-misc-configs
        image: ci-operator:latest
        imagePullPolicy: Always
        name: ""
        resources:
          requests:
            cpu: 10m
        volumeMounts:
        - mountPath: /etc/apici
          name: apici-ci-operator-credentials
          readOnly: true
        - mountPath: /etc/pull-secret
          name: pull-secret
          readOnly: true
        - mountPath: /etc/sentry-dsn
          name: sentry-dsn
          readOnly: true
      serviceAccountName: ci-operator
      volumes:
      - name: apici-ci-operator-credentials
        secret:
          items:
          - key: sa.ci-operator.apici.config
            path: kubeconfig
          secretName: apici-ci-operator-credentials
      - name: pull-secret
        secret:
          secretName: regcred
      - name: sentry-dsn
        secret:
          secretName: sentry-dsn
    trigger: (?m)^/test( | .* )images-build01,?($|\s.*)
  - agent: kubernetes
    always_run: true
    branches:
    - branch
    context: ci/prow/unit
    decorate: true
    decoration_config:
      skip_cloning: true
    labels:
      ci-operator.openshift.io/prowgen-controlled: "true"
      pj-rehearse.openshift.io/can-be-rehearsed: "true"
    name: pull-ci-super-duper-branch-unit
    rerun_command: /test unit
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --branch=branch
        - --give-pr-author-access-to-namespace=true
        - --image-import-pull-secret=/etc/pull-secret/.dockerconfigjson
        - --kubeconfig=/etc/apici/kubeconfig
        - --org=super
        - --repo=duper
        - --resolver-address=http://ci-operator-configresolver-ci.svc.ci.openshift.org
        - --sentry-dsn-path=/etc/sentry-dsn/ci-operator
        - --target=unit
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: super-duper-branch.yaml
              name: ci-operator-misc-configs
        image: ci-operator:latest
        imagePullPolicy: Always
        name: ""
        resources:
          requests:
            cpu: 10m
        volumeMounts:
        - mountPath: /etc/apici
          name: apici-ci-operator-credentials
          readOnly: true
        - mountPath: /etc/pull-secret
          name: pull-secret
          readOnly: true
        - mountPath: /etc/sentry-dsn
          name: sentry-dsn
          readOnly: true
      serviceAccountName: ci-operator
      volumes:
      - name: apici-ci-operator-credentials
        secret:
          items:
          - key: sa.ci-operator.apici.config
            path: kubeconfig
          secretName: apici-ci-operator-credentials
      - name: pull-secret
        secret:
          secretName: regcred
      - name: sentry-dsn
        secret:
          secretName: sentry-dsn
    trigger: (?m)^/test( | .* )unit,?($|\s.*)
  - agent: kubernetes
    always_run: true
    branches:
    - branch
    cluster: ci/api-build01-ci-devcluster-openshift-com:6443
    context: ci/prow/unit-build01
    decorate: true
    decoration_config:
      skip_cloning: true
    labels:
      ci-operator.openshift.io/prowgen-controlled: "true"
      pj-rehearse.openshift.io/can-be-rehearsed: "true"
    name: pull-ci-super-duper-branch-unit-build01
    optional: true
    rerun_command: /test unit-build01
    skip_report: true
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --branch=branch
        - --give-pr-author-access-to-namespace=true
        - --image-import-pull-secret=/etc/pull-secret/.dockerconfigjson
        - --kubeconfig=/etc/apici/kubeconfig
        - --org=super
        - --repo=duper
        - --resolver-address=http://ci-operator-configresolver-ci.svc.ci.openshift.org
        - --sentry-dsn-path=/etc/sentry-dsn/ci-operator
        - --target=unit
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: super-duper-branch.yaml
              name: ci-operator-misc-configs
        image: ci-operator:latest
        imagePullPolicy: Always
        name: ""
        resources:
          requests:
            cpu: 10m
        volumeMounts:
        - mountPath: /etc/apici
          name: apici-ci-operator-credentials
          readOnly: true
        - mountPath: /etc/pull-secret
          name: pull-secret
          readOnly: true
        - mountPath: /etc/sentry-dsn
          name: sentry-dsn
          readOnly: true
      serviceAccountName: ci-operator
      volumes:
      - name: apici-ci-operator-credentials
        secret:
          items:
          - key: sa.ci-operator.apici.config
            path: kubeconfig
          secretName: apici-ci-operator-credentials
      - name: pull-secret
        secret:
          secretName: regcred
      - name: sentry-dsn
        secret:
          secretName: sentry-dsn
    trigger: (?m)^/test( | .* )unit-build01,?($|\s.*)
`),
		}, {
			id:        "Using a variant config, one test and images, one existing job. Expect one presubmit, pre/post submit images jobs. Existing job should not be changed.",
			org:       "super",
			component: "duper",
			branch:    "branch",
			variant:   "rhel",
			configYAML: []byte(`base_images:
  base:
    cluster: https://api.ci.openshift.org
    name: origin-v3.11
    namespace: openshift
    tag: base
build_root:
  image_stream_tag:
    cluster: https://api.ci.openshift.org
    name: release
    namespace: openshift
    tag: golang-1.10
images:
- from: base
  to: service-serving-cert-signer
resources:
  '*':
    limits:
      cpu: 500Mi
    requests:
      cpu: 10Mi
tag_specification:
  cluster: https://api.ci.openshift.org
  name: origin-v3.11
  namespace: openshift
  tag: ''
promotion:
  name: test
  namespace: ci
tests:
- as: unit
  commands: make test-unit
  container:
    from: src`),
			prowOldPresubmitYAML: []byte(""),
			prowOldPostsubmitYAML: []byte(`postsubmits:
  super/duper:
  - agent: kubernetes
    branches:
    - branch
    decorate: true
    decoration_config:
      skip_cloning: true
    name: branch-ci-super-duper-branch-do-not-overwrite
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --give-pr-author-access-to-namespace=true
        - --target=unit
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: super-duper-branch__rhel.yaml
              name: ci-operator-misc-configs
        image: ci-operator:latest
        imagePullPolicy: Always
        name: ""
        resources:
          requests:
            cpu: 10m
      serviceAccountName: ci-operator
`),
			prowExpectedPresubmitYAML: []byte(`presubmits:
  super/duper:
  - agent: kubernetes
    always_run: true
    branches:
    - branch
    context: ci/prow/rhel-images
    decorate: true
    decoration_config:
      skip_cloning: true
    labels:
      ci-operator.openshift.io/prowgen-controlled: "true"
      ci-operator.openshift.io/variant: rhel
      pj-rehearse.openshift.io/can-be-rehearsed: "true"
    name: pull-ci-super-duper-branch-rhel-images
    rerun_command: /test rhel-images
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --branch=branch
        - --give-pr-author-access-to-namespace=true
        - --image-import-pull-secret=/etc/pull-secret/.dockerconfigjson
        - --kubeconfig=/etc/apici/kubeconfig
        - --org=super
        - --repo=duper
        - --resolver-address=http://ci-operator-configresolver-ci.svc.ci.openshift.org
        - --sentry-dsn-path=/etc/sentry-dsn/ci-operator
        - --target=[images]
        - --variant=rhel
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: super-duper-branch__rhel.yaml
              name: ci-operator-misc-configs
        image: ci-operator:latest
        imagePullPolicy: Always
        name: ""
        resources:
          requests:
            cpu: 10m
        volumeMounts:
        - mountPath: /etc/apici
          name: apici-ci-operator-credentials
          readOnly: true
        - mountPath: /etc/pull-secret
          name: pull-secret
          readOnly: true
        - mountPath: /etc/sentry-dsn
          name: sentry-dsn
          readOnly: true
      serviceAccountName: ci-operator
      volumes:
      - name: apici-ci-operator-credentials
        secret:
          items:
          - key: sa.ci-operator.apici.config
            path: kubeconfig
          secretName: apici-ci-operator-credentials
      - name: pull-secret
        secret:
          secretName: regcred
      - name: sentry-dsn
        secret:
          secretName: sentry-dsn
    trigger: (?m)^/test( | .* )rhel-images,?($|\s.*)
  - agent: kubernetes
    always_run: true
    branches:
    - branch
    cluster: ci/api-build01-ci-devcluster-openshift-com:6443
    context: ci/prow/rhel-images-build01
    decorate: true
    decoration_config:
      skip_cloning: true
    labels:
      ci-operator.openshift.io/prowgen-controlled: "true"
      ci-operator.openshift.io/variant: rhel
      pj-rehearse.openshift.io/can-be-rehearsed: "true"
    name: pull-ci-super-duper-branch-rhel-images-build01
    optional: true
    rerun_command: /test rhel-images-build01
    skip_report: true
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --branch=branch
        - --give-pr-author-access-to-namespace=true
        - --image-import-pull-secret=/etc/pull-secret/.dockerconfigjson
        - --kubeconfig=/etc/apici/kubeconfig
        - --org=super
        - --repo=duper
        - --resolver-address=http://ci-operator-configresolver-ci.svc.ci.openshift.org
        - --sentry-dsn-path=/etc/sentry-dsn/ci-operator
        - --target=[images]
        - --variant=rhel
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: super-duper-branch__rhel.yaml
              name: ci-operator-misc-configs
        image: ci-operator:latest
        imagePullPolicy: Always
        name: ""
        resources:
          requests:
            cpu: 10m
        volumeMounts:
        - mountPath: /etc/apici
          name: apici-ci-operator-credentials
          readOnly: true
        - mountPath: /etc/pull-secret
          name: pull-secret
          readOnly: true
        - mountPath: /etc/sentry-dsn
          name: sentry-dsn
          readOnly: true
      serviceAccountName: ci-operator
      volumes:
      - name: apici-ci-operator-credentials
        secret:
          items:
          - key: sa.ci-operator.apici.config
            path: kubeconfig
          secretName: apici-ci-operator-credentials
      - name: pull-secret
        secret:
          secretName: regcred
      - name: sentry-dsn
        secret:
          secretName: sentry-dsn
    trigger: (?m)^/test( | .* )rhel-images-build01,?($|\s.*)
  - agent: kubernetes
    always_run: true
    branches:
    - branch
    context: ci/prow/rhel-unit
    decorate: true
    decoration_config:
      skip_cloning: true
    labels:
      ci-operator.openshift.io/prowgen-controlled: "true"
      ci-operator.openshift.io/variant: rhel
      pj-rehearse.openshift.io/can-be-rehearsed: "true"
    name: pull-ci-super-duper-branch-rhel-unit
    rerun_command: /test rhel-unit
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --branch=branch
        - --give-pr-author-access-to-namespace=true
        - --image-import-pull-secret=/etc/pull-secret/.dockerconfigjson
        - --kubeconfig=/etc/apici/kubeconfig
        - --org=super
        - --repo=duper
        - --resolver-address=http://ci-operator-configresolver-ci.svc.ci.openshift.org
        - --sentry-dsn-path=/etc/sentry-dsn/ci-operator
        - --target=unit
        - --variant=rhel
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: super-duper-branch__rhel.yaml
              name: ci-operator-misc-configs
        image: ci-operator:latest
        imagePullPolicy: Always
        name: ""
        resources:
          requests:
            cpu: 10m
        volumeMounts:
        - mountPath: /etc/apici
          name: apici-ci-operator-credentials
          readOnly: true
        - mountPath: /etc/pull-secret
          name: pull-secret
          readOnly: true
        - mountPath: /etc/sentry-dsn
          name: sentry-dsn
          readOnly: true
      serviceAccountName: ci-operator
      volumes:
      - name: apici-ci-operator-credentials
        secret:
          items:
          - key: sa.ci-operator.apici.config
            path: kubeconfig
          secretName: apici-ci-operator-credentials
      - name: pull-secret
        secret:
          secretName: regcred
      - name: sentry-dsn
        secret:
          secretName: sentry-dsn
    trigger: (?m)^/test( | .* )rhel-unit,?($|\s.*)
  - agent: kubernetes
    always_run: true
    branches:
    - branch
    cluster: ci/api-build01-ci-devcluster-openshift-com:6443
    context: ci/prow/rhel-unit-build01
    decorate: true
    decoration_config:
      skip_cloning: true
    labels:
      ci-operator.openshift.io/prowgen-controlled: "true"
      ci-operator.openshift.io/variant: rhel
      pj-rehearse.openshift.io/can-be-rehearsed: "true"
    name: pull-ci-super-duper-branch-rhel-unit-build01
    optional: true
    rerun_command: /test rhel-unit-build01
    skip_report: true
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --branch=branch
        - --give-pr-author-access-to-namespace=true
        - --image-import-pull-secret=/etc/pull-secret/.dockerconfigjson
        - --kubeconfig=/etc/apici/kubeconfig
        - --org=super
        - --repo=duper
        - --resolver-address=http://ci-operator-configresolver-ci.svc.ci.openshift.org
        - --sentry-dsn-path=/etc/sentry-dsn/ci-operator
        - --target=unit
        - --variant=rhel
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: super-duper-branch__rhel.yaml
              name: ci-operator-misc-configs
        image: ci-operator:latest
        imagePullPolicy: Always
        name: ""
        resources:
          requests:
            cpu: 10m
        volumeMounts:
        - mountPath: /etc/apici
          name: apici-ci-operator-credentials
          readOnly: true
        - mountPath: /etc/pull-secret
          name: pull-secret
          readOnly: true
        - mountPath: /etc/sentry-dsn
          name: sentry-dsn
          readOnly: true
      serviceAccountName: ci-operator
      volumes:
      - name: apici-ci-operator-credentials
        secret:
          items:
          - key: sa.ci-operator.apici.config
            path: kubeconfig
          secretName: apici-ci-operator-credentials
      - name: pull-secret
        secret:
          secretName: regcred
      - name: sentry-dsn
        secret:
          secretName: sentry-dsn
    trigger: (?m)^/test( | .* )rhel-unit-build01,?($|\s.*)
`),
			prowExpectedPostsubmitYAML: []byte(`postsubmits:
  super/duper:
  - agent: kubernetes
    branches:
    - branch
    decorate: true
    decoration_config:
      skip_cloning: true
    name: branch-ci-super-duper-branch-do-not-overwrite
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --give-pr-author-access-to-namespace=true
        - --target=unit
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: super-duper-branch__rhel.yaml
              name: ci-operator-misc-configs
        image: ci-operator:latest
        imagePullPolicy: Always
        name: ""
        resources:
          requests:
            cpu: 10m
      serviceAccountName: ci-operator
  - agent: kubernetes
    branches:
    - ^branch$
    decorate: true
    decoration_config:
      skip_cloning: true
    labels:
      ci-operator.openshift.io/prowgen-controlled: "true"
      ci-operator.openshift.io/variant: rhel
    name: branch-ci-super-duper-branch-rhel-images
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --branch=branch
        - --give-pr-author-access-to-namespace=true
        - --image-import-pull-secret=/etc/pull-secret/.dockerconfigjson
        - --kubeconfig=/etc/apici/kubeconfig
        - --org=super
        - --promote
        - --repo=duper
        - --resolver-address=http://ci-operator-configresolver-ci.svc.ci.openshift.org
        - --sentry-dsn-path=/etc/sentry-dsn/ci-operator
        - --target=[images]
        - --variant=rhel
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: super-duper-branch__rhel.yaml
              name: ci-operator-misc-configs
        image: ci-operator:latest
        imagePullPolicy: Always
        name: ""
        resources:
          requests:
            cpu: 10m
        volumeMounts:
        - mountPath: /etc/apici
          name: apici-ci-operator-credentials
          readOnly: true
        - mountPath: /etc/pull-secret
          name: pull-secret
          readOnly: true
        - mountPath: /etc/sentry-dsn
          name: sentry-dsn
          readOnly: true
      serviceAccountName: ci-operator
      volumes:
      - name: apici-ci-operator-credentials
        secret:
          items:
          - key: sa.ci-operator.apici.config
            path: kubeconfig
          secretName: apici-ci-operator-credentials
      - name: pull-secret
        secret:
          secretName: regcred
      - name: sentry-dsn
        secret:
          secretName: sentry-dsn
`),
		}, {
			id:        "Input is YAML and it is correctly processed",
			org:       "super",
			component: "duper",
			branch:    "branch",
			configYAML: []byte(`base_images:
  base:
    cluster: https://api.ci.openshift.org
    name: origin-v3.11
    namespace: openshift
    tag: base
images:
- from: base
  to: service-serving-cert-signer
resources:
  '*':
    limits:
      cpu: 500Mi
    requests:
      cpu: 10Mi
tag_specification:
  cluster: https://api.ci.openshift.org
  name: origin-v3.11
  namespace: openshift
  tag: ''
promotion:
  name: test
  namespace: ci
build_root:
  image_stream_tag:
    cluster: https://api.ci.openshift.org
    namespace: openshift
    name: release
    tag: golang-1.10
tests:
- as: unit
  commands: make test-unit
  container:
    from: src
`),
			prowOldPresubmitYAML: []byte(""),
			prowOldPostsubmitYAML: []byte(`postsubmits:
  super/duper:
  - agent: kubernetes
    decorate: true
    decoration_config:
      skip_cloning: true
    name: branch-ci-super-duper-branch-do-not-overwrite
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --give-pr-author-access-to-namespace=true
        - --target=unit
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: super-duper-branch.yaml
              name: ci-operator-misc-configs
        image: ci-operator:latest
        imagePullPolicy: Always
        name: ""
        resources:
          requests:
            cpu: 10m
      serviceAccountName: ci-operator
`),
			prowExpectedPresubmitYAML: []byte(`presubmits:
  super/duper:
  - agent: kubernetes
    always_run: true
    branches:
    - branch
    context: ci/prow/images
    decorate: true
    decoration_config:
      skip_cloning: true
    labels:
      ci-operator.openshift.io/prowgen-controlled: "true"
      pj-rehearse.openshift.io/can-be-rehearsed: "true"
    name: pull-ci-super-duper-branch-images
    rerun_command: /test images
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --branch=branch
        - --give-pr-author-access-to-namespace=true
        - --image-import-pull-secret=/etc/pull-secret/.dockerconfigjson
        - --kubeconfig=/etc/apici/kubeconfig
        - --org=super
        - --repo=duper
        - --resolver-address=http://ci-operator-configresolver-ci.svc.ci.openshift.org
        - --sentry-dsn-path=/etc/sentry-dsn/ci-operator
        - --target=[images]
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: super-duper-branch.yaml
              name: ci-operator-misc-configs
        image: ci-operator:latest
        imagePullPolicy: Always
        name: ""
        resources:
          requests:
            cpu: 10m
        volumeMounts:
        - mountPath: /etc/apici
          name: apici-ci-operator-credentials
          readOnly: true
        - mountPath: /etc/pull-secret
          name: pull-secret
          readOnly: true
        - mountPath: /etc/sentry-dsn
          name: sentry-dsn
          readOnly: true
      serviceAccountName: ci-operator
      volumes:
      - name: apici-ci-operator-credentials
        secret:
          items:
          - key: sa.ci-operator.apici.config
            path: kubeconfig
          secretName: apici-ci-operator-credentials
      - name: pull-secret
        secret:
          secretName: regcred
      - name: sentry-dsn
        secret:
          secretName: sentry-dsn
    trigger: (?m)^/test( | .* )images,?($|\s.*)
  - agent: kubernetes
    always_run: true
    branches:
    - branch
    cluster: ci/api-build01-ci-devcluster-openshift-com:6443
    context: ci/prow/images-build01
    decorate: true
    decoration_config:
      skip_cloning: true
    labels:
      ci-operator.openshift.io/prowgen-controlled: "true"
      pj-rehearse.openshift.io/can-be-rehearsed: "true"
    name: pull-ci-super-duper-branch-images-build01
    optional: true
    rerun_command: /test images-build01
    skip_report: true
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --branch=branch
        - --give-pr-author-access-to-namespace=true
        - --image-import-pull-secret=/etc/pull-secret/.dockerconfigjson
        - --kubeconfig=/etc/apici/kubeconfig
        - --org=super
        - --repo=duper
        - --resolver-address=http://ci-operator-configresolver-ci.svc.ci.openshift.org
        - --sentry-dsn-path=/etc/sentry-dsn/ci-operator
        - --target=[images]
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: super-duper-branch.yaml
              name: ci-operator-misc-configs
        image: ci-operator:latest
        imagePullPolicy: Always
        name: ""
        resources:
          requests:
            cpu: 10m
        volumeMounts:
        - mountPath: /etc/apici
          name: apici-ci-operator-credentials
          readOnly: true
        - mountPath: /etc/pull-secret
          name: pull-secret
          readOnly: true
        - mountPath: /etc/sentry-dsn
          name: sentry-dsn
          readOnly: true
      serviceAccountName: ci-operator
      volumes:
      - name: apici-ci-operator-credentials
        secret:
          items:
          - key: sa.ci-operator.apici.config
            path: kubeconfig
          secretName: apici-ci-operator-credentials
      - name: pull-secret
        secret:
          secretName: regcred
      - name: sentry-dsn
        secret:
          secretName: sentry-dsn
    trigger: (?m)^/test( | .* )images-build01,?($|\s.*)
  - agent: kubernetes
    always_run: true
    branches:
    - branch
    context: ci/prow/unit
    decorate: true
    decoration_config:
      skip_cloning: true
    labels:
      ci-operator.openshift.io/prowgen-controlled: "true"
      pj-rehearse.openshift.io/can-be-rehearsed: "true"
    name: pull-ci-super-duper-branch-unit
    rerun_command: /test unit
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --branch=branch
        - --give-pr-author-access-to-namespace=true
        - --image-import-pull-secret=/etc/pull-secret/.dockerconfigjson
        - --kubeconfig=/etc/apici/kubeconfig
        - --org=super
        - --repo=duper
        - --resolver-address=http://ci-operator-configresolver-ci.svc.ci.openshift.org
        - --sentry-dsn-path=/etc/sentry-dsn/ci-operator
        - --target=unit
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: super-duper-branch.yaml
              name: ci-operator-misc-configs
        image: ci-operator:latest
        imagePullPolicy: Always
        name: ""
        resources:
          requests:
            cpu: 10m
        volumeMounts:
        - mountPath: /etc/apici
          name: apici-ci-operator-credentials
          readOnly: true
        - mountPath: /etc/pull-secret
          name: pull-secret
          readOnly: true
        - mountPath: /etc/sentry-dsn
          name: sentry-dsn
          readOnly: true
      serviceAccountName: ci-operator
      volumes:
      - name: apici-ci-operator-credentials
        secret:
          items:
          - key: sa.ci-operator.apici.config
            path: kubeconfig
          secretName: apici-ci-operator-credentials
      - name: pull-secret
        secret:
          secretName: regcred
      - name: sentry-dsn
        secret:
          secretName: sentry-dsn
    trigger: (?m)^/test( | .* )unit,?($|\s.*)
  - agent: kubernetes
    always_run: true
    branches:
    - branch
    cluster: ci/api-build01-ci-devcluster-openshift-com:6443
    context: ci/prow/unit-build01
    decorate: true
    decoration_config:
      skip_cloning: true
    labels:
      ci-operator.openshift.io/prowgen-controlled: "true"
      pj-rehearse.openshift.io/can-be-rehearsed: "true"
    name: pull-ci-super-duper-branch-unit-build01
    optional: true
    rerun_command: /test unit-build01
    skip_report: true
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --branch=branch
        - --give-pr-author-access-to-namespace=true
        - --image-import-pull-secret=/etc/pull-secret/.dockerconfigjson
        - --kubeconfig=/etc/apici/kubeconfig
        - --org=super
        - --repo=duper
        - --resolver-address=http://ci-operator-configresolver-ci.svc.ci.openshift.org
        - --sentry-dsn-path=/etc/sentry-dsn/ci-operator
        - --target=unit
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: super-duper-branch.yaml
              name: ci-operator-misc-configs
        image: ci-operator:latest
        imagePullPolicy: Always
        name: ""
        resources:
          requests:
            cpu: 10m
        volumeMounts:
        - mountPath: /etc/apici
          name: apici-ci-operator-credentials
          readOnly: true
        - mountPath: /etc/pull-secret
          name: pull-secret
          readOnly: true
        - mountPath: /etc/sentry-dsn
          name: sentry-dsn
          readOnly: true
      serviceAccountName: ci-operator
      volumes:
      - name: apici-ci-operator-credentials
        secret:
          items:
          - key: sa.ci-operator.apici.config
            path: kubeconfig
          secretName: apici-ci-operator-credentials
      - name: pull-secret
        secret:
          secretName: regcred
      - name: sentry-dsn
        secret:
          secretName: sentry-dsn
    trigger: (?m)^/test( | .* )unit-build01,?($|\s.*)
`),
			prowExpectedPostsubmitYAML: []byte(`postsubmits:
  super/duper:
  - agent: kubernetes
    decorate: true
    decoration_config:
      skip_cloning: true
    name: branch-ci-super-duper-branch-do-not-overwrite
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --give-pr-author-access-to-namespace=true
        - --target=unit
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: super-duper-branch.yaml
              name: ci-operator-misc-configs
        image: ci-operator:latest
        imagePullPolicy: Always
        name: ""
        resources:
          requests:
            cpu: 10m
      serviceAccountName: ci-operator
  - agent: kubernetes
    branches:
    - ^branch$
    decorate: true
    decoration_config:
      skip_cloning: true
    labels:
      ci-operator.openshift.io/prowgen-controlled: "true"
    name: branch-ci-super-duper-branch-images
    spec:
      containers:
      - args:
        - --artifact-dir=$(ARTIFACTS)
        - --branch=branch
        - --give-pr-author-access-to-namespace=true
        - --image-import-pull-secret=/etc/pull-secret/.dockerconfigjson
        - --kubeconfig=/etc/apici/kubeconfig
        - --org=super
        - --promote
        - --repo=duper
        - --resolver-address=http://ci-operator-configresolver-ci.svc.ci.openshift.org
        - --sentry-dsn-path=/etc/sentry-dsn/ci-operator
        - --target=[images]
        command:
        - ci-operator
        env:
        - name: CONFIG_SPEC
          valueFrom:
            configMapKeyRef:
              key: super-duper-branch.yaml
              name: ci-operator-misc-configs
        image: ci-operator:latest
        imagePullPolicy: Always
        name: ""
        resources:
          requests:
            cpu: 10m
        volumeMounts:
        - mountPath: /etc/apici
          name: apici-ci-operator-credentials
          readOnly: true
        - mountPath: /etc/pull-secret
          name: pull-secret
          readOnly: true
        - mountPath: /etc/sentry-dsn
          name: sentry-dsn
          readOnly: true
      serviceAccountName: ci-operator
      volumes:
      - name: apici-ci-operator-credentials
        secret:
          items:
          - key: sa.ci-operator.apici.config
            path: kubeconfig
          secretName: apici-ci-operator-credentials
      - name: pull-secret
        secret:
          secretName: regcred
      - name: sentry-dsn
        secret:
          secretName: sentry-dsn
`),
		},
	}
	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			tempDir, err := ioutil.TempDir("", "prowgen-test")
			if err != nil {
				t.Fatalf("Unexpected error creating tmpdir: %v", err)
			}
			defer os.RemoveAll(tempDir)

			configDir := filepath.Join(tempDir, "config", tc.org, tc.component)
			if err = os.MkdirAll(configDir, os.ModePerm); err != nil {
				t.Fatalf("Unexpected error config dir: %v", err)
			}

			basename := strings.Join([]string{tc.org, tc.component, tc.branch}, "-")
			if tc.variant != "" {
				basename = fmt.Sprintf("%s__%s", basename, tc.variant)
			}

			fullConfigPath := filepath.Join(configDir, fmt.Sprintf("%s.yaml", basename))
			if err = ioutil.WriteFile(fullConfigPath, tc.configYAML, 0664); err != nil {
				t.Fatalf("Unexpected error writing config file: %v", err)
			}

			baseProwConfigDir := filepath.Join(tempDir, "jobs")
			fullProwConfigDir := filepath.Join(baseProwConfigDir, tc.org, tc.component)
			if err := os.MkdirAll(fullProwConfigDir, os.ModePerm); err != nil {
				t.Fatalf("Unexpected error creating jobs dir: %v", err)
			}
			presubmitPath := filepath.Join(fullProwConfigDir, fmt.Sprintf("%s-%s-%s-presubmits.yaml", tc.org, tc.component, tc.branch))
			if err = ioutil.WriteFile(presubmitPath, tc.prowOldPresubmitYAML, 0664); err != nil {
				t.Fatalf("Unexpected error writing old presubmits: %v", err)
			}
			postsubmitPath := filepath.Join(fullProwConfigDir, fmt.Sprintf("%s-%s-%s-postsubmits.yaml", tc.org, tc.component, tc.branch))
			if err = ioutil.WriteFile(postsubmitPath, tc.prowOldPostsubmitYAML, 0664); err != nil {
				t.Fatalf("Unexpected error writing old postsubmits: %v", err)
			}

			if err := config.OperateOnCIOperatorConfig(fullConfigPath, generateJobsToDir(baseProwConfigDir, jobconfig.Generated)); err != nil {
				t.Fatalf("Unexpected error generating jobs from config: %v", err)
			}

			presubmitData, err := ioutil.ReadFile(presubmitPath)
			if err != nil {
				t.Fatalf("Unexpected error reading generated presubmits: %v", err)
			}

			if !bytes.Equal(presubmitData, tc.prowExpectedPresubmitYAML) {
				t.Errorf("Generated Prow presubmit YAML differs from expected!\n%s", diff.StringDiff(string(tc.prowExpectedPresubmitYAML), string(presubmitData)))
			}

			postsubmitData, err := ioutil.ReadFile(postsubmitPath)
			if err != nil {
				t.Fatalf("Unexpected error reading generated postsubmits: %v", err)
			}

			if !bytes.Equal(postsubmitData, tc.prowExpectedPostsubmitYAML) {
				t.Errorf("Generated Prow postsubmit YAML differs from expected!\n%s", diff.StringDiff(string(tc.prowExpectedPostsubmitYAML), string(postsubmitData)))
			}
		})
	}
}

func TestPruneStaleJobs(t *testing.T) {
	testCases := []struct {
		name           string
		jobconfig      *prowconfig.JobConfig
		expectedPruned bool
	}{
		{
			name: "stale generated presubmit is pruned",
			jobconfig: &prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{
					"repo": {{JobBase: prowconfig.JobBase{Labels: map[string]string{jobconfig.ProwJobLabelGenerated: string(jobconfig.Generated)}}}},
				},
			},
			expectedPruned: true,
		},
		{
			name: "stale generated postsubmit is pruned",
			jobconfig: &prowconfig.JobConfig{
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{
					"repo": {{JobBase: prowconfig.JobBase{Labels: map[string]string{jobconfig.ProwJobLabelGenerated: string(jobconfig.Generated)}}}},
				},
			},
			expectedPruned: true,
		},
		{
			name: "not stale generated presubmit is kept",
			jobconfig: &prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{
					"repo": {{JobBase: prowconfig.JobBase{Labels: map[string]string{jobconfig.ProwJobLabelGenerated: string(jobconfig.New)}}}},
				},
			},
			expectedPruned: false,
		},
		{
			name: "not stale generated postsubmit is kept",
			jobconfig: &prowconfig.JobConfig{
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{
					"repo": {{JobBase: prowconfig.JobBase{Labels: map[string]string{jobconfig.ProwJobLabelGenerated: string(jobconfig.New)}}}},
				},
			},
			expectedPruned: false,
		},
		{
			name: "not generated presubmit is kept",
			jobconfig: &prowconfig.JobConfig{
				PresubmitsStatic: map[string][]prowconfig.Presubmit{
					"repo": {{JobBase: prowconfig.JobBase{Name: "job"}}},
				},
			},
			expectedPruned: false,
		},
		{
			name: "not generated postsubmit is kept",
			jobconfig: &prowconfig.JobConfig{
				PostsubmitsStatic: map[string][]prowconfig.Postsubmit{
					"repo": {{JobBase: prowconfig.JobBase{Name: "job"}}},
				},
			},
			expectedPruned: false,
		},
		{
			name: "periodics are kept",
			jobconfig: &prowconfig.JobConfig{
				Periodics: []prowconfig.Periodic{{JobBase: prowconfig.JobBase{Name: "job"}}},
			},
			expectedPruned: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Expect either unchanged or empty JobConfig
			expected := tc.jobconfig
			if tc.expectedPruned {
				expected = &prowconfig.JobConfig{}
			}

			if pruned := prune(tc.jobconfig); !reflect.DeepEqual(pruned, expected) {
				t.Errorf("Pruned config differs:\n%s", diff.ObjectReflectDiff(expected, pruned))
			}
		})
	}
}

func TestGenerateJobBase(t *testing.T) {
	yes := true
	path := "/some/where"
	var testCases = []struct {
		testName    string
		name        string
		prefix      string
		info        *prowgenInfo
		label       jobconfig.ProwgenLabel
		podSpec     *kubeapi.PodSpec
		rehearsable bool
		pathAlias   *string
		expected    prowconfig.JobBase
	}{
		{
			testName: "no special options",
			name:     "test",
			prefix:   "pull",
			info:     &prowgenInfo{Info: config.Info{Org: "org", Repo: "repo", Branch: "branch"}},
			label:    jobconfig.Generated,
			podSpec:  &kubeapi.PodSpec{Containers: []kubeapi.Container{{Name: "test"}}},
			expected: prowconfig.JobBase{
				Name:  "pull-ci-org-repo-branch-test",
				Agent: "kubernetes",
				Labels: map[string]string{
					"ci-operator.openshift.io/prowgen-controlled": "true",
				},
				Spec:          &kubeapi.PodSpec{Containers: []kubeapi.Container{{Name: "test"}}},
				UtilityConfig: prowconfig.UtilityConfig{Decorate: true, DecorationConfig: &v1.DecorationConfig{SkipCloning: &yes}},
			},
		},
		{
			testName:    "rehearsable",
			name:        "test",
			prefix:      "pull",
			info:        &prowgenInfo{Info: config.Info{Org: "org", Repo: "repo", Branch: "branch"}},
			label:       jobconfig.Generated,
			podSpec:     &kubeapi.PodSpec{Containers: []kubeapi.Container{{Name: "test"}}},
			rehearsable: true,
			expected: prowconfig.JobBase{
				Name:  "pull-ci-org-repo-branch-test",
				Agent: "kubernetes",
				Labels: map[string]string{
					"ci-operator.openshift.io/prowgen-controlled": "true",
					"pj-rehearse.openshift.io/can-be-rehearsed":   "true",
				},
				Spec:          &kubeapi.PodSpec{Containers: []kubeapi.Container{{Name: "test"}}},
				UtilityConfig: prowconfig.UtilityConfig{Decorate: true, DecorationConfig: &v1.DecorationConfig{SkipCloning: &yes}},
			},
		},
		{
			testName: "config variant",
			name:     "test",
			prefix:   "pull",
			info:     &prowgenInfo{Info: config.Info{Org: "org", Repo: "repo", Branch: "branch", Variant: "whatever"}},
			label:    jobconfig.Generated,
			podSpec:  &kubeapi.PodSpec{Containers: []kubeapi.Container{{Name: "test"}}},
			expected: prowconfig.JobBase{
				Name:  "pull-ci-org-repo-branch-test",
				Agent: "kubernetes",
				Labels: map[string]string{
					"ci-operator.openshift.io/prowgen-controlled": "true",
					"ci-operator.openshift.io/variant":            "whatever",
				},
				Spec:          &kubeapi.PodSpec{Containers: []kubeapi.Container{{Name: "test"}}},
				UtilityConfig: prowconfig.UtilityConfig{Decorate: true, DecorationConfig: &v1.DecorationConfig{SkipCloning: &yes}},
			},
		},
		{
			testName:  "path alias",
			name:      "test",
			prefix:    "pull",
			info:      &prowgenInfo{Info: config.Info{Org: "org", Repo: "repo", Branch: "branch", Variant: "whatever"}},
			label:     jobconfig.Generated,
			podSpec:   &kubeapi.PodSpec{Containers: []kubeapi.Container{{Name: "test"}}},
			pathAlias: &path,
			expected: prowconfig.JobBase{
				Name:  "pull-ci-org-repo-branch-test",
				Agent: "kubernetes",
				Labels: map[string]string{
					"ci-operator.openshift.io/prowgen-controlled": "true",
					"ci-operator.openshift.io/variant":            "whatever",
				},
				Spec:          &kubeapi.PodSpec{Containers: []kubeapi.Container{{Name: "test"}}},
				UtilityConfig: prowconfig.UtilityConfig{Decorate: true, DecorationConfig: &v1.DecorationConfig{SkipCloning: &yes}, PathAlias: "/some/where"},
			},
		},
		{
			testName: "hidden job for private repos",
			name:     "test",
			prefix:   "pull",
			info: &prowgenInfo{
				Info:   config.Info{Org: "org", Repo: "repo", Branch: "branch"},
				config: config.Prowgen{Private: true},
			},
			label:   jobconfig.Generated,
			podSpec: &kubeapi.PodSpec{Containers: []kubeapi.Container{{Name: "test"}}},
			expected: prowconfig.JobBase{
				Name:  "pull-ci-org-repo-branch-test",
				Agent: "kubernetes",
				Labels: map[string]string{
					"ci-operator.openshift.io/prowgen-controlled": "true",
				},
				Spec:          &kubeapi.PodSpec{Containers: []kubeapi.Container{{Name: "test"}}},
				UtilityConfig: prowconfig.UtilityConfig{Decorate: true, DecorationConfig: &v1.DecorationConfig{SkipCloning: &yes}},
				Hidden:        true,
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.testName, func(t *testing.T) {
			if actual, expected := generateJobBase(testCase.name, testCase.prefix, testCase.info, testCase.label, testCase.podSpec, testCase.rehearsable, testCase.pathAlias), testCase.expected; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: got incorrect job base: %v", testCase.testName, diff.ObjectReflectDiff(actual, expected))
			}
		})
	}
}
