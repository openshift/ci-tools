package onboard

import (
	"context"
	"errors"
	"path"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	routev1 "github.com/openshift/api/route/v1"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
)

func TestUpdateDexConfig(t *testing.T) {
	releaseRepo := "/release/repo"
	for _, tc := range []struct {
		name          string
		ci            clusterinstall.ClusterInstall
		dexManifests  string
		wantManifests map[string][]interface{}
		wantErr       error
	}{
		{
			name: "Add static client and env",
			ci:   clusterinstall.ClusterInstall{ClusterName: "build11", Onboard: clusterinstall.Onboard{ReleaseRepo: releaseRepo}},
			dexManifests: `apiVersion: apps/v1
kind: Deployment
spec:
  template:
    metadata:
      annotations:
        config.yaml: |
          staticClients: []
    spec:
      containers:
      - env: []`,
			wantManifests: map[string][]interface{}{
				"/release/repo/clusters/app.ci/dex/manifests.yaml": {
					appsv1.Deployment{
						TypeMeta: v1.TypeMeta{
							APIVersion: "apps/v1",
							Kind:       "Deployment",
						},
						Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{
							ObjectMeta: v1.ObjectMeta{
								Annotations: map[string]string{
									"config.yaml": `staticClients:
- idEnv: BUILD11-ID
  name: build11
  redirectURIs:
  - https://oauth-openshift.apps.build11.ci.devcluster.openshift.com/oauth2callback/RedHat_Internal_SSO
  secretEnv: BUILD11-SECRET
`,
								},
							},
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{{Env: []corev1.EnvVar{
									{
										Name: "BUILD11-ID",
										ValueFrom: &corev1.EnvVarSource{
											SecretKeyRef: &corev1.SecretKeySelector{
												Key:                  "build11-id",
												LocalObjectReference: corev1.LocalObjectReference{Name: "build11-secret"},
											},
										},
									},
									{
										Name: "BUILD11-SECRET",
										ValueFrom: &corev1.EnvVarSource{
											SecretKeyRef: &corev1.SecretKeySelector{
												Key:                  "build11-secret",
												LocalObjectReference: corev1.LocalObjectReference{Name: "build11-secret"},
											},
										},
									},
								}}},
							},
						}},
					},
				},
			},
		},
		{
			name: "Update client and env",
			ci:   clusterinstall.ClusterInstall{ClusterName: "build11", Onboard: clusterinstall.Onboard{ReleaseRepo: releaseRepo}},
			dexManifests: `apiVersion: apps/v1
kind: Deployment
spec:
  template:
    metadata:
      annotations:
        config.yaml: |
          staticClients:
          - idEnv: "???"
            name: build11
            redirectURIs:
            - "???"
            secretEnv: "???"
    spec:
      containers:
      - env:
        - name: BUILD11-ID
          valueFrom:
            secretKeyRef:
              key: "???"
              name: "???"
        - name: BUILD11-SECRET
          valueFrom:
            secretKeyRef:
              key: "???"
              name: "???"`,
			wantManifests: map[string][]interface{}{
				"/release/repo/clusters/app.ci/dex/manifests.yaml": {
					appsv1.Deployment{
						TypeMeta: v1.TypeMeta{
							APIVersion: "apps/v1",
							Kind:       "Deployment",
						},
						Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{
							ObjectMeta: v1.ObjectMeta{
								Annotations: map[string]string{
									"config.yaml": `staticClients:
- idEnv: BUILD11-ID
  name: build11
  redirectURIs:
  - https://oauth-openshift.apps.build11.ci.devcluster.openshift.com/oauth2callback/RedHat_Internal_SSO
  secretEnv: BUILD11-SECRET
`,
								},
							},
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{{Env: []corev1.EnvVar{
									{
										Name: "BUILD11-ID",
										ValueFrom: &corev1.EnvVarSource{
											SecretKeyRef: &corev1.SecretKeySelector{
												Key:                  "build11-id",
												LocalObjectReference: corev1.LocalObjectReference{Name: "build11-secret"},
											},
										},
									},
									{
										Name: "BUILD11-SECRET",
										ValueFrom: &corev1.EnvVarSource{
											SecretKeyRef: &corev1.SecretKeySelector{
												Key:                  "build11-secret",
												LocalObjectReference: corev1.LocalObjectReference{Name: "build11-secret"},
											},
										},
									},
								}}},
							},
						}},
					},
				},
			},
		},
		{
			name:    "No deployment",
			ci:      clusterinstall.ClusterInstall{ClusterName: "build11", Onboard: clusterinstall.Onboard{ReleaseRepo: releaseRepo}},
			wantErr: errors.New("deployment not found"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			scheme := runtime.NewScheme()
			if err := routev1.AddToScheme(scheme); err != nil {
				t.Fatal("add routev1 to scheme")
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(&routev1.Route{
				ObjectMeta: v1.ObjectMeta{Namespace: "openshift-authentication", Name: "oauth-openshift"},
				Spec:       routev1.RouteSpec{Host: "oauth-openshift.apps.build11.ci.devcluster.openshift.com"},
			}).Build()

			step := NewDexGenerator(c, &tc.ci)

			var readManifestsPath string
			step.readDexManifests = func(path string) (string, error) {
				readManifestsPath = path
				return tc.dexManifests, nil
			}

			manifests, err := step.Generate(context.TODO(), logrus.NewEntry(logrus.StandardLogger()))

			if err != nil && tc.wantErr == nil {
				t.Fatalf("want err nil but got: %v", err)
			}
			if err == nil && tc.wantErr != nil {
				t.Fatalf("want err %v but nil", tc.wantErr)
			}
			if err != nil && tc.wantErr != nil {
				if tc.wantErr.Error() != err.Error() {
					t.Fatalf("expect error %q but got %q", tc.wantErr.Error(), err.Error())
				}
				return
			}

			wantDexManifestsPath := path.Join(releaseRepo, dexManifests)
			if readManifestsPath != wantDexManifestsPath {
				t.Errorf("want manifests path (read) %q but got %q", wantDexManifestsPath, readManifestsPath)
			}

			if diff := cmp.Diff(tc.wantManifests, manifests); diff != "" {
				t.Errorf("manifests differs:\n%s", diff)
			}
		})
	}
}
