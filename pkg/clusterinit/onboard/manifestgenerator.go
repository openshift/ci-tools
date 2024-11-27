package onboard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"

	cinitmanifest "github.com/openshift/ci-tools/pkg/clusterinit/manifest"
	"github.com/openshift/ci-tools/pkg/clusterinit/types"
)

// manifestGeneratorStep is struct of convenience that enhances a MenifestGenerator capabilities
// by adding the following:
//
// - skip the manifest generation on `skip: true`
// - filter manifests by matching glob paths from the `exclude: []` stanza
// - apply yaml patches
type manifestGeneratorStep struct {
	log               *logrus.Entry
	manifestGenerator types.ManifestGenerator
}

func (w *manifestGeneratorStep) Name() string {
	return fmt.Sprintf("manifest-generator %s", w.manifestGenerator.Name())
}

func (w *manifestGeneratorStep) Run(ctx context.Context) error {
	log := w.log.WithField("step", w.manifestGenerator.Name())

	skipStep := w.manifestGenerator.Skip()
	if skipStep.Skip {
		log.Info("step is not enabled, skipping")
		return nil
	}

	pathTomanifests, err := w.manifestGenerator.Generate(ctx, log)
	if err != nil {
		return fmt.Errorf("generate manifests: %w", err)
	}

	exclude := w.manifestGenerator.ExcludedManifests()
	patches := w.manifestGenerator.Patches()

	for path := range pathTomanifests {
		manifests := pathTomanifests[path]
		if g, skip := exclude.Filter(path); skip {
			log.WithField("manifest", path).WithField("pattern", g).Info("exclude manifest")
			continue
		}

		manifestBytes, err := cinitmanifest.Marshal(manifests, patches)
		if err != nil {
			return fmt.Errorf("marshal manifests: %w", err)
		}

		dir := filepath.Dir(path)
		if _, err := os.Stat(dir); err != nil {
			if !os.IsNotExist(err) {
				return fmt.Errorf("stat %s: %w", dir, err)
			}
			if err := os.MkdirAll(dir, 0755); err != nil {
				return fmt.Errorf("mkdirall %s: %w", dir, err)
			}
		}

		if err := os.WriteFile(path, manifestBytes, 0644); err != nil {
			return fmt.Errorf("write manifest %s: %w", path, err)
		}
	}

	return nil
}

func NewManifestGeneratorStep(log *logrus.Entry, manifestGenerator types.ManifestGenerator) *manifestGeneratorStep {
	return &manifestGeneratorStep{
		log:               log,
		manifestGenerator: manifestGenerator,
	}
}
