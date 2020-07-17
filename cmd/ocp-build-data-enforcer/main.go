package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/openshift/builder/pkg/build/builder/util/dockerfile"
	"github.com/openshift/imagebuilder"
	dockercmd "github.com/openshift/imagebuilder/dockerfile/command"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

type options struct {
	ocpBuildDataRepoDir string
}

func gatherOptions() (*options, error) {
	o := &options{}
	flag.StringVar(&o.ocpBuildDataRepoDir, "ocp-build-data-repo-dir", "../ocp-build-data", "The directory in which the ocp-build-data reposity is")
	flag.Parse()
	return o, nil
}
func main() {
	opts, err := gatherOptions()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to gather options")
	}

	configs, err := gatherAllOCPImageConfigs(opts.ocpBuildDataRepoDir)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to gather all ocp image configs")
	}
}

func gatherAllOCPImageConfigs(ocpBuildDataDir string) ([]ocpImageConfig, error) {
	var result []ocpImageConfig
	resultLock := &sync.Mutex{}
	errGroup := &errgroup.Group{}

	path := filepath.Join(ocpBuildDataDir, "images")
	if err := filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		errGroup.Go(func() error {
			data, err := ioutil.ReadFile(path)
			if err != nil {
				return fmt.Errorf("failed to read file from %s: %w", path, err)
			}
			var config ocpImageConfig
			if err := json.Unmarshal(data, &config); err != nil {
				return fmt.Errorf("failed to unmarshal data from %s intp ocpImageConfig: %w", path, err)
			}
			config.SourceFileName = strings.TrimLeft(path, ocpBuildDataDir)
			resultLock.Lock()
			result = append(result, config)
			resultLock.Unlock()

			return nil
		})

		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to walk")
	}

	if err := errGroup.Wait(); err != nil {
		return nil, fmt.Errorf("failed to read all files: %w", err)
	}

	return result, nil
}

func updateDockerfile(initialDockerfile []byte, config ocpImageConfig) ([]byte, bool, error) {
	rootNode, err := imagebuilder.ParseDockerfile(bytes.NewBuffer(initialDockerfile))
	if err != nil {
		return nil, false, fmt.Errorf("failed to parse Dockerfile: %w", err)
	}

	stages, err := imagebuilder.NewStages(rootNode, imagebuilder.NewBuilder(nil))
	if err != nil {
		return nil, false, fmt.Errorf("failed to construct imagebuilder stages: %w", err)
	}

	cfgStages := config.stages()
	if expected := len(cfgStages); expected != len(stages) {
		return nil, false, fmt.Errorf("expected %d stages based on ocp config %s but got %d", expected, config.SourceFileName, len(stages))
	}

	// The parsing strips off comments so we have to track this manually
	var hasChanges bool
	for stageIdx, stage := range stages {
		for _, child := range stage.Node.Children {
			if child.Value != dockercmd.From {
				continue
			}
			if child.Next == nil {
				return nil, false, fmt.Errorf("dockerfile has FROM directive without value on line %d", child.StartLine)
			}
			if child.Next.Value != cfgStages[stageIdx] {
				hasChanges = true
				child.Next.Value = cfgStages[stageIdx]
			}
		}
	}

	updatedDockerfile := dockerfile.Write(rootNode)
	return updatedDockerfile, hasChanges, nil
}
