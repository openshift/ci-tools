package main

import (
	"fmt"
	"github.com/sirupsen/logrus"
	"path/filepath"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/api/secretbootstrap"
)

const (
	Credentials         = "credentials"
	RegPullCredsAll     = "registry-pull-credentials-all"
	DotDockerConfigJson = ".dockerconfigjson"
	Arm01               = "arm01"
	TestCredentials     = "test-credentials"
	Kubeconfig          = "kubeconfig"
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
	for _, groupName := range []string{BuildUFarm, "non_app_ci", "non_app_ci_x86"} {
		c.ClusterGroups[groupName] = append(c.ClusterGroups[groupName], o.clusterName)
	}
	c.UserSecretsTargetClusters = append(c.UserSecretsTargetClusters, o.clusterName)
	if err := updatePodScalerSecret(c, o); err != nil {
		return err
	}
	if err := updateBuildFarmSecrets(c, o); err != nil {
		return err
	}
	if err := updateDPTPControllerManagerSecret(c, o); err != nil {
		return err
	}
	if err := updateRehearseSecret(c, o); err != nil {
		return err
	}
	if err := updateChatBotSecret(c, o); err != nil {
		return err
	}
	if err := updateExistingRegistryPullCredentialsAllSecrets(c, o); err != nil {
		return err
	}
	appendSecret(generateRegistryPushCredentialsSecret, c, o)
	appendSecret(generateRegistryPullCredentialsSecret, c, o)
	appendSecret(generateCiOperatorSecret, c, o)
	generateRegistryPullCredentialsAllSecrets(c, o)
	return nil
}

func appendSecret(secretGenerator func(options) secretbootstrap.SecretConfig, c *secretbootstrap.Config, o options) {
	secret := secretGenerator(o)
	logrus.Infof("Creating new secret with 'to' of: %v", secret.To)
	c.Secrets = append(c.Secrets, secret)
}

func generateCiOperatorSecret(o options) secretbootstrap.SecretConfig {
	return secretbootstrap.SecretConfig{
		From: map[string]secretbootstrap.ItemContext{
			Kubeconfig: {
				Field: serviceAccountKubeconfigPath(CiOperator, o.clusterName),
				Item:  BuildUFarm,
			},
		},
		To: []secretbootstrap.SecretContext{
			{
				Cluster:   o.clusterName,
				Name:      CiOperator,
				Namespace: TestCredentials,
			},
		},
	}
}

func generateRegistryPushCredentialsSecret(o options) secretbootstrap.SecretConfig {
	items := []secretbootstrap.DockerConfigJSONData{
		{
			AuthField:   "token_image-pusher_app.ci_reg_auth_value.txt",
			Item:        BuildUFarm,
			RegistryURL: api.ServiceDomainAPPCIRegistry,
		},
	}
	from := generatePushPullSecretFrom(o.clusterName, items)
	sc := secretbootstrap.SecretConfig{
		From: map[string]secretbootstrap.ItemContext{
			DotDockerConfigJson: from,
		},
		To: []secretbootstrap.SecretContext{
			generateDockerConfigJsonSecretConfigTo(api.RegistryPushCredentialsCICentralSecret, Ci, o.clusterName),
			generateDockerConfigJsonSecretConfigTo(api.RegistryPushCredentialsCICentralSecret, TestCredentials, o.clusterName),
		},
	}
	return sc
}

func generateRegistryPullCredentialsSecret(o options) secretbootstrap.SecretConfig {
	items := []secretbootstrap.DockerConfigJSONData{
		{
			AuthField:   registryPullTokenField(Ci),
			Item:        BuildUFarm,
			RegistryURL: "registry.svc.ci.openshift.org",
		},
		{
			AuthField:   registryPullTokenField(string(api.ClusterAPPCI)),
			Item:        BuildUFarm,
			RegistryURL: api.ServiceDomainAPPCIRegistry,
		},
	}
	from := generatePushPullSecretFrom(o.clusterName, items)
	sc := secretbootstrap.SecretConfig{
		From: map[string]secretbootstrap.ItemContext{
			DotDockerConfigJson: from,
		},
		To: []secretbootstrap.SecretContext{
			generateDockerConfigJsonSecretConfigTo(api.RegistryPullCredentialsSecret, Ci, o.clusterName),
			generateDockerConfigJsonSecretConfigTo(api.RegistryPullCredentialsSecret, TestCredentials, o.clusterName),
		},
	}
	return sc
}

