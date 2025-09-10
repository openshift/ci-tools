package dispatcher

import (
	"errors"
	"sync"
	"time"
)

const (
	// cacheTTL denotes how long an entry within the cache is supposed to exist. This value matches the default TTL
	// value (see the --delete-after option) of a namespace created by ci-operator.
	cacheTTL = 24 * time.Hour
)

var (
	ErrNoClusterAvailable = errors.New("no clusters available")
)

// ephemeralClusterScheduler schedules Konflux ephemeral cluster requests accross the build farms
// by implementing a simple round-robin algorithm.
type ephemeralClusterScheduler struct {
	clusters []string
	// idx is the index of the last choosen cluster
	idx int
	// m keeps this struct thread safe
	m sync.Mutex

	// This cache keeps track of which cluster has been assigned to a PJ. Each entry has a TTL associated.
	cache map[string]struct {
		cluster       string
		insertionTime time.Time
	}

	// For testing purposes only
	now func() time.Time
}

func (ecs *ephemeralClusterScheduler) Dispatch(jobName string) (string, error) {
	ecs.m.Lock()
	defer ecs.m.Unlock()

	if len(ecs.clusters) == 0 {
		return "", ErrNoClusterAvailable
	}

	now := ecs.now()
	if entry, ok := ecs.cache[jobName]; ok {
		if now.Sub(entry.insertionTime) <= cacheTTL {
			return entry.cluster, nil
		}
		delete(ecs.cache, jobName)
	}

	next := (ecs.idx + 1) % len(ecs.clusters)
	c := ecs.clusters[next]
	ecs.idx = next
	ecs.cache[jobName] = struct {
		cluster       string
		insertionTime time.Time
	}{
		cluster:       c,
		insertionTime: now,
	}

	return c, nil
}

// Reset should be called each time the dispatcher receives a full dispatch request.
// It resets the scheduler to its default values and assign the new cluster list (that it might
// have changed from the last time)
func (ecs *ephemeralClusterScheduler) Reset(clusters []string) {
	ecs.m.Lock()
	defer ecs.m.Unlock()

	ecs.idx = -1
	ecs.clusters = clusters
	for k := range ecs.cache {
		delete(ecs.cache, k)
	}
}

func NewEphemeralClusterDispatcher(clusters []string) *ephemeralClusterScheduler {
	return &ephemeralClusterScheduler{
		clusters: clusters,
		idx:      -1,
		m:        sync.Mutex{},
		cache: make(map[string]struct {
			cluster       string
			insertionTime time.Time
		}),
		now: time.Now,
	}
}
