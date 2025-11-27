package onboard

import (
	"path"

	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	cinitmanifest "github.com/openshift/ci-tools/pkg/clusterinit/manifest"
	cinittypes "github.com/openshift/ci-tools/pkg/clusterinit/types"
)

type nestedPodmanStep struct {
	log            *logrus.Entry
	clusterInstall *clusterinstall.ClusterInstall
}

func (s *nestedPodmanStep) Name() string {
	return "nested-podman"
}

func (s *nestedPodmanStep) Skip() cinittypes.SkipStep {
	return s.clusterInstall.Onboard.NestedPodman.SkipStep
}

func (s *nestedPodmanStep) ExcludedManifests() cinittypes.ExcludeManifest {
	return s.clusterInstall.Onboard.NestedPodman.ExcludeManifest
}

func (s *nestedPodmanStep) Patches() []cinitmanifest.Patch {
	return s.clusterInstall.Onboard.NestedPodman.Patches
}

func (s *nestedPodmanStep) Generate(ctx context.Context, log *logrus.Entry) (map[string][]any, error) {
	manifestsPath := NestedPodmanManifestsPath(s.clusterInstall.Onboard.ReleaseRepo, s.clusterInstall.ClusterName)
	pathToManifests := make(map[string][]any)

	manifests := s.rbacManifests()
	pathToManifests[path.Join(manifestsPath, "rbac.yaml")] = manifests

	return pathToManifests, nil
}

func (s *nestedPodmanStep) rbacManifests() []any {
	return []any{
		map[string]any{
			"allowedCapabilities": []any{
				"SETUID",
				"SETGID",
			},
			"apiVersion": "security.openshift.io/v1",
			"kind":       "SecurityContextConstraints",
			"priority":   nil,
			"runAsUser": map[string]any{
				"type": "MustRunAs",
				"uid":  1000,
			},
			"userNamespaceLevel":       "RequirePodLevel",
			"allowPrivilegeEscalation": true,
			"fsGroup": map[string]any{
				"ranges": []any{
					map[string]any{
						"max": 65534,
						"min": 1000,
					},
				},
				"type": "MustRunAs",
			},
			"metadata": map[string]any{
				"name": "nested-podman",
			},
			"seLinuxContext": map[string]any{
				"seLinuxOptions": map[string]any{
					"type": "container_engine_t",
				},
				"type": "MustRunAs",
			},
			"supplementalGroups": map[string]any{
				"ranges": []any{
					map[string]any{
						"max": 65534,
						"min": 1000,
					},
				},
				"type": "MustRunAs",
			},
		},
		map[string]any{
			"metadata": map[string]any{
				"name": "nested-podman-creater",
			},
			"rules": []any{
				map[string]any{
					"resources": []any{
						"securitycontextconstraints",
					},
					"verbs": []any{
						"use",
					},
					"apiGroups": []any{
						"security.openshift.io",
					},
					"resourceNames": []any{
						"nested-podman",
					},
				},
			},
			"apiVersion": "rbac.authorization.k8s.io/v1",
			"kind":       "ClusterRole",
		},
		map[string]any{
			"apiVersion": "rbac.authorization.k8s.io/v1",
			"kind":       "ClusterRole",
			"metadata": map[string]any{
				"name": "ci-operator-nested-podman",
			},
			"rules": []any{
				map[string]any{
					"apiGroups": []any{
						"security.openshift.io",
					},
					"resourceNames": []any{
						"nested-podman",
					},
					"resources": []any{
						"securitycontextconstraints",
					},
					"verbs": []any{
						"use",
					},
				},
			},
		},
		map[string]any{
			"apiVersion": "rbac.authorization.k8s.io/v1",
			"kind":       "ClusterRoleBinding",
			"metadata": map[string]any{
				"name": "ci-operator-nested-podman",
			},
			"roleRef": map[string]any{
				"apiGroup": "rbac.authorization.k8s.io",
				"kind":     "ClusterRole",
				"name":     "ci-operator-nested-podman",
			},
			"subjects": []any{
				map[string]any{
					"kind":      "ServiceAccount",
					"name":      "ci-operator",
					"namespace": "ci",
				},
			},
		},
	}
}

func NewNestedPodmanStep(log *logrus.Entry, clusterInstall *clusterinstall.ClusterInstall) *nestedPodmanStep {
	return &nestedPodmanStep{
		log:            log,
		clusterInstall: clusterInstall,
	}
}