func generatePushPullSecretFrom(clusterName string, items []secretbootstrap.DockerConfigJSONData) secretbootstrap.ItemContext {
	itemContext := secretbootstrap.ItemContext{
		DockerConfigJSONData: []secretbootstrap.DockerConfigJSONData{
			{
				AuthField:   registryPullTokenField(clusterName),
				Item:        BuildUFarm,
				RegistryURL: "image-registry.openshift-image-registry.svc.cluster.local:5000",
			},
			{
				AuthField:   registryPullTokenField(clusterName),
				Item:        BuildUFarm,
				RegistryURL: "image-registry.openshift-image-registry.svc:5000",
			},
			{
				AuthField:   registryPullTokenField(clusterName),
				Item:        BuildUFarm,
				RegistryURL: fmt.Sprintf("registry.%s.ci.openshift.org", clusterName),
			},
		},
	}
	itemContext.DockerConfigJSONData =
		append(itemContext.DockerConfigJSONData, items...)
	return itemContext
}

func registryPullTokenField(clusterName string) string {
	return fmt.Sprintf("token_image-puller_%s_reg_auth_value.txt", clusterName)
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
	key := fmt.Sprintf("%s.%s", o.clusterName, Config)
	return appendSecretItemContext(c, PodScaler, string(api.ClusterAPPCI), key, secretbootstrap.ItemContext{
		Field: serviceAccountKubeconfigPath(PodScaler, o.clusterName),
		Item:  PodScaler,
	})
}

func updateDPTPControllerManagerSecret(c *secretbootstrap.Config, o options) error {
	const DPTPControllerManager = "dptp-controller-manager"
	keyAndField := serviceAccountKubeconfigPath(DPTPControllerManager, o.clusterName)
	return appendSecretItemContext(c, DPTPControllerManager, string(api.ClusterAPPCI), keyAndField, secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  BuildUFarm,
	})
}

func updateRehearseSecret(c *secretbootstrap.Config, o options) error {
	keyAndField := serviceAccountKubeconfigPath(CiOperator, o.clusterName)
	return appendSecretItemContext(c, "pj-rehearse", string(api.ClusterBuild01), keyAndField, secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  BuildUFarm,
	})
}

func updateChatBotSecret(c *secretbootstrap.Config, o options) error {
	const chatBot = "ci-chat-bot"
	name := chatBot + "-kubeconfigs"
	keyAndField := serviceAccountKubeconfigPath(chatBot, o.clusterName)
	return appendSecretItemContext(c, name, string(api.ClusterAPPCI), keyAndField, secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  chatBot,
	})
}

func appendSecretItemContext(c *secretbootstrap.Config, name string, cluster string, key string, value secretbootstrap.ItemContext) error {
	logrus.Infof("Appending secret item to: {name: %s, cluster: %s}", name, cluster)
	sc, err := findSecretConfig(name, cluster, c.Secrets)
	if err != nil {
		return err
	}
	sc.From[key] = value
	return nil
}

func updateExistingRegistryPullCredentialsAllSecrets(c *secretbootstrap.Config, o options) error {
	for _, cluster := range c.UserSecretsTargetClusters {
		if cluster != string(api.ClusterHive) && cluster != Arm01 && cluster != o.clusterName {
			return appendRegistrySecretItemContext(c, RegPullCredsAll, cluster, secretbootstrap.DockerConfigJSONData{
				AuthField:   fmt.Sprintf("token_image-puller_%s_reg_auth_value.txt", o.clusterName),
				Item:        BuildUFarm,
				RegistryURL: fmt.Sprintf("registry.%s.ci.openshift.org", o.clusterName),
			})
		}
	}
	return nil
}

