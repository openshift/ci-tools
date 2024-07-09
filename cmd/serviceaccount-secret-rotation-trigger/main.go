package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	"sigs.k8s.io/prow/pkg/flagutil"
	"sigs.k8s.io/prow/pkg/logrusutil"

	serviceaccountsecretrefresher "github.com/openshift/ci-tools/pkg/controller/serviceaccount_secret_refresher"
)

type options struct {
	kubernetesOptions flagutil.KubernetesOptions
	namespaces        flagutil.Strings
	dry               bool
}

func opts() (*options, error) {
	o := &options{kubernetesOptions: flagutil.KubernetesOptions{NOInClusterConfigDefault: true}}
	fs := flag.CommandLine
	o.kubernetesOptions.AddFlags(fs)
	fs.Var(&o.namespaces, "namespace", "Namespace to run in, can be passed multiple times")
	fs.BoolVar(&o.dry, "dry-run", true, "Enable dry-run")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return nil, fmt.Errorf("failed to parse flags: %w", err)
	}
	return o, nil
}

func main() {
	logrusutil.ComponentInit()

	o, err := opts()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to get options")
	}
	if len(o.namespaces.Strings()) == 0 {
		logrus.Fatal("Must pass at least one namespace")
	}
	if err := o.kubernetesOptions.Validate(o.dry); err != nil {
		logrus.WithError(err).Fatal("Failed to validate the kubernetesOptions")
	}

	loadedKubeconfigs, err := o.kubernetesOptions.LoadClusterConfigs()
	if err != nil {
		logrus.WithError(err).Warn("Failed to load kubeconfigs")
	}
	if len(loadedKubeconfigs) == 0 {
		logrus.Fatal("No kubeconfigs available")
	}
	kubeconfigs := map[string]rest.Config{}
	for cluster, kubeconfig := range loadedKubeconfigs {
		kubeconfig.QPS = 50
		kubeconfig.Burst = 500
		kubeconfigs[cluster] = kubeconfig
	}

	ctx := signals.SetupSignalHandler()

	clients := make(map[string]ctrlruntimeclient.Client, len(kubeconfigs))
	lock := sync.Mutex{}
	eg := errgroup.Group{}
	for clusterName, config := range kubeconfigs {
		clusterName, config := clusterName, config
		eg.Go(func() error {
			client, err := ctrlruntimeclient.New(&config, ctrlruntimeclient.Options{})
			if err != nil {
				logrus.WithError(err).WithField("cluster", clusterName).Warn("Failed to construct client for cluster")
				return nil
			}
			if o.dry {
				client = ctrlruntimeclient.NewDryRunClient(client)
			}
			logrus.WithField("cluster", clusterName).Info("Successfully constructed client")
			lock.Lock()
			defer lock.Unlock()
			clients[clusterName] = client
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		logrus.WithError(err).Warn("Failed to construct clients")
	}
	if len(clients) == 0 {
		logrus.Fatal("No clients available")
	}

	if err := clean(ctx, clients, o.namespaces.Strings()); err != nil {
		logrus.WithError(err).Fatal("failed to clean")
	}
}

func clean(ctx context.Context, clients map[string]ctrlruntimeclient.Client, namespaces []string) error {
	eg := errgroup.Group{}
	for clusterName, client := range clients {
		clusterName, client := clusterName, client
		for _, namespace := range namespaces {
			namespace := namespace
			eg.Go(func() error {
				return cleanNamespace(ctx, logrus.WithField("cluster", clusterName).WithField("namespace", namespace), client, namespace)
			})
		}
	}

	return eg.Wait()
}

func cleanNamespace(ctx context.Context, l *logrus.Entry, client ctrlruntimeclient.Client, namespace string) error {
	secretList := &corev1.SecretList{}
	if err := client.List(ctx, secretList, ctrlruntimeclient.InNamespace(namespace)); err != nil {
		return fmt.Errorf("failed to list secrets: %w", err)
	}

	eg := errgroup.Group{}
	for _, item := range secretList.Items {
		if _, hasSAAnnotation := item.Annotations[corev1.ServiceAccountUIDKey]; !hasSAAnnotation {
			continue
		}
		if _, hasTTLAnnotation := item.Annotations[serviceaccountsecretrefresher.TTLAnnotationKey]; hasTTLAnnotation {
			continue
		}
		item := item
		eg.Go(func() error {
			old := item.DeepCopy()
			item.Annotations[serviceaccountsecretrefresher.TTLAnnotationKey] = time.Now().Add(24 * time.Hour).Format(time.RFC3339)
			if err := client.Patch(ctx, &item, ctrlruntimeclient.MergeFrom(old)); err != nil {
				return fmt.Errorf("failed to patch secret %s/%s: %w", item.Namespace, item.Name, err)
			}
			l.WithField("secret", fmt.Sprintf("%s/%s", item.Namespace, item.Name)).Info("Set TTL annotation on secret")
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return fmt.Errorf("failed to add ttl annotation to secrets: %w", err)
	}

	serviceAccountList := &corev1.ServiceAccountList{}
	if err := client.List(ctx, serviceAccountList, ctrlruntimeclient.InNamespace(namespace)); err != nil {
		return fmt.Errorf("failed to list serviceaccounts: %w", err)
	}

	eg = errgroup.Group{}
	for _, item := range serviceAccountList.Items {
		item := item
		eg.Go(func() error {
			old := item.DeepCopy()
			item.ImagePullSecrets = nil
			item.Secrets = nil
			if err := client.Patch(ctx, &item, ctrlruntimeclient.MergeFrom(old)); err != nil {
				return fmt.Errorf("failed to update serviceaccount %s/%s: %w", item.Namespace, item.Name, err)
			}
			l.WithField("ServiceAccount", fmt.Sprintf("%s/%s", item.Namespace, item.Name)).Info("Removed secrets from SA to trigger re-creation")
			return nil
		})
	}

	return eg.Wait()
}
