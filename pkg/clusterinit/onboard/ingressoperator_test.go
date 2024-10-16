package onboard

import (
	"context"
	"io/fs"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
)

func TestIngressOperatorManifests(t *testing.T) {
	releaseRepo := "/release/repo"
	for _, tt := range []struct {
		name             string
		ci               clusterinstall.ClusterInstall
		wantManifests    string
		wantManifestPath string
		wantErr          error
	}{
		{
			name: "Write manifests successfully",
			ci: clusterinstall.ClusterInstall{
				Onboard: clusterinstall.Onboard{ReleaseRepo: releaseRepo},
			},
			wantManifestPath: "/release/repo/clusters/build-clusters/openshift-ingress-operator/ingress-controller-default.yaml",
			wantManifests: `#We put only the customized fields in git
apiVersion: operator.openshift.io/v1
kind: IngressController
metadata:
  name: default
  namespace: openshift-ingress-operator
spec:
  defaultCertificate:
    name: apps-tls
  nodePlacement:
    nodeSelector:
      matchLabels:
        node-role.kubernetes.io/infra: ""
    tolerations:
    - effect: NoSchedule
      key: node-role.kubernetes.io/infra
      operator: Exists
`,
		},
		{
			name: "Skip manifests",
			ci: clusterinstall.ClusterInstall{
				Onboard: clusterinstall.Onboard{
					ReleaseRepo:     releaseRepo,
					IngressOperator: clusterinstall.IngressOperator{SkipStep: clusterinstall.SkipStep{Skip: true}},
				},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			step := NewIngressOperatorStep(logrus.NewEntry(logrus.StandardLogger()), &tt.ci)

			var manifests string
			var writeManifestsPaths string
			step.writeManifest = func(name string, data []byte, _ fs.FileMode) error {
				writeManifestsPaths = name
				manifests = string(data)
				return nil
			}
			step.mkdirAll = func(path string, perm fs.FileMode) error { return nil }

			err := step.Run(context.TODO())

			if err != nil && tt.wantErr == nil {
				t.Fatalf("want err nil but got: %v", err)
			}
			if err == nil && tt.wantErr != nil {
				t.Fatalf("want err %v but nil", tt.wantErr)
			}
			if err != nil && tt.wantErr != nil {
				if tt.wantErr.Error() != err.Error() {
					t.Fatalf("expect error %q but got %q", tt.wantErr.Error(), err.Error())
				}
				return
			}

			if diff := cmp.Diff(tt.wantManifestPath, writeManifestsPaths); diff != "" {
				t.Errorf("manifest paths differs:\n%s", diff)
			}

			if diff := cmp.Diff(tt.wantManifests, manifests); diff != "" {
				t.Errorf("manifests differs:\n%s", diff)
			}
		})
	}
}
