package onboard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/sirupsen/logrus"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	routev1 "github.com/openshift/api/route/v1"

	"github.com/openshift/ci-tools/pkg/clusterinit/clusterinstall"
	cinitmanifest "github.com/openshift/ci-tools/pkg/clusterinit/manifest"
	cinittypes "github.com/openshift/ci-tools/pkg/clusterinit/types"
)

type dexGenerator struct {
	clusterInstall   *clusterinstall.ClusterInstall
	kubeClient       ctrlruntimeclient.Client
	readDexManifests func(path string) (string, error)
}

// FIXME: this is a workaround; the real type from dex repository can't be imported because
// it has been placed inside the main package and Golang doesn't allow to import it.
// https://github.com/dexidp/dex/blob/447b68845a89f3e624eddbb4f4fd54358c8cc80d/cmd/dex/config.go#L24-L52
type dexConfig map[string]interface{}

func (s *dexGenerator) Name() string {
	return "dex-manifests"
}

func (s *dexGenerator) Skip() cinittypes.SkipStep {
	return s.clusterInstall.Onboard.Dex.SkipStep
}

func (s *dexGenerator) ExcludedManifests() cinittypes.ExcludeManifest {
	return s.clusterInstall.Onboard.Dex.ExcludeManifest
}

func (s *dexGenerator) Patches() []cinitmanifest.Patch {
	return s.clusterInstall.Onboard.Dex.Patches
}

func (s *dexGenerator) Generate(ctx context.Context, log *logrus.Entry) (map[string][]interface{}, error) {
	dexManifestsPath := path.Join(s.clusterInstall.Onboard.ReleaseRepo, dexManifests)
	dexManifests, err := s.readDexManifests(dexManifestsPath)
	if err != nil {
		return nil, err
	}

	manifestsSplit := strings.Split(dexManifests, "---")
	deploy, deployIdx := appsv1.Deployment{}, -1
	manifests := make([]interface{}, 0, len(manifestsSplit))
	for i := range manifestsSplit {
		m := map[string]interface{}{}
		if err := yaml.Unmarshal([]byte(manifestsSplit[i]), &m); err != nil {
			return nil, fmt.Errorf("unmarshal: %w", err)
		}

		manifests = append(manifests, m)

		if kind, ok := m["kind"]; ok && kind == "Deployment" {
			deployIdx = i
			if err := yaml.Unmarshal([]byte(manifestsSplit[i]), &deploy); err != nil {
				return nil, fmt.Errorf("unmarshal: %w", err)
			}
		}
	}

	if deployIdx == -1 {
		return nil, errors.New("deployment not found")
	}

	dexConfig, err := unmarshalDexConfig(&deploy)
	if err != nil {
		return nil, err
	}

	if err := s.updateDexConfig(ctx, log, dexConfig); err != nil {
		return nil, err
	}

	if err := s.updateOIDCConnectorScopes(log, dexConfig); err != nil {
		return nil, err
	}

	if len(deploy.Spec.Template.Spec.Containers) > 0 {
		s.updateEnvVar(&deploy.Spec.Template.Spec.Containers[0], log, s.clusterInstall.ClusterName)
	} else {
		return nil, fmt.Errorf("no containers spec found in %s", dexManifestsPath)
	}

	if err := marshalDexConfig(&deploy, dexConfig); err != nil {
		return nil, err
	}

	pathToManifests := make(map[string][]interface{})
	manifests[deployIdx] = deploy
	pathToManifests[dexManifestsPath] = manifests

	return pathToManifests, nil
}

func (s *dexGenerator) updateDexConfig(ctx context.Context, log *logrus.Entry, config dexConfig) error {
	redirectURI, err := s.redirectURI(ctx)
	if err != nil {
		return fmt.Errorf("redirect uri: %w", err)
	}

	clusterNameUpper := strings.ToUpper(s.clusterInstall.ClusterName)
	target := map[string]interface{}{
		"idEnv":        clusterNameUpper + "-ID",
		"name":         s.clusterInstall.ClusterName,
		"redirectURIs": []string{redirectURI},
		"secretEnv":    clusterNameUpper + "-SECRET",
	}
	clients, ok := config["staticClients"]
	if !ok {
		log.Info("static client stanza not found, adding")
		config["staticClients"] = []interface{}{target}
		return nil
	}
	clientsSlice, ok := clients.([]interface{})
	if !ok {
		return errors.New("cannot cast staticClients to a slice")
	}
	for i := range clientsSlice {
		c, ok := clientsSlice[i].(map[string]interface{})
		if !ok {
			return errors.New("cannot cast a staticClient to a map")
		}
		name := c["name"]
		if name == target["name"] {
			clientsSlice[i] = target
			log.Info("static client found, updating")
			return nil
		}
	}
	log.Info("static client found, adding a new one")
	clientsSlice = append(clientsSlice, target)
	config["staticClients"] = clientsSlice
	return nil
}

