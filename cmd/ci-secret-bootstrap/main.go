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
	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
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

func (o *options) completeOptions() error {
	bytes, err := ioutil.ReadFile(o.bwPasswordPath)
	if err != nil {
		return err
	}
	o.bwPassword = strings.TrimSpace(string(bytes))
	secrets := sets.NewString(o.bwPassword)
	logrus.SetFormatter(logrusutil.NewCensoringFormatter(logrus.StandardLogger().Formatter, func() sets.String {
		return secrets
	}))

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
	for i, secretConfig := range o.config {
		for j, secretContext := range secretConfig.To {
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
			if bwContext.Field == "" && bwContext.Attachment == "" {
				return fmt.Errorf("config[%d].from[%s]: either field or attachment needs to be non-empty", i, key)
			}
			if bwContext.Field != "" && bwContext.Attachment != "" {
				return fmt.Errorf("config[%d].from[%s]: cannot use both field and attachment", i, key)
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

type bitWardenContext struct {
	BWItem     string `json:"bw_item"`
	Field      string `json:"field,omitempty"`
	Attachment string `json:"attachment,omitempty"`
}

type secretContext struct {
	Cluster   string `json:"cluster"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

type secretConfig struct {
	From map[string]bitWardenContext `json:"from"`
	To   []secretContext             `json:"to"`
}

func constructSecrets(config []secretConfig, bwClient bitwarden.Client) (map[string][]*coreapi.Secret, error) {
	secretsMap := map[string][]*coreapi.Secret{}
	for _, secretConfig := range config {
		data := make(map[string][]byte)
		for key, bwContext := range secretConfig.From {
			var value []byte
			var err error
			if bwContext.Field != "" {
				value, err = bwClient.GetFieldOnItem(bwContext.BWItem, bwContext.Field)
			} else {
				value, err = bwClient.GetAttachmentOnItem(bwContext.BWItem, bwContext.Attachment)
			}
			if err != nil {
				return nil, err
			}
			data[key] = value
		}
		for _, secretContext := range secretConfig.To {
			secret := &coreapi.Secret{
				Data: data,
				ObjectMeta: meta.ObjectMeta{
					Name:      secretContext.Name,
					Namespace: secretContext.Namespace,
					Labels:    map[string]string{"ci.openshift.org/auto-managed": "true"},
				},
			}
			secretsMap[secretContext.Cluster] = append(secretsMap[secretContext.Cluster], secret)
		}
	}
	return secretsMap, nil
}

func updateSecrets(secretsGetters map[string]coreclientset.SecretsGetter, secretsMap map[string][]*coreapi.Secret) error {
	for cluster, secrets := range secretsMap {
		for _, secret := range secrets {
			secretsGetter := secretsGetters[cluster]
			if _, err := secretsGetter.Secrets(secret.Namespace).Create(secret); err != nil {
				if !kerrors.IsAlreadyExists(err) {
					return err
				}
				if _, err := secretsGetter.Secrets(secret.Namespace).Update(secret); err != nil {
					return err
				}
			}
		}
	}
	return nil
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
	if err := o.validateOptions(); err != nil {
		logrus.WithError(err).Fatal("Invalid arguments.")
	}
	if err := o.completeOptions(); err != nil {
		logrus.WithError(err).Fatal("Failed to complete options.")
	}
	bwClient, err := bitwarden.NewClient(o.bwUser, o.bwPassword)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to get Bitwarden client.")
	}

	secretsMap, err := constructSecrets(o.config, bwClient)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to get secrets.")
	}

	if o.dryRun {
		if err := printSecrets(secretsMap, os.Stdout); err != nil {
			logrus.WithError(err).Fatalf("Failed to print secrets on dry run.")
		}
	} else {
		if err := updateSecrets(o.secretsGetters, secretsMap); err != nil {
			logrus.WithError(err).Fatalf("Failed to update secrets.")
		}
	}
}
