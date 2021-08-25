package main

import (
	"context"
	"flag"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/test-infra/prow/flagutil"
	"k8s.io/test-infra/prow/logrusutil"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"

	serviceaccountsecretrefresher "github.com/openshift/ci-tools/pkg/controller/serviceaccount_secret_refresher"
	"github.com/openshift/ci-tools/pkg/util"
)

type options struct {
	kubeconfig    string
	kubeconfigDir string
	namespaces    flagutil.Strings
	dry           bool
}

func opts() *options {
	opts := &options{}
	flag.StringVar(&opts.kubeconfig, "kubeconfig", "", "The kubeconfig to use")
	flag.StringVar(&opts.kubeconfigDir, "kubeconfig-dir", "", "Path to the directory containing kubeconfig files to use")
	flag.Var(&opts.namespaces, "namespace", "Namespace to run in, can be passed multiple times")
	flag.BoolVar(&opts.dry, "dry-run", true, "Enable dry-run")
	flag.Parse()
	return opts
}

func main() {
	logrusutil.ComponentInit()

	o := opts()
	if len(o.namespaces.Strings()) == 0 {
		logrus.Fatal("Must pass at least one namespace")
	}
	kubeconfigs, err := util.LoadKubeConfigs(o.kubeconfig, o.kubeconfigDir, nil)
	if err != nil {
		logrus.WithError(err).Warn("Failed to load kubeconfigs")
	}
	if len(kubeconfigs) == 0 {
		logrus.Fatal("No kubeconfigs available")
	}
	for idx := range kubeconfigs {
		kubeconfigs[idx].QPS = 50
		kubeconfigs[idx].Burst = 500
	}

	ctx := signals.SetupSignalHandler()

	clients := make(map[string]ctrlruntimeclient.Client, len(kubeconfigs))
	lock := sync.Mutex{}
	eg := errgroup.Group{}
	for clusterName, config := range kubeconfigs {
		clusterName, config := clusterName, config
		eg.Go(func() error {
			client, err := ctrlruntimeclient.New(config, ctrlruntimeclient.Options{})
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
