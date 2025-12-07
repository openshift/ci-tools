package dockerfile

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/imagebuilder"
	"github.com/openshift/imagebuilder/dockerfile/parser"

	"github.com/openshift/ci-tools/pkg/api"
)

// registryRegex matches registry references to registry.ci.openshift.org or quay-proxy.ci.openshift.org
var registryRegex = regexp.MustCompile(`(registry\.(|svc\.)ci\.openshift\.org|quay-proxy\.ci\.openshift\.org)/\S+`)

// orgRepoTag represents a parsed image reference
type orgRepoTag struct {
	Org, Repo, Tag string
}

func (ort orgRepoTag) String() string {
	return ort.Org + "_" + ort.Repo + "_" + ort.Tag
}

// DetectInputsFromDockerfile parses a Dockerfile and detects registry references that need to be added as base images
// Returns a map of base image names to ImageStreamTagReferences
// The ImageStreamTagReference.As field contains the original registry reference from the Dockerfile
func DetectInputsFromDockerfile(
	dockerfile []byte,
	existingInputs map[string]api.ImageBuildInputs,
) (map[string]api.ImageStreamTagReference, error) {
	if len(dockerfile) == 0 {
		return nil, nil
	}

	// If there are manual inputs.as[] defined, skip auto-detection
	if hasManualInputs(existingInputs) {
		logrus.Info("Skipping Dockerfile inputs detection: manual inputs defined")
		return nil, nil
	}

	// Extract all registry references from the Dockerfile
	registryRefs := extractRegistryReferences(dockerfile)
	if len(registryRefs) == 0 {
		return nil, nil
	}

	baseImages := make(map[string]api.ImageStreamTagReference)

	for _, ref := range registryRefs {
		// Skip if there's already a manual input for this reference
		if hasManualReplacementFor(existingInputs, ref) {
			logrus.WithField("reference", ref).Debug("Skipping auto-detection: manual replacement exists")
			continue
		}

		// Parse the registry reference into org/repo/tag
		orgRepoTag, err := orgRepoTagFromPullString(ref)
		if err != nil {
			return nil, fmt.Errorf("failed to parse registry reference %s: %w", ref, err)
		}

		// Generate the base image key
		baseImageKey := orgRepoTag.String()

		// Add to base images map
		// The As field stores the original registry reference for replacement during build
		baseImages[baseImageKey] = api.ImageStreamTagReference{
			Namespace: orgRepoTag.Org,
			Name:      orgRepoTag.Repo,
			Tag:       orgRepoTag.Tag,
			As:        ref, // Store the original reference (e.g., "registry.svc.ci.openshift.org/ocp/4.19:base")
		}

		logrus.WithFields(logrus.Fields{
			"reference":      ref,
			"base_image_key": baseImageKey,
			"namespace":      orgRepoTag.Org,
			"name":           orgRepoTag.Repo,
			"tag":            orgRepoTag.Tag,
		}).Info("Dockerfile-inputs: Detected registry reference")
	}

	return baseImages, nil
}

// hasManualInputs checks if there are any manual inputs.as[] defined
func hasManualInputs(inputs map[string]api.ImageBuildInputs) bool {
	for _, input := range inputs {
		if len(input.As) > 0 {
			return true
		}
	}
	return false
}

// extractRegistryReferences finds all registry.ci.openshift.org and quay-proxy.ci.openshift.org references in the Dockerfile
func extractRegistryReferences(dockerfile []byte) []string {
	var refs []string
	seen := sets.Set[string]{}

	for _, line := range bytes.Split(dockerfile, []byte("\n")) {
		// Only look at lines that could contain image references (FROM and COPY --from)
		if !bytes.Contains(line, []byte("FROM")) && !bytes.Contains(line, []byte("COPY")) && !bytes.Contains(line, []byte("copy")) {
			continue
		}

		match := registryRegex.Find(line)
		if match == nil {
			continue
		}

		ref := string(match)
		if !seen.Has(ref) {
			refs = append(refs, ref)
			seen.Insert(ref)
		}
	}

	return refs
}

