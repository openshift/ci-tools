// generate-registry-metadata creates a metadata.json file for a step registry directory
// that contains extra information useful for the configresolver's web UI
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/test-infra/prow/repoowners"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/load"
	"github.com/openshift/ci-tools/pkg/util/gzip"
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
	metadata := make(map[string]api.RegistryInfo)
	var errs []error
	err := filepath.WalkDir(registryPath, func(path string, info fs.DirEntry, err error) error {
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
			owners, err := gzip.ReadFileMaybeGZIP(ownersPath)
			if err != nil {
				if os.IsNotExist(err) {
					errs = append(errs, fmt.Errorf("missing OWNERS file at %s", ownersPath))
				} else {
					errs = append(errs, fmt.Errorf("failed to read OWNERS file for component %s at %s: %w", info.Name(), ownersPath, err))
				}
				return nil
			}
			var ownersConfig repoowners.Config
			err = yaml.Unmarshal(owners, &ownersConfig)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to unmarshal OWNERS file at %s: %w", ownersPath, err))
				return nil
			}
			metadata[info.Name()] = api.RegistryInfo{
				Path:   relpath,
				Owners: ownersConfig,
			}
		}
		return nil
	})
	if err := utilerrors.NewAggregate(append(errs, err)); err != nil {
		return api.RegistryMetadata{}, fmt.Errorf("Failed to update registry metadata: %w", err)
	}
	return metadata, nil
}

func writeMetadata(registryPath string, metadata api.RegistryMetadata) error {
	for filename, data := range metadata {
		metadataPath := filepath.Join(registryPath, filepath.Dir(data.Path), fmt.Sprintf("%s%s", strings.TrimSuffix(filename, ".yaml"), load.MetadataSuffix))
		output, err := json.MarshalIndent(data, "", "\t")
		if err != nil {
			return fmt.Errorf("Failed to marshal metadata file `%s`: %w", metadataPath, err)
		}
		if err := ioutil.WriteFile(metadataPath, output, 0644); err != nil {
			return fmt.Errorf("Failed to write metadata file `%s`: %w", metadataPath, err)
		}
	}
	return nil
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

	if err := writeMetadata(o.registry, metadata); err != nil {
		log.Fatalf("Failed to write metadata: %v", err)
	}
}
