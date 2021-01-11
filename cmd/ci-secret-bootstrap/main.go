package main

import (
	"bytes"
	"context"
	"encoding/json"
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
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubejson "k8s.io/apimachinery/pkg/runtime/serializer/json"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes/scheme"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/logrusutil"

	"github.com/openshift/ci-tools/pkg/api/secretbootstrap"
	"github.com/openshift/ci-tools/pkg/bitwarden"
	"github.com/openshift/ci-tools/pkg/kubernetes/pkg/credentialprovider"
	"github.com/openshift/ci-tools/pkg/util"
)

type options struct {
	dryRun               bool
	force                bool
	validateBWItemsUsage bool

	kubeConfigPath  string
	configPath      string
	bwUser          string
	bwPasswordPath  string
	cluster         string
	logLevel        string
	impersonateUser string
	bwPassword      string

	maxConcurrency int

	secretsGetters map[string]coreclientset.SecretsGetter
	config         secretbootstrap.Config
	bwAllowUnused  flagutil.Strings

	validateOnly bool
}

const (
	// When checking for unused secrets in BitWarden, only report secrets that were last modified before X days, allowing to set up
	// BitWarden items and matching bootstrap config without tripping an alert
	allowUnusedDays = 7
)

func parseOptions() options {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	o.bwAllowUnused = flagutil.NewStrings()
	fs.BoolVar(&o.validateOnly, "validate-only", false, "If set, the tool exists after validating its config file.")
	fs.Var(&o.bwAllowUnused, "bw-allow-unused", "One or more bitwarden items that will be ignored when the --validate-bitwarden-items-usage is specified")
	fs.BoolVar(&o.validateBWItemsUsage, "validate-bitwarden-items-usage", false, fmt.Sprintf("If set, the tool only validates if all attachments and custom fields that exist in BitWarden and were last modified before %d days ago are being used in the given config.", allowUnusedDays))
	fs.BoolVar(&o.dryRun, "dry-run", true, "Whether to actually create the secrets with oc command")
	fs.StringVar(&o.kubeConfigPath, "kubeconfig", "", "Path to the kubeconfig file to use for CLI requests.")
	fs.StringVar(&o.configPath, "config", "", "Path to the config file to use for this tool.")
	fs.StringVar(&o.bwUser, "bw-user", "", "Username to access BitWarden.")
	fs.StringVar(&o.bwPasswordPath, "bw-password-path", "", "Path to a password file to access BitWarden.")
	fs.StringVar(&o.cluster, "cluster", "", "If set, only provision secrets for this cluster")
	fs.BoolVar(&o.force, "force", false, "If true, update the secrets even if existing one differs from Bitwarden items instead of existing with error. Default false.")
	fs.StringVar(&o.logLevel, "log-level", "info", fmt.Sprintf("Log level is one of %v.", logrus.AllLevels))
	fs.StringVar(&o.impersonateUser, "as", "", "Username to impersonate")
	fs.IntVar(&o.maxConcurrency, "concurrency", 0, "Maximum number of concurrent in-flight goroutines to BitWarden.")
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Errorf("cannot parse args: %q", os.Args[1:])
	}
	return o
}

func (o *options) validateOptions() error {
	level, err := logrus.ParseLevel(o.logLevel)
	if err != nil {
		return fmt.Errorf("invalid log level specified: %w", err)
	}
	logrus.SetLevel(level)
	if o.bwUser == "" {
		return fmt.Errorf("--bw-user is empty")
	}
	if o.bwPasswordPath == "" {
		return fmt.Errorf("--bw-password-path is empty")
	}
	if o.configPath == "" {
		return fmt.Errorf("--config is empty")
	}
	if len(o.bwAllowUnused.Strings()) > 0 && !o.validateBWItemsUsage {
		return fmt.Errorf("--bw-allow-unused must be specified with --validate-bitwarden-items-usage")
	}
	return nil
}

