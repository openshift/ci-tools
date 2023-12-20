package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubejson "k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes/scheme"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/logrusutil"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/api/secretbootstrap"
	"github.com/openshift/ci-tools/pkg/api/secretgenerator"
	vaultapi "github.com/openshift/ci-tools/pkg/api/vault"
	"github.com/openshift/ci-tools/pkg/kubernetes/pkg/credentialprovider"
	"github.com/openshift/ci-tools/pkg/prowconfigutils"
	"github.com/openshift/ci-tools/pkg/secrets"
)

type options struct {
	secrets secrets.CLIOptions

	dryRun             bool
	force              bool
	validateItemsUsage bool
	confirm            bool

	kubernetesOptions   flagutil.KubernetesOptions
	configPath          string
	generatorConfigPath string
	cluster             string
	secretNamesRaw      flagutil.Strings
	logLevel            string
	impersonateUser     string

	secretsGetters  map[string]Getter
	config          secretbootstrap.Config
	generatorConfig secretgenerator.Config

	allowUnused flagutil.Strings

	validateOnly bool
}

const (
	// When checking for unused secrets in BitWarden, only report secrets that were last modified before X days, allowing to set up
	// BitWarden items and matching bootstrap config without tripping an alert
	allowUnusedDays = 7
)

func parseOptions(censor *secrets.DynamicCensor) (options, error) {
	o := options{kubernetesOptions: flagutil.KubernetesOptions{NOInClusterConfigDefault: true}}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	o.allowUnused = flagutil.NewStrings()
	fs.BoolVar(&o.validateOnly, "validate-only", false, "If set, the tool exists after validating its config file.")
	fs.Var(&o.allowUnused, "bw-allow-unused", "One or more items that will be ignored when the --validate-items-usage is specified")
	fs.BoolVar(&o.validateItemsUsage, "validate-bitwarden-items-usage", false, fmt.Sprintf("If set, the tool only validates if all fields that exist in Vault and were last modified before %d days ago are being used in the given config.", allowUnusedDays))
	fs.BoolVar(&o.dryRun, "dry-run", true, "Whether to actually create the secrets with oc command")
	fs.BoolVar(&o.confirm, "confirm", true, "Whether to mutate the actual secrets in the targeted clusters")
	o.kubernetesOptions.AddFlags(fs)
	fs.StringVar(&o.configPath, "config", "", "Path to the config file to use for this tool.")
	fs.StringVar(&o.generatorConfigPath, "generator-config", "", "Path to the secret-generator config file.")
	fs.StringVar(&o.cluster, "cluster", "", "If set, only provision secrets for this cluster")
	fs.Var(&o.secretNamesRaw, "secret-names", "If set, only provision secrets with the given name. user_secrets_target_clusters in the configuration is ignored. Can be passed multiple times.")
	fs.BoolVar(&o.force, "force", false, "If true, update the secrets even if existing one differs from Bitwarden items instead of existing with error. Default false.")
	fs.StringVar(&o.logLevel, "log-level", "info", fmt.Sprintf("Log level is one of %v.", logrus.AllLevels))
	fs.StringVar(&o.impersonateUser, "as", "", "Username to impersonate")
	o.secrets.Bind(fs, os.Getenv, censor)
	if err := fs.Parse(os.Args[1:]); err != nil {
		return options{}, err
	}
	return o, nil
}

func (o *options) validateOptions() error {
	var errs []error
	level, err := logrus.ParseLevel(o.logLevel)
	if err != nil {
		errs = append(errs, fmt.Errorf("invalid log level specified: %w", err))
	}
	logrus.SetLevel(level)
	errs = append(errs, o.secrets.Validate())
	if o.configPath == "" {
		errs = append(errs, errors.New("--config is required"))
	}
	if len(o.allowUnused.Strings()) > 0 && !o.validateItemsUsage {
		errs = append(errs, errors.New("--bw-allow-unused must be specified with --validate-items-usage"))
	}
	errs = append(errs, o.kubernetesOptions.Validate(o.dryRun))
	return utilerrors.NewAggregate(errs)
}

