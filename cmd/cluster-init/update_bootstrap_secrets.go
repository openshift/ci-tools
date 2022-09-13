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
	regPullCredsAll     = "registry-pull-credentials-all"
	dotDockerConfigJson = ".dockerconfigjson"
	testCredentials     = "test-credentials"
	kubeconfig          = "kubeconfig"
	config              = "config"
)

type pushPull string

const (
	pull pushPull = "puller"
	push pushPull = "pusher"
)

func updateCiSecretBootstrap(o options) error {
	secretBootstrapDir := filepath.Join(o.releaseRepo, "core-services", "ci-secret-bootstrap")
	secretBootstrapConfigFile := filepath.Join(secretBootstrapDir, "_config.yaml")
	logrus.Infof("Updating ci-secret-bootstrap: %s", secretBootstrapConfigFile)

	var c secretbootstrap.Config
	if err := secretbootstrap.LoadConfigFromFile(secretBootstrapConfigFile, &c); err != nil {
		return err
	}
	if err := updateCiSecretBootstrapConfig(o, &c); err != nil {
		return err
	}
	return secretbootstrap.SaveConfigToFile(secretBootstrapConfigFile, &c)
}

func updateCiSecretBootstrapConfig(o options, c *secretbootstrap.Config) error {
	for _, groupName := range []string{buildUFarm, "non_app_ci"} {
		c.ClusterGroups[groupName] = sets.NewString(c.ClusterGroups[groupName]...).Insert(o.clusterName).List()
	}
	c.UserSecretsTargetClusters = sets.NewString(c.UserSecretsTargetClusters...).Insert(o.clusterName).List()

	for _, step := range []func(c *secretbootstrap.Config, o options) error{
		updatePodScalerSecret,
		updateBuildFarmSecrets,
		updateDPTPControllerManagerSecret,
		updateRehearseSecret,
		updateGithubLdapUserGroupCreatorSecret,
		updatePromotedImageGovernor,
		updateClusterDisplay,
		updateChatBotSecret,
		updateExistingRegistryPullCredentialsAllSecrets,
		updateSecret(generateRegistryPushCredentialsSecret),
		updateSecret(generateRegistryPullCredentialsSecret),
		updateSecret(generateCiOperatorSecret),
		updateSecret(generateRegistryPullCredentialsAllSecrets(c)),
	} {
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
	return secretbootstrap.SecretConfig{
		From: map[string]secretbootstrap.ItemContext{
			kubeconfig: {
				Field: serviceAccountKubeconfigPath(ciOperator, o.clusterName),
				Item:  buildUFarm,
			},
		},
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
	key := fmt.Sprintf("%s.%s", o.clusterName, config)
	return updateSecretItemContext(c, podScaler, string(api.ClusterAPPCI), key, secretbootstrap.ItemContext{
		Field: serviceAccountKubeconfigPath(podScaler, o.clusterName),
		Item:  podScaler,
	})
}

func updateDPTPControllerManagerSecret(c *secretbootstrap.Config, o options) error {
	const DPTPControllerManager = "dptp-controller-manager"
	keyAndField := serviceAccountKubeconfigPath(DPTPControllerManager, o.clusterName)
	return updateSecretItemContext(c, DPTPControllerManager, string(api.ClusterAPPCI), keyAndField, secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  buildUFarm,
	})
}

func updateRehearseSecret(c *secretbootstrap.Config, o options) error {
	keyAndField := serviceAccountKubeconfigPath(ciOperator, o.clusterName)
	return updateSecretItemContext(c, "pj-rehearse", string(api.ClusterBuild01), keyAndField, secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  buildUFarm,
	})
}

func updateGithubLdapUserGroupCreatorSecret(c *secretbootstrap.Config, o options) error {
	keyAndField := serviceAccountKubeconfigPath(githubLdapUserGroupCreator, o.clusterName)
	return updateSecretItemContext(c, githubLdapUserGroupCreator, string(api.ClusterAPPCI), keyAndField, secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  buildUFarm,
	})
}

func updatePromotedImageGovernor(c *secretbootstrap.Config, o options) error {
	keyAndField := serviceAccountKubeconfigPath(promotedImageGovernor, o.clusterName)
	return updateSecretItemContext(c, promotedImageGovernor, string(api.ClusterAPPCI), keyAndField, secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  buildUFarm,
	})
}

