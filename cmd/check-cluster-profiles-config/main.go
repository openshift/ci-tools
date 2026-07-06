package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"slices"
	"sort"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/load"
	"github.com/openshift/ci-tools/pkg/registry/server"
	"github.com/openshift/ci-tools/pkg/util"
)

var (
	configResolverAddress = api.URLForService(api.ServiceConfig)
)

type options struct {
	configPath string
	normalize  bool
}

type profileValidator struct {
	profiles   api.ClusterProfilesMap
	kubeClient ctrlruntimeclient.Client
}

func gatherOptions() options {
	o := options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.StringVar(&o.configPath, "config-path", "", "Path to the cluster profile config file")
	fs.BoolVar(&o.normalize, "normalize", false, "Normalize the cluster profiles")

	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse arguments")
	}

	return o
}

func newValidator(client ctrlruntimeclient.Client) *profileValidator {
	return &profileValidator{
		profiles:   make(api.ClusterProfilesMap),
		kubeClient: client,
	}
}

func main() {
	logger := logrus.WithField("component", "check-cluster-profiles-config")
	o := gatherOptions()

	profiles, err := load.ClusterProfiles(o.configPath)
	if err != nil {
		logger.WithError(err).Fatal("failed to load cluster profiles from config file")
	}

	if o.normalize {
		validator := newValidator(fakectrlruntimeclient.NewFakeClient())

		if err := validator.Validate(profiles); err != nil {
			logger.WithError(err).Fatal("failed to validate cluster profiles")
		}

		profiles.ClusterProfiles = normalize(profiles.ClusterProfiles)

		if err := writeConfig(o.configPath, profiles); err != nil {
			logger.WithError(err).Fatal("failed to write cluster profiles")
		}

		return
	}

	inClusterConfig, err := util.LoadClusterConfig()
	if err != nil {
		logrus.WithError(err).Fatal("failed to load in-cluster config")
	}

	client, err := ctrlruntimeclient.New(inClusterConfig, ctrlruntimeclient.Options{})
	if err != nil {
		logrus.WithError(err).Fatal("failed to create client")
	}

	validator := newValidator(client)

	if err := validator.Validate(profiles); err != nil {
		logger.WithError(err).Fatal("failed to validate cluster profiles")
	}

	normalizedProfiles := normalize(profiles.ClusterProfiles)

	if diff := cmp.Diff(profiles.ClusterProfiles, normalizedProfiles); diff != "" {
		fmt.Print(diff)
		logger.Fatal("\nProfiles have not been normalized, run `make check-cluster-profiles`")
	}

	if err := validator.checkCISecrets(); err != nil {
		logger.WithError(err).Fatal("failed to validate secrets for cluster profiles")
	}

	logger.Info("Cluster profiles successfully checked.")
}

func writeConfig(configPath string, profiles api.ClusterProfiles) error {
	bytes, err := yaml.Marshal(&profiles)
	if err != nil {
		return fmt.Errorf("marshal profiles: %w", err)
	}

	if err := os.WriteFile(configPath, bytes, 0644); err != nil {
		return fmt.Errorf("write profiles %q: %w", configPath, err)
	}

	return nil
}

func (validator *profileValidator) Validate(profiles api.ClusterProfiles) error {
	for _, p := range profiles.ClusterProfiles {
		// Check for duplicate orgs/tenants
		tenantMap := sets.New[string]()
		orgMap := sets.New[string]()

		for _, owner := range p.Owners {
			tenant := ""
			if owner.Konflux != nil {
				tenant = owner.Konflux.Tenant
			}

			if tenant == "" && owner.Org == "" {
				return fmt.Errorf("cluster profile '%v' has an invalid owner", p.Name)
			}

			if tenant != "" && owner.Org != "" {
				return fmt.Errorf("cluster profile '%v' has both org and tenant set", p.Name)
			}

			if tenant != "" {
				if tenantMap.Has(tenant) {
					return fmt.Errorf("cluster profile '%v' has duplicate tenant %q", p.Name, tenant)
				}
				tenantMap.Insert(tenant)
			}

			if owner.Org != "" {
				if orgMap.Has(owner.Org) {
					return fmt.Errorf("cluster profile '%v' has duplicate org %q", p.Name, owner.Org)
				}
				orgMap.Insert(owner.Org)
			}
		}

		// Check if a profile isn't already defined in the config
		if _, found := validator.profiles[p.Name]; found {
			return fmt.Errorf("cluster profile '%v' already exists in the configuration file", p.Name)
		}

		validator.profiles[p.Name] = p
	}
	return nil
}

// checkCISecrets verifies that the secret for each cluster profile exists in the ci namespace
func (validator *profileValidator) checkCISecrets() error {
	// FIXME: temporary workaround, we should read this information from a config file.
	clusterProfileSets := sets.New("openshift-org-aws", "openshift-org-azure", "openshift-org-gcp")
	errs := make([]error, 0)

	for profileName := range validator.profiles {
		if clusterProfileSets.Has(profileName) {
			continue
		}

		profileDetails, err := server.NewResolverClient(configResolverAddress).ClusterProfile(profileName)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to retrieve details from config resolver for '%s' cluster profile: %w", profileName, err))
			continue
		}

		ciSecret := &coreapi.Secret{}
		err = validator.kubeClient.Get(context.Background(), ctrlruntimeclient.ObjectKey{Namespace: "ci", Name: profileDetails.Secret}, ciSecret)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to get secret '%s' for cluster profile '%s': %w", profileDetails.Secret, profileName, err))
		}
	}

	return utilerrors.NewAggregate(errs)
}

func normalize(profiles []api.ClusterProfile) []api.ClusterProfile {
	if profiles == nil {
		return nil
	}

	res := make([]api.ClusterProfile, len(profiles))
	for i, profile := range profiles {
		profile := profile.DeepCopy()
		sortOwners(profile.Owners)
		res[i] = *profile
	}

	return res
}

// sortOwners does what follows:
//   - Sort repos by name.
//   - Sort owners by org.
func sortOwners(owners []api.ClusterProfileOwners) {
	if len(owners) == 0 {
		return
	}

	for i := range owners {
		owner := &owners[i]
		if len(owner.Repos) > 0 {
			slices.Sort(owner.Repos)
		}
	}

	sort.Slice(owners, func(i, j int) bool { return owners[i].Org < owners[j].Org })
}
