package main

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/ci-tools/pkg/api"
	podscaler "github.com/openshift/ci-tools/pkg/pod-scaler"
)

func TestShouldBeMeasured(t *testing.T) {
	logger := logrus.WithField("test", "TestShouldBeMeasured")
	cache := NewMeasuredPodCache(logger)
	bqClient := &BigQueryClient{
		cache:  cache,
		logger: logger,
	}

	meta := podscaler.FullMetadata{
		Metadata: api.Metadata{
			Org:    "test-org",
			Repo:   "test-repo",
			Branch: "main",
		},
		Container: "test-container",
	}

	// Test case 1: No data exists - should be measured
	if !bqClient.ShouldBeMeasured(meta) {
		t.Error("Expected pod to be measured when no data exists")
	}

	// Test case 2: Recent data exists - should not be measured
	recentData := map[podscaler.FullMetadata]*MeasuredPodData{
		meta: {
			Metadata:         meta,
			LastMeasuredTime: time.Now().Add(-5 * 24 * time.Hour), // 5 days ago
		},
	}
	cache.Update(recentData)
	if bqClient.ShouldBeMeasured(meta) {
		t.Error("Expected pod not to be measured when recent data exists")
	}

	// Test case 3: Stale data exists - should be measured
	staleData := map[podscaler.FullMetadata]*MeasuredPodData{
		meta: {
			Metadata:         meta,
			LastMeasuredTime: time.Now().Add(-15 * 24 * time.Hour), // 15 days ago
		},
	}
	cache.Update(staleData)
	if !bqClient.ShouldBeMeasured(meta) {
		t.Error("Expected pod to be measured when data is stale (>10 days)")
	}
}

func TestClassifyPod(t *testing.T) {
	logger := logrus.WithField("test", "TestClassifyPod")
	cache := NewMeasuredPodCache(logger)
	bqClient := &BigQueryClient{
		cache:  cache,
		logger: logger,
	}

	// Test case 1: Pod with no data - should be classified as measured
	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod-1",
			Labels: map[string]string{
				"ci.openshift.io/metadata.org":    "test-org",
				"ci.openshift.io/metadata.repo":   "test-repo",
				"ci.openshift.io/metadata.branch": "main",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "test-container"},
			},
		},
	}

	ClassifyPod(pod1, bqClient, logger)
	if pod1.Labels[PodScalerLabelKey] != PodScalerLabelValueMeasured {
		t.Errorf("Expected pod to be classified as measured, got %s", pod1.Labels[PodScalerLabelKey])
	}

	// Test case 2: Pod with recent data - should be classified as normal
	// Note: We need to use the same metadata structure that MetadataFor creates
	meta2 := podscaler.MetadataFor(
		map[string]string{
			"ci.openshift.io/metadata.org":    "test-org",
			"ci.openshift.io/metadata.repo":   "test-repo",
			"ci.openshift.io/metadata.branch": "main",
		},
		"test-pod-2",
		"test-container",
	)
	recentData := map[podscaler.FullMetadata]*MeasuredPodData{
		meta2: {
			Metadata:         meta2,
			LastMeasuredTime: time.Now().Add(-5 * 24 * time.Hour), // 5 days ago
		},
	}
	cache.Update(recentData)

	pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod-2",
			Labels: map[string]string{
				"ci.openshift.io/metadata.org":    "test-org",
				"ci.openshift.io/metadata.repo":   "test-repo",
				"ci.openshift.io/metadata.branch": "main",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "test-container"},
			},
		},
	}

	ClassifyPod(pod2, bqClient, logger)
	if pod2.Labels[PodScalerLabelKey] != PodScalerLabelValueNormal {
		t.Errorf("Expected pod to be classified as normal, got %s", pod2.Labels[PodScalerLabelKey])
	}

	// Test case 3: Pod with nil BigQuery client - should default to measured
	pod3 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-pod-3",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "test-container"},
			},
		},
	}

	ClassifyPod(pod3, nil, logger)
	if pod3.Labels[PodScalerLabelKey] != PodScalerLabelValueMeasured {
		t.Errorf("Expected pod to be classified as measured when BigQuery client is nil, got %s", pod3.Labels[PodScalerLabelKey])
	}
}

func TestAddPodAntiAffinity(t *testing.T) {
	logger := logrus.WithField("test", "TestAddPodAntiAffinity")

	// Test case 1: Measured pod should avoid all pod-scaler labeled pods
	measuredPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "measured-pod",
			Labels: map[string]string{
				PodScalerLabelKey: PodScalerLabelValueMeasured,
			},
		},
		Spec: corev1.PodSpec{},
	}

	AddPodAntiAffinity(measuredPod, logger)
	if measuredPod.Spec.Affinity == nil {
		t.Fatal("Expected affinity to be set")
	}
	if measuredPod.Spec.Affinity.PodAntiAffinity == nil {
		t.Fatal("Expected podAntiAffinity to be set")
	}
	if len(measuredPod.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution) == 0 {
		t.Fatal("Expected required anti-affinity terms to be set")
	}

	// Test case 2: Normal pod should avoid measured pods
	normalPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "normal-pod",
			Labels: map[string]string{
				PodScalerLabelKey: PodScalerLabelValueNormal,
			},
		},
		Spec: corev1.PodSpec{},
	}

	AddPodAntiAffinity(normalPod, logger)
	if normalPod.Spec.Affinity == nil {
		t.Fatal("Expected affinity to be set")
	}
	if normalPod.Spec.Affinity.PodAntiAffinity == nil {
		t.Fatal("Expected podAntiAffinity to be set")
	}
	if len(normalPod.Spec.Affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution) == 0 {
		t.Fatal("Expected required anti-affinity terms to be set")
	}

	// Test case 3: Pod without label should not get anti-affinity
	unlabeledPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "unlabeled-pod",
		},
		Spec: corev1.PodSpec{},
	}

	AddPodAntiAffinity(unlabeledPod, logger)
	if unlabeledPod.Spec.Affinity != nil {
		t.Error("Expected affinity not to be set for unlabeled pod")
	}
}

func TestMeasuredPodCache(t *testing.T) {
	logger := logrus.WithField("test", "TestMeasuredPodCache")
	cache := NewMeasuredPodCache(logger)

	meta := podscaler.FullMetadata{
		Metadata: api.Metadata{
			Org:    "test-org",
			Repo:   "test-repo",
			Branch: "main",
		},
		Container: "test-container",
	}

	// Test Get with no data
	_, exists := cache.Get(meta)
	if exists {
		t.Error("Expected no data to exist initially")
	}

	// Test Update and Get
	data := map[podscaler.FullMetadata]*MeasuredPodData{
		meta: {
			Metadata:             meta,
			MaxCPUUtilization:    2.5,
			MaxMemoryUtilization: 4 * 1024 * 1024 * 1024, // 4Gi
			LastMeasuredTime:     time.Now(),
			ContainerDurations:   make(map[string]time.Duration),
		},
	}
	cache.Update(data)

	retrieved, exists := cache.Get(meta)
	if !exists {
		t.Error("Expected data to exist after update")
	}
	if retrieved.MaxCPUUtilization != 2.5 {
		t.Errorf("Expected MaxCPUUtilization to be 2.5, got %f", retrieved.MaxCPUUtilization)
	}
	if retrieved.MaxMemoryUtilization != 4*1024*1024*1024 {
		t.Errorf("Expected MaxMemoryUtilization to be 4Gi, got %d", retrieved.MaxMemoryUtilization)
	}
}
