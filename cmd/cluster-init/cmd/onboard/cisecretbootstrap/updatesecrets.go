package cisecretbootstrap

import (
	"fmt"
	"path/filepath"
	"reflect"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/api/secretbootstrap"
	"github.com/openshift/ci-tools/pkg/clustermgmt/onboard"
)

const (
	credentials                  = "credentials"
	dotDockerConfigJson          = ".dockerconfigjson"
	testCredentials              = "test-credentials"
	kubeconfig                   = "kubeconfig"
	Config                       = "config"
	pjRehearse                   = "pj-rehearse"
	pull                pushPull = "puller"
	push                pushPull = "pusher"
)

type Options struct {
	ClusterName              string
	ReleaseRepo              string
	UseTokenFileInKubeconfig bool
	Unmanaged                bool
	Hosted                   bool
	osd                      bool
}

type pushPull string

func UpdateCiSecretBootstrap(log *logrus.Entry, o Options, osdClusters []string) error {
	log = log.WithField("step", "ci-secret-bootstrap")
	secretBootstrapDir := filepath.Join(o.ReleaseRepo, "core-services", "ci-secret-bootstrap")
	secretBootstrapConfigFile := filepath.Join(secretBootstrapDir, "_config.yaml")
	log.Infof("Updating ci-secret-bootstrap: %s", secretBootstrapConfigFile)

	var c secretbootstrap.Config
	if err := secretbootstrap.LoadConfigFromFile(secretBootstrapConfigFile, &c); err != nil {
		return err
	}
	o.osd = sets.New(osdClusters...).Has(o.ClusterName)
	if err := updateCiSecretBootstrapConfig(log, o, &c); err != nil {
		return err
	}
	return secretbootstrap.SaveConfigToFile(secretBootstrapConfigFile, &c)
}

func updateCiSecretBootstrapConfig(log *logrus.Entry, o Options, c *secretbootstrap.Config) error {
	groupNames := []string{onboard.BuildUFarm, "non_app_ci"}

	// non-OSD clusters should never be in the group
	if o.osd && !o.Unmanaged {
		groupNames = append(groupNames, secretbootstrap.OSDGlobalPullSecretGroupName)
	}
	if !o.osd {
		groupNames = append(groupNames, secretbootstrap.OpenShiftConfigPullSecretGroupName)
	}
	if !o.Unmanaged {
		groupNames = append(groupNames, "managed_clusters")
	}

	for _, groupName := range groupNames {
		c.ClusterGroups[groupName] = sets.List(sets.New(c.ClusterGroups[groupName]...).Insert(o.ClusterName))
	}

	c.UserSecretsTargetClusters = sets.List(sets.New(c.UserSecretsTargetClusters...).Insert(o.ClusterName))

	var steps = []func(log *logrus.Entry, c *secretbootstrap.Config, o Options) error{
		updateBuildFarmSecrets,
		updateDPTPControllerManagerSecret,
		updateRehearseSecret,
		updateGithubLdapUserGroupCreatorSecret,
		updatePromotedImageGovernor,
		updateClusterDisplay,
		updateChatBotSecret,
		updateSecret(generateRegistryPushCredentialsSecret),
		updateSecret(generateRegistryPullCredentialsSecret),
		updateSecret(generateCiOperatorSecret),
		updateDexIdAndSecret,
		updateDexClientSecret,
	}
	if !o.Unmanaged {
		steps = append(steps, updatePodScalerSecret)
	}

	for _, step := range steps {
		if err := step(log, c, o); err != nil {
			return err
		}
	}

	return nil
}

