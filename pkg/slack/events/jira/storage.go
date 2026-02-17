package jira

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// ThreadJiraMapping stores and retrieves mappings between Slack thread timestamps and Jira issue keys.
type ThreadJiraMapping struct {
	client    ctrlruntimeclient.Client
	namespace string
	cmName    string
}

// NewThreadJiraMapping creates a new ThreadJiraMapping instance.
func NewThreadJiraMapping(client ctrlruntimeclient.Client, namespace, cmName string) *ThreadJiraMapping {
	return &ThreadJiraMapping{
		client:    client,
		namespace: namespace,
		cmName:    cmName,
	}
}

// Store saves a mapping between a thread timestamp and a Jira issue key.
func (s *ThreadJiraMapping) Store(ctx context.Context, threadTS, jiraKey string) error {
	cm := &corev1.ConfigMap{}
	key := ctrlruntimeclient.ObjectKey{Namespace: s.namespace, Name: s.cmName}

	err := s.client.Get(ctx, key, cm)
	if apierrors.IsNotFound(err) {
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      s.cmName,
				Namespace: s.namespace,
			},
			Data: make(map[string]string),
		}
	} else if err != nil {
		return fmt.Errorf("failed to get ConfigMap: %w", err)
	}

	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}
	cm.Data[threadTS] = jiraKey

	if err := s.client.Get(ctx, key, cm); apierrors.IsNotFound(err) {
		return s.client.Create(ctx, cm)
	}
	return s.client.Update(ctx, cm)
}

// Get retrieves a Jira issue key for a given thread timestamp.
func (s *ThreadJiraMapping) Get(ctx context.Context, threadTS string) (string, bool) {
	cm := &corev1.ConfigMap{}
	key := ctrlruntimeclient.ObjectKey{Namespace: s.namespace, Name: s.cmName}

	if err := s.client.Get(ctx, key, cm); err != nil {
		return "", false
	}

	if cm.Data == nil {
		return "", false
	}

	jiraKey, exists := cm.Data[threadTS]
	return jiraKey, exists
}