func (o *options) completeOptions(censor *secrets.DynamicCensor, kubeConfigs map[string]rest.Config, disabledClusters sets.Set[string]) error {
	if err := o.secrets.Complete(censor); err != nil {
		return err
	}

	if err := secretbootstrap.LoadConfigFromFile(o.configPath, &o.config); err != nil {
		return err
	}

	if vals := o.secretNamesRaw.Strings(); len(vals) > 0 {
		secretNames := sets.New[string](vals...)
		logrus.WithField("secretNames", sets.List(secretNames)).Info("pruning irrelevant configuration ...")
		pruneIrrelevantConfiguration(&o.config, secretNames)
		logrus.WithField("secretNames", sets.List(secretNames)).WithField("o.config.Secrets", o.config.Secrets).Info("pruned irrelevant configuration")
	}

	if o.generatorConfigPath != "" {
		var err error
		o.generatorConfig, err = secretgenerator.LoadConfigFromPath(o.generatorConfigPath)
		if err != nil {
			return err
		}
	}

	if !o.validateOnly {
		if o.impersonateUser != "" {
			for _, kubeConfig := range kubeConfigs {
				kubeConfig.Impersonate = rest.ImpersonationConfig{UserName: o.impersonateUser}
			}
		}

	}

	o.secretsGetters = map[string]Getter{}
	var filteredSecrets []secretbootstrap.SecretConfig
	for i, secretConfig := range o.config.Secrets {
		var to []secretbootstrap.SecretContext

		for j, secretContext := range secretConfig.To {
			if disabledClusters.Has(secretContext.Cluster) {
				logrus.WithFields(logrus.Fields{"target-cluster": o.cluster, "secret-cluster": secretContext.Cluster}).Debug("Skipping provisioning of secrets for a cluster that is disabled by Prow")
				continue
			}
			if o.cluster != "" && o.cluster != secretContext.Cluster {
				logrus.WithFields(logrus.Fields{"target-cluster": o.cluster, "secret-cluster": secretContext.Cluster}).Debug("Skipping provisioning of secrets for a cluster that does not match the one configured via --cluster")
				continue
			}
			to = append(to, secretContext)

			if !o.validateOnly {
				if o.secretsGetters[secretContext.Cluster] == nil {
					kc, ok := kubeConfigs[secretContext.Cluster]
					if !ok {
						return fmt.Errorf("config[%d].to[%d]: failed to find cluster context %q in the kubeconfig", i, j, secretContext.Cluster)
					}
					client, err := coreclientset.NewForConfig(&kc)
					if err != nil {
						return err
					}
					o.secretsGetters[secretContext.Cluster] = client
				}
			}
		}

		if len(to) > 0 {
			secretConfig.To = to
			filteredSecrets = append(filteredSecrets, secretConfig)
		}
	}
	o.config.Secrets = filteredSecrets

	return o.validateCompletedOptions()
}

func pruneIrrelevantConfiguration(c *secretbootstrap.Config, secretNames sets.Set[string]) {
	var secretConfigs []secretbootstrap.SecretConfig
	for _, secretConfig := range c.Secrets {
		for _, secretContext := range secretConfig.To {
			if secretNames.Has(secretContext.Name) {
				secretConfigs = append(secretConfigs, secretConfig)
				break
			}
		}
	}
	c.Secrets = secretConfigs
	c.UserSecretsTargetClusters = nil
}

func (o *options) validateCompletedOptions() error {
	if err := o.config.Validate(); err != nil {
		return fmt.Errorf("failed to validate the config: %w", err)
	}
	toMap := map[string]map[string]string{}
	for i, secretConfig := range o.config.Secrets {
		if len(secretConfig.From) == 0 {
			return fmt.Errorf("config[%d].from is empty", i)
		}
		if len(secretConfig.To) == 0 {
			return fmt.Errorf("config[%d].to is empty", i)
		}
		for key, itemContext := range secretConfig.From {
			if key == "" {
				return fmt.Errorf("config[%d].from: empty key is not allowed", i)
			}

			if itemContext.Item == "" && len(itemContext.DockerConfigJSONData) == 0 {
				return fmt.Errorf("config[%d].from[%s]: empty value is not allowed", i, key)
			}

			if itemContext.Item != "" && len(itemContext.DockerConfigJSONData) > 0 {
				return fmt.Errorf("config[%d].from[%s]: both bitwarden dockerconfigJSON items are not allowed.", i, key)
			}

			if len(itemContext.DockerConfigJSONData) > 0 {
				for _, data := range itemContext.DockerConfigJSONData {
					if data.Item == "" {
						return fmt.Errorf("config[%d].from[%s]: item is missing", i, key)
					}
					if data.RegistryURL == "" {
						return fmt.Errorf("config[%d].from[%s]: registry_url must be set", i, key)
					}

					if data.AuthField == "" {
						return fmt.Errorf("config[%d].from[%s]: auth_field is missing", i, key)
					}
				}
			} else if itemContext.Item != "" {
				if itemContext.Field == "" {
					return fmt.Errorf("config[%d].from[%s]: field must be set", i, key)
				}
			}
		}
		for j, secretContext := range secretConfig.To {
			if secretContext.Cluster == "" {
				return fmt.Errorf("config[%d].to[%d].cluster: empty value is not allowed", i, j)
			}
			if secretContext.Namespace == "" {
				return fmt.Errorf("config[%d].to[%d].namespace: empty value is not allowed", i, j)
			}
			if secretContext.Name == "" {
				return fmt.Errorf("config[%d].to[%d].name: empty value is not allowed", i, j)
			}

			if toMap[secretContext.Cluster] == nil {
				toMap[secretContext.Cluster] = map[string]string{secretContext.Namespace: secretContext.Name}
			} else if toMap[secretContext.Cluster][secretContext.Namespace] != secretContext.Name {
				toMap[secretContext.Cluster][secretContext.Namespace] = secretContext.Name
			} else {
				return fmt.Errorf("config[%d].to[%d]: secret %s listed more than once in the config", i, j, secretContext)
			}
		}
	}
	return nil
}

