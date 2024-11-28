package onboard

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/api/secretbootstrap"
	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
)

const (
	credentials                  = "credentials"
	dotDockerConfigJson          = ".dockerconfigjson"
	testCredentials              = "test-credentials"
	kubeconfig                   = "kubeconfig"
	config                       = "config"
	pjRehearse                   = "pj-rehearse"
	pull                pushPull = "puller"
	push                pushPull = "pusher"
	dockerconfigjson             = "kubernetes.io/dockerconfigjson"
)

type pushPull string

type ciSecretBootstrapStep struct {
	log            *logrus.Entry
	clusterInstall *clusterinstall.ClusterInstall
}

func (s *ciSecretBootstrapStep) Name() string { return "ci-secret-bootstrap" }

func (s *ciSecretBootstrapStep) Run(ctx context.Context) error {
	s.log = s.log.WithField("step", "ci-secret-bootstrap")
	secretBootstrapDir := filepath.Join(s.clusterInstall.Onboard.ReleaseRepo, "core-services", "ci-secret-bootstrap")
	secretBootstrapConfigFile := filepath.Join(secretBootstrapDir, "_config.yaml")
	s.log.Infof("Updating ci-secret-bootstrap: %s", secretBootstrapConfigFile)

	var c secretbootstrap.Config
	if err := secretbootstrap.LoadConfigFromFile(secretBootstrapConfigFile, &c); err != nil {
		return err
	}
	if err := s.updateCiSecretBootstrapConfig(&c); err != nil {
		return err
	}
	return secretbootstrap.SaveConfigToFile(secretBootstrapConfigFile, &c)
}

func (s *ciSecretBootstrapStep) updateCiSecretBootstrapConfig(c *secretbootstrap.Config) error {
	groupNames := []string{BuildUFarm, "non_app_ci"}

	// non-OSD clusters should never be in the group
	if *s.clusterInstall.Onboard.OSD && !*s.clusterInstall.Onboard.Unmanaged {
		groupNames = append(groupNames, secretbootstrap.OSDGlobalPullSecretGroupName)
	}
	if !*s.clusterInstall.Onboard.OSD {
		groupNames = append(groupNames, secretbootstrap.OpenShiftConfigPullSecretGroupName)
	}
	if !*s.clusterInstall.Onboard.Unmanaged {
		groupNames = append(groupNames, "managed_clusters")
	}

	for _, groupName := range groupNames {
		c.ClusterGroups[groupName] = sets.List(sets.New(c.ClusterGroups[groupName]...).Insert(s.clusterInstall.ClusterName))
	}

	c.UserSecretsTargetClusters = sets.List(sets.New(c.UserSecretsTargetClusters...).Insert(s.clusterInstall.ClusterName))

	var steps = []func(c *secretbootstrap.Config) error{
		s.updateBuildFarmSecrets,
		s.updateDPTPControllerManagerSecret,
		s.updateRehearseSecret,
		s.updateGithubLdapUserGroupCreatorSecret,
		s.updatePromotedImageGovernor,
		s.updateClusterDisplay,
		s.updateChatBotSecret,
		s.updateSecret(s.generateRegistryPushCredentialsSecret),
		s.updateSecret(s.generateRegistryPullCredentialsSecret),
		s.updateSecret(s.generateCIOperatorSecret),
		s.updateSecret(s.generateMultiarchBuilderControllerSecret),
		s.updateDexIdAndSecret,
		s.updateDexClientSecret,
		s.upsertClusterInitSecret,
		s.upsertManifestToolSecret,
	}
	if !*s.clusterInstall.Onboard.Unmanaged {
		steps = append(steps, s.updatePodScalerSecret)
	}

	for _, step := range steps {
		if err := step(c); err != nil {
			return err
		}
	}

	return nil
}