func updateSecret(secretGenerator func(*logrus.Entry, Options) *secretbootstrap.SecretConfig) func(log *logrus.Entry, c *secretbootstrap.Config, o Options) error {
	return func(log *logrus.Entry, c *secretbootstrap.Config, o Options) error {
		secret := secretGenerator(log, o)
		if secret == nil {
			return nil
		}
		idx, _, _ := findSecretConfig(secret.To[0].Name, o.ClusterName, c.Secrets)
		if idx != -1 {
			log.Infof("Replacing existing secret with 'to' of: %v", secret.To)
			c.Secrets = append(c.Secrets[:idx], append([]secretbootstrap.SecretConfig{*secret}, c.Secrets[idx+1:]...)...)
		} else {
			log.Infof("Creating new secret with 'to' of: %v", secret.To)
			c.Secrets = append(c.Secrets, *secret)
		}
		return nil
	}
}

func generateCiOperatorSecret(_ *logrus.Entry, o Options) *secretbootstrap.SecretConfig {
	from := map[string]secretbootstrap.ItemContext{
		kubeconfig: {
			Field: onboard.ServiceAccountKubeconfigPath(onboard.CIOperator, o.ClusterName),
			Item:  onboard.BuildUFarm,
		},
	}
	if o.UseTokenFileInKubeconfig {
		tokenFile := onboard.ServiceAccountTokenFile(onboard.CIOperator, o.ClusterName)
		from[tokenFile] = secretbootstrap.ItemContext{
			Field: tokenFile,
			Item:  onboard.BuildUFarm,
		}
	}
	return &secretbootstrap.SecretConfig{
		From: from,
		To: []secretbootstrap.SecretContext{
			{
				Cluster:   o.ClusterName,
				Name:      onboard.CIOperator,
				Namespace: testCredentials,
			},
		},
	}
}

