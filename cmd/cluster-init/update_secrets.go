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
)

func updateCiSecretBootstrapConfig(o options) {
	ciSecBootFile := filepath.Join(o.releaseRepo, "core-services", "ci-secret-bootstrap", "_config.yaml")
	c := &secretbootstrap.Config{}
	loadConfig(ciSecBootFile, c)
	for _, groupName := range []string{"build_farm", "non_app_ci", "non_app_ci_x86"} {
		c.ClusterGroups[groupName] = append(c.ClusterGroups[groupName], o.clusterName)
	}

	updatePodScalerSecret(c, o)
	updateBuildFarmSecret(c, o)
	appendSecret(generateCiOperatorSecret, c, o)
	updateDPTPConManSecret(c, o)
	updateRehearseSecret(c, o)
	updateChatBotSecret(c, o)
	appendSecret(generateRegistryPushCredsSecret, c, o)
	appendSecret(generateRegistryPullCredsSecret, c, o)
	updateRegPullCredsAllSecret(c, o)
	saveConfig(ciSecBootFile, c)
}

func appendSecret(generateFunction func(options) secretbootstrap.SecretConfig, c *secretbootstrap.Config, o options) {
	secret := generateFunction(o)
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
	//TODO: will the following still function???
	sc := secretbootstrap.SecretConfig{
		From: map[string]secretbootstrap.ItemContext{
			".dockerconfigjson": from,
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
	//TODO: will the following still function???
	sc := secretbootstrap.SecretConfig{
		From: map[string]secretbootstrap.ItemContext{
			".dockerconfigjson": from,
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
	appendSecretFromFieldItem(c, name, key, secretbootstrap.ItemContext{
		Field: field,
		Item:  PodScaler,
	})
}

func updateDPTPConManSecret(c *secretbootstrap.Config, o options) {
	key := fmt.Sprintf("%s.%s", o.clusterName, strings.ToLower(Kubeconfig))
	field := fmt.Sprintf("%s.%s.%s.%s", Sa, DPTPControllerManager, o.clusterName, Config)
	appendSecretFromFieldItem(c, DPTPControllerManager, key, secretbootstrap.ItemContext{
		Field: field,
		Item:  BuildFarm,
	})
}

func updateRehearseSecret(c *secretbootstrap.Config, o options) {
	sc, err := findSecretConfig(PjRehearse, "", c.Secrets)
	check(err)
	keyAndField := fmt.Sprintf("%s.%s.%s.%s", Sa, CiOperator, o.clusterName, Config)
	sc.From[keyAndField] = secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  BuildFarm,
	}
}

func updateChatBotSecret(c *secretbootstrap.Config, o options) {
	name := fmt.Sprintf("%s-%s-%s", Ci, ChatBot, Kubeconfigs)
	keyAndField := fmt.Sprintf("%s.%s-%s.%s.%s", Sa, Ci, ChatBot, o.clusterName, Config)
	item := fmt.Sprintf("%s-%s", Ci, ChatBot)
	appendSecretFromFieldItem(c, name, keyAndField, secretbootstrap.ItemContext{
		Field: keyAndField,
		Item:  item,
	})
}

func appendSecretFromFieldItem(c *secretbootstrap.Config, name string, key string, value secretbootstrap.ItemContext) {
	sc, err := findSecretConfig(name, AppDotCi, c.Secrets)
	check(err)
	sc.From[key] = value
}

func updateRegPullCredsAllSecret(c *secretbootstrap.Config, o options) {
	appendRegistrySecretFromItem(c, RegPullCredsAll, secretbootstrap.DockerConfigJSONData{
		AuthField:   "token_image-puller_build03_reg_auth_value.txt",
		Item:        BuildUFarm,
		RegistryURL: "registry.build03.ci.openshift.org",
	})
}

func appendRegistrySecretFromItem(c *secretbootstrap.Config, name string, value secretbootstrap.DockerConfigJSONData) {
	sc, err := findSecretConfig(name, AppDotCi, c.Secrets)
	check(err)
	data := append(sc.From[".dockerconfigjson"].DockerConfigJSONData, value)
	sc.From[".dockerconfigjson"] = secretbootstrap.ItemContext{
		DockerConfigJSONData: data,
	}
}

func updateBuildFarmSecret(c *secretbootstrap.Config, o options) {
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
	return &secretbootstrap.SecretConfig{}, fmt.Errorf("couldn't find SecretConfig with name: %s", name)
}