// hasManualReplacementFor checks if there's already a manual input configuration for the given reference
func hasManualReplacementFor(inputs map[string]api.ImageBuildInputs, target string) bool {
	for _, input := range inputs {
		if sets.New(input.As...).Has(target) {
			return true
		}
	}
	return false
}

// orgRepoTagFromPullString parses a pull string like "registry.ci.openshift.org/ocp/4.19:base"
// into its component parts (org, repo, tag)
// For quay-proxy references, the tag contains org_repo_tag format that needs special parsing
func orgRepoTagFromPullString(pullString string) (orgRepoTag, error) {
	res := orgRepoTag{}

	slashSplit := strings.Split(pullString, "/")
	if len(slashSplit) != 3 {
		return orgRepoTag{}, fmt.Errorf("unexpected pull string format: %q", pullString)
	}

	repoTag := strings.Split(slashSplit[2], ":")
	if len(repoTag) != 2 {
		return orgRepoTag{}, fmt.Errorf("unexpected repo:tag format in pull string: %q", pullString)
	}

	if strings.Contains(pullString, "quay-proxy.ci.openshift.org/openshift/ci") {
		return orgRepoTagFromQuayProxyTag(repoTag[1])
	}

	res.Org = slashSplit[1]
	res.Repo = repoTag[0]
	res.Tag = repoTag[1]
	return res, nil
}

// orgRepoTagFromQuayProxyTag parses a quay-proxy tag like "ocp_builder_rhel-9-golang-1.21-openshift-4.16"
// which encodes org_repo_tag format, into its component parts
func orgRepoTagFromQuayProxyTag(quayTag string) (orgRepoTag, error) {
	// Split by underscore - format is org_repo_tag
	// We need to split on the first two underscores only
	parts := strings.SplitN(quayTag, "_", 3)
	if len(parts) < 3 {
		return orgRepoTag{}, fmt.Errorf("quay-proxy tag %q doesn't match org_repo_tag format", quayTag)
	}

	return orgRepoTag{
		Org:  parts[0],
		Repo: parts[1],
		Tag:  parts[2],
	}, nil
}

// extractReplacementCandidatesFromDockerfile extracts all image references from a Dockerfile
// This is used to understand which images are actually referenced in the Dockerfile
func extractReplacementCandidatesFromDockerfile(dockerfile []byte) (sets.Set[string], error) {
	replacementCandidates := sets.Set[string]{}
	node, err := imagebuilder.ParseDockerfile(bytes.NewBuffer(dockerfile))
	if err != nil {
		return nil, fmt.Errorf("failed to parse Dockerfile: %w", err)
	}

	// copied from https://github.com/openshift/builder/blob/1205194b1d67f2b68c163add5ae17e4b81962ec3/pkg/build/builder/common.go#L472-L497
	// only difference: We collect the replacement source values rather than doing the replacements
	names := make(map[string]string)
	stages, err := imagebuilder.NewStages(node, imagebuilder.NewBuilder(make(map[string]string)))
	if err != nil {
		return nil, fmt.Errorf("failed to construct imagebuilder stages: %w", err)
	}
	for _, stage := range stages {
		for _, child := range stage.Node.Children {
			switch {
			case child.Value == "from" && child.Next != nil:
				image := child.Next
				replacementCandidates.Insert(image.Value)
				names[stage.Name] = image.Value
				if alias := image.Next; alias != nil && alias.Value == "AS" && alias.Next != nil {
					replacementCandidates.Insert(alias.Next.Value)
				}
			case child.Value == "copy":
				if ref, ok := nodeHasFromRef(child); ok {
					if len(ref) > 0 {
						if _, ok := names[ref]; !ok {
							replacementCandidates.Insert(ref)
						}
					}
				}
			}
		}
	}

	return replacementCandidates, nil
}

// nodeHasFromRef checks if a COPY instruction has a --from flag
func nodeHasFromRef(node *parser.Node) (string, bool) {
	for _, arg := range node.Flags {
		if after, ok := strings.CutPrefix(arg, "--from="); ok {
			from := after
			return from, true
		}
	}
	return "", false
}
