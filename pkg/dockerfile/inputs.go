package dockerfile

import (
	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/api"
)

// DetectInputsFromDockerfile parses a Dockerfile and detects registry references that need to be added as base images
// Returns a map of base image names to ImageStreamTagReferences
// The ImageStreamTagReference.As field contains the original registry reference from the Dockerfile
func DetectInputsFromDockerfile(dockerfile []byte, existingInputs map[string]api.ImageBuildInputs) map[string]api.ImageStreamTagReference {
	registryRefs := ExtractRegistryReferences(dockerfile)
	baseImages := make(map[string]api.ImageStreamTagReference)

	for _, ref := range registryRefs {
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
		baseImages[baseImageKey] = api.ImageStreamTagReference{
			Namespace: orgRepoTag.Org,
			Name:      orgRepoTag.Repo,
			Tag:       orgRepoTag.Tag,
			As:        ref,
		}
	}

	return baseImages
}
