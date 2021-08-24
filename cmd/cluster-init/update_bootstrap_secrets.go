package main

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/api/secretbootstrap"
)

const (
	Credentials         = "credentials"
	PodScaler           = "pod-scaler"
	RegPullCredsAll     = "registry-pull-credentials-all"
	BuildUFarm          = "build_farm"
	DotDockerConfigJson = ".dockerconfigjson"
	Arm01               = "arm01"
	TestCredentials     = "test-credentials"
)

func updateCiSecretBootstrapConfig(o options) error {
	ciSecBootDir := filepath.Join(o.releaseRepo, "core-services", "ci-secret-bootstrap")
	ciSecBootConfigFile := filepath.Join(ciSecBootDir, "_config.yaml")
	logrus.Printf("Updating ci-secret-bootstrap: %s\n", ciSecBootConfigFile)

	data, err := ioutil.ReadFile(ciSecBootConfigFile)
	if err != nil {
		return err
	}
	c := &secretbootstrap.Config{}
	if err := yaml.Unmarshal(data, c); err != nil {
		return err
	}

	for _, groupName := range []string{BuildUFarm, "non_app_ci", "non_app_ci_x86"} {
		c.ClusterGroups[groupName] = append(c.ClusterGroups[groupName], o.clusterName)
	}
	c.UserSecretsTargetClusters = append(c.UserSecretsTargetClusters, o.clusterName)
	if err := updatePodScalerSecret(c, o); err != nil {
		return err
	}
	if err := updateBuildFarmSecret(c, o); err != nil {
		return err
	}
	if err := updateDPTPConManSecret(c, o); err != nil {
		return err
	}
	if err := updateRehearseSecret(c, o); err != nil {
		return err
	}
	if err := updateChatBotSecret(c, o); err != nil {
		return err
	}
	if err := updateExistingRegistryPullCredsAllSecrets(c, o); err != nil {
		return err
	}
	appendSecret(generateRegistryPushCredsSecret, c, o)
	appendSecret(generateRegistryPullCredsSecret, c, o)
	appendSecret(generateCiOperatorSecret, c, o)
	generateRegistryPullCredsAllSecrets(c, o)

	y, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	if err := ioutil.WriteFile(ciSecBootConfigFile, y, 0644); err != nil {
		return err
	}

	return nil
}

func appendSecret(generateFunction func(options) secretbootstrap.SecretConfig, c *secretbootstrap.Config, o options) {
	secret := generateFunction(o)
	logrus.Printf("Creating new secret with 'to' of: %v\n", secret.To)
	c.Secrets = append(c.Secrets, secret)
}

