package onboard

import (
	"context"
	"io/fs"
	"path"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
)

func TestImageRegistryManifests(t *testing.T) {
	releaseRepo := "/release/repo"
	for _, tt := range []struct {
		name          string
		ci            clusterinstall.ClusterInstall
		wantManifests []string
		wantErr       error
	}{
		{
			name: "Write manifests successfully",
			ci: clusterinstall.ClusterInstall{
				ClusterName: "build99",
				Onboard:     clusterinstall.Onboard{ReleaseRepo: releaseRepo},
			},
			wantManifests: []string{
				`apiVersion: imageregistry.operator.openshift.io/v1
kind: Config
metadata:
  name: cluster
spec:
  affinity:
    podAntiAffinity:
      preferredDuringSchedulingIgnoredDuringExecution:
      - podAffinityTerm:
          labelSelector:
            matchExpressions:
            - key: docker-registry
              operator: In
              values:
              - default
          topologyKey: kubernetes.io/hostname
        weight: 100
  managementState: Managed
  nodeSelector:
    node-role.kubernetes.io/infra: ""
  replicas: 5
  routes:
  - hostname: registry.build99.ci.openshift.org
    name: public-routes
    secretName: public-route-tls
  tolerations:
  - effect: NoSchedule
    key: node-role.kubernetes.io/infra
    operator: Exists
`,
				`apiVersion: imageregistry.operator.openshift.io/v1
kind: ImagePruner
metadata:
  name: cluster
spec:
  failedJobsHistoryLimit: 3
  keepTagRevisions: 3
  schedule: ""
  successfulJobsHistoryLimit: 3
  suspend: false
`,
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			step := NewImageRegistryStepStep(logrus.NewEntry(logrus.StandardLogger()), &tt.ci)

			manifests := make([]string, 0)
			writeManifestsPaths := make([]string, 0)
			step.writeManifest = func(name string, data []byte, _ fs.FileMode) error {
				writeManifestsPaths = append(writeManifestsPaths, name)
				manifests = append(manifests, string(data))
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

			sortStr := func(a, b string) bool { return strings.Compare(a, b) <= 0 }

			wantManifestsPath := []string{
				path.Join(ImageRegistryManifestsPath(releaseRepo, tt.ci.ClusterName), "config-cluster.yaml"),
				path.Join(ImageRegistryManifestsPath(releaseRepo, tt.ci.ClusterName), "imagepruner-cluster.yaml"),
			}
			if diff := cmp.Diff(wantManifestsPath, writeManifestsPaths, cmpopts.SortSlices(sortStr)); diff != "" {
				t.Errorf("manifest paths differs:\n%s", diff)
			}

			if diff := cmp.Diff(tt.wantManifests, manifests, cmpopts.SortSlices(sortStr)); diff != "" {
				t.Errorf("manifests differs:\n%s", diff)
			}
		})
	}
}
