package dockerfile

import (
	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/api"
)

// DetectInputsFromDockerfile parses a Dockerfile and detects registry references that need to be added as base images
// Returns a map of base image names to ImageStreamTagReferences
// The ImageStreamTagReference.As field contains the original registry reference from the Dockerfile
func DetectInputsFromDockerfile(dockerfile []byte, existingInputs map[string]api.ImageBuildInputs, from api.PipelineImageStreamTagReference, baseImages map[string]api.ImageStreamTagReference) map[string]api.ImageStreamTagReference {
	registryRefs := ExtractRegistryReferences(dockerfile)
	detected := make(map[string]api.ImageStreamTagReference)

	for _, ref := range registryRefs {
		if from != "" && matchesFromBaseImage(ref, from, baseImages) {
			logrus.WithField("reference", ref).WithField("from", from).Debug("Skipping Dockerfile input already provided by image from")
			continue
		}
		if HasManualReplacementFor(existingInputs, ref) {
			logrus.WithField("reference", ref).Debug("Skipping Dockerfile inputs detection: manual replacement exists")
			continue
		}
		orgRepoTag, err := OrgRepoTagFromPullString(ref)
		if err != nil {
			logrus.WithField("reference", ref).WithError(err).Debug("Failed to parse registry reference, skipping")
			continue
		}
		baseImageKey := orgRepoTag.String()
		detected[baseImageKey] = api.ImageStreamTagReference{
			Namespace: orgRepoTag.Org,
			Name:      orgRepoTag.Repo,
			Tag:       orgRepoTag.Tag,
			As:        ref,
		}
	}

	return detected
}

func matchesFromBaseImage(ref string, from api.PipelineImageStreamTagReference, baseImages map[string]api.ImageStreamTagReference) bool {
	if from == "" || baseImages == nil {
		return false
	}
	base, ok := baseImages[string(from)]
	if !ok {
		return false
	}
	orgRepoTag, err := OrgRepoTagFromPullString(ref)
	if err != nil {
		return false
	}
	return orgRepoTag.Org == base.Namespace && orgRepoTag.Repo == base.Name && orgRepoTag.Tag == base.Tag
}