func constructDockerConfigJSON(client secrets.ReadOnlyClient, dockerConfigJSONData []secretbootstrap.DockerConfigJSONData) ([]byte, error) {
	auths := make(map[string]secretbootstrap.DockerAuth)

	for _, data := range dockerConfigJSONData {
		authData := secretbootstrap.DockerAuth{}

		authBWAttachmentValue, err := client.GetFieldOnItem(data.Item, data.AuthField)
		if err != nil {
			return nil, fmt.Errorf("couldn't get auth field '%s' from item %s: %w", data.AuthField, data.Item, err)
		}
		authData.Auth = string(bytes.TrimSpace(authBWAttachmentValue))

		if data.EmailField != "" {
			emailValue, err := client.GetFieldOnItem(data.Item, data.EmailField)
			if err != nil {
				return nil, fmt.Errorf("couldn't get email field '%s' from item %s: %w", data.EmailField, data.Item, err)
			}
			authData.Email = string(emailValue)
		}

		auths[data.RegistryURL] = authData
	}

	b, err := json.Marshal(&secretbootstrap.DockerConfigJSON{Auths: auths})
	if err != nil {
		return nil, fmt.Errorf("couldn't marshal to json %w", err)
	}

	if err := json.Unmarshal(b, &credentialprovider.DockerConfigJSON{}); err != nil {
		return nil, fmt.Errorf("the constructed dockerconfigJSON doesn't parse: %w", err)
	}

	return b, nil
}

func constructSecrets(config secretbootstrap.Config, client secrets.ReadOnlyClient, prowDisabledClusters sets.Set[string]) (map[string][]*coreapi.Secret, error) {
	secretsByClusterAndName := map[string]map[types.NamespacedName]coreapi.Secret{}
	secretsMapLock := &sync.Mutex{}

	var potentialErrors int
	for _, item := range config.Secrets {
		potentialErrors = potentialErrors + len(item.From)
	}
	errChan := make(chan error, potentialErrors)

	secretConfigWG := &sync.WaitGroup{}
	for idx, cfg := range config.Secrets {
		idx := idx
		secretConfigWG.Add(1)

		cfg := cfg
		go func() {
			defer secretConfigWG.Done()

			data := make(map[string][]byte)
			var keys []string
			for key := range cfg.From {
				keys = append(keys, key)
			}
			sort.Strings(keys)

			keyWg := sync.WaitGroup{}
			dataLock := &sync.Mutex{}
			keyWg.Add(len(keys))
			for _, key := range keys {

				key := key
				go func() {
					defer keyWg.Done()
					itemContext := cfg.From[key]
					var value []byte
					var err error
					if itemContext.Field != "" {
						value, err = client.GetFieldOnItem(itemContext.Item, itemContext.Field)
					} else if len(itemContext.DockerConfigJSONData) > 0 {
						value, err = constructDockerConfigJSON(client, itemContext.DockerConfigJSONData)
					}
					if err != nil {
						errChan <- fmt.Errorf("config.%d.\"%s\": %w", idx, key, err)
						return
					}
					if cfg.From[key].Base64Decode {
						decoded, err := base64.StdEncoding.DecodeString(string(value))
						if err != nil {
							errChan <- fmt.Errorf(`failed to base64-decode config.%d."%s": %w`, idx, key, err)
							return
						}
						value = decoded
					}
					dataLock.Lock()
					data[key] = value
					dataLock.Unlock()

				}()
			}
			// We copy the data map to not have multiple secrets with the same inner data map. This implies
			// that we need to wait for that map to be fully populated.
			keyWg.Wait()

			for _, secretContext := range cfg.To {
				if prowDisabledClusters.Has(secretContext.Cluster) {
					logrus.WithField("cluster", secretContext.Cluster).Info("Skipped constructing of secrets on a Prow disabled cluster")
					continue
				}
				if secretContext.Type == "" {
					secretContext.Type = coreapi.SecretTypeOpaque
				}
				secret := coreapi.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      secretContext.Name,
						Namespace: secretContext.Namespace,
						Labels:    map[string]string{api.DPTPRequesterLabel: "ci-secret-bootstrap"},
					},
					Type: secretContext.Type,
				}
				secret.Data = make(map[string][]byte, len(data))
				for k, v := range data {
					secret.Data[k] = v
				}
				secretsMapLock.Lock()
				if _, ok := secretsByClusterAndName[secretContext.Cluster]; !ok {
					secretsByClusterAndName[secretContext.Cluster] = map[types.NamespacedName]coreapi.Secret{}
				}
				secretsByClusterAndName[secretContext.Cluster][types.NamespacedName{Namespace: secret.Namespace, Name: secret.Name}] = secret
				secretsMapLock.Unlock()
			}

		}()
	}
	secretConfigWG.Wait()
	close(errChan)
	var errs []error
	for err := range errChan {
		errs = append(errs, err)
	}

	var err error
	statBefore := generateSecretStats(secretsByClusterAndName)
	logrus.WithField("count", statBefore.count).WithField("median", statBefore.median).Info("Secret stats before fetching user secrets")
	secretsByClusterAndName, err = fetchUserSecrets(secretsByClusterAndName, client, config.UserSecretsTargetClusters)
	if err != nil {
		errs = append(errs, err)
	}
	statAfter := generateSecretStats(secretsByClusterAndName)
	logrus.WithField("count", statAfter.count).WithField("median", statAfter.median).Info("Secret stats after fetching user secrets")

	result := map[string][]*coreapi.Secret{}
	for cluster, secretMap := range secretsByClusterAndName {
		for _, secret := range secretMap {
			result[cluster] = append(result[cluster], secret.DeepCopy())
		}
	}

	sort.Slice(errs, func(i, j int) bool {
		return errs[i] != nil && errs[j] != nil && errs[i].Error() < errs[j].Error()
	})
	return result, utilerrors.NewAggregate(errs)
}

