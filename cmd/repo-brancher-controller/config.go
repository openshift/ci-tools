package main

import (
	"fmt"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/promotion"
)

func loadDesiredState(configDir string, forwardingConfig *forwardingConfig) (map[repoKey]sets.Set[string], error) {
	result := map[repoKey]sets.Set[string]{}
	err := config.OperateOnCIOperatorConfigDir(configDir, func(configuration *api.ReleaseBuildConfiguration, info *config.Info) error {
		addTargets := func(sourceRelease string, targets []string) error {
			key := repoKey{org: info.Org, repo: info.Repo, source: info.Branch}
			for _, targetRelease := range targets {
				target, err := promotion.DetermineReleaseBranch(sourceRelease, targetRelease, info.Branch)
				if err != nil {
					return fmt.Errorf("determine target branch for %s: %w", key, err)
				}
				if target != info.Branch {
					if result[key] == nil {
						result[key] = sets.New[string]()
					}
					result[key].Insert(target)
				}
			}
			return nil
		}

		if current := forwardingConfig.DefaultBranch; current != nil && (info.Branch == "main" || info.Branch == "master") && !isIgnored(current.Ignore, info.Org, info.Repo) && api.PromotesOfficialImage(configuration, api.WithoutOKD, current.ConfigsPromotingTo) {
			if err := addTargets(current.ConfigsPromotingTo, current.Targets); err != nil {
				return err
			}
		}

		for _, forwarding := range forwardingConfig.ReleaseBranches {
			if isIgnored(forwarding.Ignore, info.Org, info.Repo) || !api.PromotesOfficialImage(configuration, api.WithoutOKD, forwarding.Source) {
				continue
			}
			if info.Branch != "release-"+forwarding.Source && info.Branch != "openshift-"+forwarding.Source {
				continue
			}
			if err := addTargets(forwarding.Source, forwarding.Targets); err != nil {
				return err
			}
		}
		return nil
	})
	return result, err
}
