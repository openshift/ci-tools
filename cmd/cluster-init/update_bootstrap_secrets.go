package main

import (
	"fmt"
	"github.com/openshift/ci-tools/pkg/api/secretbootstrap"
	"path/filepath"
	"strings"
)

const (
	DPTPControllerManager = "dptp-controller-manager"
	Credentials           = "credentials"
	PjRehearse            = "pj-rehearse"
	PodScaler             = "pod-scaler"
	Crier                 = "crier"
	Deck                  = "deck"
	Hook                  = "hook"
	ProwControllerManager = "prow-controller-manager"
	Sinker                = "sinker"
	ChatBot               = "chat-bot"
	Kubeconfigs           = "kubeconfigs"
	RegPushCredCiCentral  = "registry-push-credentials-ci-central"
	RegPullCreds          = "registry-pull-credentials"
	RegPullCredsAll       = "registry-pull-credentials-all"
	BuildUFarm            = "build_farm"
	DotDockerConfigJson   = ".dockerconfigjson"
	Vsphere               = "vsphere"
	Auth                  = "auth"
	Email                 = "email"
	Arm01                 = "arm01"
	Hive                  = "hive"
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
	field := fmt.Sprintf("%s.%s.%s.%s", Sa, CiOperator, o.clusterName, Config)
	return secretbootstrap.SecretConfig{
		From: map[string]secretbootstrap.ItemContext{
			key: {
				Field: field,
				Item:  BuildFarm,
			},
		},
		To: []secretbootstrap.SecretContext{
			{
				Cluster:   o.clusterName,
				Name:      CiOperator,
				Namespace: fmt.Sprintf("%s-%s", Test, Credentials),
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
	sc := secretbootstrap.SecretConfig{
		From: map[string]secretbootstrap.ItemContext{
			DotDockerConfigJson: from,
		},
		To: []secretbootstrap.SecretContext{
			generateDCJSecretConfigTo(RegPushCredCiCentral, Ci, o.clusterName),
			generateDCJSecretConfigTo(RegPushCredCiCentral, fmt.Sprintf("%s-%s", Test, Credentials), o.clusterName),
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
	sc := secretbootstrap.SecretConfig{
		From: map[string]secretbootstrap.ItemContext{
			DotDockerConfigJson: from,
		},
		To: []secretbootstrap.SecretContext{
			generateDCJSecretConfigTo(RegPullCreds, Ci, o.clusterName),
			generateDCJSecretConfigTo(RegPullCreds, fmt.Sprintf("%s-%s", Test, Credentials), o.clusterName),
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
	field := fmt.Sprintf("%s.%s.%s.%s", Sa, PodScaler, o.clusterName, Config)
	appendSecretItemContext(c, name, AppDotCi, key, secretbootstrap.ItemContext{
		Field: field,
		Item:  PodScaler,
	})
}

func updateDPTPConManSecret(c *secretbootstrap.Config, o options) {
	key := fmt.Sprintf("%s.%s", o.clusterName, strings.ToLower(Kubeconfig))
	field := fmt.Sprintf("%s.%s.%s.%s", Sa, DPTPControllerManager, o.clusterName, Config)
	appendSecretItemContext(c, DPTPControllerManager, AppDotCi, key, secretbootstrap.ItemContext{
		Field: field,
		Item:  BuildFarm,
	})
}

func updateRehearseSecret(c *secretbootstrap.Config, o options) {
	keyAndField := fmt.Sprintf("%s.%s.%s.%s", Sa, CiOperator, o.clusterName, Config)
	appendSecretItemContext(c, PjRehearse, Build01, keyAndField, secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  BuildFarm,
	})
}

func updateChatBotSecret(c *secretbootstrap.Config, o options) {
	name := fmt.Sprintf("%s-%s-%s", Ci, ChatBot, Kubeconfigs)
	keyAndField := fmt.Sprintf("%s.%s-%s.%s.%s", Sa, Ci, ChatBot, o.clusterName, Config)
	item := fmt.Sprintf("%s-%s", Ci, ChatBot)
	appendSecretItemContext(c, name, AppDotCi, keyAndField, secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  item,
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
		if cluster != Hive && cluster != Arm01 && cluster != o.clusterName {
			appendRegistrySecretItemContext(c, RegPullCredsAll, cluster, secretbootstrap.DockerConfigJSONData{
				AuthField:   fmt.Sprintf("token_image-puller_%s_reg_auth_value.txt", o.clusterName),
				Item:        BuildUFarm,
				RegistryURL: fmt.Sprintf("registry.%s.ci.openshift.org", o.clusterName),
			})
		}
	}
}

func generateRegistryPullCredsAllSecrets(c *secretbootstrap.Config, o options) {
	items := []secretbootstrap.DockerConfigJSONData{
		{
			AuthField:   "token_image-puller_ci_reg_auth_value.txt",
			Item:        BuildUFarm,
			RegistryURL: "registry.svc.ci.openshift.org",
		},
		{
			AuthField:   Auth,
			Item:        "cloud.openshift.com-pull-secret",
			RegistryURL: "cloud.openshift.com",
			EmailField:  Email,
		},
		{
			AuthField:   Auth,
			Item:        "quay.io-pull-secret",
			RegistryURL: "quay.io",
			EmailField:  Email,
		},
		{
			AuthField:   Auth,
			Item:        "registry.connect.redhat.com-pull-secret",
			RegistryURL: "registry.connect.redhat.com",
			EmailField:  Email,
		},
		{
			AuthField:   Auth,
			Item:        "registry.redhat.io-pull-secret",
			RegistryURL: "registry.redhat.io",
			EmailField:  Email,
		},
	}
	for _, cluster := range c.UserSecretsTargetClusters {
		if cluster != Hive {
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
			generateDCJSecretConfigTo(RegPullCredsAll, fmt.Sprintf("%s-%s", Test, Credentials), o.clusterName),
		},
	}
	fmt.Printf("Creating new secret with 'to' of: %v\n", sc.To)
	c.Secrets = append(c.Secrets, sc)
}

func getRegistryUrlFor(cluster string) string {
	switch cluster {
	case Vsphere:
		return "registry.apps.build01-us-west-2.vmc.ci.openshift.org"
	case AppDotCi:
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
	buildFarmCreds, err := findSecretConfig(fmt.Sprintf("%s-%s", BuildFarm, Credentials), AppDotCi, c.Secrets)
	check(err)
	for _, s := range []string{ConfigUpdater, Crier, Deck, Hook, ProwControllerManager, Sinker} {
		keyAndField := fmt.Sprintf("%s.%s.%s.%s", Sa, s, o.clusterName, Config)
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
