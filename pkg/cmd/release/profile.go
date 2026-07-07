package release

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/load"
)

const (
	clusterProfilesConfig = "ci-operator/step-registry/cluster-profiles/cluster-profiles-config.yaml"
)

func newProfileCommand(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "profile",
		Short: "cluster profile commands",
		RunE: func(_ *cobra.Command, args []string) error {
			return cmdProfileList(o, args)
		},
	}
}

func cmdProfileList(o *options, args []string) error {
	profilesConfigPath := path.Join(o.rootPath, clusterProfilesConfig)
	clusterProfiles, err := load.ClusterProfiles(profilesConfigPath)
	if err != nil {
		return fmt.Errorf("read cluster profiles %s: %w", clusterProfilesConfig, err)
	}

	profiles := make([]api.ClusterProfile, 0)
	if len(args) == 0 {
		for _, p := range clusterProfiles.Items {
			profiles = append(profiles, p)
		}
	} else {
		for _, arg := range args {
			if p, ok := clusterProfiles.Get(arg); !ok {
				return fmt.Errorf("invalid cluster profile: %s", arg)
			} else {
				profiles = append(profiles, p)
			}
		}
	}

	slices.SortFunc(profiles, func(a, b api.ClusterProfile) int { return strings.Compare(a.Name, b.Name) })
	return printYAML(profiles)
}
