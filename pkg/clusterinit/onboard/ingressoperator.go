package onboard

import (
	"context"
	_ "embed"
	"fmt"
	"io/fs"
	"os"
	"path"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
)

//go:embed manifest/openshift-ingress-operator.yaml
var openshiftIngressOperator string

type ingressOperatorStep struct {
	log            *logrus.Entry
	clusterInstall *clusterinstall.ClusterInstall
	writeManifest  func(name string, data []byte, perm fs.FileMode) error
	mkdirAll       func(path string, perm fs.FileMode) error
}

func (s *ingressOperatorStep) Name() string {
	return "openshift-ingress-operator"
}

func (s *ingressOperatorStep) Run(ctx context.Context) error {
	log := s.log.WithField("step", s.Name())

	if s.clusterInstall.Onboard.IngressOperator.Skip {
		log.Info("step is not enabled, skipping")
		return nil
	}

	manifestsPath := IngressOperatorManifestsPath(s.clusterInstall.Onboard.ReleaseRepo, s.clusterInstall.ClusterName)
	if err := s.mkdirAll(manifestsPath, 0755); err != nil && !os.IsExist(err) {
		return fmt.Errorf("mkdir %s: %w", manifestsPath, err)
	}

	if err := s.writeManifest(path.Join(manifestsPath, "ingress-controller-default.yaml"), []byte(openshiftIngressOperator), 0644); err != nil {
		return fmt.Errorf("write manifests: %w", err)
	}
	return nil
}

func NewIngressOperatorStep(log *logrus.Entry, clusterInstall *clusterinstall.ClusterInstall) *ingressOperatorStep {
	return &ingressOperatorStep{
		log:            log,
		clusterInstall: clusterInstall,
		writeManifest:  os.WriteFile,
		mkdirAll:       os.MkdirAll,
	}
}
