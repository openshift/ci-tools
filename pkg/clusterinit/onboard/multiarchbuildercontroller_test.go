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

func TestMultiarchBuilderControllerManifests(t *testing.T) {
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
				`apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: mabc-updater
rules:
- apiGroups:
  - ci.openshift.io
  resources:
  - multiarchbuildconfigs
  verbs:
  - '*'
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: mabc-updater
  namespace: ci
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: self-provisioner-mabc-updater
  namespace: ci
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: mabc-updater
subjects:
- kind: ServiceAccount
  name: mabc-updater
  namespace: ci
`,
				`apiVersion: apps/v1
kind: Deployment
metadata:
  name: multi-arch-builder-controller
  namespace: ci
spec:
  replicas: 1
  selector:
    matchLabels:
      app: multi-arch-builder-controller
  template:
    metadata:
      labels:
        app: multi-arch-builder-controller
    spec:
      containers:
      - args:
        - --dry-run=false
        image: quay-proxy.ci.openshift.org/openshift/ci:ci_multi-arch-builder-controller_latest
        imagePullPolicy: Always
        name: multi-arch-builder-controller
        resources:
          limits:
            cpu: 100m
            memory: 128Mi
          requests:
            cpu: 100m
            memory: 128Mi
        volumeMounts:
        - mountPath: /.docker/config.json
          name: docker-config
          readOnly: true
          subPath: .dockerconfigjson
      nodeSelector:
        kubernetes.io/arch: amd64
      serviceAccountName: multi-arch-builder-controller
      volumes:
      - name: docker-config
        secret:
          secretName: multi-arch-builder-controller-build99-registry-credentials
`,
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			step := NewMultiarchBuilderControllerStep(logrus.NewEntry(logrus.StandardLogger()), &tt.ci)

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
				path.Join(MultiarchBuilderControllerManifestsPath(releaseRepo, tt.ci.ClusterName), "000_mabc-updater_rbac.yaml"),
				path.Join(MultiarchBuilderControllerManifestsPath(releaseRepo, tt.ci.ClusterName), "100_deploy.yaml"),
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
