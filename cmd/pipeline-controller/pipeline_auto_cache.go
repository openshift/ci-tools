package main

import (
	"fmt"
	"sync"
	"time"
)

// PipelineAutoCache caches which PRs have the pipeline-auto label
// Key format: "org/repo/number"
// Entries are automatically cleaned up after 48 hours
type PipelineAutoCache struct {
	cache sync.Map
}

type pipelineAutoCacheEntry struct {
	addedAt time.Time
}

const pipelineAutoCacheTTL = 48 * time.Hour

func NewPipelineAutoCache() *PipelineAutoCache {
	return &PipelineAutoCache{}
}

func (c *PipelineAutoCache) composePRKey(org, repo string, number int) string {
	return fmt.Sprintf("%s/%s/%d", org, repo, number)
}

// Set marks a PR as having the pipeline-auto label
func (c *PipelineAutoCache) Set(org, repo string, number int) {
	c.cache.Store(c.composePRKey(org, repo, number), pipelineAutoCacheEntry{addedAt: time.Now()})
}

// Has checks if a PR is known to have the pipeline-auto label
func (c *PipelineAutoCache) Has(org, repo string, number int) bool {
	val, ok := c.cache.Load(c.composePRKey(org, repo, number))
	if !ok {
		return false
	}
	entry := val.(pipelineAutoCacheEntry)
	// Check if entry is still valid (not expired)
	if time.Since(entry.addedAt) > pipelineAutoCacheTTL {
		// Entry expired, remove it
		c.cache.Delete(c.composePRKey(org, repo, number))
		return false
	}
	return true
}

// CleanExpired removes all expired entries from the cache
func (c *PipelineAutoCache) CleanExpired() {
	c.cache.Range(func(key, value interface{}) bool {
		entry := value.(pipelineAutoCacheEntry)
		if time.Since(entry.addedAt) > pipelineAutoCacheTTL {
			c.cache.Delete(key)
		}
		return true
	})
}