func fetchUserSecrets(secretsMap map[string]map[types.NamespacedName]coreapi.Secret, secretStoreClient secrets.ReadOnlyClient, targetClusters []string) (map[string]map[types.NamespacedName]coreapi.Secret, error) {
	if len(targetClusters) == 0 {
		logrus.Warn("No target clusters for user secrets configured, skipping...")
		return secretsMap, nil
	}

	userSecrets, err := secretStoreClient.GetUserSecrets()
	if err != nil {
		return secretsMap, err
	}

	if len(userSecrets) == 0 {
		logrus.Warn("No user secrets found")
		return secretsMap, nil
	}

	var errs []error
	for secretName, secretKeys := range userSecrets {
		logger := logrus.WithField("secret", secretName.String())
		for _, cluster := range targetClusters {
			if !vaultapi.TargetsCluster(cluster, secretKeys) {
				continue
			}
			logger = logger.WithField("cluster", cluster)
			if _, ok := secretsMap[cluster]; !ok {
				secretsMap[cluster] = map[types.NamespacedName]coreapi.Secret{}
			}
			entry, alreadyExists := secretsMap[cluster][secretName]
			if !alreadyExists {
				entry = coreapi.Secret{
					ObjectMeta: metav1.ObjectMeta{Namespace: secretName.Namespace, Name: secretName.Name, Labels: map[string]string{api.DPTPRequesterLabel: "ci-secret-bootstrap"}},
					Data:       map[string][]byte{},
					Type:       coreapi.SecretTypeOpaque,
				}
			}
			if entry.Type != coreapi.SecretTypeOpaque {
				errs = append(errs, fmt.Errorf("secret %s in cluster %s has ci-secret-bootstrap config as non-opaque type and is targeted by user sync from key %s", secretName.String(), cluster, secretKeys[vaultapi.VaultSourceKey]))
				continue
			}
			for vaultKey, vaultValue := range secretKeys {
				if vaultKey == vaultapi.SecretSyncTargetClusterKey {
					continue
				}
				if _, alreadyExists := entry.Data[vaultKey]; alreadyExists {
					errs = append(errs, fmt.Errorf("key %s in secret %s in cluster %s is targeted by ci-secret-bootstrap config and by vault item in path %s", vaultKey, secretName.String(), cluster, secretKeys[vaultapi.VaultSourceKey]))
					continue
				}
				entry.Data[vaultKey] = []byte(vaultValue)
				logger.WithField("key", vaultKey).Debug("Populating key from Vault data.")
			}
			secretsMap[cluster][secretName] = entry
		}
	}

	return secretsMap, utilerrors.NewAggregate(errs)
}

type Getter interface {
	coreclientset.SecretsGetter
	coreclientset.NamespacesGetter
}

