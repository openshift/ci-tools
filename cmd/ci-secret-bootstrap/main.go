package main

import (
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"sort"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes/scheme"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/test-infra/prow/logrusutil"

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

	bwPassword     string
	secretsGetters map[string]coreclientset.SecretsGetter
	config         []secretConfig
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
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Errorf("cannot parse args: %q", os.Args[1:])
	}
	return o
}

func (o *options) validateOptions() error {
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
		return err
	}

	bytes, err = ioutil.ReadFile(o.configPath)
	if err != nil {
		return err
	}
	var config []secretConfig
	err = yaml.Unmarshal(bytes, &config)
	if err != nil {
		return err
	}
	o.config = config

	o.secretsGetters = map[string]coreclientset.SecretsGetter{}
	for i, secretConfig := range config {
		o.config[i].To = nil

		for j, secretContext := range secretConfig.To {
			if o.cluster != "" && o.cluster != secretContext.Cluster {
				logrus.WithField("cluster", o.cluster).Info("Skipping provisioniong of secret for cluster that does not match the one configured via --cluster")

				continue
			}
			o.config[i].To = append(o.config[i].To, secretContext)

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

	return o.validateCompletedOptions()
}

func (o *options) validateCompletedOptions() error {
	if o.bwPassword == "" {
		return fmt.Errorf("--bw-password-file was empty")
	}
	if len(o.config) == 0 {
		return fmt.Errorf("--config was empty")
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
			case attributeTypePassword, "":
			default:
				return fmt.Errorf("config[%d].from[%s].attribute: only the '%s' is supported, not %s", i, key, attributeTypePassword, bwContext.Attribute)
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

type attributeType string

const (
	attributeTypePassword attributeType = "password"
)

type bitWardenContext struct {
	BWItem     string        `json:"bw_item"`
	Field      string        `json:"field,omitempty"`
	Attachment string        `json:"attachment,omitempty"`
	Attribute  attributeType `json:"attribute,omitempty"`
}

type secretContext struct {
	Cluster   string             `json:"cluster"`
	Namespace string             `json:"namespace"`
	Name      string             `json:"name"`
	Type      coreapi.SecretType `json:"type,omitempty"`
}

type secretConfig struct {
	From map[string]bitWardenContext `json:"from"`
	To   []secretContext             `json:"to"`
}

func constructSecrets(config []secretConfig, bwClient bitwarden.Client) (map[string][]*coreapi.Secret, error) {
	secretsMap := map[string][]*coreapi.Secret{}
	for _, secretConfig := range config {
		data := make(map[string][]byte)
		var keys []string
		for key := range secretConfig.From {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			bwContext := secretConfig.From[key]
			var value []byte
			var err error
			if bwContext.Field != "" {
				value, err = bwClient.GetFieldOnItem(bwContext.BWItem, bwContext.Field)
			} else if bwContext.Attachment != "" {
				value, err = bwClient.GetAttachmentOnItem(bwContext.BWItem, bwContext.Attachment)
			} else {
				switch bwContext.Attribute {
				case attributeTypePassword:
					value, err = bwClient.GetPassword(bwContext.BWItem)
				default:
					// should never happen since we have validated the config
					return nil, fmt.Errorf("invalid attribute: only the '%s' is supported, not %s", attributeTypePassword, bwContext.Attribute)
				}
			}
			if err != nil {
				return nil, err
			}
			data[key] = value
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
			secretsMap[secretContext.Cluster] = append(secretsMap[secretContext.Cluster], secret)
		}
	}
	return secretsMap, nil
}

func updateSecrets(secretsGetters map[string]coreclientset.SecretsGetter, secretsMap map[string][]*coreapi.Secret, force bool) error {
	for cluster, secrets := range secretsMap {
		for _, secret := range secrets {
			logrus.Infof("handling secret: %s:%s/%s", cluster, secret.Namespace, secret.Name)
			secretsGetter := secretsGetters[cluster]
			if existingSecret, err := secretsGetter.Secrets(secret.Namespace).Get(secret.Name, meta.GetOptions{}); err == nil {
				if secret.Type != existingSecret.Type {
					return fmt.Errorf("cannot change secret type from %q to %q (immutable field): %s:%s/%s", existingSecret.Type, secret.Type, cluster, secret.Namespace, secret.Name)
				}
				if !force && !equality.Semantic.DeepEqual(secret.Data, existingSecret.Data) {
					logrus.Errorf("actual %s:%s/%s differs the expected:\n%s", cluster, secret.Namespace, secret.Name,
						cmp.Diff(bytesMapToStringMap(secret.Data), bytesMapToStringMap(existingSecret.Data)))
					return fmt.Errorf("found unsynchronized secret: %s:%s/%s", cluster, secret.Namespace, secret.Name)
				}
				if _, err := secretsGetter.Secrets(secret.Namespace).Update(secret); err != nil {
					return err
				}
			} else if kerrors.IsNotFound(err) {
				if _, err := secretsGetter.Secrets(secret.Namespace).Create(secret); err != nil {
					return err
				}
			} else {
				return err
			}
		}
	}
	return nil
}

func bytesMapToStringMap(bytesMap map[string][]byte) map[string]string {
	strMap := map[string]string{}
	for k, v := range bytesMap {
		strMap[k] = string(v)
	}
	return strMap
}

func printSecrets(secretsMap map[string][]*coreapi.Secret, w io.Writer) error {
	var clusters []string
	for cluster := range secretsMap {
		clusters = append(clusters, cluster)
	}
	sort.Strings(clusters)
	for _, cluster := range clusters {
		if _, err := fmt.Fprintln(w, fmt.Sprintf("###%s###", cluster)); err != nil {
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
	defer func() {
		if _, err := bwClient.Logout(); err != nil {
			logrus.WithError(err).Fatal("Failed to logout.")
		}
	}()

	secretsMap, err := constructSecrets(o.config, bwClient)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to get secrets.")
	}

	if o.dryRun {
		if err := printSecrets(secretsMap, os.Stdout); err != nil {
			logrus.WithError(err).Fatalf("Failed to print secrets on dry run.")
		}
	} else {
		if err := updateSecrets(o.secretsGetters, secretsMap, o.force); err != nil {
			logrus.WithError(err).Fatalf("Failed to update secrets.")
		}
	}
}
