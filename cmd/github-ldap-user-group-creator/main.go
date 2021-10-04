package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/kube"
	"k8s.io/test-infra/prow/logrusutil"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	userv1 "github.com/openshift/api/user/v1"

	"github.com/openshift/ci-tools/pkg/api"
)

type options struct {
	kubernetesOptions flagutil.KubernetesOptions

	mappingFile string
	logLevel    string
	dryRun      bool
}

func parseOptions() *options {
	opts := &options{}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	opts.kubernetesOptions.AddFlags(fs)
	fs.StringVar(&opts.logLevel, "log-level", "info", fmt.Sprintf("Log level is one of %v.", logrus.AllLevels))
	fs.BoolVar(&opts.dryRun, "dry-run", true, "Whether to run the controller-manager with dry-run")
	fs.StringVar(&opts.mappingFile, "mapping-file", "", "File to the mapping results of m(github_login)=kerberos_id.")
	if err := fs.Parse(os.Args[1:]); err != nil {
		logrus.WithError(err).Fatal("could not parse args")
	}
	return opts
}

func (o *options) validate() error {
	level, err := logrus.ParseLevel(o.logLevel)
	if err != nil {
		return fmt.Errorf("invalid log level specified: %w", err)
	}
	logrus.SetLevel(level)

	if o.mappingFile == "" {
		return fmt.Errorf("--mapping-file must not be empty")
	}
	return nil
}

const (
	appCIContextName = string(api.ClusterAPPCI)
	toolName         = "github-ldap-user-group-creator"
)

func addSchemes() error {
	if err := userv1.AddToScheme(scheme.Scheme); err != nil {
		return fmt.Errorf("failed to add userv1 to scheme: %w", err)
	}
	return nil
}

func main() {
	logrusutil.ComponentInit()

	opts := parseOptions()

	if err := opts.validate(); err != nil {
		logrus.WithError(err).Fatal("failed to validate the option")
	}

	if err := addSchemes(); err != nil {
		logrus.WithError(err).Fatal("failed to add schemes")
	}

	kubeconfigs, err := opts.kubernetesOptions.LoadClusterConfigs()
	if err != nil {
		logrus.WithError(err).Fatal("failed to load kubeconfigs")
	}

	inClusterConfig, hasInClusterConfig := kubeconfigs[kube.InClusterContext]
	delete(kubeconfigs, kube.InClusterContext)
	delete(kubeconfigs, kube.DefaultClusterAlias)

	if _, hasAppCi := kubeconfigs[appCIContextName]; !hasAppCi {
		if !hasInClusterConfig {
			logrus.WithError(err).Fatalf("had no context for '%s' and loading InClusterConfig failed", appCIContextName)
		}
		logrus.Infof("use InClusterConfig for %s", appCIContextName)
		kubeconfigs[appCIContextName] = inClusterConfig
	}

	kubeConfig := kubeconfigs[appCIContextName]
	appCIClient, err := ctrlruntimeclient.New(&kubeConfig, ctrlruntimeclient.Options{})
	if err != nil {
		logrus.WithError(err).Fatalf("could not create client")
	}

	clients := map[string]ctrlruntimeclient.Client{}
	for cluster, config := range kubeconfigs {
		cluster, config := cluster, config
		if cluster == appCIContextName {
			continue
		}
		client, err := ctrlruntimeclient.New(&config, ctrlruntimeclient.Options{})
		if err != nil {
			logrus.WithError(err).WithField("cluster", cluster).Fatal("could not create client for cluster")
		}
		clients[cluster] = client
	}

	clients[appCIContextName] = appCIClient

	ctx := interrupts.Context()

	mapping := func(path string) map[string]string {
		logrus.WithField("path", path).Debug("Loading the mapping file ...")
		bytes, err := ioutil.ReadFile(path)
		if err != nil {
			logrus.WithField("path", path).WithError(err).Debug("Failed to read file")
			return nil
		}
		var mapping map[string]string
		if err := yaml.Unmarshal(bytes, &mapping); err != nil {
			logrus.WithField("string(bytes)", string(bytes)).WithError(err).Debug("Failed to unmarshal")
			return nil
		}
		return mapping
	}(opts.mappingFile)

	if err := ensureGroups(ctx, clients, mapping, opts.dryRun); err != nil {
		logrus.WithError(err).Fatal("could not ensure groups")
	}
}

func ensureGroups(ctx context.Context, clients map[string]ctrlruntimeclient.Client, mapping map[string]string, dryRun bool) error {
	var errs []error
	for cluster, client := range clients {
		listOption := ctrlruntimeclient.MatchingLabels{
			api.DPTPRequesterLabel: toolName,
		}
		groups := &userv1.GroupList{}
		if err := client.List(ctx, groups, listOption); err != nil {
			errs = append(errs, fmt.Errorf("failed to list groups on cluster %s: %w", cluster, err))
		} else {
			for _, group := range groups.Items {
				_, ok := mapping[strings.TrimSuffix(group.Name, api.GroupSuffix)]
				if !strings.HasSuffix(group.Name, api.GroupSuffix) || !ok {
					logrus.WithField("cluster", cluster).WithField("group.Name", group.Name).Info("Deleting group ...")
					if dryRun {
						continue
					}
					if err := client.Delete(ctx, &userv1.Group{ObjectMeta: metav1.ObjectMeta{Name: group.Name}}); err != nil && !errors.IsNotFound(err) {
						errs = append(errs, fmt.Errorf("failed to delete group %s on cluster %s: %w", group.Name, cluster, err))
						continue
					}
					logrus.WithField("cluster", cluster).WithField("group.Name", group.Name).Info("Deleted group")
				}
			}
		}

		for githubLogin, kerberosId := range mapping {
			groupName := fmt.Sprintf("%s%s", githubLogin, api.GroupSuffix)
			logrus.WithField("cluster", cluster).WithField("groupName", groupName).Info("Upserting group ...")
			if dryRun {
				continue
			}
			if _, err := UpsertGroup(ctx, client, &userv1.Group{
				ObjectMeta: metav1.ObjectMeta{Name: groupName, Labels: map[string]string{api.DPTPRequesterLabel: toolName}},
				Users:      userv1.OptionalNames{githubLogin, kerberosId},
			}); err != nil {
				errs = append(errs, fmt.Errorf("failed to upsert group %s on cluster %s: %w", groupName, cluster, err))
				continue
			}
			logrus.WithField("cluster", cluster).WithField("group.Name", groupName).Info("Upserted group")
		}
	}
	return kerrors.NewAggregate(errs)
}

func UpsertGroup(ctx context.Context, client ctrlruntimeclient.Client, group *userv1.Group) (created bool, err error) {
	err = client.Create(ctx, group.DeepCopy())
	if err == nil {
		return true, nil
	}
	if !errors.IsAlreadyExists(err) {
		return false, err
	}
	existing := &userv1.Group{}
	if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: group.Name}, existing); err != nil {
		return false, err
	}
	if equality.Semantic.DeepEqual(group.Users, existing.Users) {
		return false, nil
	}
	if err := client.Delete(ctx, existing); err != nil {
		return false, fmt.Errorf("delete failed: %w", err)
	}
	// Recreate counts as "Update"
	return false, client.Create(ctx, group)
}
