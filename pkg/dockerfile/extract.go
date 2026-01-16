package dockerfile

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/api"
)

// RegistryRegex matches registry references to registry.ci.openshift.org or quay-proxy.ci.openshift.org
var RegistryRegex = regexp.MustCompile(`(registry\.(?:svc\.)?ci\.openshift\.org|quay-proxy\.ci\.openshift\.org)/[^\s\\]+`)

// OrgRepoTag represents a parsed image reference
type OrgRepoTag struct {
	Org, Repo, Tag string
}

func (ort OrgRepoTag) String() string {
	return ort.Org + "_" + ort.Repo + "_" + ort.Tag
}

// ExtractRegistryReferences finds all registry.ci.openshift.org and quay-proxy.ci.openshift.org references in the Dockerfile
func ExtractRegistryReferences(dockerfile []byte) []string {
	var refs []string
	seen := sets.Set[string]{}

	for _, line := range bytes.Split(dockerfile, []byte("\n")) {
		upper := bytes.ToUpper(line)
		if !bytes.Contains(upper, []byte("FROM")) && !bytes.Contains(upper, []byte("COPY")) {
			continue
		}

		match := RegistryRegex.Find(line)
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

// HasManualReplacementFor checks if there's already a manual input configuration for the given reference
func HasManualReplacementFor(inputs map[string]api.ImageBuildInputs, target string) bool {
	for _, input := range inputs {
		if sets.New(input.As...).Has(target) {
			return true
		}
	}
	return false
}

// OrgRepoTagFromPullString parses a pull string like "registry.ci.openshift.org/ocp/4.19:base"
// into its component parts (org, repo, tag)
// For quay-proxy references, the tag contains org_repo_tag format that needs special parsing
func OrgRepoTagFromPullString(pullString string) (OrgRepoTag, error) {
	res := OrgRepoTag{Tag: "latest"}

	slashSplit := strings.Split(pullString, "/")
	n := len(slashSplit)

	switch {
	case n == 1:
		res.Org = "_"
		res.Repo = slashSplit[0]
	case n >= 2:
		res.Org = slashSplit[n-2]
		res.Repo = slashSplit[n-1]
	default:
		return res, fmt.Errorf("pull string %q couldn't be parsed, got %d components", pullString, n)
	}
	if repoTag := strings.Split(res.Repo, ":"); len(repoTag) == 2 {
		res.Repo = repoTag[0]
		res.Tag = repoTag[1]
	}

	if strings.Contains(pullString, "quay-proxy.ci.openshift.org/openshift/ci") {
		return orgRepoTagFromQuayProxyTag(res.Tag)
	}

	return res, nil
}

// orgRepoTagFromQuayProxyTag parses a quay-proxy tag like "ocp_builder_rhel-9-golang-1.21-openshift-4.16"
// which encodes org_repo_tag format, into its component parts
func orgRepoTagFromQuayProxyTag(quayTag string) (OrgRepoTag, error) {
	parts := strings.SplitN(quayTag, "_", 3)
	if len(parts) < 3 {
		return OrgRepoTag{}, fmt.Errorf("quay-proxy tag %q doesn't match org_repo_tag format", quayTag)
	}

	return OrgRepoTag{
		Org:  parts[0],
		Repo: parts[1],
		Tag:  parts[2],
	}, nil
}
