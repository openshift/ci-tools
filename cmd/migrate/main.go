package main

import (
	"flag"

	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/config"
	"github.com/openshift/ci-tools/pkg/promotion"
)

func main() {
	dir := flag.String("dir", "", "")
	flag.Parse()
	var toCommit []config.DataWithInfo
	if err := config.OperateOnCIOperatorConfigDir(*dir, func(configuration *api.ReleaseBuildConfiguration, info *config.Info) error {
		if promotion.PromotesOfficialImages(configuration) {
			return nil
		}
		if configuration.ReleaseTagConfiguration != nil {
			updated := *configuration
			updated.ReleaseTagConfiguration = nil
			if updated.Releases == nil {
				updated.Releases = map[string]api.UnresolvedRelease{}
			}
			updated.Releases[api.InitialReleaseName] = api.UnresolvedRelease{
				Integration: &api.Integration{
					Namespace: configuration.ReleaseTagConfiguration.Namespace,
					Name:      configuration.ReleaseTagConfiguration.Name,
				},
			}
			updated.Releases[api.LatestReleaseName] = api.UnresolvedRelease{
				Integration: &api.Integration{
					Namespace:          configuration.ReleaseTagConfiguration.Namespace,
					Name:               configuration.ReleaseTagConfiguration.Name,
					IncludeBuiltImages: true,
				},
			}

			// we are walking the config so we need to commit once we're done
			toCommit = append(toCommit, config.DataWithInfo{
				Configuration: updated,
				Info:          *info,
			})
		}
		return nil
	}); err != nil {
		logrus.WithError(err).Fatal("Could not branch configurations.")
	}

	var failed bool
	for _, output := range toCommit {
		if err := output.CommitTo(*dir); err != nil {
			failed = true
		}
	}
	if failed {
		logrus.Fatal("Failed to commit configuration to disk.")
	}
}
