package main

import (
	"fmt"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
)

func loadDesiredState(configDir string, forwardingConfig *forwardingConfig) (map[repoKey]sets.Set[string], error) {
	result := map[repoKey]sets.Set[string]{}
	err := config.OperateOnCIOperatorConfigDir(configDir, func(configuration *api.ReleaseBuildConfiguration, info *config.Info) error {
		addTargets := func(sourceRelease string, forward forwardBlock, allowDefaultBranch bool) error {
			key := repoKey{org: info.Org, repo: info.Repo, source: info.Branch}
			for _, targetRelease := range forward.Targets {
				target, matches, err := determineTargetBranch(sourceRelease, targetRelease, info.Branch, forward.Family, allowDefaultBranch)
				if err != nil {
					return fmt.Errorf("determine target branch for %s: %w", key, err)
				}
				if matches && target != info.Branch && isIncluded(forward.Only, info.Org, info.Repo, info.Branch, target) && !isIgnored(forward.Ignore, info.Org, info.Repo, info.Branch, target) {
					if result[key] == nil {
						result[key] = sets.New[string]()
					}
					result[key].Insert(target)
				}
			}
			return nil
		}

		if current := forwardingConfig.DefaultBranch; current != nil && (info.Branch == "main" || info.Branch == "master") && api.PromotesOfficialImage(configuration, api.WithoutOKD, current.ConfigsPromotingTo) {
			for _, forward := range current.forwardBlocks() {
				if err := addTargets(current.ConfigsPromotingTo, forward, true); err != nil {
					return err
				}
			}
		}

		for _, forwarding := range forwardingConfig.ReleaseBranches {
			for _, forward := range forwarding.forwardBlocks() {
				if err := addTargets(forwarding.Source, forward, false); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return result, err
}

func determineTargetBranch(sourceRelease, targetRelease, sourceBranch, family string, allowDefaultBranch bool) (string, bool, error) {
	switch family {
	case branchFamilyRelease:
		if (allowDefaultBranch && (sourceBranch == "main" || sourceBranch == "master")) || sourceBranch == "release-"+sourceRelease {
			return "release-" + targetRelease, true, nil
		}
	case branchFamilyOpenShift:
		if (allowDefaultBranch && (sourceBranch == "main" || sourceBranch == "master")) || sourceBranch == "openshift-"+sourceRelease {
			return "openshift-" + targetRelease, true, nil
		}
	default:
		return "", false, fmt.Errorf("unknown branch family %q", family)
	}
	return "", false, nil
}