func generateCiOperatorSecret(o options) secretbootstrap.SecretConfig {
	key := strings.ToLower(Kubeconfig)
	return secretbootstrap.SecretConfig{
		From: map[string]secretbootstrap.ItemContext{
			key: {
				Field: "sa.ci-operator." + o.clusterName + ".config",
				Item:  BuildFarm,
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

func generateRegistryPushCredsSecret(o options) secretbootstrap.SecretConfig {
	items := []secretbootstrap.DockerConfigJSONData{
		{
			AuthField:   "token_image-pusher_app.ci_reg_auth_value.txt",
			Item:        BuildUFarm,
			RegistryURL: "registry.ci.openshift.org",
		},
	}
	from := generatePushPullSecretFrom(o.clusterName, items)
	const credentialName = "registry-push-credentials-ci-central"
	sc := secretbootstrap.SecretConfig{
		From: map[string]secretbootstrap.ItemContext{
			DotDockerConfigJson: from,
		},
		To: []secretbootstrap.SecretContext{
			generateDCJSecretConfigTo(credentialName, Ci, o.clusterName),
			generateDCJSecretConfigTo(credentialName, TestCredentials, o.clusterName),
		},
	}
	return sc
}

func generateRegistryPullCredsSecret(o options) secretbootstrap.SecretConfig {
	items := []secretbootstrap.DockerConfigJSONData{
		{
			AuthField:   registryPullTokenField(Ci),
			Item:        BuildUFarm,
			RegistryURL: "registry.svc.ci.openshift.org",
		},
		{
			AuthField:   registryPullTokenField(string(api.ClusterAPPCI)),
			Item:        BuildUFarm,
			RegistryURL: "registry.ci.openshift.org",
		},
	}
	from := generatePushPullSecretFrom(o.clusterName, items)
	const credentialName = "registry-pull-credentials"
	sc := secretbootstrap.SecretConfig{
		From: map[string]secretbootstrap.ItemContext{
			DotDockerConfigJson: from,
		},
		To: []secretbootstrap.SecretContext{
			generateDCJSecretConfigTo(credentialName, Ci, o.clusterName),
			generateDCJSecretConfigTo(credentialName, TestCredentials, o.clusterName),
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

func generateDCJSecretConfigTo(name string, namespace string, clusterName string) secretbootstrap.SecretContext {
	return secretbootstrap.SecretContext{
		Cluster:   clusterName,
		Name:      name,
		Namespace: namespace,
		Type:      "kubernetes.io/dockerconfigjson",
	}
}

func updatePodScalerSecret(c *secretbootstrap.Config, o options) error {
	name := fmt.Sprintf("%s-%s", PodScaler, Credentials)
	key := fmt.Sprintf("%s.%s", o.clusterName, Config)
	return appendSecretItemContext(c, name, string(api.ClusterAPPCI), key, secretbootstrap.ItemContext{
		Field: secretConfigFor(PodScaler, o.clusterName),
		Item:  PodScaler,
	})
}

func updateDPTPConManSecret(c *secretbootstrap.Config, o options) error {
	key := fmt.Sprintf("%s.%s", o.clusterName, strings.ToLower(Kubeconfig))
	const DPTPControllerManager = "dptp-controller-manager"
	field := secretConfigFor(DPTPControllerManager, o.clusterName)
	return appendSecretItemContext(c, DPTPControllerManager, string(api.ClusterAPPCI), key, secretbootstrap.ItemContext{
		Field: field,
		Item:  BuildFarm,
	})
}

func updateRehearseSecret(c *secretbootstrap.Config, o options) error {
	keyAndField := secretConfigFor(CiOperator, o.clusterName)
	return appendSecretItemContext(c, "pj-rehearse", string(api.ClusterBuild01), keyAndField, secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  BuildFarm,
	})
}

func updateChatBotSecret(c *secretbootstrap.Config, o options) error {
	const chatBot = "ci-chat-bot"
	name := chatBot + "-kubeconfigs"
	keyAndField := secretConfigFor(chatBot, o.clusterName)
	return appendSecretItemContext(c, name, string(api.ClusterAPPCI), keyAndField, secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  chatBot,
	})
}

func appendSecretItemContext(c *secretbootstrap.Config, name string, cluster string, key string, value secretbootstrap.ItemContext) error {
	logrus.Printf("Appending secret item to: {name: %s, cluster: %s}\n", name, cluster)
	sc, err := findSecretConfig(name, cluster, c.Secrets)
	if err != nil {
		return err
	}
	sc.From[key] = value
	return nil
}

func updateExistingRegistryPullCredsAllSecrets(c *secretbootstrap.Config, o options) error {
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

func generateRegistryPullCredsAllSecrets(c *secretbootstrap.Config, o options) {
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
			generateDCJSecretConfigTo(RegPullCredsAll, Ci, o.clusterName),
			generateDCJSecretConfigTo(RegPullCredsAll, TestCredentials, o.clusterName),
		},
	}
	logrus.Printf("Creating new secret with 'to' of: %v\n", sc.To)
	c.Secrets = append(c.Secrets, sc)
}

func getRegistryUrlFor(cluster string) string {
	switch cluster {
	case string(api.ClusterVSphere):
		return "registry.apps.build01-us-west-2.vmc.ci.openshift.org"
	case string(api.ClusterAPPCI):
		return "registry.ci.openshift.org"
	case Arm01:
		return "registry.arm-build01.arm-build.devcluster.openshift.com"
	default:
		return fmt.Sprintf("registry.%s.ci.openshift.org", cluster)
	}
}

func appendRegistrySecretItemContext(c *secretbootstrap.Config, name string, cluster string, value secretbootstrap.DockerConfigJSONData) error {
	logrus.Printf("Appending registry secret item to: {name: %s, cluster: %s}\n", name, cluster)
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

func updateBuildFarmSecret(c *secretbootstrap.Config, o options) error {
	//TODO: this function will likely need to be modified to support the new schema
	buildFarmCreds, err := findSecretConfig(fmt.Sprintf("%s-%s", BuildFarm, Credentials), string(api.ClusterAPPCI), c.Secrets)
	if err != nil {
		return err
	}
	for _, s := range []string{ConfigUpdater, "crier", "deck", "hook", "prow-controller-manager", "sinker"} {
		keyAndField := secretConfigFor(s, o.clusterName)
		buildFarmCreds.From[keyAndField] = secretbootstrap.ItemContext{
			Field: keyAndField,
			Item:  BuildFarm,
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
	return &secretbootstrap.SecretConfig{}, fmt.Errorf("couldn't find SecretConfig with name: %s and cluster: %s", name, cluster)
}