func updateSecrets(getters map[string]Getter, secretsMap map[string][]*coreapi.Secret, force bool, confirm bool, osdGlobalPullSecretGroup sets.Set[string]) error {
	var errs []error

	var dryRunOptions []string
	if !confirm {
		logrus.Warn("No secrets will be mutated")
		dryRunOptions = append(dryRunOptions, "All")
	}

	for cluster, secrets := range secretsMap {
		logger := logrus.WithField("cluster", cluster)
		logger.Debug("Syncing secrets for cluster")
		existingNamespaces := sets.New[string]()
		for _, secret := range secrets {
			logger := logger.WithFields(logrus.Fields{"namespace": secret.Namespace, "name": secret.Name, "type": secret.Type})
			logger.Debug("handling secret")

			if !existingNamespaces.Has(secret.Namespace) {
				nsClient := getters[cluster].Namespaces()
				if _, err := nsClient.Get(context.TODO(), secret.Namespace, metav1.GetOptions{}); err != nil {
					if !kerrors.IsNotFound(err) {
						errs = append(errs, fmt.Errorf("failed to check if namespace %s exists on cluster %s: %w", secret.Namespace, cluster, err))
						continue
					}
					if _, err := nsClient.Create(context.TODO(), &coreapi.Namespace{ObjectMeta: metav1.ObjectMeta{
						Name:   secret.Namespace,
						Labels: map[string]string{api.DPTPRequesterLabel: "ci-secret-bootstrap"},
					}}, metav1.CreateOptions{DryRun: dryRunOptions}); err != nil && !kerrors.IsAlreadyExists(err) {
						errs = append(errs, fmt.Errorf("failed to create namespace %s: %w", secret.Namespace, err))
						continue
					}
				}
				existingNamespaces.Insert(secret.Namespace)
			}

			secretClient := getters[cluster].Secrets(secret.Namespace)

			existingSecret, err := secretClient.Get(context.TODO(), secret.Name, metav1.GetOptions{})

			if secret.Namespace == "openshift-config" && secret.Name == "pull-secret" && osdGlobalPullSecretGroup.Has(cluster) {
				logger.Debug("handling the global pull secret on an OSD cluster")
				if mutated, err := mutateGlobalPullSecret(existingSecret, secret); err != nil {
					errs = append(errs, fmt.Errorf("failed to mutate secret %s:%s/%s: %w", cluster, secret.Namespace, secret.Name, err))
				} else {
					if mutated {
						if _, err := secretClient.Update(context.TODO(), existingSecret, metav1.UpdateOptions{DryRun: dryRunOptions}); err != nil {
							errs = append(errs, fmt.Errorf("error updating global pull secret %s:%s/%s: %w", cluster, existingSecret.Namespace, existingSecret.Name, err))
						}
						logger.Debug("global pull secret updated")
					} else {
						logger.Debug("global pull secret skipped")
					}
				}
				continue
			}

			if err != nil && !kerrors.IsNotFound(err) {
				errs = append(errs, fmt.Errorf("error reading secret %s:%s/%s: %w", cluster, secret.Namespace, secret.Name, err))
				continue
			}

			shouldCreate := false
			if err == nil {
				if secret.Type != existingSecret.Type {
					if !force {
						errs = append(errs, fmt.Errorf("cannot change secret type from %q to %q (immutable field): %s:%s/%s", existingSecret.Type, secret.Type, cluster, secret.Namespace, secret.Name))
						continue
					}
					if err := secretClient.Delete(context.TODO(), secret.Name, metav1.DeleteOptions{DryRun: dryRunOptions}); err != nil {
						errs = append(errs, fmt.Errorf("error deleting secret: %w", err))
						continue
					}
					shouldCreate = true
				}

				if len(secret.Data) > 0 {
					for k := range existingSecret.Data {
						if _, exists := secret.Data[k]; exists {
							continue
						}
						logger.WithFields(logrus.Fields{"cluster": cluster, "key": k, "namespace": existingSecret.Namespace, "secret": existingSecret.Name}).Warning("Stale key in secret will be deleted")
					}
				}

				if !shouldCreate {
					differentData := !equality.Semantic.DeepEqual(secret.Data, existingSecret.Data)
					if !force && differentData {
						logger.Errorf("actual secret data differs the expected")
						errs = append(errs, fmt.Errorf("secret %s:%s/%s needs updating in place, use --force to do so", cluster, secret.Namespace, secret.Name))
						continue
					}
					if existingSecret.Labels == nil || existingSecret.Labels[api.DPTPRequesterLabel] != "ci-secret-bootstrap" || differentData {
						if _, err := secretClient.Update(context.TODO(), secret, metav1.UpdateOptions{DryRun: dryRunOptions}); err != nil {
							errs = append(errs, fmt.Errorf("error updating secret %s:%s/%s: %w", cluster, secret.Namespace, secret.Name, err))
							continue
						}
						logger.Debug("secret updated")
					} else {
						logger.Debug("secret skipped")
					}
				}
			}

			if kerrors.IsNotFound(err) || shouldCreate {
				if _, err := secretClient.Create(context.TODO(), secret, metav1.CreateOptions{DryRun: dryRunOptions}); err != nil {
					errs = append(errs, fmt.Errorf("error creating secret %s:%s/%s: %w", cluster, secret.Namespace, secret.Name, err))
					continue
				}
				logger.Debug("secret created")
			}
		}
	}
	return utilerrors.NewAggregate(errs)
}

