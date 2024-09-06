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

	"github.com/openshift/ci-tools/pkg/clustermgmt"
)

type dexStep struct {
	log               *logrus.Entry
	getClusterInstall clustermgmt.ClusterInstallGetter
	kubeClient        ctrlruntimeclient.Client
	readDexManifests  func(path string) (string, error)
	writeDexManifests func(path string, manifests []byte) error
}

type dexStaticClient struct {
	Name         string   `json:"name" yaml:"name"`
	IDEnv        string   `json:"idEnv" yaml:"idEnv"`
	SecretEnv    string   `json:"secretEnv" yaml:"secretEnv"`
	RedirectURIs []string `json:"redirectURIs" yaml:"redirectURIs"`
}

type dexConfig struct {
	StaticClients []dexStaticClient `json:"staticClients"`
}

func (s *dexStep) Name() string {
	return "dex-manifests"
}

func (s *dexStep) Run(ctx context.Context) error {
	ci, err := s.getClusterInstall()
	if err != nil {
		return fmt.Errorf("get cluster install: %w", err)
	}

	dexManifestsPath := path.Join(ci.Onboard.ReleaseRepo, dexManifests)
	dexManifests, err := s.readDexManifests(dexManifestsPath)
	if err != nil {
		return err
	}

	manifestsSplit := strings.Split(string(dexManifests), "---")
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

	if err := s.updateDexConfig(ctx, dexConfig, ci.ClusterName); err != nil {
		return err
	}

	if len(deploy.Spec.Template.Spec.Containers) > 0 {
		s.updateEnvVar(&deploy.Spec.Template.Spec.Containers[0], ci.ClusterName)
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

func (s *dexStep) updateDexConfig(ctx context.Context, config *dexConfig, clusterName string) error {
	oauthEndpoint, err := getOAuthEndpoint(ctx, s.kubeClient)
	if err != nil {
		return fmt.Errorf("get oauth endopoint: %w", err)
	}

	redirectURI := fmt.Sprintf("https://%s/oauth2callback/RedHat_Internal_SSO", oauthEndpoint)
	clusterNameUpper := strings.ToUpper(clusterName)
	target := dexStaticClient{
		IDEnv:        clusterNameUpper + "-ID",
		Name:         clusterName,
		RedirectURIs: []string{redirectURI},
		SecretEnv:    clusterNameUpper + "-SECRET",
	}
	for i := range config.StaticClients {
		if config.StaticClients[i].Name == target.Name {
			config.StaticClients[i] = target
			s.log.Info("static client found, updating")
			return nil
		}
	}
	s.log.Info("static client found, adding a new one")
	config.StaticClients = append(config.StaticClients, target)
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

func getOAuthEndpoint(ctx context.Context, client ctrlruntimeclient.Client) (string, error) {
	oauthRoute := routev1.Route{}
	if err := client.Get(ctx, types.NamespacedName{Namespace: "openshift-authentication", Name: "oauth-openshift"}, &oauthRoute); err != nil {
		return "", fmt.Errorf("get route: %w", err)
	}
	return oauthRoute.Spec.Host, nil
}

func unmarshalDexConfig(deploy *appsv1.Deployment) (*dexConfig, error) {
	dexConfigRaw, exists := deploy.Spec.Template.Annotations["config.yaml"]
	if !exists {
		return nil, errors.New("dex config not found")
	}
	dexConfig := dexConfig{}
	if err := yaml.Unmarshal([]byte(dexConfigRaw), &dexConfig); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	return &dexConfig, nil
}

func marshalDexConfig(deploy *appsv1.Deployment, dexConfig *dexConfig) error {
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
	if err := os.WriteFile(path, []byte(manifests), 0644); err != nil {
		return fmt.Errorf("write file %s: %w", path, err)
	}
	return nil
}

func NewDexStep(log *logrus.Entry, getClusterInstall clustermgmt.ClusterInstallGetter,
	kubeClient ctrlruntimeclient.Client) *dexStep {
	return &dexStep{
		log:               log,
		getClusterInstall: getClusterInstall,
		kubeClient:        kubeClient,
		readDexManifests:  readDexManifests,
		writeDexManifests: writeDexManifests,
	}
}