func updateClusterDisplay(c *secretbootstrap.Config, o options) error {
	keyAndField := serviceAccountKubeconfigPath(clusterDisplay, o.clusterName)
	return updateSecretItemContext(c, clusterDisplay, string(api.ClusterAPPCI), keyAndField, secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  buildUFarm,
	})
}

func updateChatBotSecret(c *secretbootstrap.Config, o options) error {
	const chatBot = "ci-chat-bot"
	name := chatBot + "-kubeconfigs"
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

func updateExistingRegistryPullCredentialsAllSecrets(c *secretbootstrap.Config, o options) error {
	for _, cluster := range c.UserSecretsTargetClusters {
		if cluster != string(api.ClusterHive) && cluster != string(api.ClusterARM01) && cluster != o.clusterName {
			return updateRegistrySecretItemContext(c, regPullCredsAll, cluster, secretbootstrap.DockerConfigJSONData{
				AuthField:   registryCommandTokenField(o.clusterName, pull),
				Item:        buildUFarm,
				RegistryURL: registryUrlFor(o.clusterName),
			})
		}
	}
	return nil
}

func generateRegistryPullCredentialsAllSecrets(c *secretbootstrap.Config) func(options) secretbootstrap.SecretConfig {
	return func(o options) secretbootstrap.SecretConfig {
		items := []secretbootstrap.DockerConfigJSONData{
			{
				AuthField:   "auth",
				Item:        "cloud.openshift.com-pull-secret",
				RegistryURL: "cloud.openshift.com",
				EmailField:  "email",
			},
			{
				AuthField:   "auth",
				Item:        "quay.io-pull-secret",
				RegistryURL: "quay.io",
				EmailField:  "email",
			},
			{
				AuthField:   "auth",
				Item:        "registry.connect.redhat.com-pull-secret",
				RegistryURL: "registry.connect.redhat.com",
				EmailField:  "email",
			},
			{
				AuthField:   "auth",
				Item:        "registry.redhat.io-pull-secret",
				RegistryURL: "registry.redhat.io",
				EmailField:  "email",
			},
		}
		for _, cluster := range c.UserSecretsTargetClusters {
			if cluster != string(api.ClusterHive) && cluster != o.clusterName { // This cluster's SecretItem is added in generatePushPullSecretFrom
				items = append(items, secretbootstrap.DockerConfigJSONData{
					AuthField:   registryCommandTokenField(cluster, pull),
					Item:        buildUFarm,
					RegistryURL: registryUrlFor(cluster),
				})
			}
		}
		return secretbootstrap.SecretConfig{
			From: map[string]secretbootstrap.ItemContext{
				dotDockerConfigJson: generatePushPullSecretFrom(o.clusterName, items),
			},
			To: []secretbootstrap.SecretContext{
				generateDockerConfigJsonSecretConfigTo(regPullCredsAll, ci, o.clusterName),
				generateDockerConfigJsonSecretConfigTo(regPullCredsAll, testCredentials, o.clusterName),
			},
		}
	}
}

func registryUrlFor(cluster string) string {
	switch cluster {
	case string(api.ClusterVSphere):
		return "registry.apps.build01-us-west-2.vmc.ci.openshift.org"
	case string(api.ClusterAPPCI):
		return api.ServiceDomainAPPCIRegistry
	case string(api.ClusterARM01):
		return "registry.arm-build01.arm-build.devcluster.openshift.com"
	default:
		return fmt.Sprintf("registry.%s.ci.openshift.org", cluster)
	}
}

func updateRegistrySecretItemContext(c *secretbootstrap.Config, name, cluster string, value secretbootstrap.DockerConfigJSONData) error {
	logrus.Infof("Appending registry secret item to: {name: %s, cluster: %s}", name, cluster)
	_, sc, err := findSecretConfig(name, cluster, c.Secrets)
	if err != nil {
		return err
	}
	for _, si := range sc.From[dotDockerConfigJson].DockerConfigJSONData {
		if si.AuthField == value.AuthField && si.RegistryURL == value.RegistryURL {
			return nil
		}
	}
	sc.From[dotDockerConfigJson] = secretbootstrap.ItemContext{
		DockerConfigJSONData: append(sc.From[dotDockerConfigJson].DockerConfigJSONData, value),
	}
	return nil
}

func updateBuildFarmSecrets(c *secretbootstrap.Config, o options) error {
	if o.clusterName == string(api.ClusterVSphere) {
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
		sc.From[keyAndField] = secretbootstrap.ItemContext{
			Field: keyAndField,
			Item:  buildUFarm,
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