func generateRegistryPushCredentialsSecret(_ *logrus.Entry, o Options) *secretbootstrap.SecretConfig {
	return &secretbootstrap.SecretConfig{
		From: map[string]secretbootstrap.ItemContext{
			dotDockerConfigJson: generatePushPullSecretFrom(o.ClusterName, []secretbootstrap.DockerConfigJSONData{
				{
					AuthField:   registryCommandTokenField(string(api.ClusterAPPCI), push),
					Item:        onboard.BuildUFarm,
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
			generateDockerConfigJsonSecretConfigTo(api.RegistryPushCredentialsCICentralSecret, onboard.CI, o.ClusterName),
			generateDockerConfigJsonSecretConfigTo(api.RegistryPushCredentialsCICentralSecret, testCredentials, o.ClusterName),
		},
	}
}

func generateRegistryPullCredentialsSecret(_ *logrus.Entry, o Options) *secretbootstrap.SecretConfig {
	return &secretbootstrap.SecretConfig{
		From: map[string]secretbootstrap.ItemContext{
			dotDockerConfigJson: generatePushPullSecretFrom(o.ClusterName, []secretbootstrap.DockerConfigJSONData{
				{
					AuthField:   registryCommandTokenField(string(api.ClusterAPPCI), pull),
					Item:        onboard.BuildUFarm,
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
			generateDockerConfigJsonSecretConfigTo(api.RegistryPullCredentialsSecret, onboard.CI, o.ClusterName),
			generateDockerConfigJsonSecretConfigTo(api.RegistryPullCredentialsSecret, testCredentials, o.ClusterName),
		},
	}
}

func generatePushPullSecretFrom(clusterName string, items []secretbootstrap.DockerConfigJSONData) secretbootstrap.ItemContext {
	itemContext := secretbootstrap.ItemContext{
		DockerConfigJSONData: []secretbootstrap.DockerConfigJSONData{
			{
				AuthField:   registryCommandTokenField(clusterName, pull),
				Item:        onboard.BuildUFarm,
				RegistryURL: "image-registry.openshift-image-registry.svc.cluster.local:5000",
			},
			{
				AuthField:   registryCommandTokenField(clusterName, pull),
				Item:        onboard.BuildUFarm,
				RegistryURL: "image-registry.openshift-image-registry.svc:5000",
			},
			{
				AuthField:   registryCommandTokenField(clusterName, pull),
				Item:        onboard.BuildUFarm,
				RegistryURL: registryUrlFor(clusterName),
			},
		},
	}
	itemContext.DockerConfigJSONData =
		append(itemContext.DockerConfigJSONData, items...)
	return itemContext
}

func registryCommandTokenField(clusterName string, pushPull pushPull) string {
	return fmt.Sprintf("token_image-%s_%s_reg_auth_value.txt", string(pushPull), clusterName)
}

func generateDockerConfigJsonSecretConfigTo(name string, namespace string, clusterName string) secretbootstrap.SecretContext {
	return secretbootstrap.SecretContext{
		Cluster:   clusterName,
		Name:      name,
		Namespace: namespace,
		Type:      "kubernetes.io/dockerconfigjson",
	}
}

func updatePodScalerSecret(log *logrus.Entry, c *secretbootstrap.Config, o Options) error {
	if o.UseTokenFileInKubeconfig {
		key := onboard.ServiceAccountTokenFile(onboard.PodScaler, o.ClusterName)
		if err := updateSecretItemContext(log, c, onboard.PodScaler, string(api.ClusterAPPCI),
			key, secretbootstrap.ItemContext{
				Field: key,
				Item:  onboard.PodScaler,
			}); err != nil {
			return err
		}
	}
	key := fmt.Sprintf("%s.%s", o.ClusterName, Config)
	return updateSecretItemContext(log, c, onboard.PodScaler, string(api.ClusterAPPCI), key, secretbootstrap.ItemContext{
		Field: onboard.ServiceAccountKubeconfigPath(onboard.PodScaler, o.ClusterName),
		Item:  onboard.PodScaler,
	})
}

func updateDPTPControllerManagerSecret(log *logrus.Entry, c *secretbootstrap.Config, o Options) error {
	const DPTPControllerManager = "dptp-controller-manager"
	if o.UseTokenFileInKubeconfig {
		keyAndFieldToken := onboard.ServiceAccountTokenFile(DPTPControllerManager, o.ClusterName)
		if err := updateSecretItemContext(log, c, DPTPControllerManager, string(api.ClusterAPPCI), keyAndFieldToken, secretbootstrap.ItemContext{
			Field: keyAndFieldToken,
			Item:  onboard.BuildUFarm,
		}); err != nil {
			return err
		}
	}
	keyAndField := onboard.ServiceAccountKubeconfigPath(DPTPControllerManager, o.ClusterName)
	return updateSecretItemContext(log, c, DPTPControllerManager, string(api.ClusterAPPCI), keyAndField, secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  onboard.BuildUFarm,
	})
}

func updateRehearseSecret(log *logrus.Entry, c *secretbootstrap.Config, o Options) error {
	if o.UseTokenFileInKubeconfig {
		keyAndFieldToken := onboard.ServiceAccountTokenFile(onboard.CIOperator, o.ClusterName)
		if err := updateSecretItemContext(log, c, pjRehearse, string(api.ClusterBuild01), keyAndFieldToken, secretbootstrap.ItemContext{
			Field: keyAndFieldToken,
			Item:  onboard.BuildUFarm,
		}); err != nil {
			return err
		}
	}
	keyAndField := onboard.ServiceAccountKubeconfigPath(onboard.CIOperator, o.ClusterName)
	return updateSecretItemContext(log, c, pjRehearse, string(api.ClusterBuild01), keyAndField, secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  onboard.BuildUFarm,
	})
}

func updateGithubLdapUserGroupCreatorSecret(log *logrus.Entry, c *secretbootstrap.Config, o Options) error {
	if o.UseTokenFileInKubeconfig {
		keyAndFieldToken := onboard.ServiceAccountTokenFile(onboard.GithubLdapUserGroupCreator, o.ClusterName)
		if err := updateSecretItemContext(log, c, onboard.GithubLdapUserGroupCreator, string(api.ClusterAPPCI), keyAndFieldToken, secretbootstrap.ItemContext{
			Field: keyAndFieldToken,
			Item:  onboard.BuildUFarm,
		}); err != nil {
			return err
		}
	}
	keyAndField := onboard.ServiceAccountKubeconfigPath(onboard.GithubLdapUserGroupCreator, o.ClusterName)
	return updateSecretItemContext(log, c, onboard.GithubLdapUserGroupCreator, string(api.ClusterAPPCI), keyAndField, secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  onboard.BuildUFarm,
	})
}

func updatePromotedImageGovernor(log *logrus.Entry, c *secretbootstrap.Config, o Options) error {
	if o.UseTokenFileInKubeconfig {
		keyAndFieldToken := onboard.ServiceAccountTokenFile(onboard.PromotedImageGovernor, o.ClusterName)
		if err := updateSecretItemContext(log, c, onboard.PromotedImageGovernor, string(api.ClusterAPPCI), keyAndFieldToken, secretbootstrap.ItemContext{
			Field: keyAndFieldToken,
			Item:  onboard.BuildUFarm,
		}); err != nil {
			return err
		}
	}
	keyAndField := onboard.ServiceAccountKubeconfigPath(onboard.PromotedImageGovernor, o.ClusterName)
	return updateSecretItemContext(log, c, onboard.PromotedImageGovernor, string(api.ClusterAPPCI), keyAndField, secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  onboard.BuildUFarm,
	})
}

func updateClusterDisplay(log *logrus.Entry, c *secretbootstrap.Config, o Options) error {
	if o.UseTokenFileInKubeconfig {
		keyAndFieldToken := onboard.ServiceAccountTokenFile(onboard.ClusterDisplay, o.ClusterName)
		if err := updateSecretItemContext(log, c, onboard.ClusterDisplay, string(api.ClusterAPPCI), keyAndFieldToken, secretbootstrap.ItemContext{
			Field: keyAndFieldToken,
			Item:  onboard.BuildUFarm,
		}); err != nil {
			return err
		}
	}
	keyAndField := onboard.ServiceAccountKubeconfigPath(onboard.ClusterDisplay, o.ClusterName)
	return updateSecretItemContext(log, c, onboard.ClusterDisplay, string(api.ClusterAPPCI), keyAndField, secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  onboard.BuildUFarm,
	})
}

func updateChatBotSecret(log *logrus.Entry, c *secretbootstrap.Config, o Options) error {
	const chatBot = "ci-chat-bot"
	name := chatBot + "-kubeconfigs"
	if o.UseTokenFileInKubeconfig {
		keyAndFieldToken := onboard.ServiceAccountTokenFile(chatBot, o.ClusterName)
		if err := updateSecretItemContext(log, c, name, string(api.ClusterAPPCI), keyAndFieldToken, secretbootstrap.ItemContext{
			Field: keyAndFieldToken,
			Item:  chatBot,
		}); err != nil {
			return err
		}
	}
	keyAndField := onboard.ServiceAccountKubeconfigPath(chatBot, o.ClusterName)
	return updateSecretItemContext(log, c, name, string(api.ClusterAPPCI), keyAndField, secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  chatBot,
	})
}

