package util

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	crcontrollerutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// EnsureNamespaceNotMonitored make sure the namespace is ignored by openshift-monitoring
func EnsureNamespaceNotMonitored(ctx context.Context, name string, client ctrlruntimeclient.Client, log *logrus.Entry) error {
	ns := &corev1.Namespace{}
	if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Name: name}, ns); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("The namespace is deleted")
			return nil
		}
		return fmt.Errorf("failed to get the namespace %s: %w", name, err)
	}

	s, mutateFn := namespace(ns)
	return upsertObject(ctx, client, s, mutateFn, log)
}

func namespace(template *corev1.Namespace) (*corev1.Namespace, crcontrollerutil.MutateFn) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: template.Name,
		},
	}

	return ns, func() error {
		labels := template.Labels
		if labels == nil {
			labels = map[string]string{}
		}
		labels["openshift.io/user-monitoring"] = "false"
		if labels["openshift.io/cluster-monitoring"] == "true" {
			delete(labels, "openshift.io/cluster-monitoring")
		}
		ns.Labels = labels
		return nil
	}
}
