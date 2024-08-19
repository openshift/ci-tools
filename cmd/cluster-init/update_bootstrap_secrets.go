package main

import (
	"fmt"
	"path/filepath"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/api/secretbootstrap"
)

const (
	credentials         = "credentials"
	dotDockerConfigJson = ".dockerconfigjson"
	testCredentials     = "test-credentials"
	kubeconfig          = "kubeconfig"
	config              = "config"
	pjRehearse          = "pj-rehearse"
)

type pushPull string

const (
	pull pushPull = "puller"
	push pushPull = "pusher"
)

func updateCiSecretBootstrap(o options, osdClusters []string) error {
	secretBootstrapDir := filepath.Join(o.releaseRepo, "core-services", "ci-secret-bootstrap")
	secretBootstrapConfigFile := filepath.Join(secretBootstrapDir, "_config.yaml")
	logrus.Infof("Updating ci-secret-bootstrap: %s", secretBootstrapConfigFile)

	var c secretbootstrap.Config
	if err := secretbootstrap.LoadConfigFromFile(secretBootstrapConfigFile, &c); err != nil {
		return err
	}
	osdClustersSet := sets.New[string](osdClusters...)
	if err := updateCiSecretBootstrapConfig(o, &c, osdClustersSet.Has(o.clusterName)); err != nil {
		return err
	}
	return secretbootstrap.SaveConfigToFile(secretBootstrapConfigFile, &c)
}

func updateCiSecretBootstrapConfig(o options, c *secretbootstrap.Config, osd bool) error {
	for _, groupName := range []string{buildUFarm, "non_app_ci"} {
		c.ClusterGroups[groupName] = sets.List(sets.New[string](c.ClusterGroups[groupName]...).Insert(o.clusterName))
	}
	// non-OSD clusters should never be in the group
	var groupName string = ""
	if osd && !o.unmanaged {
		groupName = secretbootstrap.OSDGlobalPullSecretGroupName
	}
	if !osd {
		groupName = secretbootstrap.OpenShiftConfigPullSecretGroupName
	}
	if groupName != "" {
		c.ClusterGroups[groupName] = sets.List(sets.New[string](append(c.ClusterGroups[groupName], o.clusterName)...))
	}
	c.UserSecretsTargetClusters = sets.List(sets.New[string](c.UserSecretsTargetClusters...).Insert(o.clusterName))

	var steps = []func(c *secretbootstrap.Config, o options) error{
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
	}
	if !o.unmanaged {
		steps = append(steps, updatePodScalerSecret)
	}

	for _, step := range steps {
		if err := step(c, o); err != nil {
			return err
		}
	}

	return nil
}

func updateSecret(secretGenerator func(options) secretbootstrap.SecretConfig) func(c *secretbootstrap.Config, o options) error {
	return func(c *secretbootstrap.Config, o options) error {
		secret := secretGenerator(o)
		idx, _, _ := findSecretConfig(secret.To[0].Name, o.clusterName, c.Secrets)
		if idx != -1 {
			logrus.Infof("Replacing existing secret with 'to' of: %v", secret.To)
			c.Secrets = append(c.Secrets[:idx], append([]secretbootstrap.SecretConfig{secret}, c.Secrets[idx+1:]...)...)
		} else {
			logrus.Infof("Creating new secret with 'to' of: %v", secret.To)
			c.Secrets = append(c.Secrets, secret)
		}
		return nil
	}
}

func generateCiOperatorSecret(o options) secretbootstrap.SecretConfig {
	from := map[string]secretbootstrap.ItemContext{
		kubeconfig: {
			Field: serviceAccountKubeconfigPath(ciOperator, o.clusterName),
			Item:  buildUFarm,
		},
	}
	if o.useTokenFileInKubeconfig {
		tokenFile := serviceAccountTokenFile(ciOperator, o.clusterName)
		from[tokenFile] = secretbootstrap.ItemContext{
			Field: tokenFile,
			Item:  buildUFarm,
		}
	}
	return secretbootstrap.SecretConfig{
		From: from,
		To: []secretbootstrap.SecretContext{
			{
				Cluster:   o.clusterName,
				Name:      ciOperator,
				Namespace: testCredentials,
			},
		},
	}
}