// mutateGlobalPullSecret mutates the original secret based on the refreshed value stored in another secret.
func mutateGlobalPullSecret(original, secret *coreapi.Secret) (bool, error) {
	dockerConfig, err := dockerConfigJSON(secret)
	if err != nil {
		return false, fmt.Errorf("failed to parse the constructed secret: %w", err)
	}
	registryDomain := api.DomainForService(api.ServiceRegistry)
	if dockerConfig.Auths == nil || dockerConfig.Auths[api.DomainForService(api.ServiceRegistry)].Auth == "" {
		return false, fmt.Errorf("failed to get token for %s", registryDomain)
	}
	token := dockerConfig.Auths[api.DomainForService(api.ServiceRegistry)].Auth
	dockerConfig, err = dockerConfigJSON(original)
	if err != nil {
		return false, fmt.Errorf("failed to parse the original secret: %w", err)
	}
	if dockerConfig.Auths[api.DomainForService(api.ServiceRegistry)].Auth == token {
		return false, nil
	}
	dockerConfig.Auths[api.DomainForService(api.ServiceRegistry)] = secretbootstrap.DockerAuth{
		Auth: token,
	}
	data, err := json.Marshal(dockerConfig)
	if err != nil {
		return false, fmt.Errorf("failed to marshal the docker config: %w", err)
	}
	original.Data[coreapi.DockerConfigJsonKey] = data
	return true, nil
}

func dockerConfigJSON(secret *coreapi.Secret) (*secretbootstrap.DockerConfigJSON, error) {
	if secret == nil {
		return nil, fmt.Errorf("failed to get content from nil secret")
	}
	if secret.Data == nil {
		return nil, fmt.Errorf("failed to get content from an secret with no data")
	}
	bytes, ok := secret.Data[coreapi.DockerConfigJsonKey]
	if !ok {
		return nil, fmt.Errorf("there is no key in the secret: %s", coreapi.DockerConfigJsonKey)
	}
	var ret secretbootstrap.DockerConfigJSON
	if err := json.Unmarshal(bytes, &ret); err != nil {
		return nil, fmt.Errorf("failed to unmarshal the docker config: %w", err)
	}
	return &ret, nil
}

func writeSecrets(secretsMap map[string][]*coreapi.Secret) error {
	var tmpFiles []*os.File
	defer func() {
		for _, tf := range tmpFiles {
			tf.Close()
		}
	}()

	for cluster, secrets := range secretsMap {
		tmpFile, err := os.CreateTemp("", fmt.Sprintf("%s_*.yaml", cluster))
		if err != nil {
			return fmt.Errorf("failed to create tempfile: %w", err)
		}
		tmpFiles = append(tmpFiles, tmpFile)

		logrus.Infof("Writing secrets from cluster %s to %s", cluster, tmpFile.Name())
		if err := writeSecretsToFile(secrets, tmpFile); err != nil {
			return fmt.Errorf("error while writing secrets for cluster %s to file %s: %w", cluster, tmpFile.Name(), err)
		}
	}
	return nil
}

func writeSecretsToFile(secrets []*coreapi.Secret, w io.Writer) error {
	serializerOptions := kubejson.SerializerOptions{Yaml: true, Pretty: true, Strict: true}
	serializer := kubejson.NewSerializerWithOptions(kubejson.DefaultMetaFactory, scheme.Scheme, scheme.Scheme, serializerOptions)

	for _, secret := range secrets {
		if err := serializer.Encode(secret, w); err != nil {
			return err
		}
		fmt.Fprintf(w, "---\n")
	}
	return nil
}

type comparable struct {
	fields            sets.Set[string]
	superfluousFields sets.Set[string]
}

func (c *comparable) string() string {
	var ret string

	if c.fields.Len() > 0 {
		ret += fmt.Sprintf("Fields: '%s'", strings.Join(sets.List(c.fields), ", "))
	}

	if len(c.superfluousFields) > 0 {
		ret += fmt.Sprintf(" SuperfluousFields: %v", sets.List(c.superfluousFields))
	}
	return ret
}

