package main

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/api"
	cioperatorconfig "github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/privateorg"
)

// publicCounterpart resolves private mirror metadata to a unique public repository.
func publicCounterpart(metadata api.Metadata, configs cioperatorconfig.ByOrgRepo) (api.Metadata, error) {
	flattenedOrgs := sets.New[string](privateorg.DefaultFlattenOrgs...)
	candidates := sets.New[string]()
	for org, repos := range configs {
		if org == "openshift-priv" {
			continue
		}
		for repo := range repos {
			if privateorg.MirroredRepoName(org, repo, flattenedOrgs) == metadata.Repo {
				candidates.Insert(org + "/" + repo)
			}
		}
	}
	if candidates.Len() != 1 {
		return api.Metadata{}, fmt.Errorf("expected one public counterpart for %s/%s, found %d: %v", metadata.Org, metadata.Repo, candidates.Len(), sets.List(candidates))
	}
	parts := strings.SplitN(sets.List(candidates)[0], "/", 2)
	metadata.Org, metadata.Repo = parts[0], parts[1]
	return metadata, nil
}
