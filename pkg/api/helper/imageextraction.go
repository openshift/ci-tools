package helper

import (
	"fmt"
	"k8s.io/apimachinery/pkg/types"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/load"
)

// GetAllTestInputImageStreamTags returns a deduplicated and unsorted list of all ImageStreamTags
// referenced anywhere as input by a test in the config. It only returns their namespace and name and drops the
// cluster field, as we plan to remove that.
// The key is in namespace/name format.
func GetAllTestInputImageStreamTags(config load.ByOrgRepo) map[string]types.NamespacedName {
	result := map[string]types.NamespacedName{}
	for _, org := range config {
		for _, repo := range org {
			for _, cfg := range repo {
				for k, v := range TestInputImageStreamTagsFromConfig(cfg) {
					result[k] = v
				}
			}
		}
	}

	return result
}

// TestInputImageStreamTagsFromConfig returns all ImageStreamTags referenced anywhere in the config as input.
// It only returns their namespace and name and drops the cluster field, as we plan to remove that.
// The key is in namespace/name format.
func TestInputImageStreamTagsFromConfig(cfg api.ReleaseBuildConfiguration) map[string]types.NamespacedName {
	result := map[string]types.NamespacedName{}

	imageStreamTagReferenceMapIntoMap(cfg.BaseImages, result)
	imageStreamTagReferenceMapIntoMap(cfg.BaseRPMImages, result)
	if cfg.BuildRootImage != nil && cfg.BuildRootImage.ImageStreamTagReference != nil {
		insert(*cfg.BuildRootImage.ImageStreamTagReference, result)
	}

	for _, testStep := range cfg.Tests {
		if testStep.MultiStageTestConfiguration != nil {
			insertTagReferencesFromSteps(testStep.MultiStageTestConfiguration, result)
		}
	}

	for _, rawStep := range cfg.RawSteps {
		if rawStep.InputImageTagStepConfiguration != nil {
			insert(rawStep.InputImageTagStepConfiguration.BaseImage, result)
		}
		if rawStep.SourceStepConfiguration != nil {
			insert(rawStep.SourceStepConfiguration.ClonerefsImage, result)
		}
		if rawStep.TestStepConfiguration != nil && rawStep.TestStepConfiguration.MultiStageTestConfiguration != nil {
			insertTagReferencesFromSteps(rawStep.TestStepConfiguration.MultiStageTestConfiguration, result)
		}
	}

	return result
}

func imageStreamTagReferenceMapIntoMap(i map[string]api.ImageStreamTagReference, m map[string]types.NamespacedName) {
	for _, item := range i {
		insert(item, m)
	}
}

func imageStreamTagReferenceToString(istr api.ImageStreamTagReference) string {
	return fmt.Sprintf("%s/%s:%s", istr.Namespace, istr.Name, istr.Tag)
}

func insertTagReferencesFromSteps(config *api.MultiStageTestConfiguration, m map[string]types.NamespacedName) {
	for _, subStep := range append(append(config.Pre, config.Test...), config.Post...) {
		if subStep.LiteralTestStep != nil && subStep.LiteralTestStep.FromImage != nil {
			insert(*subStep.LiteralTestStep.FromImage, m)
		}
	}
}

func insert(item api.ImageStreamTagReference, m map[string]types.NamespacedName) {
	if _, ok := m[imageStreamTagReferenceToString(item)]; ok {
		return
	}
	m[imageStreamTagReferenceToString(item)] = types.NamespacedName{
		Namespace: item.Namespace,
		Name:      fmt.Sprintf("%s:%s", item.Name, item.Tag),
	}
}