func (s *ciSecretBootstrapStep) updateSecret(secretGenerator func() *secretbootstrap.SecretConfig) func(c *secretbootstrap.Config) error {
	return func(c *secretbootstrap.Config) error {
		secret := secretGenerator()
		if secret == nil {
			return nil
		}
		idx, _, _ := s.findSecretConfig(secret.To[0].Name, s.clusterInstall.ClusterName, c.Secrets)
		if idx != -1 {
			s.log.Infof("Replacing existing secret with 'to' of: %v", secret.To)
			c.Secrets = append(c.Secrets[:idx], append([]secretbootstrap.SecretConfig{*secret}, c.Secrets[idx+1:]...)...)
		} else {
			s.log.Infof("Creating new secret with 'to' of: %v", secret.To)
			c.Secrets = append(c.Secrets, *secret)
		}
		return nil
	}
}

func (s *ciSecretBootstrapStep) generateCIOperatorSecret() *secretbootstrap.SecretConfig {
	from := map[string]secretbootstrap.ItemContext{
		kubeconfig: {
			Field: ServiceAccountKubeconfigPath(CIOperator, s.clusterInstall.ClusterName),
			Item:  BuildUFarm,
		},
	}
	if *s.clusterInstall.Onboard.UseTokenFileInKubeconfig {
		tokenFile := ServiceAccountTokenFile(CIOperator, s.clusterInstall.ClusterName)
		from[tokenFile] = secretbootstrap.ItemContext{
			Field: tokenFile,
			Item:  BuildUFarm,
		}
	}
	return &secretbootstrap.SecretConfig{
		From: from,
		To: []secretbootstrap.SecretContext{
			{
				Cluster:   s.clusterInstall.ClusterName,
				Name:      CIOperator,
				Namespace: testCredentials,
			},
		},
	}
}

func (s *ciSecretBootstrapStep) generateRegistryPushCredentialsSecret() *secretbootstrap.SecretConfig {
	return &secretbootstrap.SecretConfig{
		From: map[string]secretbootstrap.ItemContext{
			dotDockerConfigJson: s.generatePushPullSecretFrom(s.clusterInstall.ClusterName, []secretbootstrap.DockerConfigJSONData{
				{
					AuthField:   s.registryCommandTokenField(string(api.ClusterAPPCI), push),
					Item:        BuildUFarm,
					RegistryURL: api.ServiceDomainAPPCIRegistry,
				},
				{
					AuthField:   "auth",
					Item:        "quay-io-push-credentials",
					RegistryURL: "quay.io/openshift/ci",
				},
			}),
		},
		To: []secretbootstrap.SecretContext{
			s.generateDockerConfigJsonSecretConfigTo(api.RegistryPushCredentialsCICentralSecret, CI, s.clusterInstall.ClusterName),
			s.generateDockerConfigJsonSecretConfigTo(api.RegistryPushCredentialsCICentralSecret, testCredentials, s.clusterInstall.ClusterName),
		},
	}
}

func (s *ciSecretBootstrapStep) generateRegistryPullCredentialsSecret() *secretbootstrap.SecretConfig {
	return &secretbootstrap.SecretConfig{
		From: map[string]secretbootstrap.ItemContext{
			dotDockerConfigJson: s.generatePushPullSecretFrom(s.clusterInstall.ClusterName, []secretbootstrap.DockerConfigJSONData{
				{
					AuthField:   s.registryCommandTokenField(string(api.ClusterAPPCI), pull),
					Item:        BuildUFarm,
					RegistryURL: api.ServiceDomainAPPCIRegistry,
				},
				{
					AuthField:   "auth",
					EmailField:  "email",
					Item:        "quay.io-pull-secret",
					RegistryURL: "quay.io",
				},
				{
					AuthField:   "auth",
					Item:        "quayio-ci-read-only-robot",
					RegistryURL: "quay-proxy.ci.openshift.org",
				},
				{
					AuthField:   "auth",
					Item:        "quayio-ci-read-only-robot",
					RegistryURL: "quay.io/openshift/ci",
				},
				{
					AuthField:   "auth",
					Item:        "quayio-ci-read-only-robot",
					RegistryURL: "quay.io/openshift/network-edge-testing",
				},
				{
					AuthField:   "auth",
					Item:        "quayio-ci-read-only-robot",
					RegistryURL: "qci-pull-through-cache-us-east-1-ci.apps.ci.l2s4.p1.openshiftapps.com",
				},
			}),
		},
		To: []secretbootstrap.SecretContext{
			s.generateDockerConfigJsonSecretConfigTo(api.RegistryPullCredentialsSecret, CI, s.clusterInstall.ClusterName),
			s.generateDockerConfigJsonSecretConfigTo(api.RegistryPullCredentialsSecret, testCredentials, s.clusterInstall.ClusterName),
		},
	}
}

