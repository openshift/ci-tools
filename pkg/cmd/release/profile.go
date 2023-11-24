package release

import (
	"fmt"

	"github.com/spf13/cobra"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/api"
)

func newProfileCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "profile",
		Short: "cluster profile commands",
		RunE: func(_ *cobra.Command, args []string) error {
			return cmdProfileList(args)
		},
	}
}

func cmdProfileList(args []string) error {
	if len(args) == 0 {
		for _, p := range api.ClusterProfiles() {
			args = append(args, string(p))
		}
	} else {
		valid := sets.New[string]()
		for _, p := range api.ClusterProfiles() {
			valid.Insert(string(p))
		}
		for _, arg := range args {
			if !valid.Has(arg) {
				return fmt.Errorf("invalid cluster profile: %s", arg)
			}
		}
	}
	return profilePrint(args)
}

func profilePrint(args []string) error {
	type P struct {
		Profile     api.ClusterProfile `json:"profile"`
		ClusterType string             `json:"cluster_type"`
		LeaseType   string             `json:"lease_type"`
		Secret      string             `json:"secret"`
		ConfigMap   string             `json:"config_map,omitempty"`
	}
	var l []P
	for _, arg := range args {
		p := api.ClusterProfile(arg)
		l = append(l, P{
			Profile:     p,
			ClusterType: p.ClusterType(),
			LeaseType:   p.LeaseType(),
			Secret:      p.Secret(),
			ConfigMap:   p.ConfigMap(),
		})
	}
	return printYAML(l)
}
