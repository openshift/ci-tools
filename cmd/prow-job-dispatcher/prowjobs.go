package main

import (
	"sync"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
)

type prowjobs struct {
	mu              sync.Mutex
	data            map[string]string
	jobsStoragePath string
}

func newProwjobs(jobsStoragePath string) *prowjobs {
	var loadedJobs map[string]string
	if err := readGob(jobsStoragePath, &loadedJobs); err != nil {
		logrus.Errorf("falling back to empty map, error reading Gob file: %v", err)
		loadedJobs = make(map[string]string)
	}
	return &prowjobs{
		data:            loadedJobs,
		mu:              sync.Mutex{},
		jobsStoragePath: jobsStoragePath,
	}
}

func (pjs *prowjobs) regenerate(prowjobs map[string]string) {
	pjs.mu.Lock()
	defer pjs.mu.Unlock()
	pjs.data = make(map[string]string, len(prowjobs))
	for key, value := range prowjobs {
		pjs.data[key] = value
	}
}

func (pjs *prowjobs) getDataCopy() map[string]string {
	pjs.mu.Lock()
	defer pjs.mu.Unlock()

	copy := make(map[string]string, len(pjs.data))
	for key, value := range pjs.data {
		copy[key] = value
	}
	return copy
}

func (pjs *prowjobs) get(pj string) string {
	pjs.mu.Lock()
	defer pjs.mu.Unlock()

	cluster, exists := pjs.data[pj]
	if exists {
		return cluster
	}
	return ""
}

func (pjs *prowjobs) hasAnyOfClusters(clusters sets.Set[string]) bool {
	pjs.mu.Lock()
	defer pjs.mu.Unlock()
	for _, cluster := range pjs.data {
		if clusters.Has(cluster) {
			return true
		}
	}
	return false
}