func updateDexClientSecret(log *logrus.Entry, c *secretbootstrap.Config, o Options) error {
	if o.Hosted || o.osd {
		log.Info("Cluster is either hosted or osd, skipping dex-rh-sso")
		return nil
	}
	secret := &secretbootstrap.SecretConfig{
		From: map[string]secretbootstrap.ItemContext{
			"client-secret": {
				Field: o.ClusterName + "-secret",
				Item:  c.VaultDPTPPrefix + "/dex",
			},
		},
		To: []secretbootstrap.SecretContext{{
			Cluster:   o.ClusterName,
			Name:      "dex-rh-sso",
			Namespace: "openshift-config",
		}},
	}

	if !secretConfigExist(secret, c.Secrets) {
		c.Secrets = append(c.Secrets, *secret)
	}

	return nil
}

func updateDexIdAndSecret(log *logrus.Entry, c *secretbootstrap.Config, o Options) error {
	secret := &secretbootstrap.SecretConfig{
		From: map[string]secretbootstrap.ItemContext{
			o.ClusterName + "-id": {
				Field: o.ClusterName + "-id",
				Item:  c.VaultDPTPPrefix + "/dex",
			},
			o.ClusterName + "-secret": {
				Field: o.ClusterName + "-secret",
				Item:  c.VaultDPTPPrefix + "/dex",
			},
		},
		To: []secretbootstrap.SecretContext{
			{
				Cluster:   string(api.ClusterAPPCI),
				Name:      o.ClusterName + "-secret",
				Namespace: "dex",
			},
		},
	}

	if !(o.Hosted || o.osd) {
		log.Info("Cluster is neither hosted nor osd, syncing dex OIDC secret")
		secret.To = append(secret.To, secretbootstrap.SecretContext{
			Cluster:   string(api.ClusterAPPCI),
			Name:      o.ClusterName + "-dex-oidc",
			Namespace: "ci",
		})
	}

	if !secretConfigExist(secret, c.Secrets) {
		c.Secrets = append(c.Secrets, *secret)
	}

	return nil
}

