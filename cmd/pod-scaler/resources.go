package main

import (
	"context"
	"sync"

	"github.com/openhistogram/circonusllhist"
	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/test-infra/prow/interrupts"
	"k8s.io/test-infra/prow/pjutil"
)

func newResourceServer(cpu, memory []*cacheReloader, health *pjutil.Health) *resourceServer {
	logger := logrus.WithField("component", "request_server")
	server := &resourceServer{
		logger:     logger,
		lock:       sync.RWMutex{},
		byMetaData: map[FullMetadata]corev1.ResourceRequirements{},
	}
	var infos []digestInfo
	for i := range cpu {
		infos = append(infos, digestInfo{name: cpu[i].name, data: cpu[i], digest: server.digestCPU})
	}
	for i := range memory {
		infos = append(infos, digestInfo{name: memory[i].name, data: memory[i], digest: server.digestMemory})
	}
	loadDone := digest(logger, infos...)
	interrupts.Run(func(ctx context.Context) {
		select {
		case <-ctx.Done():
			logger.Debug("Waiting for readiness cancelled.")
			return
		case <-loadDone:
			logger.Debug("Ready to serve resource request recommendations.")
			health.ServeReady()
		}
	})

	return server
}

type resourceServer struct {
	logger *logrus.Entry
	lock   sync.RWMutex
	// byMetaData caches resource requirements calculated for the full assortment of
	// metadata labels.
	byMetaData map[FullMetadata]corev1.ResourceRequirements
}

const (
	// cpuRequestQuantile is the quantile of CPU core usage data to use as the CPU request
	cpuRequestQuantile = 0.8
)

func formatCPU() toQuantity {
	return func(valueAtQuantile float64) *resource.Quantity {
		return resource.NewMilliQuantity(int64(valueAtQuantile*1000), resource.DecimalSI)
	}
}

func (s *resourceServer) digestCPU(data *CachedQuery) {
	s.logger.Debugf("Digesting new CPU consumption metrics.")
	s.digestData(data, cpuRequestQuantile, corev1.ResourceCPU, formatCPU())
}

const (
	// memRequestQuantile is the quantile of memory usage data to use as the memory request
	memRequestQuantile = 0.8
)

func formatMemory() toQuantity {
	return func(valueAtQuantile float64) *resource.Quantity {
		return resource.NewQuantity(int64(valueAtQuantile), resource.BinarySI)
	}
}

func (s *resourceServer) digestMemory(data *CachedQuery) {
	s.logger.Debugf("Digesting new CPU consumption metrics.")
	s.digestData(data, memRequestQuantile, corev1.ResourceMemory, formatMemory())
}

type toQuantity func(valueAtQuantile float64) (quantity *resource.Quantity)

func (s *resourceServer) digestData(data *CachedQuery, quantile float64, request corev1.ResourceName, quantity toQuantity) {
	s.logger.Debugf("Digesting %d identifiers.", len(data.DataByMetaData))
	i := 0
	for meta, fingerprints := range data.DataByMetaData {
		if i%(len(data.DataByMetaData)/10) == 0 {
			s.logger.Debugf("Digested %d/%d full identifiers.", i, len(data.DataByMetaData))
		}
		i += 1
		overall := circonusllhist.New()
		for _, fingerprint := range fingerprints {
			overall.Merge(data.Data[fingerprint])
		}
		valueAtQuantile := overall.ValueAtQuantile(quantile)
		s.lock.Lock()
		if _, exists := s.byMetaData[meta]; !exists {
			s.byMetaData[meta] = corev1.ResourceRequirements{
				Requests: corev1.ResourceList{},
				Limits:   corev1.ResourceList{},
			}
		}
		q := quantity(valueAtQuantile)
		s.byMetaData[meta].Requests[request] = *q
		s.lock.Unlock()
	}
	s.logger.Debug("Finished digesting new data.")
}

func (s *resourceServer) recommendedRequestFor(meta FullMetadata) (corev1.ResourceRequirements, bool) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	data, ok := s.byMetaData[meta]
	return data, ok
}