func constructConfigItemsByName(config secretbootstrap.Config) map[string]*comparable {
	cfgComparableItemsByName := make(map[string]*comparable)

	for _, cfg := range config.Secrets {
		for _, itemContext := range cfg.From {
			if itemContext.Item != "" {
				item, ok := cfgComparableItemsByName[itemContext.Item]
				if !ok {
					item = &comparable{
						fields: sets.New[string](),
					}
				}
				item.fields = insertIfNotEmpty(item.fields, itemContext.Field)
				cfgComparableItemsByName[itemContext.Item] = item
			}

			if len(itemContext.DockerConfigJSONData) > 0 {
				for _, context := range itemContext.DockerConfigJSONData {
					item, ok := cfgComparableItemsByName[context.Item]
					if !ok {
						item = &comparable{
							fields: sets.New[string](),
						}
					}

					item.fields = insertIfNotEmpty(item.fields, context.AuthField, context.EmailField)

					cfgComparableItemsByName[context.Item] = item
				}
			}
		}
	}

	return cfgComparableItemsByName
}

func insertIfNotEmpty(s sets.Set[string], items ...string) sets.Set[string] {
	for _, item := range items {
		if item != "" {
			s.Insert(item)
		}
	}
	return s
}

func getUnusedItems(config secretbootstrap.Config, client secrets.ReadOnlyClient, allowUnused sets.Set[string], allowUnusedAfter time.Time) error {
	allSecretStoreItems, err := client.GetInUseInformationForAllItems(config.VaultDPTPPrefix)
	if err != nil {
		return fmt.Errorf("failed to get in-use information from secret store: %w", err)
	}
	cfgComparableItemsByName := constructConfigItemsByName(config)

	unused := make(map[string]*comparable)
	for itemName, item := range allSecretStoreItems {
		l := logrus.WithField("item", itemName)
		if item.LastChanged().After(allowUnusedAfter) {
			logrus.WithFields(logrus.Fields{
				"item":      itemName,
				"threshold": allowUnusedAfter,
				"modified":  item.LastChanged(),
			}).Info("Unused item last modified after threshold")
			continue
		}

		if _, ok := cfgComparableItemsByName[itemName]; !ok {
			if allowUnused.Has(itemName) {
				l.Info("Unused item allowed by arguments")
				continue
			}

			unused[itemName] = &comparable{}
			continue
		}

		diffFields := item.UnusedFields(cfgComparableItemsByName[itemName].fields)
		if diffFields.Len() > 0 {
			if allowUnused.Has(itemName) {
				l.WithField("fields", strings.Join(sets.List(diffFields), ",")).Info("Unused fields from item are allowed by arguments")
				continue
			}

			if _, ok := unused[itemName]; !ok {
				unused[itemName] = &comparable{}
			}
			unused[itemName].fields = diffFields
		}

		if superfluousFields := item.SuperfluousFields(); len(superfluousFields) > 0 {
			if allowUnused.Has(itemName) {
				l.WithField("superfluousFields", superfluousFields).Info("Superfluous fields from item are allowed by arguments")
				continue
			}

			if _, ok := unused[itemName]; !ok {
				unused[itemName] = &comparable{}
			}
			unused[itemName].superfluousFields = superfluousFields
		}
	}

	var errs []error
	for name, item := range unused {
		err := fmt.Sprintf("Unused item: '%s'", name)
		if s := item.string(); s != "" {
			err += fmt.Sprintf(" with %s", s)
		}
		errs = append(errs, errors.New(err))
	}

	sort.Slice(errs, func(i, j int) bool {
		return errs[i] != nil && errs[j] != nil && errs[i].Error() < errs[j].Error()
	})

	return utilerrors.NewAggregate(errs)
}

func (o *options) validateItems(client secrets.ReadOnlyClient) error {
	var errs []error

	for _, config := range o.config.Secrets {
		for _, item := range config.From {
			logger := logrus.WithField("item", item.Item)

			if item.DockerConfigJSONData != nil {
				for _, data := range item.DockerConfigJSONData {
					hasItem, err := client.HasItem(data.Item)
					if err != nil {
						errs = append(errs, fmt.Errorf("failed to check if item %s exists: %w", data.Item, err))
						continue
					}
					if !hasItem {
						errs = append(errs, fmt.Errorf("item %s doesn't exist", data.Item))
						break
					}
					if _, err := client.GetFieldOnItem(data.Item, data.AuthField); err != nil {
						if o.generatorConfig.IsFieldGenerated(stripDPTPPrefixFromItem(data.Item, &o.config), data.AuthField) {
							logger.WithField("field", data.AuthField).Warn("Field doesn't exist but it will be generated")
						} else {
							errs = append(errs, fmt.Errorf("field %s in item %s doesn't exist", data.AuthField, data.Item))
						}
					}
				}
			} else {
				hasItem, err := client.HasItem(item.Item)
				if err != nil {
					errs = append(errs, fmt.Errorf("failed to check if item %s exists: %w", item.Item, err))
					continue
				}
				if !hasItem {
					if o.generatorConfig.IsItemGenerated(stripDPTPPrefixFromItem(item.Item, &o.config)) {
						logrus.Warn("Item doesn't exist but it will be generated")
					} else {
						errs = append(errs, fmt.Errorf("item %s doesn't exist", item.Item))
						continue
					}
				}

				if item.Field != "" {
					if _, err := client.GetFieldOnItem(item.Item, item.Field); err != nil {
						if o.generatorConfig.IsFieldGenerated(stripDPTPPrefixFromItem(item.Item, &o.config), item.Field) {
							logger.WithField("field", item.Field).Warn("Field doesn't exist but it will be generated")
						} else {
							errs = append(errs, fmt.Errorf("field %s in item %s doesn't exist", item.Field, item.Item))
						}
					}
				}
			}
		}
	}

	return utilerrors.NewAggregate(errs)
}