func (s *ciSecretBootstrapStep) generatePushPullSecretFrom(clusterName string, items []secretbootstrap.DockerConfigJSONData) secretbootstrap.ItemContext {
	itemContext := secretbootstrap.ItemContext{
		DockerConfigJSONData: []secretbootstrap.DockerConfigJSONData{
			{
				AuthField:   s.registryCommandTokenField(clusterName, pull),
				Item:        BuildUFarm,
				RegistryURL: "image-registry.openshift-image-registry.svc.cluster.local:5000",
			},
			{
				AuthField:   s.registryCommandTokenField(clusterName, pull),
				Item:        BuildUFarm,
				RegistryURL: "image-registry.openshift-image-registry.svc:5000",
			},
			{
				AuthField:   s.registryCommandTokenField(clusterName, pull),
				Item:        BuildUFarm,
				RegistryURL: s.registryUrlFor(clusterName),
			},
		},
	}
	itemContext.DockerConfigJSONData =
		append(itemContext.DockerConfigJSONData, items...)
	return itemContext
}

func (s *ciSecretBootstrapStep) registryCommandTokenField(clusterName string, pushPull pushPull) string {
	return fmt.Sprintf("token_image-%s_%s_reg_auth_value.txt", string(pushPull), clusterName)
}

func (s *ciSecretBootstrapStep) generateDockerConfigJsonSecretConfigTo(name string, namespace string, clusterName string) secretbootstrap.SecretContext {
	return secretbootstrap.SecretContext{
		Cluster:   clusterName,
		Name:      name,
		Namespace: namespace,
		Type:      dockerconfigjson,
	}
}

func (s *ciSecretBootstrapStep) updatePodScalerSecret(c *secretbootstrap.Config) error {
	if *s.clusterInstall.Onboard.UseTokenFileInKubeconfig {
		key := ServiceAccountTokenFile(PodScaler, s.clusterInstall.ClusterName)
		if err := s.updateSecretItemContext(c, PodScaler, string(api.ClusterAPPCI),
			key, secretbootstrap.ItemContext{
				Field: key,
				Item:  PodScaler,
			}); err != nil {
			return err
		}
	}
	key := fmt.Sprintf("%s.%s", s.clusterInstall.ClusterName, config)
	return s.updateSecretItemContext(c, PodScaler, string(api.ClusterAPPCI), key, secretbootstrap.ItemContext{
		Field: ServiceAccountKubeconfigPath(PodScaler, s.clusterInstall.ClusterName),
		Item:  PodScaler,
	})
}

func (s *ciSecretBootstrapStep) updateDPTPControllerManagerSecret(c *secretbootstrap.Config) error {
	const DPTPControllerManager = "dptp-controller-manager"
	if *s.clusterInstall.Onboard.UseTokenFileInKubeconfig {
		keyAndFieldToken := ServiceAccountTokenFile(DPTPControllerManager, s.clusterInstall.ClusterName)
		if err := s.updateSecretItemContext(c, DPTPControllerManager, string(api.ClusterAPPCI), keyAndFieldToken, secretbootstrap.ItemContext{
			Field: keyAndFieldToken,
			Item:  BuildUFarm,
		}); err != nil {
			return err
		}
	}
	keyAndField := ServiceAccountKubeconfigPath(DPTPControllerManager, s.clusterInstall.ClusterName)
	return s.updateSecretItemContext(c, DPTPControllerManager, string(api.ClusterAPPCI), keyAndField, secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  BuildUFarm,
	})
}

