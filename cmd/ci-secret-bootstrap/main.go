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
	"io/ioutil"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/sync/semaphore"

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
	"github.com/openshift/ci-tools/pkg/secrets"
	"github.com/openshift/ci-tools/pkg/util"
)

type options struct {
	secrets secrets.CLIOptions

	dryRun               bool
	force                bool
	validateBWItemsUsage bool

	kubeConfigPath      string
	configPath          string
	generatorConfigPath string
	cluster             string
	logLevel            string
	impersonateUser     string

	maxConcurrency int

	secretsGetters  map[string]coreclientset.SecretsGetter
	config          secretbootstrap.Config
	generatorConfig secretgenerator.Config

	bwAllowUnused flagutil.Strings

	validateOnly bool
}

const (
	// When checking for unused secrets in BitWarden, only report secrets that were last modified before X days, allowing to set up
	// BitWarden items and matching bootstrap config without tripping an alert
	allowUnusedDays = 7
)

func parseOptions(censor *secrets.DynamicCensor) (options, error) {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	o.bwAllowUnused = flagutil.NewStrings()
	fs.BoolVar(&o.validateOnly, "validate-only", false, "If set, the tool exists after validating its config file.")
	fs.Var(&o.bwAllowUnused, "bw-allow-unused", "One or more bitwarden items that will be ignored when the --validate-bitwarden-items-usage is specified")
	fs.BoolVar(&o.validateBWItemsUsage, "validate-bitwarden-items-usage", false, fmt.Sprintf("If set, the tool only validates if all attachments and custom fields that exist in BitWarden and were last modified before %d days ago are being used in the given config.", allowUnusedDays))
	fs.BoolVar(&o.dryRun, "dry-run", true, "Whether to actually create the secrets with oc command")
	fs.StringVar(&o.kubeConfigPath, "kubeconfig", "", "Path to the kubeconfig file to use for CLI requests.")
	fs.StringVar(&o.configPath, "config", "", "Path to the config file to use for this tool.")
	fs.StringVar(&o.generatorConfigPath, "generator-config", "", "Path to the secret-generator config file.")
	fs.StringVar(&o.cluster, "cluster", "", "If set, only provision secrets for this cluster")
	fs.BoolVar(&o.force, "force", false, "If true, update the secrets even if existing one differs from Bitwarden items instead of existing with error. Default false.")
	fs.StringVar(&o.logLevel, "log-level", "info", fmt.Sprintf("Log level is one of %v.", logrus.AllLevels))
	fs.StringVar(&o.impersonateUser, "as", "", "Username to impersonate")
	fs.IntVar(&o.maxConcurrency, "concurrency", 0, "Maximum number of concurrent in-flight goroutines to BitWarden.")
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
	errs = append(errs, o.secrets.Validate()...)
	if o.configPath == "" {
		errs = append(errs, errors.New("--config is required"))
	}
	if len(o.bwAllowUnused.Strings()) > 0 && !o.validateBWItemsUsage {
		errs = append(errs, errors.New("--bw-allow-unused must be specified with --validate-bitwarden-items-usage"))
	}
	return utilerrors.NewAggregate(errs)
}