func generateRegistryPullCredentialsAllSecrets(c *secretbootstrap.Config, o options) {
	const auth, email = "auth", "email"
	items := []secretbootstrap.DockerConfigJSONData{
		{
			AuthField:   "token_image-puller_ci_reg_auth_value.txt",
			Item:        BuildUFarm,
			RegistryURL: "registry.svc.ci.openshift.org",
		},
		{
			AuthField:   auth,
			Item:        "cloud.openshift.com-pull-secret",
			RegistryURL: "cloud.openshift.com",
			EmailField:  email,
		},
		{
			AuthField:   auth,
			Item:        "quay.io-pull-secret",
			RegistryURL: "quay.io",
			EmailField:  email,
		},
		{
			AuthField:   auth,
			Item:        "registry.connect.redhat.com-pull-secret",
			RegistryURL: "registry.connect.redhat.com",
			EmailField:  email,
		},
		{
			AuthField:   auth,
			Item:        "registry.redhat.io-pull-secret",
			RegistryURL: "registry.redhat.io",
			EmailField:  email,
		},
	}
	for _, cluster := range c.UserSecretsTargetClusters {
		if cluster != string(api.ClusterHive) {
			items = append(items, secretbootstrap.DockerConfigJSONData{
				AuthField:   registryPullTokenField(cluster),
				Item:        BuildUFarm,
				RegistryURL: getRegistryUrlFor(cluster),
			})
		}
	}
	from := generatePushPullSecretFrom(o.clusterName, items)
	sc := secretbootstrap.SecretConfig{
		From: map[string]secretbootstrap.ItemContext{
			DotDockerConfigJson: from,
		},
		To: []secretbootstrap.SecretContext{
			generateDockerConfigJsonSecretConfigTo(RegPullCredsAll, Ci, o.clusterName),
			generateDockerConfigJsonSecretConfigTo(RegPullCredsAll, TestCredentials, o.clusterName),
		},
	}
	logrus.Infof("Creating new secret with 'to' of: %v", sc.To)
	c.Secrets = append(c.Secrets, sc)
}

func getRegistryUrlFor(cluster string) string {
	switch cluster {
	case string(api.ClusterVSphere):
		return "registry.apps.build01-us-west-2.vmc.ci.openshift.org"
	case string(api.ClusterAPPCI):
		return api.ServiceDomainAPPCIRegistry
	case Arm01:
		return "registry.arm-build01.arm-build.devcluster.openshift.com"
	default:
		return fmt.Sprintf("registry.%s.ci.openshift.org", cluster)
	}
}

func appendRegistrySecretItemContext(c *secretbootstrap.Config, name string, cluster string, value secretbootstrap.DockerConfigJSONData) error {
	logrus.Infof("Appending registry secret item to: {name: %s, cluster: %s}", name, cluster)
	sc, err := findSecretConfig(name, cluster, c.Secrets)
	if err != nil {
		return err
	}
	data := append(sc.From[DotDockerConfigJson].DockerConfigJSONData, value)
	sc.From[DotDockerConfigJson] = secretbootstrap.ItemContext{
		DockerConfigJSONData: data,
	}
	return nil
}

func updateBuildFarmSecrets(c *secretbootstrap.Config, o options) error {
	buildFarmCredentials, err := findSecretConfig(fmt.Sprintf("%s-%s", BuildFarm, Credentials), string(api.ClusterAPPCI), c.Secrets)
	if err != nil {
		return err
	}
	clientId := o.clusterName + "_github_client_id"
	buildFarmCredentials.From[clientId] = secretbootstrap.ItemContext{
		Item:  fmt.Sprintf("%s_%s", BuildUFarm, o.clusterName),
		Field: "github_client_id",
	}
	for _, s := range []string{ConfigUpdater, "crier", "deck", "hook", "prow-controller-manager", "sinker"} {
		sc, err := findSecretConfig(s, string(api.ClusterAPPCI), c.Secrets)
		if err != nil {
			return err
		}
		keyAndField := serviceAccountKubeconfigPath(s, o.clusterName)
		sc.From[keyAndField] = secretbootstrap.ItemContext{
			Field: keyAndField,
			Item:  BuildUFarm,
		}
	}
	return nil
}

func findSecretConfig(name string, cluster string, sc []secretbootstrap.SecretConfig) (*secretbootstrap.SecretConfig, error) {
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
		return &sc[idx], nil
	}
	return nil, fmt.Errorf("couldn't find SecretConfig with name: %s and cluster: %s", name, cluster)
}
