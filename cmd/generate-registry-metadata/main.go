// generate-registry-metadata creates a metadata.json file for a step registry directory
// that contains extra information useful for the configresolver's web UI
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/sirupsen/logrus"
	"k8s.io/test-infra/prow/repoowners"
	"sigs.k8s.io/yaml"
)

type options struct {
	registry string
}

func (o *options) Validate() error {
	if o.registry == "" {
		return errors.New("--registry is required")
	}
	return nil
}

func gatherOptions() (options, error) {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.registry, "registry", "", "Path to the step registry directory.")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return options{}, fmt.Errorf("could not parse input: %w", err)
	}
	return o, nil
}

func generateMetadata(registryPath string) (api.RegistryMetadata, error) {
	metadata := api.RegistryMetadata{
		Metadata: make(map[string]api.RegistryInfo),
	}
	if err := filepath.Walk(registryPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info != nil && !info.IsDir() && filepath.Ext(info.Name()) == ".yaml" {
			relpath, err := filepath.Rel(registryPath, path)
			if err != nil {
				return fmt.Errorf("failed to determine relative path for %s: %w", path, err)
			}
			ownersPath := filepath.Join(filepath.Dir(path), "OWNERS")
			// all step registry components are required to have an owners file in the same directory as the component
			owners, err := ioutil.ReadFile(ownersPath)
			if err != nil {
				return fmt.Errorf("failed to read OWNERS file for component %s at %s: %w", info.Name(), ownersPath, err)
			}
			var ownersConfig repoowners.Config
			err = yaml.Unmarshal(owners, &ownersConfig)
			if err != nil {
				return fmt.Errorf("failed to unmarshal OWNERS file at %s: %w", ownersPath, err)
			}
			metadata.Metadata[info.Name()] = api.RegistryInfo{
				Path:   relpath,
				Owners: ownersConfig,
			}
		}
		return nil
	}); err != nil {
		return api.RegistryMetadata{}, fmt.Errorf("Failed to update registry metadata: %w", err)
	}
	return metadata, nil
}

func main() {
	o, err := gatherOptions()
	if err != nil {
		logrus.WithError(err).Fatal("failed to gather options")
	}
	if err := o.Validate(); err != nil {
		logrus.WithError(err).Fatal("invalid options")
	}

	metadata, err := generateMetadata(o.registry)
	if err != nil {
		log.Fatalf("Failed to update metadata: %v", err)
	}

	output, err := json.MarshalIndent(metadata, "", "\t")
	if err != nil {
		log.Fatalf("Failed to marshal metadata file: %v", err)
	}

	metadataPath := filepath.Join(o.registry, api.RegistryMetadataPath)
	if err := ioutil.WriteFile(metadataPath, output, 0644); err != nil {
		log.Fatalf("Failed to write metadata file %s: %v", metadataPath, err)
	}
}
