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
	"sync/atomic"
	"time"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
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
	"sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/logrusutil"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/api/secretbootstrap"
	"github.com/openshift/ci-tools/pkg/api/secretgenerator"
	vaultapi "github.com/openshift/ci-tools/pkg/api/vault"
	gsm "github.com/openshift/ci-tools/pkg/gsm-secrets"
	gsmvalidation "github.com/openshift/ci-tools/pkg/gsm-validation"
	"github.com/openshift/ci-tools/pkg/kubernetes/pkg/credentialprovider"
	"github.com/openshift/ci-tools/pkg/prowconfigutils"
	"github.com/openshift/ci-tools/pkg/secrets"
)

// gsmSecretRef uniquely identifies a GSM secret by collection, group and secret name
type gsmSecretRef struct {
	collection string
	group      string
	field      string
}

// fetchedSecret holds the result of fetching a GSM secret (payload or error)
type fetchedSecret struct {
	payload []byte
	err     error
}

type options struct {
	secrets secrets.CLIOptions

	dryRun             bool
	force              bool
	validateItemsUsage bool
	confirm            bool
	enableGsm          bool

	kubernetesOptions   flagutil.KubernetesOptions
	vaultConfigPath     string
	gsmConfigPath       string
	generatorConfigPath string

	cluster         string
	secretNamesRaw  flagutil.Strings
	logLevel        string
	impersonateUser string

	secretsGetters  map[string]Getter
	vaultConfig     secretbootstrap.Config
	generatorConfig secretgenerator.Config

	gsmConfig        api.GSMConfig
	gsmProjectConfig gsm.Config

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
	fs.BoolVar(&o.enableGsm, "enable-gsm", false, "Whether to enable GSM bundles mechanism")
	o.kubernetesOptions.AddFlags(fs)
	fs.StringVar(&o.vaultConfigPath, "config", "", "Path to the config file to use for this tool.")
	fs.StringVar(&o.generatorConfigPath, "generator-config", "", "Path to the secret-generator config file.")
	fs.StringVar(&o.gsmConfigPath, "gsm-config", "", "Path to the Google Secret Manager config file.")
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
	if o.vaultConfigPath == "" {
		errs = append(errs, errors.New("--config is required"))
	}
	if o.enableGsm && o.gsmConfigPath == "" {
		errs = append(errs, errors.New("--gsm-config is required when --enable-gsm is true"))
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

	if err := secretbootstrap.LoadConfigFromFile(o.vaultConfigPath, &o.vaultConfig); err != nil {
		return err
	}

	if o.enableGsm {
		if err := api.LoadGSMConfigFromFile(o.gsmConfigPath, &o.gsmConfig); err != nil {
			return err
		}
		gsmProjectConfig, err := gsm.GetConfigFromEnv()
		if err != nil {
			return err
		}
		o.gsmProjectConfig = gsmProjectConfig
	}

	if vals := o.secretNamesRaw.Strings(); len(vals) > 0 {
		secretNames := sets.New[string](vals...)
		logrus.WithField("secretNames", sets.List(secretNames)).Info("pruning irrelevant configuration ...")
		pruneIrrelevantConfiguration(&o.vaultConfig, secretNames)
		logrus.WithField("secretNames", sets.List(secretNames)).WithField("o.config.Fields", o.vaultConfig.Secrets).Info("pruned irrelevant configuration")
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
	for i, secretConfig := range o.vaultConfig.Secrets {
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
	o.vaultConfig.Secrets = filteredSecrets

	// Filter GSM bundle targets based on disabled clusters and --cluster flag.
	// This mirrors the Vault filtering above and ensures we only process bundles
	// for clusters that are available and match any user-specified cluster filter.
	if o.enableGsm && len(o.gsmConfig.Bundles) > 0 {
		var filteredBundles []api.GSMBundle
		for i := range o.gsmConfig.Bundles {
			bundle := &o.gsmConfig.Bundles[i]
			// Preserve bundles with SyncToCluster=false regardless of targets
			if !bundle.SyncToCluster {
				filteredBundles = append(filteredBundles, *bundle)
				continue
			}
			var filteredTargets []api.TargetSpec
			for _, target := range bundle.Targets {
				if disabledClusters.Has(target.Cluster) {
					logrus.WithFields(logrus.Fields{
						"bundle":  bundle.Name,
						"cluster": target.Cluster,
					}).Debug("Skipping GSM bundle for a cluster that is disabled by Prow")
					continue
				}
				if o.cluster != "" && o.cluster != target.Cluster {
					logrus.WithFields(logrus.Fields{
						"target-cluster": o.cluster,
						"bundle-cluster": target.Cluster,
					}).Debug("Skipping GSM bundle for a cluster that does not match the one configured via --cluster")
					continue
				}
				filteredTargets = append(filteredTargets, target)
				if !o.validateOnly {
					if o.secretsGetters[target.Cluster] == nil {
						kc, ok := kubeConfigs[target.Cluster]
						if !ok {
							return fmt.Errorf("bundle %s target cluster %q not found in kubeconfig", bundle.Name, target.Cluster)
						}
						client, err := coreclientset.NewForConfig(&kc)
						if err != nil {
							return err
						}
						o.secretsGetters[target.Cluster] = client
					}
				}
			}
			// Only keep bundles that have at least one target after filtering
			if len(filteredTargets) > 0 {
				bundle.Targets = filteredTargets
				filteredBundles = append(filteredBundles, *bundle)
			}
		}
		o.gsmConfig.Bundles = filteredBundles
	}

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
	if err := o.vaultConfig.Validate(); err != nil {
		return fmt.Errorf("failed to validate the config: %w", err)
	}
	toMap := map[string]map[string]string{}
	for i, secretConfig := range o.vaultConfig.Secrets {
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
				return fmt.Errorf("config[%d].from[%s]: both bitwarden dockerconfigJSON items are not allowed", i, key)
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
	if o.enableGsm && len(o.gsmConfig.Bundles) > 0 {
		if err := o.gsmConfig.Validate(); err != nil {
			return fmt.Errorf("failed to validate the GSM config: %w", err)
		}
		if err := o.validateVaultGSMConflicts(); err != nil {
			return fmt.Errorf("conflicts between Vault and GSM configs: %w", err)
		}
	}
	return nil
}

func (o *options) validateVaultGSMConflicts() error {
	return validateGSMVaultConflicts(&o.gsmConfig, &o.vaultConfig)
}

// validateGSMVaultConflicts checks for conflicts between GSM bundles and Vault secrets.
// It ensures that no GSM bundle attempts to create a secret (cluster/namespace/name combination)
// that already exists in the Vault configuration. This prevents accidental overwrites and ensures
// clear ownership of secrets during the migration from Vault to GSM.
//
// Returns an aggregate error containing all detected conflicts, or nil if no conflicts exist.
func validateGSMVaultConflicts(gsmConfig *api.GSMConfig, vaultConfig *secretbootstrap.Config) error {
	var errs []error

	// Build index of Vault secrets
	vaultIndex := make(map[string]map[types.NamespacedName]bool)
	for _, secretCfg := range vaultConfig.Secrets {
		for _, to := range secretCfg.To {
			if vaultIndex[to.Cluster] == nil {
				vaultIndex[to.Cluster] = make(map[types.NamespacedName]bool)
			}
			nsName := types.NamespacedName{Namespace: to.Namespace, Name: to.Name}
			vaultIndex[to.Cluster][nsName] = true
		}
	}
	for _, bundle := range gsmConfig.Bundles {
		if !bundle.SyncToCluster {
			continue
		}
		for _, target := range bundle.Targets {
			nsName := types.NamespacedName{Namespace: target.Namespace, Name: bundle.Name}
			if vaultIndex[target.Cluster] != nil && vaultIndex[target.Cluster][nsName] {
				errs = append(errs, fmt.Errorf(
					"bundle %s conflicts with Vault: secret %s/%s on cluster %s",
					bundle.Name, target.Namespace, bundle.Name, target.Cluster,
				))
			}
		}
	}

	return utilerrors.NewAggregate(errs)
}

// constructDockerConfigJSONFromVault constructs a .dockerconfigjson from Vault secrets
func constructDockerConfigJSONFromVault(client secrets.ReadOnlyClient, dockerConfigJSONData []secretbootstrap.DockerConfigJSONData) ([]byte, error) {
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

// constructDockerConfigJSONFromGSM constructs a .dockerconfigjson from GSM secrets cache
func constructDockerConfigJSONFromGSM(secretsCache map[gsmSecretRef]fetchedSecret, registries []api.RegistryAuthData) ([]byte, error) {
	auths := make(map[string]secretbootstrap.DockerAuth)

	for _, reg := range registries {
		authData := secretbootstrap.DockerAuth{}

		authRef := gsmSecretRef{
			collection: reg.Collection,
			group:      reg.Group,
			field:      reg.AuthField,
		}
		fetchedAuth, exists := secretsCache[authRef]
		if !exists {
			return nil, fmt.Errorf("auth field '%s' (collection: %s, group: %s) not found in fetched secrets", reg.AuthField, reg.Collection, reg.Group)
		}
		if fetchedAuth.err != nil {
			return nil, fmt.Errorf("couldn't get auth field '%s' (collection: %s, group: %s): %w", reg.AuthField, reg.Collection, reg.Group, fetchedAuth.err)
		}
		authData.Auth = string(bytes.TrimSpace(fetchedAuth.payload))

		if reg.EmailField != "" {
			emailRef := gsmSecretRef{
				collection: reg.Collection,
				group:      reg.Group,
				field:      reg.EmailField,
			}
			fetchedEmail, exists := secretsCache[emailRef]
			if !exists {
				return nil, fmt.Errorf("email field '%s' (collection: %s, group: %s) not found in fetched secrets", reg.EmailField, reg.Collection, reg.Group)
			}
			if fetchedEmail.err != nil {
				return nil, fmt.Errorf("couldn't get email field '%s' (collection: %s, group: %s): %w", reg.EmailField, reg.Collection, reg.Group, fetchedEmail.err)
			}
			authData.Email = string(fetchedEmail.payload)
		}

		auths[reg.RegistryURL] = authData
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

func constructSecretsFromVault(config secretbootstrap.Config, client secrets.ReadOnlyClient, prowDisabledClusters sets.Set[string]) (map[string][]*coreapi.Secret, error) {
	secretsByClusterAndName := map[string]map[types.NamespacedName]coreapi.Secret{}
	secretsMapLock := &sync.Mutex{}

	var potentialErrors int
	for _, item := range config.Secrets {
		potentialErrors = potentialErrors + len(item.From)
	}
	errChan := make(chan error, potentialErrors)

	secretConfigWG := &sync.WaitGroup{}
	for idx, cfg := range config.Secrets {
		secretConfigWG.Add(1)

		go func() {
			defer secretConfigWG.Done()

			data := make(map[string][]byte)
			var keys []string
			for key := range cfg.From {
				keys = append(keys, key)
			}
			sort.Strings(keys)

			secretInError := atomic.Bool{}
			keyWg := sync.WaitGroup{}
			dataLock := &sync.Mutex{}
			keyWg.Add(len(keys))
			for _, key := range keys {
				go func() {
					defer keyWg.Done()
					itemContext := cfg.From[key]
					var value []byte
					var err error
					if itemContext.Field != "" {
						value, err = client.GetFieldOnItem(itemContext.Item, itemContext.Field)
					} else if len(itemContext.DockerConfigJSONData) > 0 {
						value, err = constructDockerConfigJSONFromVault(client, itemContext.DockerConfigJSONData)
					}
					if err != nil {
						secretInError.Store(true)
						errChan <- fmt.Errorf("config.%d.\"%s\": %w", idx, key, err)
						return
					}
					if cfg.From[key].Base64Decode {
						decoded, err := base64.StdEncoding.DecodeString(string(value))
						if err != nil {
							secretInError.Store(true)
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

			// We don't want to sync secrets that have not been fully fetched from the secret manager
			// and/or have not been properly constructed.
			if secretInError.Load() {
				targets := make([]string, len(cfg.To))
				for i, sc := range cfg.To {
					targets[i] = fmt.Sprintf("%s/%s@%s", sc.Namespace, sc.Name, sc.Cluster)
				}
				logrus.WithField("secrets", strings.Join(targets, " ")).
					Errorf("Failed to construct secret, skipping sync")
				return
			}

			for _, secretContext := range cfg.To {
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
		if prowDisabledClusters.Has(cluster) {
			logrus.WithField("cluster", cluster).Info("Skipped secrets on a Prow disabled cluster")
			continue
		}
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

func updateSecrets(getters map[string]Getter, secretsMap map[string][]*coreapi.Secret, force bool, confirm bool, osdGlobalPullSecretGroup, prowDisabledClusters sets.Set[string]) error {
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
			// This should never happen if constructSecrets() is implemented correctly
			if prowDisabledClusters.Has(cluster) {
				errs = append(errs, fmt.Errorf("attempted to update a secret %s in namespace %s on a Prow disabled cluster %s", secret.Name, secret.Namespace, cluster))
				continue
			}

			clientGetter, ok := getters[cluster]
			if !ok {
				errs = append(errs, fmt.Errorf("failed to get client getter for cluster %s", cluster))
				continue
			}

			if !existingNamespaces.Has(secret.Namespace) {
				nsClient := clientGetter.Namespaces()
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

			secretClient := clientGetter.Secrets(secret.Namespace)

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
	if dockerConfig.Auths == nil {
		return false, fmt.Errorf("failed to get any token")
	}
	originalDockerConfig, err := dockerConfigJSON(original)
	if err != nil {
		return false, fmt.Errorf("failed to parse the original secret: %w", err)
	}
	mutatedSecret := false
	domains := []string{api.DomainForService(api.ServiceRegistry), api.QCIAPPCIDomain, api.QuayOpenShiftCIRepo, api.QuayOpenShiftNetworkEdgeRepo, api.QCICacheDomain}
	for _, domain := range domains {
		if dockerConfig.Auths[domain].Auth == "" {
			return false, fmt.Errorf("failed to get token for %s", domain)
		}
		originalToken := originalDockerConfig.Auths[domain].Auth
		originalDockerConfig.Auths[domain] = secretbootstrap.DockerAuth{
			Auth: dockerConfig.Auths[domain].Auth,
		}
		if !mutatedSecret && originalToken != originalDockerConfig.Auths[domain].Auth {
			mutatedSecret = true
		}
	}
	if mutatedSecret {
		data, err := json.Marshal(originalDockerConfig)
		if err != nil {
			return false, fmt.Errorf("failed to marshal the docker config: %w", err)
		}
		original.Data[coreapi.DockerConfigJsonKey] = data
	}
	return mutatedSecret, nil
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

	for _, config := range o.vaultConfig.Secrets {
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
						if o.generatorConfig.IsFieldGenerated(stripDPTPPrefixFromItem(data.Item, &o.vaultConfig), data.AuthField) {
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
					if o.generatorConfig.IsItemGenerated(stripDPTPPrefixFromItem(item.Item, &o.vaultConfig)) {
						logrus.Warn("Item doesn't exist but it will be generated")
					} else {
						errs = append(errs, fmt.Errorf("item %s doesn't exist", item.Item))
						continue
					}
				}

				if item.Field != "" {
					if _, err := client.GetFieldOnItem(item.Item, item.Field); err != nil {
						if o.generatorConfig.IsFieldGenerated(stripDPTPPrefixFromItem(item.Item, &o.vaultConfig), item.Field) {
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
		logrus.WithError(err).Fatal("Failed to complete options.")
	}
	client, err := o.secrets.NewReadOnlyClient(&censor)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create client.")
	}

	var gsmClient *secretmanager.Client
	if o.enableGsm && !o.validateOnly {
		ctx := context.Background()
		var err error
		gsmClient, err = secretmanager.NewClient(ctx)
		if err != nil {
			logrus.WithError(err).Fatal("Failed to create GSM client.")
		}
		defer gsmClient.Close()
		logrus.Info("GSM client initialized successfully")
	}

	if errs := reconcileSecrets(o, client, gsmClient, disabledClusters); len(errs) > 0 {
		logrus.WithError(utilerrors.NewAggregate(errs)).Fatalf("errors while updating secrets")
	}
}

func reconcileSecrets(o options, vaultClient secrets.ReadOnlyClient, gsmClient *secretmanager.Client, prowDisabledClusters sets.Set[string]) (errs []error) {
	if o.validateOnly {
		var config secretbootstrap.Config
		if err := secretbootstrap.LoadConfigFromFile(o.vaultConfigPath, &config); err != nil {
			return append(errs, fmt.Errorf("failed to load config from file: %s", o.vaultConfigPath))
		}
		if err := config.Validate(); err != nil {
			return append(errs, fmt.Errorf("failed to validate the config: %w", err))
		}

		if err := o.validateItems(vaultClient); err != nil {
			return append(errs, fmt.Errorf("failed to validate items: %w", err))
		}

		logrus.Infof("the config file %s has been validated", o.vaultConfigPath)

		if o.enableGsm {
			var gsmConfig api.GSMConfig
			if err := api.LoadGSMConfigFromFile(o.gsmConfigPath, &gsmConfig); err != nil {
				return append(errs, fmt.Errorf("failed to load GSM config from file: %s", o.gsmConfigPath))
			}
			if err := gsmConfig.Validate(); err != nil {
				return append(errs, fmt.Errorf("failed to validate GSM config: %w", err))
			}
			// Check for conflicts between Vault and GSM configs
			if err := validateGSMVaultConflicts(&gsmConfig, &config); err != nil {
				return append(errs, fmt.Errorf("conflicts between Vault and GSM configs: %w", err))
			}
			logrus.Infof("GSM config file %s has been validated", o.gsmConfigPath)
		}

		return nil
	}

	// errors returned by constructSecrets will be handled once the rest of the secrets have been uploaded
	secretsMap, err := constructSecretsFromVault(o.vaultConfig, vaultClient, prowDisabledClusters)
	if err != nil {
		errs = append(errs, err)
	}

	if o.enableGsm && gsmClient != nil && len(o.gsmConfig.Bundles) > 0 {
		ctx := context.Background()
		var gsmSecretsMap map[string][]*coreapi.Secret
		gsmSecretsMap, err = constructSecretsFromGSM(ctx, o.gsmConfig, gsmClient, o.gsmProjectConfig, prowDisabledClusters)
		if err != nil {
			errs = append(errs, err)
		}

		if gsmSecretsMap != nil {
			secretsMap, err = mergeSecretMaps(secretsMap, gsmSecretsMap)
			if err != nil {
				errs = append(errs, err)
			}
		}
	}

	if o.validateItemsUsage {
		unusedGracePeriod := time.Now().AddDate(0, 0, -allowUnusedDays)
		err := getUnusedItems(o.vaultConfig, vaultClient, o.allowUnused.StringSet(), unusedGracePeriod)
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
		if err := updateSecrets(o.secretsGetters, secretsMap, o.force, o.confirm, sets.New[string](o.vaultConfig.OSDGlobalPullSecretGroup()...), prowDisabledClusters); err != nil {
			errs = append(errs, fmt.Errorf("failed to update secrets: %w", err))
		}
		logrus.Info("Updated secrets.")
	}

	return errs
}

// mergeSecretMaps combines Vault and GSM secret maps, with Vault taking precedence on conflicts.
// Returns the merged map and any conflict errors encountered.
func mergeSecretMaps(vaultSecrets, gsmSecrets map[string][]*coreapi.Secret) (map[string][]*coreapi.Secret, error) {
	if len(gsmSecrets) == 0 {
		return vaultSecrets, nil
	}
	if len(vaultSecrets) == 0 {
		return gsmSecrets, nil
	}

	vaultIndex := make(map[string]map[types.NamespacedName]bool)
	for cluster, secretList := range vaultSecrets {
		if vaultIndex[cluster] == nil {
			vaultIndex[cluster] = make(map[types.NamespacedName]bool)
		}
		for _, secret := range secretList {
			nsName := types.NamespacedName{Namespace: secret.Namespace, Name: secret.Name}
			vaultIndex[cluster][nsName] = true
		}
	}

	var errs []error
	merged := make(map[string][]*coreapi.Secret)

	for cluster, secretList := range vaultSecrets {
		merged[cluster] = make([]*coreapi.Secret, len(secretList))
		copy(merged[cluster], secretList)
	}

	for cluster, secretList := range gsmSecrets {
		for _, secret := range secretList {
			nsName := types.NamespacedName{Namespace: secret.Namespace, Name: secret.Name}

			if vaultIndex[cluster] != nil && vaultIndex[cluster][nsName] {
				errs = append(errs, fmt.Errorf(
					"conflict: GSM secret %s/%s on cluster %s conflicts with Vault (Vault takes precedence)",
					secret.Namespace, secret.Name, cluster,
				))
				continue
			}

			merged[cluster] = append(merged[cluster], secret)
		}
	}

	return merged, utilerrors.NewAggregate(errs)
}

// collectionGroupKey is used to track auto-discovered fields for a collection+group pair
type collectionGroupKey struct {
	collection string
	group      string
}

// constructSecretsFromGSM fetches secrets from GSM and builds Kubernetes Secret objects.
// For bundles without explicit field lists, it discovers fields by querying GSM.
// Returns a map of cluster name to list of Kubernetes Secret objects, and any fetch/build errors.
func constructSecretsFromGSM(
	ctx context.Context,
	gsmConfig api.GSMConfig,
	gsmClient gsm.SecretManagerClient,
	gsmProjectConfig gsm.Config,
	prowDisabledClusters sets.Set[string]) (map[string][]*coreapi.Secret, error) {
	var errs []error
	uniqueSecretNames := sets.New[gsmSecretRef]()
	discoveredFields := make(map[collectionGroupKey][]string) // track fields for collection+group pairs when `fields` stanza is empty

	for _, bundle := range gsmConfig.Bundles {
		if !bundle.SyncToCluster {
			continue
		}
		for _, secretEntry := range bundle.GSMSecrets {
			if len(secretEntry.Fields) == 0 { // if fields are not specified, discover them using GSM listing
				key := collectionGroupKey{
					collection: secretEntry.Collection,
					group:      secretEntry.Group,
				}

				// Check if we've already discovered fields for this collection+group
				if _, alreadyDiscovered := discoveredFields[key]; !alreadyDiscovered {
					fieldNames, err := gsm.ListSecretFieldsByCollectionAndGroup(ctx, gsmClient, gsmProjectConfig, secretEntry.Collection, secretEntry.Group)
					if err != nil {
						errs = append(errs, fmt.Errorf("failed to list fields for collection=%s, group=%s: %w", secretEntry.Collection, secretEntry.Group, err))
						continue
					}
					discoveredFields[key] = fieldNames
					logrus.Debugf("discovered %d fields for collection=%s, group=%s", len(fieldNames), secretEntry.Collection, secretEntry.Group)
				}

				for _, fieldName := range discoveredFields[key] {
					s := gsmSecretRef{
						collection: secretEntry.Collection,
						group:      secretEntry.Group,
						field:      fieldName,
					}
					uniqueSecretNames.Insert(s)
				}
			} else {
				for _, field := range secretEntry.Fields {
					s := gsmSecretRef{
						collection: secretEntry.Collection,
						group:      secretEntry.Group,
						field:      field.Name,
					}
					uniqueSecretNames.Insert(s)
				}
			}
		}

		if bundle.DockerConfig == nil {
			continue
		}
		for _, registryEntry := range bundle.DockerConfig.Registries {
			s := gsmSecretRef{
				collection: registryEntry.Collection,
				group:      registryEntry.Group,
				field:      registryEntry.AuthField,
			}
			uniqueSecretNames.Insert(s)

			if registryEntry.EmailField != "" {
				s := gsmSecretRef{
					collection: registryEntry.Collection,
					group:      registryEntry.Group,
					field:      registryEntry.EmailField,
				}
				uniqueSecretNames.Insert(s)
			}
		}
	}

	fetchedGsmSecretsMap := make(map[gsmSecretRef]fetchedSecret)
	mapLock := sync.Mutex{}
	errChan := make(chan error, uniqueSecretNames.Len())
	wg := &sync.WaitGroup{}

	for secretRef := range uniqueSecretNames {
		wg.Add(1)

		go func() {
			defer wg.Done()

			resourceName := gsm.GetGSMSecretResourceName(gsmProjectConfig.ProjectIdNumber, secretRef.collection, secretRef.group, secretRef.field)
			payload, err := gsm.GetSecretPayload(ctx, gsmClient, resourceName)

			mapLock.Lock()
			fetchedGsmSecretsMap[secretRef] = fetchedSecret{
				payload: payload,
				err:     err,
			}
			mapLock.Unlock()

			if err != nil {
				errChan <- fmt.Errorf("failed to fetch secret '%s': %w", resourceName, err)
			}
		}()
	}

	wg.Wait()
	close(errChan)
	for err := range errChan {
		errs = append(errs, err)
	}

	result := map[string][]*coreapi.Secret{}
	for _, bundle := range gsmConfig.Bundles {
		if !bundle.SyncToCluster {
			continue
		}

		k8sSecretData := make(map[string][]byte)
		bundleHasError := false

		for _, gsmSecretEntry := range bundle.GSMSecrets {
			var fieldsToProcess []api.FieldEntry
			if len(gsmSecretEntry.Fields) == 0 {
				key := collectionGroupKey{
					collection: gsmSecretEntry.Collection,
					group:      gsmSecretEntry.Group,
				}
				fieldNames, exists := discoveredFields[key]
				if !exists {
					errs = append(errs, fmt.Errorf("skipping bundle %s: no fields discovered for collection=%s, group=%s", bundle.Name, gsmSecretEntry.Collection, gsmSecretEntry.Group))
					bundleHasError = true
					break
				}
				for _, fieldName := range fieldNames {
					fieldsToProcess = append(fieldsToProcess, api.FieldEntry{
						Name: fieldName,
						As:   "",
					})
				}
			} else {
				fieldsToProcess = gsmSecretEntry.Fields
			}

			for _, field := range fieldsToProcess {
				ref := gsmSecretRef{
					collection: gsmSecretEntry.Collection,
					group:      gsmSecretEntry.Group,
					field:      field.Name,
				}

				fetchedFromGsm, exists := fetchedGsmSecretsMap[ref]
				if !exists {
					errs = append(errs, fmt.Errorf("skipping bundle %s: secret '%s' not found among fetched GSM secrets", bundle.Name, gsm.GetGSMSecretName(ref.collection, ref.group, ref.field)))
					bundleHasError = true
					break
				}

				if fetchedFromGsm.err != nil {
					logrus.WithError(fetchedFromGsm.err).Errorf("skipping bundle %s: failed to fetch secret %s from GSM", bundle.Name, gsm.GetGSMSecretName(ref.collection, ref.group, ref.field))
					bundleHasError = true
					break
				}

				var keyName string
				if field.As != "" {
					keyName = field.As
				} else {
					keyName = gsmvalidation.DenormalizeName(field.Name) // we want the original name notation in k8s secrets
				}
				k8sSecretData[keyName] = fetchedFromGsm.payload
			}

			if bundleHasError {
				break
			}
		}

		if bundleHasError {
			continue // we don't want to construct an incomplete k8s secret, so skip this bundle entirely
		}

		if bundle.DockerConfig != nil {
			dockerConfigData, err := constructDockerConfigJSONFromGSM(fetchedGsmSecretsMap, bundle.DockerConfig.Registries)
			if err != nil {
				logrus.WithError(err).Errorf("skipping bundle %s: failed to construct dockerconfig", bundle.Name)
				errs = append(errs, fmt.Errorf("bundle %s: failed to construct dockerconfig: %w", bundle.Name, err))
				continue
			}

			dockerConfigName := bundle.DockerConfig.As
			if dockerConfigName == "" {
				dockerConfigName = ".dockerconfigjson"
			}
			k8sSecretData[dockerConfigName] = dockerConfigData
		}

		// finally, construct the whole k8s secret out of the bundle
		for _, target := range bundle.Targets {
			if prowDisabledClusters.Has(target.Cluster) {
				logrus.WithField("cluster", target.Cluster).Info("Skipped secrets on a Prow disabled cluster")
				continue
			}
			if target.Type == "" {
				target.Type = coreapi.SecretTypeOpaque
			}
			secret := &coreapi.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      bundle.Name,
					Namespace: target.Namespace,
					Labels:    map[string]string{api.DPTPRequesterLabel: "ci-secret-bootstrap"},
				},
				Type: target.Type,
				Data: make(map[string][]byte, len(k8sSecretData)),
			}
			for k, v := range k8sSecretData {
				secret.Data[k] = v
			}
			result[target.Cluster] = append(result[target.Cluster], secret)
		}
	}

	return result, utilerrors.NewAggregate(errs)
}