func (s *dexGenerator) updateOIDCConnectorScopes(log *logrus.Entry, config dexConfig) error {
	connectors, ok := config["connectors"]
	if !ok {
		log.Info("connectors stanza not found, skipping scope update")
		return nil
	}
	connectorsSlice, ok := connectors.([]interface{})
	if !ok {
		return errors.New("cannot cast connectors to a slice")
	}
	for i := range connectorsSlice {
		connector, ok := connectorsSlice[i].(map[string]interface{})
		if !ok {
			continue
		}
		connectorID, ok := connector["id"]
		if !ok || connectorID != "sso" {
			continue
		}
		connectorConfig, ok := connector["config"].(map[string]interface{})
		if !ok {
			continue
		}
		// Ensure required scopes are present: openid, profile, email
		requiredScopes := []string{"openid", "profile", "email"}
		scopes, ok := connectorConfig["scopes"]
		if !ok {
			scopesList := make([]interface{}, len(requiredScopes))
			for i, s := range requiredScopes {
				scopesList[i] = s
			}
			connectorConfig["scopes"] = scopesList
			log.Info("Added scopes to SSO OIDC connector")
			return nil
		}
		scopesSlice, ok := scopes.([]interface{})
		if !ok {
			scopesList := make([]interface{}, len(requiredScopes))
			for i, s := range requiredScopes {
				scopesList[i] = s
			}
			connectorConfig["scopes"] = scopesList
			log.Info("Replaced invalid scopes in SSO OIDC connector")
			return nil
		}
		// Check which required scopes are missing
		existingScopes := make(map[string]bool)
		for _, scope := range scopesSlice {
			if s, ok := scope.(string); ok {
				existingScopes[s] = true
			}
		}
		updated := false
		for _, required := range requiredScopes {
			if !existingScopes[required] {
				scopesSlice = append(scopesSlice, required)
				updated = true
			}
		}
		if updated {
			connectorConfig["scopes"] = scopesSlice
			log.Info("Added missing scopes to SSO OIDC connector")
		}
		return nil
	}
	log.Info("SSO connector not found, skipping scope update")
	return nil
}

func (s *dexGenerator) updateEnvVar(c *corev1.Container, log *logrus.Entry, clusterName string) {
	upsert := func(targetEnv corev1.EnvVar) {
		log := log.WithField("env", targetEnv.Name)
		for i := range c.Env {
			if c.Env[i].Name == targetEnv.Name {
				log.Info("Env variable found, updating")
				c.Env[i] = targetEnv
				return
			}
		}
		log.Info("Env variable not found, adding a new one")
		c.Env = append(c.Env, targetEnv)
	}
	upsert(corev1.EnvVar{
		Name: strings.ToUpper(clusterName) + "-ID",
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				Key: clusterName + "-id",
				LocalObjectReference: corev1.LocalObjectReference{
					Name: clusterName + "-secret",
				}}}})
	upsert(corev1.EnvVar{
		Name: strings.ToUpper(clusterName) + "-SECRET",
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				Key: clusterName + "-secret",
				LocalObjectReference: corev1.LocalObjectReference{
					Name: clusterName + "-secret",
				}}}})
}

func (s *dexGenerator) redirectURI(ctx context.Context) (string, error) {
	oauthRoute := routev1.Route{}
	if err := s.kubeClient.Get(ctx, types.NamespacedName{Namespace: "openshift-authentication", Name: "oauth-openshift"}, &oauthRoute); err != nil {
		return "", fmt.Errorf("get route: %w", err)
	}

	return fmt.Sprintf("https://%s/oauth2callback/RedHat_Internal_SSO", oauthRoute.Spec.Host), nil
}

func unmarshalDexConfig(deploy *appsv1.Deployment) (dexConfig, error) {
	dexConfigRaw, exists := deploy.Spec.Template.Annotations["config.yaml"]
	if !exists {
		return nil, errors.New("dex config not found")
	}
	dexConfig := dexConfig{}
	if err := yaml.Unmarshal([]byte(dexConfigRaw), &dexConfig); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	return dexConfig, nil
}

func marshalDexConfig(deploy *appsv1.Deployment, dexConfig dexConfig) error {
	dexConfigMarshaled, err := yaml.Marshal(dexConfig)
	if err != nil {
		return fmt.Errorf("marshal dex config: %w", err)
	}
	deploy.Spec.Template.Annotations["config.yaml"] = string(dexConfigMarshaled)
	return nil
}

func readDexManifests(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read file %s: %w", path, err)
	}
	return string(data), nil
}

func NewDexGenerator(kubeClient ctrlruntimeclient.Client, clusterInstall *clusterinstall.ClusterInstall) *dexGenerator {
	return &dexGenerator{
		kubeClient:       kubeClient,
		clusterInstall:   clusterInstall,
		readDexManifests: readDexManifests,
	}
}