func (s *ciSecretBootstrapStep) updateRehearseSecret(c *secretbootstrap.Config) error {
	if *s.clusterInstall.Onboard.UseTokenFileInKubeconfig {
		keyAndFieldToken := ServiceAccountTokenFile(CIOperator, s.clusterInstall.ClusterName)
		if err := s.updateSecretItemContext(c, pjRehearse, string(api.ClusterBuild01), keyAndFieldToken, secretbootstrap.ItemContext{
			Field: keyAndFieldToken,
			Item:  BuildUFarm,
		}); err != nil {
			return err
		}
	}
	keyAndField := ServiceAccountKubeconfigPath(CIOperator, s.clusterInstall.ClusterName)
	return s.updateSecretItemContext(c, pjRehearse, string(api.ClusterBuild01), keyAndField, secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  BuildUFarm,
	})
}

func (s *ciSecretBootstrapStep) updateGithubLdapUserGroupCreatorSecret(c *secretbootstrap.Config) error {
	if *s.clusterInstall.Onboard.UseTokenFileInKubeconfig {
		keyAndFieldToken := ServiceAccountTokenFile(GithubLdapUserGroupCreator, s.clusterInstall.ClusterName)
		if err := s.updateSecretItemContext(c, GithubLdapUserGroupCreator, string(api.ClusterAPPCI), keyAndFieldToken, secretbootstrap.ItemContext{
			Field: keyAndFieldToken,
			Item:  BuildUFarm,
		}); err != nil {
			return err
		}
	}
	keyAndField := ServiceAccountKubeconfigPath(GithubLdapUserGroupCreator, s.clusterInstall.ClusterName)
	return s.updateSecretItemContext(c, GithubLdapUserGroupCreator, string(api.ClusterAPPCI), keyAndField, secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  BuildUFarm,
	})
}

func (s *ciSecretBootstrapStep) updatePromotedImageGovernor(c *secretbootstrap.Config) error {
	if *s.clusterInstall.Onboard.UseTokenFileInKubeconfig {
		keyAndFieldToken := ServiceAccountTokenFile(PromotedImageGovernor, s.clusterInstall.ClusterName)
		if err := s.updateSecretItemContext(c, PromotedImageGovernor, string(api.ClusterAPPCI), keyAndFieldToken, secretbootstrap.ItemContext{
			Field: keyAndFieldToken,
			Item:  BuildUFarm,
		}); err != nil {
			return err
		}
	}
	keyAndField := ServiceAccountKubeconfigPath(PromotedImageGovernor, s.clusterInstall.ClusterName)
	return s.updateSecretItemContext(c, PromotedImageGovernor, string(api.ClusterAPPCI), keyAndField, secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  BuildUFarm,
	})
}

func (s *ciSecretBootstrapStep) updateClusterDisplay(c *secretbootstrap.Config) error {
	if *s.clusterInstall.Onboard.UseTokenFileInKubeconfig {
		keyAndFieldToken := ServiceAccountTokenFile(ClusterDisplay, s.clusterInstall.ClusterName)
		if err := s.updateSecretItemContext(c, ClusterDisplay, string(api.ClusterAPPCI), keyAndFieldToken, secretbootstrap.ItemContext{
			Field: keyAndFieldToken,
			Item:  BuildUFarm,
		}); err != nil {
			return err
		}
	}
	keyAndField := ServiceAccountKubeconfigPath(ClusterDisplay, s.clusterInstall.ClusterName)
	return s.updateSecretItemContext(c, ClusterDisplay, string(api.ClusterAPPCI), keyAndField, secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  BuildUFarm,
	})
}

