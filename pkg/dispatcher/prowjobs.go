package dispatcher

import (
	"sync"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"
)

type Prowjobs struct {
	mu              sync.Mutex
	data            map[string]ProwJobData
	jobsStoragePath string
}

type ProwJobData struct {
	Cluster      string
	Capabilities []string
}

func NewProwjobs(jobsStoragePath string) *Prowjobs {
	var loadedJobs map[string]ProwJobData
	if err := ReadGob(jobsStoragePath, &loadedJobs); err != nil {
		logrus.Errorf("falling back to empty map, error reading Gob file: %v", err)
		loadedJobs = make(map[string]ProwJobData)
	}
	return &Prowjobs{
		data:            loadedJobs,
		mu:              sync.Mutex{},
		jobsStoragePath: jobsStoragePath,
	}
}

func (pjs *Prowjobs) Regenerate(prowjobs map[string]ProwJobData) {
	pjs.mu.Lock()
	defer pjs.mu.Unlock()
	pjs.data = make(map[string]ProwJobData, len(prowjobs))
	for key, value := range prowjobs {
		pjs.data[key] = value
	}
}

func (pjs *Prowjobs) GetDataCopy() map[string]ProwJobData {
	pjs.mu.Lock()
	defer pjs.mu.Unlock()

	copy := make(map[string]ProwJobData, len(pjs.data))
	for key, value := range pjs.data {
		copy[key] = value
	}
	return copy
}

func (pjs *Prowjobs) GetCluster(pj string) string {
	pjs.mu.Lock()
	defer pjs.mu.Unlock()

	data, exists := pjs.data[pj]
	if exists {
		return data.Cluster
	}
	return ""
}

func (pjs *Prowjobs) HasAnyOfClusters(clusters sets.Set[string]) bool {
	pjs.mu.Lock()
	defer pjs.mu.Unlock()
	for _, data := range pjs.data {
		if clusters.Has(data.Cluster) {
			return true
		}
	}
	return false
}
