package dispatcher

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	dispatcherv1 "github.com/openshift/ci-tools/pkg/api/dispatcher/v1"
)

// OverrideStore is the durable source of dispatcher runtime overrides.
type OverrideStore interface {
	List(context.Context) ([]dispatcherv1.DispatchOverride, error)
	Get(context.Context, string) (*dispatcherv1.DispatchOverride, error)
	Create(context.Context, *dispatcherv1.DispatchOverride) error
	Update(context.Context, *dispatcherv1.DispatchOverride) error
	UpdateStatus(context.Context, *dispatcherv1.DispatchOverride) error
}

// KubernetesOverrideStore persists DispatchOverride custom resources.
type KubernetesOverrideStore struct {
	client    ctrlruntimeclient.Client
	namespace string
}

// NewKubernetesOverrideStore creates a namespace-scoped Kubernetes override store.
func NewKubernetesOverrideStore(client ctrlruntimeclient.Client, namespace string) *KubernetesOverrideStore {
	return &KubernetesOverrideStore{client: client, namespace: namespace}
}

// List returns every override in deterministic name order.
func (s *KubernetesOverrideStore) List(ctx context.Context) ([]dispatcherv1.DispatchOverride, error) {
	var list dispatcherv1.DispatchOverrideList
	if err := s.client.List(ctx, &list, ctrlruntimeclient.InNamespace(s.namespace)); err != nil {
		return nil, fmt.Errorf("list DispatchOverrides: %w", err)
	}
	sort.Slice(list.Items, func(i, j int) bool { return list.Items[i].Name < list.Items[j].Name })
	return list.Items, nil
}

// Get returns a named override.
func (s *KubernetesOverrideStore) Get(ctx context.Context, name string) (*dispatcherv1.DispatchOverride, error) {
	var override dispatcherv1.DispatchOverride
	if err := s.client.Get(ctx, types.NamespacedName{Namespace: s.namespace, Name: name}, &override); err != nil {
		return nil, err
	}
	return &override, nil
}

// Create persists a new override.
func (s *KubernetesOverrideStore) Create(ctx context.Context, override *dispatcherv1.DispatchOverride) error {
	override.Namespace = s.namespace
	return s.client.Create(ctx, override)
}

// Update persists spec changes such as approval or cancellation.
func (s *KubernetesOverrideStore) Update(ctx context.Context, override *dispatcherv1.DispatchOverride) error {
	return s.client.Update(ctx, override)
}

// UpdateStatus persists controller-observed state.
func (s *KubernetesOverrideStore) UpdateStatus(ctx context.Context, override *dispatcherv1.DispatchOverride) error {
	return s.client.Status().Update(ctx, override)
}

// MemoryOverrideStore is an in-process store intended for tests and shadow-mode development.
type MemoryOverrideStore struct {
	mu        sync.Mutex
	overrides map[string]*dispatcherv1.DispatchOverride
}

// NewMemoryOverrideStore creates an empty memory store.
func NewMemoryOverrideStore() *MemoryOverrideStore {
	return &MemoryOverrideStore{overrides: make(map[string]*dispatcherv1.DispatchOverride)}
}

// List returns every stored override in deterministic order.
func (s *MemoryOverrideStore) List(_ context.Context) ([]dispatcherv1.DispatchOverride, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]dispatcherv1.DispatchOverride, 0, len(s.overrides))
	for _, override := range s.overrides {
		result = append(result, *override.DeepCopy())
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

// Get returns a named override.
func (s *MemoryOverrideStore) Get(_ context.Context, name string) (*dispatcherv1.DispatchOverride, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	override, exists := s.overrides[name]
	if !exists {
		return nil, apierrors.NewNotFound(dispatcherv1.Resource("dispatchoverrides"), name)
	}
	return override.DeepCopy(), nil
}

// Create stores a new override and rejects duplicate names.
func (s *MemoryOverrideStore) Create(_ context.Context, override *dispatcherv1.DispatchOverride) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.overrides[override.Name]; exists {
		return apierrors.NewAlreadyExists(dispatcherv1.Resource("dispatchoverrides"), override.Name)
	}
	override.ResourceVersion = "1"
	s.overrides[override.Name] = override.DeepCopy()
	return nil
}

// Update applies spec changes while preserving status as the Kubernetes status subresource does.
func (s *MemoryOverrideStore) Update(_ context.Context, override *dispatcherv1.DispatchOverride) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, exists := s.overrides[override.Name]
	if !exists {
		return apierrors.NewNotFound(dispatcherv1.Resource("dispatchoverrides"), override.Name)
	}
	if override.ResourceVersion == "" || override.ResourceVersion != stored.ResourceVersion {
		return apierrors.NewConflict(dispatcherv1.Resource("dispatchoverrides"), override.Name, fmt.Errorf("resource version %q does not match current version %q", override.ResourceVersion, stored.ResourceVersion))
	}
	resourceVersion, err := strconv.ParseUint(stored.ResourceVersion, 10, 64)
	if err != nil {
		return fmt.Errorf("parse stored DispatchOverride resource version: %w", err)
	}
	updated := stored.DeepCopy()
	updated.Spec = *override.Spec.DeepCopy()
	updated.ResourceVersion = strconv.FormatUint(resourceVersion+1, 10)
	s.overrides[override.Name] = updated
	override.ResourceVersion = updated.ResourceVersion
	return nil
}

// UpdateStatus replaces status on an existing override.
func (s *MemoryOverrideStore) UpdateStatus(_ context.Context, override *dispatcherv1.DispatchOverride) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, exists := s.overrides[override.Name]
	if !exists {
		return apierrors.NewNotFound(dispatcherv1.Resource("dispatchoverrides"), override.Name)
	}
	stored.Status = *override.Status.DeepCopy()
	return nil
}