func generateRegistryPushCredentialsSecret(o options) secretbootstrap.SecretConfig {
	return secretbootstrap.SecretConfig{
		From: map[string]secretbootstrap.ItemContext{
			dotDockerConfigJson: generatePushPullSecretFrom(o.clusterName, []secretbootstrap.DockerConfigJSONData{
				{
					AuthField:   registryCommandTokenField(string(api.ClusterAPPCI), push),
					Item:        buildUFarm,
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
			generateDockerConfigJsonSecretConfigTo(api.RegistryPushCredentialsCICentralSecret, ci, o.clusterName),
			generateDockerConfigJsonSecretConfigTo(api.RegistryPushCredentialsCICentralSecret, testCredentials, o.clusterName),
		},
	}
}

func generateRegistryPullCredentialsSecret(o options) secretbootstrap.SecretConfig {
	return secretbootstrap.SecretConfig{
		From: map[string]secretbootstrap.ItemContext{
			dotDockerConfigJson: generatePushPullSecretFrom(o.clusterName, []secretbootstrap.DockerConfigJSONData{
				{
					AuthField:   registryCommandTokenField(string(api.ClusterAPPCI), pull),
					Item:        buildUFarm,
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
					RegistryURL: "quay.io/openshift",
				},
				{
					AuthField:   "auth",
					Item:        "quayio-ci-read-only-robot",
					RegistryURL: "qci-pull-through-cache-us-east-1-ci.apps.ci.l2s4.p1.openshiftapps.com",
				},
			}),
		},
		To: []secretbootstrap.SecretContext{
			generateDockerConfigJsonSecretConfigTo(api.RegistryPullCredentialsSecret, ci, o.clusterName),
			generateDockerConfigJsonSecretConfigTo(api.RegistryPullCredentialsSecret, testCredentials, o.clusterName),
		},
	}
}

func generatePushPullSecretFrom(clusterName string, items []secretbootstrap.DockerConfigJSONData) secretbootstrap.ItemContext {
	itemContext := secretbootstrap.ItemContext{
		DockerConfigJSONData: []secretbootstrap.DockerConfigJSONData{
			{
				AuthField:   registryCommandTokenField(clusterName, pull),
				Item:        buildUFarm,
				RegistryURL: "image-registry.openshift-image-registry.svc.cluster.local:5000",
			},
			{
				AuthField:   registryCommandTokenField(clusterName, pull),
				Item:        buildUFarm,
				RegistryURL: "image-registry.openshift-image-registry.svc:5000",
			},
			{
				AuthField:   registryCommandTokenField(clusterName, pull),
				Item:        buildUFarm,
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

func updatePodScalerSecret(c *secretbootstrap.Config, o options) error {
	if o.useTokenFileInKubeconfig {
		key := serviceAccountTokenFile(podScaler, o.clusterName)
		if err := updateSecretItemContext(c, podScaler, string(api.ClusterAPPCI),
			key, secretbootstrap.ItemContext{
				Field: key,
				Item:  podScaler,
			}); err != nil {
			return err
		}
	}
	key := fmt.Sprintf("%s.%s", o.clusterName, config)
	return updateSecretItemContext(c, podScaler, string(api.ClusterAPPCI), key, secretbootstrap.ItemContext{
		Field: serviceAccountKubeconfigPath(podScaler, o.clusterName),
		Item:  podScaler,
	})
}

func updateDPTPControllerManagerSecret(c *secretbootstrap.Config, o options) error {
	const DPTPControllerManager = "dptp-controller-manager"
	if o.useTokenFileInKubeconfig {
		keyAndFieldToken := serviceAccountTokenFile(DPTPControllerManager, o.clusterName)
		if err := updateSecretItemContext(c, DPTPControllerManager, string(api.ClusterAPPCI), keyAndFieldToken, secretbootstrap.ItemContext{
			Field: keyAndFieldToken,
			Item:  buildUFarm,
		}); err != nil {
			return err
		}
	}
	keyAndField := serviceAccountKubeconfigPath(DPTPControllerManager, o.clusterName)
	return updateSecretItemContext(c, DPTPControllerManager, string(api.ClusterAPPCI), keyAndField, secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  buildUFarm,
	})
}

func updateRehearseSecret(c *secretbootstrap.Config, o options) error {
	if o.useTokenFileInKubeconfig {
		keyAndFieldToken := serviceAccountTokenFile(ciOperator, o.clusterName)
		if err := updateSecretItemContext(c, pjRehearse, string(api.ClusterBuild01), keyAndFieldToken, secretbootstrap.ItemContext{
			Field: keyAndFieldToken,
			Item:  buildUFarm,
		}); err != nil {
			return err
		}
	}
	keyAndField := serviceAccountKubeconfigPath(ciOperator, o.clusterName)
	return updateSecretItemContext(c, pjRehearse, string(api.ClusterBuild01), keyAndField, secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  buildUFarm,
	})
}

func updateGithubLdapUserGroupCreatorSecret(c *secretbootstrap.Config, o options) error {
	if o.useTokenFileInKubeconfig {
		keyAndFieldToken := serviceAccountTokenFile(githubLdapUserGroupCreator, o.clusterName)
		if err := updateSecretItemContext(c, githubLdapUserGroupCreator, string(api.ClusterAPPCI), keyAndFieldToken, secretbootstrap.ItemContext{
			Field: keyAndFieldToken,
			Item:  buildUFarm,
		}); err != nil {
			return err
		}
	}
	keyAndField := serviceAccountKubeconfigPath(githubLdapUserGroupCreator, o.clusterName)
	return updateSecretItemContext(c, githubLdapUserGroupCreator, string(api.ClusterAPPCI), keyAndField, secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  buildUFarm,
	})
}

func updatePromotedImageGovernor(c *secretbootstrap.Config, o options) error {
	if o.useTokenFileInKubeconfig {
		keyAndFieldToken := serviceAccountTokenFile(promotedImageGovernor, o.clusterName)
		if err := updateSecretItemContext(c, promotedImageGovernor, string(api.ClusterAPPCI), keyAndFieldToken, secretbootstrap.ItemContext{
			Field: keyAndFieldToken,
			Item:  buildUFarm,
		}); err != nil {
			return err
		}
	}
	keyAndField := serviceAccountKubeconfigPath(promotedImageGovernor, o.clusterName)
	return updateSecretItemContext(c, promotedImageGovernor, string(api.ClusterAPPCI), keyAndField, secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  buildUFarm,
	})
}

func updateClusterDisplay(c *secretbootstrap.Config, o options) error {
	if o.useTokenFileInKubeconfig {
		keyAndFieldToken := serviceAccountTokenFile(clusterDisplay, o.clusterName)
		if err := updateSecretItemContext(c, clusterDisplay, string(api.ClusterAPPCI), keyAndFieldToken, secretbootstrap.ItemContext{
			Field: keyAndFieldToken,
			Item:  buildUFarm,
		}); err != nil {
			return err
		}
	}
	keyAndField := serviceAccountKubeconfigPath(clusterDisplay, o.clusterName)
	return updateSecretItemContext(c, clusterDisplay, string(api.ClusterAPPCI), keyAndField, secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  buildUFarm,
	})
}

func updateChatBotSecret(c *secretbootstrap.Config, o options) error {
	const chatBot = "ci-chat-bot"
	name := chatBot + "-kubeconfigs"
	if o.useTokenFileInKubeconfig {
		keyAndFieldToken := serviceAccountTokenFile(chatBot, o.clusterName)
		if err := updateSecretItemContext(c, name, string(api.ClusterAPPCI), keyAndFieldToken, secretbootstrap.ItemContext{
			Field: keyAndFieldToken,
			Item:  chatBot,
		}); err != nil {
			return err
		}
	}
	keyAndField := serviceAccountKubeconfigPath(chatBot, o.clusterName)
	return updateSecretItemContext(c, name, string(api.ClusterAPPCI), keyAndField, secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  chatBot,
	})
}

func updateSecretItemContext(c *secretbootstrap.Config, name, cluster, key string, value secretbootstrap.ItemContext) error {
	logrus.WithFields(logrus.Fields{
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

func updateBuildFarmSecrets(c *secretbootstrap.Config, o options) error {
	if o.clusterName == string(api.ClusterVSphere02) {
		_, buildFarmCredentials, err := findSecretConfig(fmt.Sprintf("%s-%s", buildFarm, credentials), string(api.ClusterAPPCI), c.Secrets)
		if err != nil {
			return err
		}
		clientId := o.clusterName + "_github_client_id"
		buildFarmCredentials.From[clientId] = secretbootstrap.ItemContext{
			Item:  fmt.Sprintf("%s_%s", buildUFarm, o.clusterName),
			Field: "github_client_id",
		}
	}
	for _, s := range []string{configUpdater, "crier", "deck", "hook", "prow-controller-manager", "sinker"} {
		_, sc, err := findSecretConfig(s, string(api.ClusterAPPCI), c.Secrets)
		if err != nil {
			return err
		}
		keyAndField := serviceAccountKubeconfigPath(s, o.clusterName)
		item := buildUFarm
		if s == configUpdater {
			item = configUpdater
		}
		sc.From[keyAndField] = secretbootstrap.ItemContext{
			Field: keyAndField,
			Item:  item,
		}
		if o.useTokenFileInKubeconfig && s != configUpdater {
			keyAndFieldToken := serviceAccountTokenFile(s, o.clusterName)
			sc.From[keyAndFieldToken] = secretbootstrap.ItemContext{
				Field: keyAndFieldToken,
				Item:  buildUFarm,
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