func (s *ciSecretBootstrapStep) updateChatBotSecret(c *secretbootstrap.Config) error {
	const chatBot = "ci-chat-bot"
	name := chatBot + "-kubeconfigs"
	if *s.clusterInstall.Onboard.UseTokenFileInKubeconfig {
		keyAndFieldToken := ServiceAccountTokenFile(chatBot, s.clusterInstall.ClusterName)
		if err := s.updateSecretItemContext(c, name, string(api.ClusterAPPCI), keyAndFieldToken, secretbootstrap.ItemContext{
			Field: keyAndFieldToken,
			Item:  chatBot,
		}); err != nil {
			return err
		}
	}
	keyAndField := ServiceAccountKubeconfigPath(chatBot, s.clusterInstall.ClusterName)
	return s.updateSecretItemContext(c, name, string(api.ClusterAPPCI), keyAndField, secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  chatBot,
	})
}

func (s *ciSecretBootstrapStep) updateDexClientSecret(c *secretbootstrap.Config) error {
	if *s.clusterInstall.Onboard.Hosted || *s.clusterInstall.Onboard.OSD {
		s.log.Info("Cluster is either hosted or osd, skipping dex-rh-sso")
		return nil
	}
	secret := &secretbootstrap.SecretConfig{
		From: map[string]secretbootstrap.ItemContext{
			"clientSecret": {
				Field: s.clusterInstall.ClusterName + "-secret",
				Item:  c.VaultDPTPPrefix + "/dex",
			},
		},
		To: []secretbootstrap.SecretContext{{
			Cluster:   s.clusterInstall.ClusterName,
			Name:      "dex-rh-sso",
			Namespace: "openshift-config",
		}},
	}

	if !s.secretConfigExist(secret, c.Secrets) {
		c.Secrets = append(c.Secrets, *secret)
	}

	return nil
}

func (s *ciSecretBootstrapStep) updateDexIdAndSecret(c *secretbootstrap.Config) error {
	secret := &secretbootstrap.SecretConfig{
		From: map[string]secretbootstrap.ItemContext{
			s.clusterInstall.ClusterName + "-id": {
				Field: s.clusterInstall.ClusterName + "-id",
				Item:  c.VaultDPTPPrefix + "/dex",
			},
			s.clusterInstall.ClusterName + "-secret": {
				Field: s.clusterInstall.ClusterName + "-secret",
				Item:  c.VaultDPTPPrefix + "/dex",
			},
		},
		To: []secretbootstrap.SecretContext{
			{
				Cluster:   string(api.ClusterAPPCI),
				Name:      s.clusterInstall.ClusterName + "-secret",
				Namespace: "dex",
			},
		},
	}

	if !(*s.clusterInstall.Onboard.Hosted || *s.clusterInstall.Onboard.OSD) {
		s.log.Info("Cluster is neither hosted nor osd, syncing dex OIDC secret")
		secret.To = append(secret.To, secretbootstrap.SecretContext{
			Cluster:   string(api.ClusterAPPCI),
			Name:      s.clusterInstall.ClusterName + "-dex-oidc",
			Namespace: "ci",
		})
	}

	if !s.secretConfigExist(secret, c.Secrets) {
		c.Secrets = append(c.Secrets, *secret)
	}

	return nil
}

func (s *ciSecretBootstrapStep) updateSecretItemContext(c *secretbootstrap.Config, name, cluster, key string, value secretbootstrap.ItemContext) error {
	s.log.WithFields(logrus.Fields{
		"name":    name,
		"cluster": cluster,
	}).Info("Appending registry secret item.")
	_, sc, err := s.findSecretConfig(name, cluster, c.Secrets)
	if err != nil {
		return err
	}
	sc.From[key] = value
	return nil
}

func (s *ciSecretBootstrapStep) registryUrlFor(cluster string) string {
	switch cluster {
	case string(api.ClusterVSphere02):
		return "registry.apps.build02.vmc.ci.openshift.org"
	case string(api.ClusterAPPCI):
		return api.ServiceDomainAPPCIRegistry
	case string(api.ClusterARM01):
		return "registry.arm-build01.arm-build.devcluster.openshift.com"
	default:
		return fmt.Sprintf("registry.%s.ci.openshift.org", cluster)
	}
}