// stripDPTPPrefixFromItem strips the dptp prefix from an item name. It is needed when
// interacting with the secret generator config, because the secret generator gets the full
// dptp prefix as cli arg (kv/dptp) whereas the ci-secret-bootstrapper which needs to interact with
// both dptp and user secrets only gets the store path as cli prefix (kv) and prepends all item
// names with the dptp prefix from the config during deserialization.
func stripDPTPPrefixFromItem(itemName string, cfg *secretbootstrap.Config) string {
	return strings.TrimPrefix(itemName, cfg.VaultDPTPPrefix+"/")
}

func main() {
	logrusutil.ComponentInit()
	censor := secrets.NewDynamicCensor()
	logrus.SetFormatter(logrusutil.NewFormatterWithCensor(logrus.StandardLogger().Formatter, &censor))
	o, err := parseOptions(&censor)
	if err != nil {
		logrus.WithError(err).Fatalf("cannot parse args: %q", os.Args[1:])
	}

	if err := o.validateOptions(); err != nil {
		logrus.WithError(err).Fatal("Invalid arguments.")
	}
	prowDisabledClusters, err := prowconfigutils.ProwDisabledClusters(&o.kubernetesOptions)
	if err != nil {
		logrus.WithError(err).Warn("Failed to get Prow disable clusters")
	}
	kubeconfigs, err := o.kubernetesOptions.LoadClusterConfigs()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to load cluster configs.")
	}
	disabledClusters := sets.New[string](prowDisabledClusters...)
	if err := o.completeOptions(&censor, kubeconfigs, disabledClusters); err != nil {
		logrus.WithError(err).Error("Failed to complete options.")
	}
	client, err := o.secrets.NewReadOnlyClient(&censor)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create client.")
	}

	if errs := reconcileSecrets(o, client, disabledClusters); len(errs) > 0 {
		logrus.WithError(utilerrors.NewAggregate(errs)).Fatalf("errors while updating secrets")
	}
}

func reconcileSecrets(o options, client secrets.ReadOnlyClient, prowDisabledClusters sets.Set[string]) (errs []error) {
	if o.validateOnly {
		var config secretbootstrap.Config
		if err := secretbootstrap.LoadConfigFromFile(o.configPath, &config); err != nil {
			return append(errs, fmt.Errorf("failed to load config from file: %s", o.configPath))
		}
		if err := config.Validate(); err != nil {
			return append(errs, fmt.Errorf("failed to validate the config: %w", err))
		}

		if err := o.validateItems(client); err != nil {
			return append(errs, fmt.Errorf("failed to validate items: %w", err))
		}

		logrus.Infof("the config file %s has been validated", o.configPath)
		return nil
	}

	// errors returned by constructSecrets will be handled once the rest of the secrets have been uploaded
	secretsMap, err := constructSecrets(o.config, client, prowDisabledClusters)
	if err != nil {
		errs = append(errs, err)
	}

	if o.validateItemsUsage {
		unusedGracePeriod := time.Now().AddDate(0, 0, -allowUnusedDays)
		err := getUnusedItems(o.config, client, o.allowUnused.StringSet(), unusedGracePeriod)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if o.dryRun {
		logrus.Infof("Running in dry-run mode")
		if err := writeSecrets(secretsMap); err != nil {
			errs = append(errs, fmt.Errorf("failed to write secrets on dry run: %w", err))
		}
	} else {
		if err := updateSecrets(o.secretsGetters, secretsMap, o.force, o.confirm, sets.New[string](o.config.OSDGlobalPullSecretGroup()...)); err != nil {
			errs = append(errs, fmt.Errorf("failed to update secrets: %w", err))
		}
		logrus.Info("Updated secrets.")
	}

	return errs
}
