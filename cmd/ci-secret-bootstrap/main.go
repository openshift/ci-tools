package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/ghodss/yaml"
	"github.com/sirupsen/logrus"

	"golang.org/x/sync/semaphore"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes/scheme"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/test-infra/prow/logrusutil"

	"github.com/openshift/ci-tools/pkg/api/secretbootstrap"
	"github.com/openshift/ci-tools/pkg/bitwarden"
	"github.com/openshift/ci-tools/pkg/util"
)

type options struct {
	dryRun         bool
	kubeConfigPath string
	configPath     string
	bwUser         string
	bwPasswordPath string
	cluster        string
	force          bool
	logLevel       string

	impersonateUser string

	bwPassword     string
	secretsGetters map[string]coreclientset.SecretsGetter
	config         secretbootstrap.Config

	maxConcurrency int
}

func parseOptions() options {
	var o options
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
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

	bytes, err = ioutil.ReadFile(o.configPath)
	if err != nil {
		return err
	}
	var config secretbootstrap.Config
	err = yaml.Unmarshal(bytes, &config)
	if err != nil {
		return err
	}

	o.secretsGetters = map[string]coreclientset.SecretsGetter{}
	for i, secretConfig := range config {
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
			o.config = append(o.config, secretConfig)
		}
	}

	if o.maxConcurrency == 0 {
		o.maxConcurrency = runtime.GOMAXPROCS(0)
	}
	logrus.Infof("The max concurrency is %d", o.maxConcurrency)

	return o.validateCompletedOptions()
}

func (o *options) validateCompletedOptions() error {
	if o.bwPassword == "" {
		return fmt.Errorf("--bw-password-file was empty")
	}
	if len(o.config) == 0 {
		msg := "no secrets found to sync"
		if o.cluster != "" {
			msg = msg + " for --cluster=" + o.cluster
		}
		return fmt.Errorf(msg)
	}
	toMap := map[string]map[string]string{}
	for i, secretConfig := range o.config {
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
			if bwContext.BWItem == "" {
				return fmt.Errorf("config[%d].from[%s]: empty value is not allowed", i, key)
			}
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
				return fmt.Errorf("config[%d].to[%d]: secret %v listed more than once in the config", i, j, secretContext)
			}
		}
	}
	return nil
}

func constructSecrets(ctx context.Context, config secretbootstrap.Config, bwClient bitwarden.Client, maxConcurrency int) (map[string][]*coreapi.Secret, error) {
	sem := semaphore.NewWeighted(int64(maxConcurrency))
	secretsMap := map[string][]*coreapi.Secret{}
	secretsMapLock := &sync.Mutex{}

	var potentialErrors int
	for _, item := range config {
		potentialErrors = potentialErrors + len(item.From)
	}
	errChan := make(chan error, potentialErrors)

	secretConfigWG := &sync.WaitGroup{}
	for _, cfg := range config {
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
					} else {
						switch bwContext.Attribute {
						case secretbootstrap.AttributeTypePassword:
							value, err = bwClient.GetPassword(bwContext.BWItem)
						default:
							// should never happen since we have validated the config
							errChan <- fmt.Errorf("invalid attribute: only the '%s' is supported, not %s", secretbootstrap.AttributeTypePassword, bwContext.Attribute)
							return
						}
					}
					if err != nil {
						errChan <- err
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
			s := json.NewSerializerWithOptions(json.DefaultMetaFactory, scheme.Scheme,
				scheme.Scheme, json.SerializerOptions{Yaml: true, Pretty: true, Strict: true})
			if err := s.Encode(secret, w); err != nil {
				return err
			}
		}
	}
	return nil
}

func main() {
	o := parseOptions()
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
	secretsMap, err := constructSecrets(ctx, o.config, bwClient, o.maxConcurrency)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to get secrets.")
	}

	if o.dryRun {
		tmpFile, err := ioutil.TempFile("", "ci-secret-bootstrapper")
		if err != nil {
			logrus.WithError(err).Fatal("failed to create tempfile")
		}
		defer tmpFile.Close()
		logrus.Infof("Dry-Run enabled, writing secrets to %s", tmpFile.Name())
		if err := writeSecrets(secretsMap, tmpFile); err != nil {
			logrus.WithError(err).Fatalf("Failed to write secrets on dry run.")
		}
	} else {
		if err := updateSecrets(o.secretsGetters, secretsMap, o.force); err != nil {
			logrus.WithError(err).Fatalf("Failed to update secrets.")
		}
		logrus.Info("Updated secrets.")
	}
}
