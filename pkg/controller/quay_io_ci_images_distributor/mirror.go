package quay_io_ci_images_distributor

import (
	"sync"
	"time"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
)

type MirrorTask struct {
	SourceTagRef      cioperatorapi.ImageStreamTagReference `json:"source_tag_ref"`
	Source            string                                `json:"source"`
	Destination       string                                `json:"destination"`
	CurrentQuayDigest string                                `json:"current_quay_digest"`
	createdAt         time.Time                             `json:"-"`
}

type MirrorStore interface {
	Put(t ...MirrorTask) error
	Take(n int) ([]MirrorTask, error)
	Show(n int) ([]MirrorTask, int, error)
	Summarize() (map[string]any, error)
}

type memoryMirrorStore struct {
	mu      sync.Mutex
	mirrors map[string]MirrorTask
}

func (s *memoryMirrorStore) Put(tasks ...MirrorTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range tasks {
		t.createdAt = time.Now()
		s.mirrors[t.Destination] = t
	}
	return nil
}

func (s *memoryMirrorStore) Take(n int) ([]MirrorTask, error) {
	var ret []MirrorTask
	s.mu.Lock()
	defer s.mu.Unlock()
	c := 0
	for k, v := range s.mirrors {
		if c < n {
			ret = append(ret, v)
			c = c + 1
		} else {
			delete(s.mirrors, k)
		}
	}
	return ret, nil
}

func (s *memoryMirrorStore) Show(n int) ([]MirrorTask, int, error) {
	var ret []MirrorTask
	s.mu.Lock()
	defer s.mu.Unlock()
	l := len(s.mirrors)
	c := 0
	for _, v := range s.mirrors {
		if c < n {
			ret = append(ret, v)
			c = c + 1
		} else {
			break
		}
	}
	return ret, l, nil
}

func (s *memoryMirrorStore) Summarize() (map[string]any, error) {
	return map[string]any{"total": len(s.mirrors)}, nil
}

// NewMirrorStore returns a mirror store
func NewMirrorStore() MirrorStore {
	return &memoryMirrorStore{
		mirrors: map[string]MirrorTask{},
	}
}
