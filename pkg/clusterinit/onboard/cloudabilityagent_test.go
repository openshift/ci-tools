package onboard

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	v1 "github.com/openshift/api/config/v1"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	"github.com/openshift/ci-tools/pkg/clusterinit/types/aws"
)

func TestCloudabilityAgentManifests(t *testing.T) {
	releaseRepo := "/release/repo"
	for _, tc := range []struct {
		name          string
		ci            clusterinstall.ClusterInstall
		wantManifests string
		wantErr       error
	}{
		{
			name: "Write manifests successfully",
			ci: clusterinstall.ClusterInstall{
				ClusterName: "build99",
				Provision:   clusterinstall.Provision{AWS: &aws.Provision{}},
				Onboard:     clusterinstall.Onboard{ReleaseRepo: releaseRepo},
				Infrastructure: v1.Infrastructure{
					Status: v1.InfrastructureStatus{PlatformStatus: &v1.PlatformStatus{
						AWS: &v1.AWSPlatformStatus{Region: "us-east-1"},
					}},
				},
			},
			wantManifests: `apiVersion: v1
kind: Namespace
metadata:
  name: cloudability
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: cloudability
  namespace: cloudability
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: cloudability-metrics-agent
  namespace: kube-system
rules:
- apiGroups:
  - ""
  - extensions
  - apps
  - batch
  resources:
  - namespaces
  - replicationcontrollers
  - services
  - nodes
  - nodes/spec
  - pods
  - jobs
  - cronjobs
  - persistentvolumes
  - persistentvolumeclaims
  - deployments
  - replicasets
  - daemonsets
  verbs:
  - get
  - watch
  - list
- apiGroups:
  - ""
  resources:
  - services/proxy
  - pods/proxy
  - nodes/proxy
  - nodes/stats
  - nodes/metrics
  verbs:
  - get
  - list
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: cloudability-metrics-agent
  namespace: kube-system
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: cloudability-metrics-agent
subjects:
- kind: ServiceAccount
  name: cloudability
  namespace: cloudability
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: cloudability-metrics-agent
  namespace: cloudability
rules:
- apiGroups:
  - '*'
  resources:
  - pods
  - pods/log
  verbs:
  - get
  - list
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: cloudability-metrics-agent
  namespace: cloudability
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: cloudability-metrics-agent
subjects:
- kind: ServiceAccount
  name: cloudability
  namespace: cloudability
---
apiVersion: apps/v1
kind: Deployment
metadata:
  labels:
    name: cloudability-metrics-agent
  name: cloudability-metrics-agent
  namespace: cloudability
spec:
  replicas: 1
  selector:
    matchLabels:
      app: cloudability-metrics-agent
  template:
    metadata:
      labels:
        app: cloudability-metrics-agent
    spec:
      containers:
      - args:
        - kubernetes
        env:
        - name: CLOUDABILITY_API_KEY
          valueFrom:
            secretKeyRef:
              key: api-key
              name: cloudability-api-key
        - name: CLOUDABILITY_CLUSTER_NAME
          value: build99
        - name: CLOUDABILITY_UPLOAD_REGION
          value: us-east-1
        - name: CLOUDABILITY_POLL_INTERVAL
          value: "180"
        image: cloudability/metrics-agent:latest
        imagePullPolicy: Always
        livenessProbe:
          exec:
            command:
            - touch
            - tmp/healthy
          initialDelaySeconds: 120
          periodSeconds: 600
        name: cloudability-metrics-agent
        resources:
          limits:
            cpu: "1"
            memory: 4Gi
          requests:
            cpu: ".5"
            memory: 2Gi
        securityContext:
          allowPrivilegeEscalation: false
          capabilities:
            drop:
            - ALL
          runAsNonRoot: true
          seccompProfile:
            type: RuntimeDefault
      serviceAccount: cloudability
`,
		},
		{
			name: "No region set",
			ci: clusterinstall.ClusterInstall{
				ClusterName: "build99",
				Provision:   clusterinstall.Provision{AWS: &aws.Provision{}},
				Onboard:     clusterinstall.Onboard{ReleaseRepo: releaseRepo},
			},
			wantErr: errors.New("region is empty"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			step := NewCloudabilityAgentStep(logrus.NewEntry(logrus.StandardLogger()), &tc.ci)

			var manifests string
			var writeManifestsPath string
			step.writeManifest = func(name string, data []byte, _ fs.FileMode) error {
				writeManifestsPath = name
				manifests = string(data)
				return nil
			}

			err := step.Run(context.TODO())

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

			wantManifestsPath := path.Join(CloudabilityAgentManifestsPath(releaseRepo, tc.ci.ClusterName), "cloudability-agent.yaml")
			if writeManifestsPath != wantManifestsPath {
				t.Errorf("want manifests path %q but got %q", wantManifestsPath, writeManifestsPath)
			}

			if diff := cmp.Diff(tc.wantManifests, manifests); diff != "" {
				t.Errorf("manifests differs:\n%s", diff)
			}
		})
	}
}
