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
)

type KubeClientGetter func() (ctrlruntimeclient.Client, error)

type dexStep struct {
	log               *logrus.Entry
	releaseRepo       string
	clusterName       string
	redirectURIs      map[string]string
	kubeClient        KubeClientGetter
	readDexManifests  func(path string) (string, error)
	writeDexManifests func(path string, manifests []byte) error
}

// FIXME: this is a workaround; the real type from dex repository can't be imported because
// it has been placed inside the main package and Golang doesn't allow to import it.
// https://github.com/dexidp/dex/blob/447b68845a89f3e624eddbb4f4fd54358c8cc80d/cmd/dex/config.go#L24-L52
type dexConfig map[string]interface{}

func (s *dexStep) Name() string {
	return "dex-manifests"
}

func (s *dexStep) Run(ctx context.Context) error {
	dexManifestsPath := path.Join(s.releaseRepo, dexManifests)
	dexManifests, err := s.readDexManifests(dexManifestsPath)
	if err != nil {
		return err
	}

	manifestsSplit := strings.Split(dexManifests, "---")
	deployIdx := -1
	var deploy appsv1.Deployment
	for i := range manifestsSplit {
		deploy = appsv1.Deployment{}
		if err := yaml.Unmarshal([]byte(manifestsSplit[i]), &deploy); err != nil {
			return fmt.Errorf("unmarshal: %w", err)
		}
		if deploy.Kind == "Deployment" {
			deployIdx = i
			break
		}
	}

	if deployIdx == -1 {
		return errors.New("deployment not found")
	}

	dexConfig, err := unmarshalDexConfig(&deploy)
	if err != nil {
		return err
	}

	if err := s.updateDexConfig(ctx, dexConfig); err != nil {
		return err
	}

	if len(deploy.Spec.Template.Spec.Containers) > 0 {
		s.updateEnvVar(&deploy.Spec.Template.Spec.Containers[0], s.clusterName)
	} else {
		return fmt.Errorf("no containers spec found in %s", dexManifestsPath)
	}

	if err := marshalDexConfig(&deploy, dexConfig); err != nil {
		return err
	}

	raw, err := yaml.Marshal(deploy)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	newline := "\n"
	if deployIdx == 0 {
		newline = ""
	}
	manifestsSplit[deployIdx] = newline + string(raw)
	dexManifestsRaw := strings.Join(manifestsSplit, "---")

	if err := s.writeDexManifests(dexManifestsPath, []byte(dexManifestsRaw)); err != nil {
		return err
	}
	return nil
}

func (s *dexStep) updateDexConfig(ctx context.Context, config dexConfig) error {
	redirectURI, err := s.redirectURI(ctx)
	if err != nil {
		return fmt.Errorf("redirect uri: %w", err)
	}

	clusterNameUpper := strings.ToUpper(s.clusterName)
	target := map[string]interface{}{
		"idEnv":        clusterNameUpper + "-ID",
		"name":         s.clusterName,
		"redirectURIs": []string{redirectURI},
		"secretEnv":    clusterNameUpper + "-SECRET",
	}
	clients, ok := config["staticClients"]
	if !ok {
		s.log.Info("static client stanza not found, adding")
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
			s.log.Info("static client found, updating")
			return nil
		}
	}
	s.log.Info("static client found, adding a new one")
	clientsSlice = append(clientsSlice, target)
	config["staticClients"] = clientsSlice
	return nil
}

func (s *dexStep) updateEnvVar(c *corev1.Container, clusterName string) {
	upsert := func(targetEnv corev1.EnvVar) {
		log := s.log.WithField("env", targetEnv.Name)
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

func (s *dexStep) redirectURI(ctx context.Context) (string, error) {
	ru, found := s.redirectURIs[s.clusterName]
	if found {
		return ru, nil
	}

	client, err := s.kubeClient()
	if err != nil {
		return "", fmt.Errorf("kube client: %w", err)
	}

	oauthRoute := routev1.Route{}
	if err := client.Get(ctx, types.NamespacedName{Namespace: "openshift-authentication", Name: "oauth-openshift"}, &oauthRoute); err != nil {
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

func writeDexManifests(path string, manifests []byte) error {
	if err := os.WriteFile(path, manifests, 0644); err != nil {
		return fmt.Errorf("write file %s: %w", path, err)
	}
	return nil
}

func NewDexStep(log *logrus.Entry, kubeClient KubeClientGetter, releaseRepo, clusterName string,
	redirectURIs map[string]string) *dexStep {
	return &dexStep{
		log:               log,
		kubeClient:        kubeClient,
		releaseRepo:       releaseRepo,
		clusterName:       clusterName,
		redirectURIs:      redirectURIs,
		readDexManifests:  readDexManifests,
		writeDexManifests: writeDexManifests,
	}
}
