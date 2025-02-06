package onboard

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/yaml"

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

	for p := range pathTomanifests {
		manifests := pathTomanifests[p]
		if g, skip := exclude.Filter(p); skip {
			log.WithField("manifest", p).WithField("pattern", g).Info("exclude manifest")
			continue
		}

		var manifestBytes []byte
		if path.Ext(p) == ".yaml" {
			if manifestBytes, err = w.marshalManifests(manifests, patches); err != nil {
				return err
			}
		} else {
			for i, m := range manifests {
				bytes, ok := m.([]byte)
				if !ok {
					return fmt.Errorf("manifest %d at %s is not %T: %T", i, p, []byte{}, m)
				}
				manifestBytes = append(manifestBytes, bytes...)
			}
		}

		dir := filepath.Dir(p)
		if _, err := os.Stat(dir); err != nil {
			if !os.IsNotExist(err) {
				return fmt.Errorf("stat %s: %w", dir, err)
			}
			if err := os.MkdirAll(dir, 0755); err != nil {
				return fmt.Errorf("mkdirall %s: %w", dir, err)
			}
		}

		if err := os.WriteFile(p, manifestBytes, 0644); err != nil {
			return fmt.Errorf("write manifest %s: %w", p, err)
		}
	}

	return nil
}

func (w *manifestGeneratorStep) marshalManifests(manifests []interface{}, patches []cinitmanifest.Patch) ([]byte, error) {
	manifestsBytes := make([][]byte, 0, len(manifests))

	for _, manifest := range manifests {
		var manifestBytes []byte
		var manifestMap map[string]interface{}

		switch value := manifest.(type) {
		case []byte:
			manifestBytes = value
			err := yaml.Unmarshal(manifestBytes, &manifestMap)
			if err != nil {
				return nil, fmt.Errorf("unmarshal: %w", err)
			}
		case map[string]interface{}:
			manifestMap = value
			bytes, err := yaml.Marshal(manifest)
			if err != nil {
				return nil, fmt.Errorf("marshal: %w", err)
			}
			manifestBytes = bytes
		default:
			bytes, err := yaml.Marshal(manifest)
			if err != nil {
				return nil, fmt.Errorf("marshal: %w", err)
			}
			manifestBytes = bytes
			err = yaml.Unmarshal(manifestBytes, &manifestMap)
			if err != nil {
				return nil, fmt.Errorf("unmarshal: %w", err)
			}
		}

		manifestBytesPatched, err := cinitmanifest.ApplyPatches(manifestMap, manifestBytes, patches)
		if err != nil {
			return nil, err
		}

		manifestsBytes = append(manifestsBytes, manifestBytesPatched)
	}

	return bytes.Join(manifestsBytes, []byte("---\n")), nil
}

func NewManifestGeneratorStep(log *logrus.Entry, manifestGenerator types.ManifestGenerator) *manifestGeneratorStep {
	return &manifestGeneratorStep{
		log:               log,
		manifestGenerator: manifestGenerator,
	}
}
