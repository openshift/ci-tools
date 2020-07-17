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
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/openshift/ci-tools/pkg/api"
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

	streamMap, err := readStreamMap(opts.ocpBuildDataRepoDir)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to read streamMap")
	}

	buildClusterMapping, err := extractBuildClusterImageStreamTagsForMapping(streamMap, configs)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to extract build cluster imagestreamtag references")
	}
}

func extractBuildClusterImageStreamTagsForMapping(streamMap streamMap, imageConfigs []ocpImageConfig) (map[string]api.ImageStreamTagReference, error) {

	result := map[string]api.ImageStreamTagReference{}
	// Search the imageConfigs once and not once per alias we care about, as its pretty big.
	// We check for missing aliases in the end and don't care about superfluous results.
	for _, imageConfig := range imageConfigs {
		if len(imageConfig.Push.Also) < 1 {
			continue
		}

		var matchingImagePushAlso []string
		for _, also := range imageConfig.Push.Also {
			if strings.HasPrefix(also, "registry.svc.ci.openshift.org") {
				matchingImagePushAlso = append(matchingImagePushAlso, also)
			}
		}

		if n := len(matchingImagePushAlso); n == 0 {
			continue
		} else if n > 1 {
			// Better complain than getting weird to debug results
			return nil, fmt.Errorf("imageConfigPushAlso in file %s doesn't have zero or one elements that match api.ci but %d", imageConfig.SourceFileName, n)
		}

		slashSplitRegistryString := strings.Split(matchingImagePushAlso[0], "/")
		if n := len(slashSplitRegistryString); n != 3 {
			return nil, fmt.Errorf("api.ci reference %q found in file %s split by '/' doesn't have three but %d elements", matchingImagePushAlso[0], imageConfig.SourceFileName, n)
		}

		imageStreamNamespace, imageStreamName := slashSplitRegistryString[1], slashSplitRegistryString[2]
		for _, additionalTag := range imageConfig.Push.AdditionalTags {
			result[additionalTag] = api.ImageStreamTagReference{
				Namespace: imageStreamNamespace,
				Name:      imageStreamName,
				Tag:       additionalTag,
			}
		}
	}

	var errs []error
	for alias := range streamMap {
		if _, exists := result[alias]; !exists {
			errs = append(errs, fmt.Errorf("couldn't resolve alias %s", alias))
		}
	}

	return result, utilerrors.NewAggregate(errs)
}

func readStreamMap(ocpBuildDataDir string) (streamMap, error) {
	path := filepath.Join(ocpBuildDataDir, "streams.yaml")
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}
	streamMap := streamMap{}
	if err := json.Unmarshal(data, &streamMap); err != nil {
		return nil, fmt.Errorf("fauiled to unmarshal %s into streamMap: %w", path, err)
	}
	return streamMap, nil
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
