package steps

import (
	"context"
	"testing"

	appsapi "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/pod-utils/downwardapi"

	routev1 "github.com/openshift/api/route/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	"github.com/openshift/ci-tools/pkg/testhelper"
	"github.com/openshift/ci-tools/pkg/util"
)

func TestEnsureRPMRepoDeployment(t *testing.T) {
	testCases := []struct {
		name       string
		existing   *appsapi.Deployment
		deployment *appsapi.Deployment
		want       *appsapi.Deployment
	}{
		{
			name: "creates when missing",
			deployment: &appsapi.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns",
					Name:      RPMRepoName,
				},
				Spec: appsapi.DeploymentSpec{
					Replicas: ptr.To(int32(1)),
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{AppLabel: RPMRepoName},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{AppLabel: RPMRepoName},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{{
								Name:  RPMRepoName,
								Image: "registry/ci-op-ns/pipeline@sha256:new",
							}},
						},
					},
				},
			},
			want: &appsapi.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns",
					Name:      RPMRepoName,
				},
				Spec: appsapi.DeploymentSpec{
					Replicas: ptr.To(int32(1)),
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{AppLabel: RPMRepoName},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{AppLabel: RPMRepoName},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{{
								Name:  RPMRepoName,
								Image: "registry/ci-op-ns/pipeline@sha256:new",
							}},
						},
					},
				},
			},
		},
		{
			name: "unchanged when same image",
			existing: &appsapi.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns",
					Name:      RPMRepoName,
				},
				Spec: appsapi.DeploymentSpec{
					Replicas: ptr.To(int32(1)),
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{AppLabel: RPMRepoName},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{AppLabel: RPMRepoName},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{{
								Name:  RPMRepoName,
								Image: "registry/ci-op-ns/pipeline@sha256:new",
							}},
						},
					},
				},
			},
			deployment: &appsapi.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns",
					Name:      RPMRepoName,
				},
				Spec: appsapi.DeploymentSpec{
					Replicas: ptr.To(int32(1)),
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{AppLabel: RPMRepoName},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{AppLabel: RPMRepoName},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{{
								Name:  RPMRepoName,
								Image: "registry/ci-op-ns/pipeline@sha256:new",
							}},
						},
					},
				},
			},
			want: &appsapi.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns",
					Name:      RPMRepoName,
				},
				Spec: appsapi.DeploymentSpec{
					Replicas: ptr.To(int32(1)),
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{AppLabel: RPMRepoName},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{AppLabel: RPMRepoName},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{{
								Name:  RPMRepoName,
								Image: "registry/ci-op-ns/pipeline@sha256:new",
							}},
						},
					},
				},
			},
		},
		{
			name: "updates when image differs",
			existing: &appsapi.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns",
					Name:      RPMRepoName,
				},
				Spec: appsapi.DeploymentSpec{
					Replicas: ptr.To(int32(1)),
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{AppLabel: RPMRepoName},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{AppLabel: RPMRepoName},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{{
								Name:  RPMRepoName,
								Image: "registry/ci-op-ns/pipeline@sha256:old",
							}},
						},
					},
				},
			},
			deployment: &appsapi.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns",
					Name:      RPMRepoName,
				},
				Spec: appsapi.DeploymentSpec{
					Replicas: ptr.To(int32(1)),
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{AppLabel: RPMRepoName},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{AppLabel: RPMRepoName},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{{
								Name:  RPMRepoName,
								Image: "registry/ci-op-ns/pipeline@sha256:new",
							}},
						},
					},
				},
			},
			want: &appsapi.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ns",
					Name:      RPMRepoName,
				},
				Spec: appsapi.DeploymentSpec{
					Replicas: ptr.To(int32(1)),
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{AppLabel: RPMRepoName},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{AppLabel: RPMRepoName},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{{
								Name:  RPMRepoName,
								Image: "registry/ci-op-ns/pipeline@sha256:new",
							}},
						},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			builder := fakectrlruntimeclient.NewClientBuilder()
			if tc.existing != nil {
				builder = builder.WithRuntimeObjects(tc.existing)
			}
			client := loggingclient.New(builder.Build(), nil)
			jobSpec := &api.JobSpec{}
			jobSpec.SetNamespace("ns")
			step := &rpmServerStep{client: client, jobSpec: jobSpec}

			desiredImage := tc.deployment.Spec.Template.Spec.Containers[0].Image
			if err := step.ensureRPMRepoDeployment(context.Background(), tc.deployment, desiredImage); err != nil {
				t.Fatalf("ensureRPMRepoDeployment() error = %v", err)
			}

			got := &appsapi.Deployment{}
			if err := client.Get(context.Background(), ctrlruntimeclient.ObjectKey{Namespace: "ns", Name: RPMRepoName}, got); err != nil {
				t.Fatalf("Get() error = %v", err)
			}
			testhelper.Diff(t, "deployment", got, tc.want, testhelper.RuntimeObjectIgnoreRvTypeMeta)
		})
	}
}

func TestRPMServerStepProvides(t *testing.T) {
	ns := "ns"
	if err := routev1.AddToScheme(scheme.Scheme); err != nil {
		t.Error(err)
	}
	for _, tc := range []struct {
		name     string
		jobSpec  api.JobSpec
		expected [][2]any
	}{{
		name: "no refs",
	}, {
		name: "ref",
		jobSpec: api.JobSpec{
			JobSpec: downwardapi.JobSpec{
				Refs: &prowapi.Refs{Org: "org", Repo: "repo"},
			},
		},
		expected: [][2]any{{"RPM_REPO_ORG_REPO", "http://host"}},
	}, {
		name: "extra refs",
		jobSpec: api.JobSpec{
			JobSpec: downwardapi.JobSpec{
				ExtraRefs: []prowapi.Refs{
					{Org: "org0", Repo: "repo0"},
					{Org: "org1", Repo: "repo1"},
				},
			},
		},
		expected: [][2]any{
			{"RPM_REPO_ORG0_REPO0", "http://host"},
			{"RPM_REPO_ORG1_REPO1", "http://host"},
		},
	}, {
		name: "refs + extra refs",
		jobSpec: api.JobSpec{
			JobSpec: downwardapi.JobSpec{
				Refs: &prowapi.Refs{Org: "org", Repo: "repo"},
				ExtraRefs: []prowapi.Refs{
					{Org: "org0", Repo: "repo0"},
					{Org: "org1", Repo: "repo1"},
				},
			},
		},
		expected: [][2]any{
			{"RPM_REPO_ORG0_REPO0", "http://host"},
			{"RPM_REPO_ORG1_REPO1", "http://host"},
			{"RPM_REPO_ORG_REPO", "http://host"},
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			client := loggingclient.New(fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(
				&routev1.Route{
					ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "rpm-repo"},
					Status: routev1.RouteStatus{
						Ingress: []routev1.RouteIngress{{
							Host: "host",
							Conditions: []routev1.RouteIngressCondition{{
								Type:   routev1.RouteAdmitted,
								Status: corev1.ConditionTrue,
							}},
						}},
					},
				},
			).Build(), nil)
			tc.jobSpec.SetNamespace(ns)
			step := RPMServerStep(api.RPMServeStepConfiguration{}, client, &tc.jobSpec)
			providesMap := step.Provides()
			var provides [][2]any
			for _, k := range util.SortedKeys(providesMap) {
				s, err := providesMap[k]()
				if err != nil {
					t.Fatal(err)
				}
				provides = append(provides, [2]any{k, s})
			}
			testhelper.Diff(t, "parameter map", provides, tc.expected)
		})
	}
}
