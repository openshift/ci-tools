package main

import (
	"fmt"
	"path/filepath"
	"strings"

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

func updateCiSecretBootstrapConfig(o options) {
	ciSecBootDir := filepath.Join(o.releaseRepo, "core-services", "ci-secret-bootstrap")
	ciSecBootConfigFile := filepath.Join(ciSecBootDir, "_config.yaml")
	fmt.Printf("Updating ci-secret-bootstrap: %s\n", ciSecBootConfigFile)
	c := &secretbootstrap.Config{}
	loadConfig(ciSecBootConfigFile, c)
	for _, groupName := range []string{BuildUFarm, "non_app_ci", "non_app_ci_x86"} {
		c.ClusterGroups[groupName] = append(c.ClusterGroups[groupName], o.clusterName)
	}
	c.UserSecretsTargetClusters = append(c.UserSecretsTargetClusters, o.clusterName)
	updatePodScalerSecret(c, o)
	updateBuildFarmSecret(c, o)
	updateDPTPConManSecret(c, o)
	updateRehearseSecret(c, o)
	updateChatBotSecret(c, o)
	updateExistingRegistryPullCredsAllSecrets(c, o)
	appendSecret(generateRegistryPushCredsSecret, c, o)
	appendSecret(generateRegistryPullCredsSecret, c, o)
	appendSecret(generateCiOperatorSecret, c, o)
	generateRegistryPullCredsAllSecrets(c, o)
	saveConfig(ciSecBootConfigFile, c)
}

func appendSecret(generateFunction func(options) secretbootstrap.SecretConfig, c *secretbootstrap.Config, o options) {
	secret := generateFunction(o)
	fmt.Printf("Creating new secret with 'to' of: %v\n", secret.To)
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
			AuthField:   "token_image-puller_ci_reg_auth_value.txt",
			Item:        BuildUFarm,
			RegistryURL: "registry.svc.ci.openshift.org",
		},
		{
			AuthField:   "token_image-puller_app.ci_reg_auth_value.txt",
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
				AuthField:   fmt.Sprintf("token_image-puller_%s_reg_auth_value.txt", clusterName),
				Item:        BuildUFarm,
				RegistryURL: "image-registry.openshift-image-registry.svc.cluster.local:5000",
			},
			{
				AuthField:   fmt.Sprintf("token_image-puller_%s_reg_auth_value.txt", clusterName),
				Item:        BuildUFarm,
				RegistryURL: "image-registry.openshift-image-registry.svc:5000",
			},
			{
				AuthField:   fmt.Sprintf("token_image-puller_%s_reg_auth_value.txt", clusterName),
				Item:        BuildUFarm,
				RegistryURL: fmt.Sprintf("registry.%s.ci.openshift.org", clusterName),
			},
		},
	}
	itemContext.DockerConfigJSONData =
		append(itemContext.DockerConfigJSONData, items...)
	return itemContext
}

func generateDCJSecretConfigTo(name string, namespace string, clusterName string) secretbootstrap.SecretContext {
	return secretbootstrap.SecretContext{
		Cluster:   clusterName,
		Name:      name,
		Namespace: namespace,
		Type:      "kubernetes.io/dockerconfigjson",
	}
}

func updatePodScalerSecret(c *secretbootstrap.Config, o options) {
	name := fmt.Sprintf("%s-%s", PodScaler, Credentials)
	key := fmt.Sprintf("%s.%s", o.clusterName, Config)
	appendSecretItemContext(c, name, string(api.ClusterAPPCI), key, secretbootstrap.ItemContext{
		Field: secretConfigFor(PodScaler, o.clusterName),
		Item:  PodScaler,
	})
}

func updateDPTPConManSecret(c *secretbootstrap.Config, o options) {
	key := fmt.Sprintf("%s.%s", o.clusterName, strings.ToLower(Kubeconfig))
	const DPTPControllerManager = "dptp-controller-manager"
	field := secretConfigFor(DPTPControllerManager, o.clusterName)
	appendSecretItemContext(c, DPTPControllerManager, string(api.ClusterAPPCI), key, secretbootstrap.ItemContext{
		Field: field,
		Item:  BuildFarm,
	})
}

func updateRehearseSecret(c *secretbootstrap.Config, o options) {
	keyAndField := secretConfigFor(CiOperator, o.clusterName)
	appendSecretItemContext(c, "pj-rehearse", string(api.ClusterBuild01), keyAndField, secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  BuildFarm,
	})
}

func updateChatBotSecret(c *secretbootstrap.Config, o options) {
	const chatBot = "ci-chat-bot"
	name := chatBot + "-kubeconfigs"
	keyAndField := secretConfigFor(chatBot, o.clusterName)
	appendSecretItemContext(c, name, string(api.ClusterAPPCI), keyAndField, secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  chatBot,
	})
}

func appendSecretItemContext(c *secretbootstrap.Config, name string, cluster string, key string, value secretbootstrap.ItemContext) {
	fmt.Printf("Appending secret item to: {name: %s, cluster: %s}\n", name, cluster)
	sc, err := findSecretConfig(name, cluster, c.Secrets)
	check(err)
	sc.From[key] = value
}

func updateExistingRegistryPullCredsAllSecrets(c *secretbootstrap.Config, o options) {
	for _, cluster := range c.UserSecretsTargetClusters {
		if cluster != string(api.ClusterHive) && cluster != Arm01 && cluster != o.clusterName {
			appendRegistrySecretItemContext(c, RegPullCredsAll, cluster, secretbootstrap.DockerConfigJSONData{
				AuthField:   fmt.Sprintf("token_image-puller_%s_reg_auth_value.txt", o.clusterName),
				Item:        BuildUFarm,
				RegistryURL: fmt.Sprintf("registry.%s.ci.openshift.org", o.clusterName),
			})
		}
	}
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
				AuthField:   fmt.Sprintf("token_image-puller_%s_reg_auth_value.txt", cluster),
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
	fmt.Printf("Creating new secret with 'to' of: %v\n", sc.To)
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

func appendRegistrySecretItemContext(c *secretbootstrap.Config, name string, cluster string, value secretbootstrap.DockerConfigJSONData) {
	fmt.Printf("Appending registry secret item to: {name: %s, cluster: %s}\n", name, cluster)
	sc, err := findSecretConfig(name, cluster, c.Secrets)
	check(err)
	data := append(sc.From[DotDockerConfigJson].DockerConfigJSONData, value)
	sc.From[DotDockerConfigJson] = secretbootstrap.ItemContext{
		DockerConfigJSONData: data,
	}
}

func updateBuildFarmSecret(c *secretbootstrap.Config, o options) {
	//TODO: this function will likely need to be modified to support the new schema
	buildFarmCreds, err := findSecretConfig(fmt.Sprintf("%s-%s", BuildFarm, Credentials), string(api.ClusterAPPCI), c.Secrets)
	check(err)
	for _, s := range []string{ConfigUpdater, "crier", "deck", "hook", "prow-controller-manager", "sinker"} {
		keyAndField := secretConfigFor(s, o.clusterName)
		buildFarmCreds.From[keyAndField] = secretbootstrap.ItemContext{
			Field: keyAndField,
			Item:  BuildFarm,
		}
	}
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
