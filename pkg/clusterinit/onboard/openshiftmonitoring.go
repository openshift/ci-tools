package onboard

import (
	"context"
	"path"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	cinitmanifest "github.com/openshift/ci-tools/pkg/clusterinit/manifest"
	"github.com/openshift/ci-tools/pkg/clusterinit/types"
)

type openshiftMonitoringGenerator struct {
	clusterInstall *clusterinstall.ClusterInstall
}

func (s *openshiftMonitoringGenerator) Name() string {
	return "openshift-monitoring"
}

func (s *openshiftMonitoringGenerator) Skip() types.SkipStep {
	return s.clusterInstall.Onboard.OpenshiftMonitoring.SkipStep
}

func (s *openshiftMonitoringGenerator) ExcludedManifests() types.ExcludeManifest {
	return s.clusterInstall.Onboard.OpenshiftMonitoring.ExcludeManifest
}

func (s *openshiftMonitoringGenerator) Patches() []cinitmanifest.Patch {
	return s.clusterInstall.Onboard.OpenshiftMonitoring.Patches
}

func (s *openshiftMonitoringGenerator) Generate(ctx context.Context, log *logrus.Entry) (map[string][]interface{}, error) {
	pathToManifests := make(map[string][]interface{})
	basePath := OpenshiftMonitoringManifestsPath(s.clusterInstall.Onboard.ReleaseRepo, s.clusterInstall.ClusterName)

	manifests := s.configMapManifests()
	pathToManifests[path.Join(basePath, "cluster-monitoring-config.yaml")] = manifests

	return pathToManifests, nil
}

func (s *openshiftMonitoringGenerator) configMapManifests() []interface{} {
	return []interface{}{
		map[string]interface{}{
			"kind": "ConfigMap",
			"metadata": map[string]interface{}{
				"name":      "cluster-monitoring-config",
				"namespace": "openshift-monitoring",
			},
			"apiVersion": "v1",
			"data": map[string]interface{}{
				"config.yaml": `alertmanagerMain:
  nodeSelector:
    node-role.kubernetes.io/infra: ""
  tolerations:
  - key: node-role.kubernetes.io/infra
    value: reserved
    effect: NoSchedule
  - key: node-role.kubernetes.io/infra
    value: reserved
    effect: NoExecute
prometheusK8s:
  nodeSelector:
    node-role.kubernetes.io/infra: ""
  tolerations:
  - key: node-role.kubernetes.io/infra
    value: reserved
    effect: NoSchedule
  - key: node-role.kubernetes.io/infra
    value: reserved
    effect: NoExecute
  volumeClaimTemplate:
    spec:
      resources:
        requests:
          storage: 100Gi
prometheusOperator:
  nodeSelector:
    node-role.kubernetes.io/infra: ""
  tolerations:
  - key: node-role.kubernetes.io/infra
    value: reserved
    effect: NoSchedule
  - key: node-role.kubernetes.io/infra
    value: reserved
    effect: NoExecute
metricsServer:
  nodeSelector:
    node-role.kubernetes.io/infra: ""
  tolerations:
  - key: node-role.kubernetes.io/infra
    value: reserved
    effect: NoSchedule
  - key: node-role.kubernetes.io/infra
    value: reserved
    effect: NoExecute
kubeStateMetrics:
  nodeSelector:
    node-role.kubernetes.io/infra: ""
  tolerations:
  - key: node-role.kubernetes.io/infra
    value: reserved
    effect: NoSchedule
  - key: node-role.kubernetes.io/infra
    value: reserved
    effect: NoExecute
telemeterClient:
  nodeSelector:
    node-role.kubernetes.io/infra: ""
  tolerations:
  - key: node-role.kubernetes.io/infra
    value: reserved
    effect: NoSchedule
  - key: node-role.kubernetes.io/infra
    value: reserved
    effect: NoExecute
openshiftStateMetrics:
  nodeSelector:
    node-role.kubernetes.io/infra: ""
  tolerations:
  - key: node-role.kubernetes.io/infra
    value: reserved
    effect: NoSchedule
  - key: node-role.kubernetes.io/infra
    value: reserved
    effect: NoExecute
thanosQuerier:
  nodeSelector:
    node-role.kubernetes.io/infra: ""
  tolerations:
  - key: node-role.kubernetes.io/infra
    value: reserved
    effect: NoSchedule
  - key: node-role.kubernetes.io/infra
    value: reserved
    effect: NoExecute`,
			},
		},
	}
}

func NewOpenshiftMonitoringGenerator(clusterInstall *clusterinstall.ClusterInstall) *openshiftMonitoringGenerator {
	return &openshiftMonitoringGenerator{clusterInstall: clusterInstall}
}