func (o *options) completeOptions(secrets *sets.String) error {
	bytes, err := ioutil.ReadFile(o.bwPasswordPath)
	if err != nil {
		return err
	}
	o.bwPassword = strings.TrimSpace(string(bytes))
	secrets.Insert(o.bwPassword)

	kubeConfigs, _, err := util.LoadKubeConfigs(o.kubeConfigPath)
	if err != nil {
		// We will bail out later on if we don't have the have the right kubeconfigs
		logrus.WithError(err).Warn("Encountered errors while loading kubeconfigs")
	}
	if o.impersonateUser != "" {
		for _, kubeConfig := range kubeConfigs {
			kubeConfig.Impersonate = rest.ImpersonationConfig{UserName: o.impersonateUser}
		}
	}

	var config secretbootstrap.Config
	if err := secretbootstrap.LoadConfigFromFile(o.configPath, &config); err != nil {
		return err
	}

	o.secretsGetters = map[string]coreclientset.SecretsGetter{}
	for i, secretConfig := range config.Secrets {
		var to []secretbootstrap.SecretContext

		for j, secretContext := range secretConfig.To {
			if o.cluster != "" && o.cluster != secretContext.Cluster {
				logrus.WithFields(logrus.Fields{"target-cluster": o.cluster, "secret-cluster": secretContext.Cluster}).Debug("Skipping provisioniong of secret for cluster that does not match the one configured via --cluster")

				continue
			}
			to = append(to, secretContext)

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

		if len(to) > 0 {
			secretConfig.To = to
			o.config.Secrets = append(o.config.Secrets, secretConfig)
		}
	}

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
	if o.bwPassword == "" {
		return fmt.Errorf("--bw-password-file was empty")
	}
	if len(o.config.Secrets) == 0 {
		msg := "no secrets found to sync"
		if o.cluster != "" {
			msg = msg + " for --cluster=" + o.cluster
		}
		return fmt.Errorf(msg)
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
				switch bwContext.Attribute {
				case secretbootstrap.AttributeTypePassword, "":
				default:
					return fmt.Errorf("config[%d].from[%s].attribute: only the '%s' is supported, not %s", i, key, secretbootstrap.AttributeTypePassword, bwContext.Attribute)
				}
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

func constructDockerConfigJSON(bwClient bitwarden.Client, dockerConfigJSONData []secretbootstrap.DockerConfigJSONData) ([]byte, error) {
	auths := make(map[string]secretbootstrap.DockerAuth)

	for _, data := range dockerConfigJSONData {
		authData := secretbootstrap.DockerAuth{}

		registryURL := data.RegistryURL
		if registryURL == "" {
			registryURLBitwardenField, err := bwClient.GetFieldOnItem(data.BWItem, data.RegistryURLBitwardenField)
			if err != nil {
				return nil, fmt.Errorf("couldn't get the entry name from bw item %s: %w", data.BWItem, err)
			}
			registryURL = string(registryURLBitwardenField)
		}

		authBWAttachmentValue, err := bwClient.GetAttachmentOnItem(data.BWItem, data.AuthBitwardenAttachment)
		if err != nil {
			return nil, fmt.Errorf("couldn't get attachment '%s' from bw item %s: %w", data.AuthBitwardenAttachment, data.BWItem, err)
		}
		authData.Auth = string(bytes.TrimSpace(authBWAttachmentValue))

		if data.EmailBitwardenField != "" {
			emailValue, err := bwClient.GetFieldOnItem(data.BWItem, data.EmailBitwardenField)
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

func constructSecrets(ctx context.Context, config secretbootstrap.Config, bwClient bitwarden.Client, maxConcurrency int) (map[string][]*coreapi.Secret, error) {
	sem := semaphore.NewWeighted(int64(maxConcurrency))
	secretsMap := map[string][]*coreapi.Secret{}
	secretsMapLock := &sync.Mutex{}

	var potentialErrors int
	for _, item := range config.Secrets {
		potentialErrors = potentialErrors + len(item.From)
	}
	errChan := make(chan error, potentialErrors)

	secretConfigWG := &sync.WaitGroup{}
	for _, cfg := range config.Secrets {
		secretConfigWG.Add(1)

		go func(secretConfig secretbootstrap.SecretConfig) {
			defer secretConfigWG.Done()

			data := make(map[string][]byte)
			dataLock := &sync.Mutex{}
			var keys []string
			for key := range secretConfig.From {
				keys = append(keys, key)
			}
			sort.Strings(keys)

			for _, key := range keys {
				if err := sem.Acquire(ctx, 1); err != nil {
					errChan <- fmt.Errorf("failed to acquire semaphore for key %s: %w", key, err)
					continue
				}

				go func(key string) {
					defer sem.Release(1)
					bwContext := secretConfig.From[key]
					var value []byte
					var err error
					if bwContext.Field != "" {
						value, err = bwClient.GetFieldOnItem(bwContext.BWItem, bwContext.Field)
					} else if bwContext.Attachment != "" {
						value, err = bwClient.GetAttachmentOnItem(bwContext.BWItem, bwContext.Attachment)
					} else if len(bwContext.DockerConfigJSONData) > 0 {
						value, err = constructDockerConfigJSON(bwClient, bwContext.DockerConfigJSONData)
					} else {
						switch bwContext.Attribute {
						case secretbootstrap.AttributeTypePassword:
							value, err = bwClient.GetPassword(bwContext.BWItem)
						default:
							// should never happen since we have validated the config
							errChan <- fmt.Errorf("[%s] invalid attribute: only the '%s' is supported, not %s", key, secretbootstrap.AttributeTypePassword, bwContext.Attribute)
							return
						}
					}
					if err != nil {
						errChan <- fmt.Errorf("[%s] %w", key, err)
						return
					}
					dataLock.Lock()
					data[key] = value
					dataLock.Unlock()
				}(key)
			}

			for _, secretContext := range secretConfig.To {
				if secretContext.Type == "" {
					secretContext.Type = coreapi.SecretTypeOpaque
				}
				secret := &coreapi.Secret{
					Data: data,
					ObjectMeta: meta.ObjectMeta{
						Name:      secretContext.Name,
						Namespace: secretContext.Namespace,
						Labels:    map[string]string{"ci.openshift.org/auto-managed": "true"},
					},
					Type: secretContext.Type,
				}
				secretsMapLock.Lock()
				secretsMap[secretContext.Cluster] = append(secretsMap[secretContext.Cluster], secret)
				secretsMapLock.Unlock()
			}
		}(cfg)
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
	sort.Slice(errs, func(i, j int) bool {
		return errs[i] != nil && errs[j] != nil && errs[i].Error() < errs[j].Error()
	})
	return secretsMap, utilerrors.NewAggregate(errs)
}

func updateSecrets(secretsGetters map[string]coreclientset.SecretsGetter, secretsMap map[string][]*coreapi.Secret, force bool) error {
	var errs []error
	for cluster, secrets := range secretsMap {
		for _, secret := range secrets {
			logrus.Debugf("handling secret: %s:%s/%s", cluster, secret.Namespace, secret.Name)
			secretsGetter := secretsGetters[cluster]
			if existingSecret, err := secretsGetter.Secrets(secret.Namespace).Get(context.TODO(), secret.Name, meta.GetOptions{}); err == nil {
				if secret.Type != existingSecret.Type {
					errs = append(errs, fmt.Errorf("cannot change secret type from %q to %q (immutable field): %s:%s/%s", existingSecret.Type, secret.Type, cluster, secret.Namespace, secret.Name))
					continue
				}
				for k, v := range existingSecret.Data {
					if _, exists := secret.Data[k]; exists {
						continue
					}
					secret.Data[k] = v
				}
				if !force && !equality.Semantic.DeepEqual(secret.Data, existingSecret.Data) {
					logrus.Errorf("actual %s:%s/%s differs the expected:\n", cluster, secret.Namespace, secret.Name)
					errs = append(errs, fmt.Errorf("secret %s:%s/%s needs updating in place, use --force to do so", cluster, secret.Namespace, secret.Name))
					continue
				}
				if _, err := secretsGetter.Secrets(secret.Namespace).Update(context.TODO(), secret, meta.UpdateOptions{}); err != nil {
					errs = append(errs, fmt.Errorf("error updating secret %s:%s/%s: %w", cluster, secret.Namespace, secret.Name, err))
					continue
				}
				logrus.Debugf("updated secret: %s:%s/%s", cluster, secret.Namespace, secret.Name)
			} else if kerrors.IsNotFound(err) {
				if _, err := secretsGetter.Secrets(secret.Namespace).Create(context.TODO(), secret, meta.CreateOptions{}); err != nil {
					errs = append(errs, fmt.Errorf("error creating secret %s:%s/%s: %w", cluster, secret.Namespace, secret.Name, err))
					continue
				}
				logrus.Debugf("created secret: %s:%s/%s", cluster, secret.Namespace, secret.Name)
			} else {
				errs = append(errs, fmt.Errorf("error reading secret %s:%s/%s: %w", cluster, secret.Namespace, secret.Name, err))
			}
		}
	}
	return utilerrors.NewAggregate(errs)
}

func writeSecrets(secretsMap map[string][]*coreapi.Secret, w io.Writer) error {
	var clusters []string
	for cluster := range secretsMap {
		clusters = append(clusters, cluster)
	}
	sort.Strings(clusters)
	for _, cluster := range clusters {
		if _, err := fmt.Fprintf(w, "###%s###\n", cluster); err != nil {
			return err
		}
		for _, secret := range secretsMap[cluster] {
			if _, err := fmt.Fprintln(w, "---"); err != nil {
				return err
			}
			s := kubejson.NewSerializerWithOptions(kubejson.DefaultMetaFactory, scheme.Scheme,
				scheme.Scheme, kubejson.SerializerOptions{Yaml: true, Pretty: true, Strict: true})
			if err := s.Encode(secret, w); err != nil {
				return err
			}
		}
	}
	return nil
}

type comparable struct {
	fields       sets.String
	attachments  sets.String
	hasPassword  bool
	revisionTime time.Time
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
	return ret
}

func constructBWItemsByName(items []bitwarden.Item) map[string]*comparable {
	bwComparableItemsByName := make(map[string]*comparable)
	for _, item := range items {
		comparableItem := &comparable{}

		if len(item.Fields) > 0 {
			fields := sets.NewString()
			for _, item := range item.Fields {
				fields.Insert(item.Name)
			}

			comparableItem.fields = fields
		}

		if len(item.Attachments) > 0 {
			attachments := sets.NewString()
			for _, attachment := range item.Attachments {
				attachments.Insert(attachment.FileName)
			}

			comparableItem.attachments = attachments
		}

		if item.Login != nil && item.Login.Password != "" {
			comparableItem.hasPassword = true
		}

		if item.RevisionTime != nil {
			comparableItem.revisionTime = *item.RevisionTime
		}

		bwComparableItemsByName[item.Name] = comparableItem
	}

	return bwComparableItemsByName
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
				item.attachments.Insert(bwContext.Attachment)
				item.fields.Insert(bwContext.Field)

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
					item.fields.Insert(context.RegistryURLBitwardenField)
					item.fields.Insert(context.EmailBitwardenField)
					item.attachments.Insert(context.AuthBitwardenAttachment)

					cfgComparableItemsByName[context.BWItem] = item
				}
			}
		}
	}

	return cfgComparableItemsByName
}

func getUnusedBWItems(config secretbootstrap.Config, bwClient bitwarden.Client, bwAllowUnused sets.String, allowUnusedAfter time.Time) error {
	bwComparableItemsByName := constructBWItemsByName(bwClient.GetAllItems())
	cfgComparableItemsByName := constructConfigItemsByName(config)

	unused := make(map[string]*comparable)
	for bwName, item := range bwComparableItemsByName {
		if bwAllowUnused.Has(bwName) {
			logrus.WithField("bw_item", bwName).Info("Unused item allowed by arguments")
			continue
		}

		if item.revisionTime.After(allowUnusedAfter) {
			logrus.WithFields(logrus.Fields{
				"bw_item":   bwName,
				"threshold": allowUnusedAfter,
				"modified":  item.revisionTime,
			}).Info("Unused item last modified after threshold")
			continue
		}

		if _, ok := cfgComparableItemsByName[bwName]; !ok {
			unused[bwName] = item
			continue
		}

		diffFields := item.fields.Difference(cfgComparableItemsByName[bwName].fields)
		if diffFields.Len() > 0 {
			if _, ok := unused[bwName]; !ok {
				unused[bwName] = &comparable{}
			}
			unused[bwName].fields = diffFields
		}

		diffAttachments := item.attachments.Difference(cfgComparableItemsByName[bwName].attachments)
		if diffAttachments.Len() > 0 {
			if _, ok := unused[bwName]; !ok {
				unused[bwName] = &comparable{}
			}
			unused[bwName].attachments = diffAttachments
		}

		if item.hasPassword && !cfgComparableItemsByName[bwName].hasPassword {
			if _, ok := unused[bwName]; !ok {
				unused[bwName] = &comparable{}
			}
			unused[bwName].hasPassword = true
		}
	}

	var errs []error
	for name, item := range unused {
		errs = append(errs, fmt.Errorf("Unused bw item: '%s' with %s", name, item.string()))
	}

	sort.Slice(errs, func(i, j int) bool {
		return errs[i] != nil && errs[j] != nil && errs[i].Error() < errs[j].Error()
	})

	return utilerrors.NewAggregate(errs)
}

func main() {
	o := parseOptions()
	if o.validateOnly {
		var config secretbootstrap.Config
		if err := secretbootstrap.LoadConfigFromFile(o.configPath, &config); err != nil {
			logrus.WithError(err).Fatalf("failed to load config from file: %s", o.configPath)
		}
		if err := config.Validate(); err != nil {
			logrus.WithError(err).Fatal("failed to validate the config")
		}
		logrus.Infof("the config file %s has been validated", o.configPath)
		return
	}
	secrets := sets.NewString()
	logrus.SetFormatter(logrusutil.NewCensoringFormatter(logrus.StandardLogger().Formatter, func() sets.String {
		return secrets
	}))
	if err := o.validateOptions(); err != nil {
		logrus.WithError(err).Fatal("Invalid arguments.")
	}
	if err := o.completeOptions(&secrets); err != nil {
		logrus.WithError(err).Fatal("Failed to complete options.")
	}
	bwClient, err := bitwarden.NewClient(o.bwUser, o.bwPassword, func(s string) {
		secrets.Insert(s)
	})
	if err != nil {
		logrus.WithError(err).Fatal("Failed to get Bitwarden client.")
	}
	logrus.RegisterExitHandler(func() {
		if _, err := bwClient.Logout(); err != nil {
			logrus.WithError(err).Fatal("Failed to logout.")
		}
	})
	defer logrus.Exit(0)
	ctx := context.TODO()
	var errs []error
	// errors returned by constructSecrets will be handled once the rest of the secrets have been uploaded
	secretsMap, err := constructSecrets(ctx, o.config, bwClient, o.maxConcurrency)
	if err != nil {
		errs = append(errs, err)
	}

	if o.validateBWItemsUsage {
		unusedGracePeriod := time.Now().AddDate(0, 0, -allowUnusedDays)
		err := getUnusedBWItems(o.config, bwClient, o.bwAllowUnused.StringSet(), unusedGracePeriod)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if o.dryRun {
		tmpFile, err := ioutil.TempFile("", "ci-secret-bootstrapper")
		if err != nil {
			logrus.WithError(err).Fatal("failed to create tempfile")
		}
		defer tmpFile.Close()
		logrus.Infof("Dry-Run enabled, writing secrets to %s", tmpFile.Name())
		if err := writeSecrets(secretsMap, tmpFile); err != nil {
			errs = append(errs, fmt.Errorf("failed to write secrets on dry run: %w", err))
		}
	} else {
		if err := updateSecrets(o.secretsGetters, secretsMap, o.force); err != nil {
			errs = append(errs, fmt.Errorf("failed to update secrets: %w", err))
		}
		logrus.Info("Updated secrets.")
	}

	if len(errs) > 0 {
		logrus.WithError(utilerrors.NewAggregate(errs)).Fatalf("errors while updating secrets")
	}
}