func (s *ciSecretBootstrapStep) updateBuildFarmSecrets(c *secretbootstrap.Config) error {
	if s.clusterInstall.ClusterName == string(api.ClusterVSphere02) {
		_, buildFarmCredentials, err := s.findSecretConfig(fmt.Sprintf("%s-%s", BuildFarm, credentials), string(api.ClusterAPPCI), c.Secrets)
		if err != nil {
			return err
		}
		clientId := s.clusterInstall.ClusterName + "_github_client_id"
		buildFarmCredentials.From[clientId] = secretbootstrap.ItemContext{
			Item:  fmt.Sprintf("%s_%s", BuildUFarm, s.clusterInstall.ClusterName),
			Field: "github_client_id",
		}
	}
	for _, sa := range []string{ConfigUpdater, "crier", "deck", "hook", "prow-controller-manager", "sinker"} {
		_, sc, err := s.findSecretConfig(sa, string(api.ClusterAPPCI), c.Secrets)
		if err != nil {
			return err
		}
		keyAndField := ServiceAccountKubeconfigPath(sa, s.clusterInstall.ClusterName)
		item := BuildUFarm
		if sa == ConfigUpdater {
			item = ConfigUpdater
		}
		sc.From[keyAndField] = secretbootstrap.ItemContext{
			Field: keyAndField,
			Item:  item,
		}
		if *s.clusterInstall.Onboard.UseTokenFileInKubeconfig && sa != ConfigUpdater {
			keyAndFieldToken := ServiceAccountTokenFile(sa, s.clusterInstall.ClusterName)
			sc.From[keyAndFieldToken] = secretbootstrap.ItemContext{
				Field: keyAndFieldToken,
				Item:  BuildUFarm,
			}
		}
	}

	return nil
}

func (s *ciSecretBootstrapStep) generateMultiarchBuilderControllerSecret() *secretbootstrap.SecretConfig {
	if !*s.clusterInstall.Onboard.Multiarch {
		s.log.Info("Cluster is not multiarch, skipping multiarch secret")
		return nil
	}

	clusterName := s.clusterInstall.ClusterName
	secretName := fmt.Sprintf("multi-arch-builder-controller-%s-registry-credentials", clusterName)
	return &secretbootstrap.SecretConfig{
		From: map[string]secretbootstrap.ItemContext{
			dotDockerConfigJson: {
				DockerConfigJSONData: []secretbootstrap.DockerConfigJSONData{
					{
						AuthField:   s.registryCommandTokenField(clusterName, push),
						Item:        BuildUFarm,
						RegistryURL: "image-registry.openshift-image-registry.svc:5000",
					},
					{
						AuthField:   fmt.Sprintf("token_multi-arch-builder-controller_%s_reg_auth_value.txt", clusterName),
						Item:        BuildUFarm,
						RegistryURL: api.ServiceDomainMulti01Registry,
					},
					{
						AuthField:   s.registryCommandTokenField(string(api.ClusterAPPCI), push),
						Item:        BuildUFarm,
						RegistryURL: api.ServiceDomainAPPCIRegistry,
					},
				},
			},
		},
		To: []secretbootstrap.SecretContext{
			s.generateDockerConfigJsonSecretConfigTo(secretName, CI, clusterName),
		},
	}
}