func (o *options) completeOptions(censor *secrets.DynamicCensor) error {
	if err := o.secrets.Complete(censor); err != nil {
		return err
	}

	if err := secretbootstrap.LoadConfigFromFile(o.configPath, &o.config); err != nil {
		return err
	}

	if o.generatorConfigPath != "" {
		var err error
		o.generatorConfig, err = secretgenerator.LoadConfigFromPath(o.generatorConfigPath)
		if err != nil {
			return err
		}
	}

	var kubeConfigs map[string]*rest.Config
	if !o.validateOnly {
		var err error
		kubeConfigs, _, err = util.LoadKubeConfigs(o.kubeConfigPath, nil)
		if err != nil {
			// We will bail out later on if we don't have the have the right kubeconfigs
			logrus.WithError(err).Warn("Encountered errors while loading kubeconfigs")
		}
		if o.impersonateUser != "" {
			for _, kubeConfig := range kubeConfigs {
				kubeConfig.Impersonate = rest.ImpersonationConfig{UserName: o.impersonateUser}
			}
		}

	}

	o.secretsGetters = map[string]coreclientset.SecretsGetter{}
	var filteredSecrets []secretbootstrap.SecretConfig
	for i, secretConfig := range o.config.Secrets {
		var to []secretbootstrap.SecretContext

		for j, secretContext := range secretConfig.To {
			if o.cluster != "" && o.cluster != secretContext.Cluster {
				logrus.WithFields(logrus.Fields{"target-cluster": o.cluster, "secret-cluster": secretContext.Cluster}).Debug("Skipping provisioniong of secret for cluster that does not match the one configured via --cluster")
				continue
			}
			to = append(to, secretContext)

			if !o.validateOnly {
				if o.secretsGetters[secretContext.Cluster] == nil {
					kc, ok := kubeConfigs[secretContext.Cluster]
					if !ok {
						return fmt.Errorf("config[%d].to[%d]: failed to find cluster context %q in the kubeconfig", i, j, secretContext.Cluster)
					}
					client, err := coreclientset.NewForConfig(kc)
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

	if o.maxConcurrency == 0 {
		o.maxConcurrency = runtime.GOMAXPROCS(0)
	}
	logrus.Infof("The max concurrency is %d", o.maxConcurrency)

	return o.validateCompletedOptions()
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
		for key, bwContext := range secretConfig.From {
			if key == "" {
				return fmt.Errorf("config[%d].from: empty key is not allowed", i)
			}

			if bwContext.BWItem == "" && len(bwContext.DockerConfigJSONData) == 0 {
				return fmt.Errorf("config[%d].from[%s]: empty value is not allowed", i, key)
			}

			if bwContext.BWItem != "" && len(bwContext.DockerConfigJSONData) > 0 {
				return fmt.Errorf("config[%d].from[%s]: both bitwarden dockerconfigJSON items are not allowed.", i, key)
			}

			if len(bwContext.DockerConfigJSONData) > 0 {
				for _, data := range bwContext.DockerConfigJSONData {
					if data.BWItem == "" {
						return fmt.Errorf("config[%d].from[%s]: bw_item is missing", i, key)
					}
					if data.RegistryURLBitwardenField == "" && data.RegistryURL == "" {
						return fmt.Errorf("config[%d].from[%s]: either registry_url_bw_field or registry_url must be set", i, key)
					}
					if data.RegistryURLBitwardenField != "" && data.RegistryURL != "" {
						return fmt.Errorf("config[%d].from[%s]: registry_url_bw_field and registry_url are mutualy exclusive", i, key)
					}
					if data.AuthBitwardenAttachment == "" {
						return fmt.Errorf("config[%d].from[%s]: auth_bw_attachment is missing", i, key)
					}
				}
			} else if bwContext.BWItem != "" {
				nonEmptyFields := 0
				if bwContext.Field != "" {
					nonEmptyFields++
				}
				if bwContext.Attachment != "" {
					nonEmptyFields++
				}
				if bwContext.Attribute != "" {
					nonEmptyFields++
				}
				if nonEmptyFields == 0 {
					return fmt.Errorf("config[%d].from[%s]: one of [field, attachment, attribute] must be set", i, key)
				}
				if nonEmptyFields > 1 {
					return fmt.Errorf("config[%d].from[%s]: cannot use more than one in [field, attachment, attribute]", i, key)
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

		registryURL := data.RegistryURL
		if registryURL == "" {
			registryURLBitwardenField, err := client.GetFieldOnItem(data.BWItem, data.RegistryURLBitwardenField)
			if err != nil {
				return nil, fmt.Errorf("couldn't get the entry name from bw item %s: %w", data.BWItem, err)
			}
			registryURL = string(registryURLBitwardenField)
		}

		authBWAttachmentValue, err := client.GetAttachmentOnItem(data.BWItem, data.AuthBitwardenAttachment)
		if err != nil {
			return nil, fmt.Errorf("couldn't get attachment '%s' from bw item %s: %w", data.AuthBitwardenAttachment, data.BWItem, err)
		}
		authData.Auth = string(bytes.TrimSpace(authBWAttachmentValue))

		if data.EmailBitwardenField != "" {
			emailValue, err := client.GetFieldOnItem(data.BWItem, data.EmailBitwardenField)
			if err != nil {
				return nil, fmt.Errorf("couldn't get email field '%s' from bw item %s: %w", data.EmailBitwardenField, data.BWItem, err)
			}
			authData.Email = string(emailValue)
		}

		auths[registryURL] = authData
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

func constructSecrets(ctx context.Context, config secretbootstrap.Config, client secrets.ReadOnlyClient, maxConcurrency int) (map[string][]*coreapi.Secret, error) {
	sem := semaphore.NewWeighted(int64(maxConcurrency))
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
				if err := sem.Acquire(ctx, 1); err != nil {
					errChan <- fmt.Errorf("failed to acquire semaphore for key %s: %w", key, err)
					keyWg.Done()
					continue
				}

				key := key
				go func() {
					defer sem.Release(1)
					defer keyWg.Done()
					bwContext := cfg.From[key]
					var value []byte
					var err error
					if bwContext.Field != "" {
						value, err = client.GetFieldOnItem(bwContext.BWItem, bwContext.Field)
					} else if bwContext.Attachment != "" {
						value, err = client.GetAttachmentOnItem(bwContext.BWItem, bwContext.Attachment)
					} else if len(bwContext.DockerConfigJSONData) > 0 {
						value, err = constructDockerConfigJSON(client, bwContext.DockerConfigJSONData)
					} else {
						switch bwContext.Attribute {
						case secretbootstrap.AttributeTypePassword:
							value, err = client.GetPassword(bwContext.BWItem)
						default:
							// should never happen since we have validated the config
							errChan <- fmt.Errorf("[%s] invalid attribute: only the '%s' is supported, not %s", key, secretbootstrap.AttributeTypePassword, bwContext.Attribute)
							return
						}
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
	if err := sem.Acquire(ctx, int64(maxConcurrency)); err != nil {
		errChan <- fmt.Errorf("failed to acquire semaphore while wating all workers to finish: %w", err)
	}
	close(errChan)
	var errs []error
	for err := range errChan {
		errs = append(errs, err)
	}

	var err error
	secretsByClusterAndName, err = fetchUserSecrets(secretsByClusterAndName, client, config.UserSecretsTargetClusters)
	if err != nil {
		errs = append(errs, err)
	}

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

func updateSecrets(secretsGetters map[string]coreclientset.SecretsGetter, secretsMap map[string][]*coreapi.Secret, force bool) error {
	var errs []error
	shouldCreate := false
	for cluster, secrets := range secretsMap {
		logger := logrus.WithField("cluster", cluster)
		logger.Debug("Syncing secrets for cluster")
		for _, secret := range secrets {
			logger := logger.WithFields(logrus.Fields{"namespace": secret.Namespace, "name": secret.Name, "type": secret.Type})
			logger.Debug("handling secret")

			secretClient := secretsGetters[cluster].Secrets(secret.Namespace)

			existingSecret, err := secretClient.Get(context.TODO(), secret.Name, metav1.GetOptions{})
			if err != nil && !kerrors.IsNotFound(err) {
				errs = append(errs, fmt.Errorf("error reading secret %s:%s/%s: %w", cluster, secret.Namespace, secret.Name, err))
			}

			if err == nil {
				if secret.Type != existingSecret.Type {
					if !force {
						errs = append(errs, fmt.Errorf("cannot change secret type from %q to %q (immutable field): %s:%s/%s", existingSecret.Type, secret.Type, cluster, secret.Namespace, secret.Name))
						continue
					}
					if err := secretClient.Delete(context.TODO(), secret.Name, metav1.DeleteOptions{}); err != nil {
						errs = append(errs, fmt.Errorf("error deleting secret: %w", err))
						continue
					}
					shouldCreate = true
				}

				if !shouldCreate {
					for k, v := range existingSecret.Data {
						if _, exists := secret.Data[k]; exists {
							continue
						}
						secret.Data[k] = v
					}
					if !force && !equality.Semantic.DeepEqual(secret.Data, existingSecret.Data) {
						logger.Errorf("actual secret data differs the expected")
						errs = append(errs, fmt.Errorf("secret %s:%s/%s needs updating in place, use --force to do so", cluster, secret.Namespace, secret.Name))
						continue
					}
					if _, err := secretClient.Update(context.TODO(), secret, metav1.UpdateOptions{}); err != nil {
						errs = append(errs, fmt.Errorf("error updating secret %s:%s/%s: %w", cluster, secret.Namespace, secret.Name, err))
						continue
					}
					logger.Debug("secret updated")
				}
			}

			if kerrors.IsNotFound(err) || shouldCreate {
				if _, err := secretClient.Create(context.TODO(), secret, metav1.CreateOptions{}); err != nil {
					errs = append(errs, fmt.Errorf("error creating secret %s:%s/%s: %w", cluster, secret.Namespace, secret.Name, err))
					continue
				}
				logger.Debug("secret created")
			}
		}
	}
	return utilerrors.NewAggregate(errs)
}

func writeSecrets(secretsMap map[string][]*coreapi.Secret) error {
	var tmpFiles []*os.File
	defer func() {
		for _, tf := range tmpFiles {
			tf.Close()
		}
	}()

	for cluster, secrets := range secretsMap {
		tmpFile, err := ioutil.TempFile("", fmt.Sprintf("%s_*.yaml", cluster))
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
	fields            sets.String
	attachments       sets.String
	hasPassword       bool
	superfluousFields sets.String
}

func (c *comparable) string() string {
	var ret string

	if c.fields.Len() > 0 {
		ret += fmt.Sprintf("Fields: '%s'", strings.Join(c.fields.List(), ", "))
	}

	if c.attachments.Len() > 0 {
		ret += fmt.Sprintf(" Attachments: '%s'", strings.Join(c.attachments.List(), ", "))

	}

	if c.hasPassword {
		ret += " Login: 'password'"
	}

	if len(c.superfluousFields) > 0 {
		ret += fmt.Sprintf(" SuperfluousFields: %v", c.superfluousFields.List())
	}
	return ret
}

func constructConfigItemsByName(config secretbootstrap.Config) map[string]*comparable {
	cfgComparableItemsByName := make(map[string]*comparable)

	for _, cfg := range config.Secrets {
		for _, bwContext := range cfg.From {
			if bwContext.BWItem != "" {
				item, ok := cfgComparableItemsByName[bwContext.BWItem]
				if !ok {
					item = &comparable{
						fields:      sets.NewString(),
						attachments: sets.NewString(),
					}
				}
				item.attachments = insertIfNotEmpty(item.attachments, bwContext.Attachment)
				item.fields = insertIfNotEmpty(item.fields, bwContext.Field)

				if bwContext.Attribute == secretbootstrap.AttributeTypePassword {
					item.hasPassword = true
				}
				cfgComparableItemsByName[bwContext.BWItem] = item
			}

			if len(bwContext.DockerConfigJSONData) > 0 {
				for _, context := range bwContext.DockerConfigJSONData {
					item, ok := cfgComparableItemsByName[context.BWItem]
					if !ok {
						item = &comparable{
							fields:      sets.NewString(),
							attachments: sets.NewString(),
						}
					}

					item.fields = insertIfNotEmpty(item.fields, context.RegistryURLBitwardenField, context.RegistryURLBitwardenField, context.EmailBitwardenField)
					item.attachments = insertIfNotEmpty(item.attachments, context.AuthBitwardenAttachment)

					cfgComparableItemsByName[context.BWItem] = item
				}
			}
		}
	}

	return cfgComparableItemsByName
}

func insertIfNotEmpty(s sets.String, items ...string) sets.String {
	for _, item := range items {
		if item != "" {
			s.Insert(item)
		}
	}
	return s
}

func getUnusedBWItems(config secretbootstrap.Config, client secrets.ReadOnlyClient, bwAllowUnused sets.String, allowUnusedAfter time.Time) error {
	allSecretStoreItems, err := client.GetInUseInformationForAllItems()
	if err != nil {
		return fmt.Errorf("failed to get in-use information from secret store: %w", err)
	}
	cfgComparableItemsByName := constructConfigItemsByName(config)

	unused := make(map[string]*comparable)
	for itemName, item := range allSecretStoreItems {
		l := logrus.WithField("bw_item", itemName)
		if item.LastChanged().After(allowUnusedAfter) {
			logrus.WithFields(logrus.Fields{
				"bw_item":   itemName,
				"threshold": allowUnusedAfter,
				"modified":  item.LastChanged(),
			}).Info("Unused item last modified after threshold")
			continue
		}

		if _, ok := cfgComparableItemsByName[itemName]; !ok {
			if bwAllowUnused.Has(itemName) {
				l.Info("Unused item allowed by arguments")
				continue
			}

			unused[itemName] = &comparable{}
			continue
		}

		diffFields := item.UnusedFields(cfgComparableItemsByName[itemName].fields)
		if diffFields.Len() > 0 {
			if bwAllowUnused.Has(itemName) {
				l.WithField("fields", strings.Join(diffFields.List(), ",")).Info("Unused fields from item are allowed by arguments")
				continue
			}

			if _, ok := unused[itemName]; !ok {
				unused[itemName] = &comparable{}
			}
			unused[itemName].fields = diffFields
		}

		diffAttachments := item.UnusedAttachments(cfgComparableItemsByName[itemName].attachments)
		if diffAttachments.Len() > 0 {
			if bwAllowUnused.Has(itemName) {
				l.WithField("attachments", strings.Join(diffAttachments.List(), ",")).Info("Unused attachments from item are allowed by arguments")
				continue
			}

			if _, ok := unused[itemName]; !ok {
				unused[itemName] = &comparable{}
			}
			unused[itemName].attachments = diffAttachments
		}

		if item.HasPassword() && !cfgComparableItemsByName[itemName].hasPassword {
			if bwAllowUnused.Has(itemName) {
				l.Info("Unused password fields from item is allowed by arguments")
				continue
			}

			if _, ok := unused[itemName]; !ok {
				unused[itemName] = &comparable{}
			}
			unused[itemName].hasPassword = true
		}

		if superfluousFields := item.SuperfluousFields(); len(superfluousFields) > 0 {
			if bwAllowUnused.Has(itemName) {
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
		err := fmt.Sprintf("Unused bw item: '%s'", name)
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

func (o *options) validateBWItems(client secrets.ReadOnlyClient) error {
	var errs []error

	for _, config := range o.config.Secrets {
		for _, item := range config.From {
			logger := logrus.WithField("item", item.BWItem)

			if item.DockerConfigJSONData != nil {
				for _, data := range item.DockerConfigJSONData {
					hasItem, err := client.HasItem(data.BWItem)
					if err != nil {
						errs = append(errs, fmt.Errorf("failed to check if item %s exists: %w", data.BWItem, err))
						continue
					}
					if !hasItem {
						errs = append(errs, fmt.Errorf("item %s doesn't exist", data.BWItem))
						break
					}
					if _, err := client.GetAttachmentOnItem(data.BWItem, data.AuthBitwardenAttachment); err != nil {
						errs = append(errs, fmt.Errorf("attachment %s in item %s doesn't exist", data.AuthBitwardenAttachment, data.BWItem))
					}
				}
			} else {
				hasItem, err := client.HasItem(item.BWItem)
				if err != nil {
					errs = append(errs, fmt.Errorf("failed to check if item %s exists: %w", item.BWItem, err))
					continue
				}
				if !hasItem {
					if o.generatorConfig.IsItemGenerated(item.BWItem) {
						logrus.Warn("Item doesn't exist but it will be generated")
					} else {
						errs = append(errs, fmt.Errorf("item %s doesn't exist", item.BWItem))
						continue
					}
				}

				if item.Field != "" {
					if _, err := client.GetFieldOnItem(item.BWItem, item.Field); err != nil {
						if o.generatorConfig.IsFieldGenerated(item.BWItem, item.Field) {
							logger.WithField("field", item.Field).Warn("Field doesn't exist but it will be generated")
						} else {
							errs = append(errs, fmt.Errorf("field %s in item %s doesn't exist", item.Field, item.BWItem))
						}
					}
				}

				if item.Attachment != "" {
					if _, err := client.GetAttachmentOnItem(item.BWItem, item.Attachment); err != nil {
						if o.generatorConfig.IsFieldGenerated(item.BWItem, item.Attachment) {
							logger.WithField("attachment", item.Attachment).Warn("Attachment doesn't exist but it will be generated")
						} else {
							errs = append(errs, fmt.Errorf("attachment %s in item %s doesn't exist", item.Attachment, item.BWItem))
						}
					}
				}

				if item.Attribute == secretbootstrap.AttributeTypePassword {
					if _, err := client.GetPassword(item.BWItem); err != nil {
						if o.generatorConfig.IsFieldGenerated(item.BWItem, string(item.Attribute)) {
							logger.WithField("attribute", item.Attribute).Warn("Attribute doesn't exist but it will be generated")
						} else {
							errs = append(errs, fmt.Errorf("password in item %s doesn't exist", item.BWItem))
						}
					}
				}
			}
		}
	}

	return utilerrors.NewAggregate(errs)
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
	if err := o.completeOptions(&censor); err != nil {
		logrus.WithError(err).Error("Failed to complete options.")
	}
	client, err := o.secrets.NewReadOnlyClient(&censor)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to create client.")
	}

	if errs := reconcileSecrets(o, client); len(errs) > 0 {
		logrus.WithError(utilerrors.NewAggregate(errs)).Fatalf("errors while updating secrets")
	}
}

func reconcileSecrets(o options, client secrets.ReadOnlyClient) (errs []error) {
	defer func() {
		if _, err := client.Logout(); err != nil {
			errs = append(errs, fmt.Errorf("failed to logout: %w", err))
		}
	}()

	if o.validateOnly {
		var config secretbootstrap.Config
		if err := secretbootstrap.LoadConfigFromFile(o.configPath, &config); err != nil {
			return append(errs, fmt.Errorf("failed to load config from file: %s", o.configPath))
		}
		if err := config.Validate(); err != nil {
			return append(errs, fmt.Errorf("failed to validate the config: %w", err))
		}

		if err := o.validateBWItems(client); err != nil {
			return append(errs, fmt.Errorf("failed to validate items: %w", err))
		}

		logrus.Infof("the config file %s has been validated", o.configPath)
		return nil
	}

	ctx := context.TODO()
	// errors returned by constructSecrets will be handled once the rest of the secrets have been uploaded
	secretsMap, err := constructSecrets(ctx, o.config, client, o.maxConcurrency)
	if err != nil {
		errs = append(errs, err)
	}

	if o.validateBWItemsUsage {
		unusedGracePeriod := time.Now().AddDate(0, 0, -allowUnusedDays)
		err := getUnusedBWItems(o.config, client, o.bwAllowUnused.StringSet(), unusedGracePeriod)
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
		if err := updateSecrets(o.secretsGetters, secretsMap, o.force); err != nil {
			errs = append(errs, fmt.Errorf("failed to update secrets: %w", err))
		}
		logrus.Info("Updated secrets.")
	}

	return errs
}
