package load

import (
	"fmt"
	"os"

	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
)

func ClusterProfilesConfig(configPath string) (api.ClusterProfilesList, error) {
	configContents, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read cluster profiles config: %w", err)
	}

	var profileOwnersList api.ClusterProfilesList
	if err = yaml.Unmarshal(configContents, &profileOwnersList); err != nil {
		return nil, fmt.Errorf("failed to unmarshall cluster profiles config: %w", err)
	}

	// The following code can be erased once profiles are completely moved
	// from code in ci-tools to the config file in openshift/release
	profileOwnersMap := make(map[api.ClusterProfile]api.ClusterProfileDetails)
	for _, p := range profileOwnersList {
		profileOwnersMap[p.Profile] = p
	}

	var mergedList api.ClusterProfilesList
	for _, profileName := range api.ClusterProfiles() {
		profile, found := profileOwnersMap[profileName]
		if found {
			mergedList = append(mergedList, profile)
		} else {
			mergedList = append(mergedList, api.ClusterProfileDetails{Profile: profileName})
		}
	}

	return mergedList, nil
}