func (s *ciSecretBootstrapStep) upsertClusterInitSecret(c *secretbootstrap.Config) error {
	secret, i := secretbootstrap.FindSecret(c.Secrets, secretbootstrap.ByDestinationFunc(func(sc *secretbootstrap.SecretContext) bool {
		return reflect.DeepEqual(sc.ClusterGroups, []string{BuildUFarm}) && sc.Namespace == "ci" && sc.Name == "cluster-init"
	}))

	if i == -1 {
		secret = &secretbootstrap.SecretConfig{
			From: map[string]secretbootstrap.ItemContext{},
			To: []secretbootstrap.SecretContext{
				{ClusterGroups: []string{BuildUFarm}, Namespace: "ci", Name: "cluster-init"},
			},
		}
	}

	for _, configContextName := range []string{
		fmt.Sprintf("sa.cluster-init.%s.config", s.clusterInstall.ClusterName),
		fmt.Sprintf("sa.cluster-init.%s.token.txt", s.clusterInstall.ClusterName),
	} {
		if _, ok := secret.From[configContextName]; !ok {
			secret.From[configContextName] = secretbootstrap.ItemContext{Field: configContextName, Item: BuildUFarm}
		}
	}

	if i == -1 {
		c.Secrets = append(c.Secrets, *secret)
	} else {
		c.Secrets[i] = *secret
	}

	return nil
}

func (s *ciSecretBootstrapStep) upsertManifestToolSecret(c *secretbootstrap.Config) error {
	clusterName := s.clusterInstall.ClusterName
	secret, i := secretbootstrap.FindSecret(c.Secrets,
		secretbootstrap.ByDestination(&secretbootstrap.SecretContext{
			Cluster:   clusterName,
			Namespace: "ci",
			Name:      "manifest-tool-local-pusher",
			Type:      dockerconfigjson}),
		secretbootstrap.ByDestination(&secretbootstrap.SecretContext{
			Cluster:   clusterName,
			Namespace: "test-credentials",
			Name:      "manifest-tool-local-pusher",
			Type:      dockerconfigjson}))

	if i == -1 {
		secret = &secretbootstrap.SecretConfig{}
	}

	registryUrl, err := api.RegistryDomainForClusterName(clusterName)
	if err != nil {
		return fmt.Errorf("registry domain for cluster %s: %w", clusterName, err)
	}

	secret.From = map[string]secretbootstrap.ItemContext{
		dotDockerConfigJson: {
			DockerConfigJSONData: []secretbootstrap.DockerConfigJSONData{
				{
					AuthField:   s.registryCommandTokenField(clusterName, push),
					Item:        BuildUFarm,
					RegistryURL: "image-registry.openshift-image-registry.svc:5000",
				},
				{
					AuthField:   s.registryCommandTokenField(clusterName, push),
					Item:        BuildUFarm,
					RegistryURL: registryUrl,
				},
			},
		},
	}

	secret.To = []secretbootstrap.SecretContext{
		s.generateDockerConfigJsonSecretConfigTo("manifest-tool-local-pusher", "ci", clusterName),
		s.generateDockerConfigJsonSecretConfigTo("manifest-tool-local-pusher", "test-credentials", clusterName),
	}

	if i == -1 {
		c.Secrets = append(c.Secrets, *secret)
	} else {
		c.Secrets[i] = *secret
	}

	return nil
}

func (s *ciSecretBootstrapStep) findSecretConfig(name string, cluster string, sc []secretbootstrap.SecretConfig) (int, *secretbootstrap.SecretConfig, error) {
	idx := func() int {
		for i, config := range sc {
			for _, to := range config.To {
				if to.Name == name && to.Cluster == cluster {
					return i
				}
			}
		}
		return -1
	}()
	if idx != -1 {
		return idx, &sc[idx], nil
	}
	return -1, nil, fmt.Errorf("couldn't find SecretConfig with name: %s and cluster: %s", name, cluster)
}

func (s *ciSecretBootstrapStep) secretConfigExist(target *secretbootstrap.SecretConfig, secrets []secretbootstrap.SecretConfig) bool {
	for _, candidate := range secrets {
		if reflect.DeepEqual(target, &candidate) {
			return true
		}
	}
	return false
}

func NewCISecretBootstrapStep(log *logrus.Entry, clusterInstall *clusterinstall.ClusterInstall) *ciSecretBootstrapStep {
	return &ciSecretBootstrapStep{
		log:            log,
		clusterInstall: clusterInstall,
	}
}