func updateSecretItemContext(log *logrus.Entry, c *secretbootstrap.Config, name, cluster, key string, value secretbootstrap.ItemContext) error {
	log.WithFields(logrus.Fields{
		"name":    name,
		"cluster": cluster,
	}).Info("Appending registry secret item.")
	_, sc, err := findSecretConfig(name, cluster, c.Secrets)
	if err != nil {
		return err
	}
	sc.From[key] = value
	return nil
}

func registryUrlFor(cluster string) string {
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

func updateBuildFarmSecrets(_ *logrus.Entry, c *secretbootstrap.Config, o Options) error {
	if o.ClusterName == string(api.ClusterVSphere02) {
		_, buildFarmCredentials, err := findSecretConfig(fmt.Sprintf("%s-%s", onboard.BuildFarm, credentials), string(api.ClusterAPPCI), c.Secrets)
		if err != nil {
			return err
		}
		clientId := o.ClusterName + "_github_client_id"
		buildFarmCredentials.From[clientId] = secretbootstrap.ItemContext{
			Item:  fmt.Sprintf("%s_%s", onboard.BuildUFarm, o.ClusterName),
			Field: "github_client_id",
		}
	}
	for _, s := range []string{onboard.ConfigUpdater, "crier", "deck", "hook", "prow-controller-manager", "sinker"} {
		// for _, s := range []string{configUpdater} {
		_, sc, err := findSecretConfig(s, string(api.ClusterAPPCI), c.Secrets)
		if err != nil {
			return err
		}
		keyAndField := onboard.ServiceAccountKubeconfigPath(s, o.ClusterName)
		item := onboard.BuildUFarm
		if s == onboard.ConfigUpdater {
			item = onboard.ConfigUpdater
		}
		sc.From[keyAndField] = secretbootstrap.ItemContext{
			Field: keyAndField,
			Item:  item,
		}
		if o.UseTokenFileInKubeconfig && s != onboard.ConfigUpdater {
			keyAndFieldToken := onboard.ServiceAccountTokenFile(s, o.ClusterName)
			sc.From[keyAndFieldToken] = secretbootstrap.ItemContext{
				Field: keyAndFieldToken,
				Item:  onboard.BuildUFarm,
			}
		}
	}

	return nil
}

func findSecretConfig(name string, cluster string, sc []secretbootstrap.SecretConfig) (int, *secretbootstrap.SecretConfig, error) {
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

func secretConfigExist(target *secretbootstrap.SecretConfig, secrets []secretbootstrap.SecretConfig) bool {
	for _, candidate := range secrets {
		if reflect.DeepEqual(target, &candidate) {
			return true
		}
	}
	return false
}
