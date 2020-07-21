package main

import (
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/load"
	"github.com/sirupsen/logrus"
	"sigs.k8s.io/yaml"
)

type options struct {
	stepRegistryDir string
}

func (o *options) Validate() error {
	if o.stepRegistryDir == "" {
		return errors.New("--step-registry-dir is required")
	}
	return nil
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&o.stepRegistryDir, "step-registry-dir", "", "Path to the step registry directory.")
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse input")
	}
	return o
}

func main() {
	o := gatherOptions()
	if err := o.Validate(); err != nil {
		logrus.WithError(err).Fatal("invalid options")
	}

	if err := filepath.Walk(o.stepRegistryDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info != nil && !info.IsDir() {
			if filepath.Ext(info.Name()) != ".yaml" {
				return nil
			}
			raw, err := ioutil.ReadFile(path)
			if err != nil {
				return err
			}
			relpath, err := filepath.Rel(o.stepRegistryDir, path)
			if err != nil {
				return fmt.Errorf("failed to determine relative path for %s: %w", path, err)
			}
			var output []byte
			switch {
			case strings.HasSuffix(path, load.ReferenceSuffix):
				ref := &api.RegistryReferenceConfig{}
				err := yaml.Unmarshal(raw, ref)
				if err != nil {
					return fmt.Errorf("Unexpected error reading reference file %s: %w", path, err)
				}
				ref.Reference.Metadata.ComponentPath = relpath
				output, err = yaml.Marshal(ref)
				if err != nil {
					return fmt.Errorf("Unexpected error marshalling reference file %s: %w", path, err)
				}
			case strings.HasSuffix(path, load.ChainSuffix):
				chain := &api.RegistryChainConfig{}
				err := yaml.Unmarshal(raw, chain)
				if err != nil {
					return fmt.Errorf("Unexpected error reading chain file %s: %w", path, err)
				}
				chain.Chain.Metadata.ComponentPath = relpath
				output, err = yaml.Marshal(chain)
				if err != nil {
					return fmt.Errorf("Unexpected error marshalling chain file %s: %w", path, err)
				}
			case strings.HasSuffix(path, load.WorkflowSuffix):
				workflow := &api.RegistryWorkflowConfig{}
				err := yaml.Unmarshal(raw, workflow)
				if err != nil {
					return fmt.Errorf("Unexpected error reading workflow file %s: %w", path, err)
				}
				workflow.Workflow.Metadata.ComponentPath = relpath
				output, err = yaml.Marshal(workflow)
				if err != nil {
					return fmt.Errorf("Unexpected error marshalling workflow file %s: %w", path, err)
				}
			default:
				return fmt.Errorf("YAML file %s has an incorrectly formatted filename", path)
			}
			if err := ioutil.WriteFile(path, output, 0664); err != nil {
				return fmt.Errorf("failed to write new step registry file: %w", err)
			}
		}
		return nil
	}); err != nil {
		logrus.WithError(err).Errorf("Failed to update step registry files")
	}
}
