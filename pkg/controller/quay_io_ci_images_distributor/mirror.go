package quay_io_ci_images_distributor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/wait"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
)

type MirrorTask struct {
	SourceTagRef      cioperatorapi.ImageStreamTagReference `json:"source_tag_ref"`
	Source            string                                `json:"source"`
	Destination       string                                `json:"destination"`
	CurrentQuayDigest string                                `json:"current_quay_digest"`
	CreatedAt         time.Time                             `json:"created_at"`
	Stale             bool                                  `json:"stale"`
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
		t.CreatedAt = time.Now()
		s.mirrors[t.Destination] = t
	}
	return nil
}

func (s *memoryMirrorStore) Take(n int) ([]MirrorTask, error) {
	ret, _, nil := s.get(n, false)
	return ret, nil
}

func (s *memoryMirrorStore) Show(n int) ([]MirrorTask, int, error) {
	return s.get(n, false)
}
func (s *memoryMirrorStore) get(n int, del bool) ([]MirrorTask, int, error) {
	var ret []MirrorTask
	s.mu.Lock()
	defer s.mu.Unlock()
	l := len(s.mirrors)
	c := 0
	for k, v := range s.mirrors {
		if c < n {
			ret = append(ret, v)
			c = c + 1
			if del {
				delete(s.mirrors, k)
			}
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

type MirrorConsumerController struct {
	logger            *logrus.Entry
	quayIOImageHelper QuayIOImageHelper
	mirrorStore       MirrorStore
	options           OCImageMirrorOptions
}

func (c *MirrorConsumerController) Run(ctx context.Context) error {
	for {
		mirrors, err := c.mirrorStore.Take(10)
		if err != nil {
			c.logger.WithError(err).Warn("Failed to take mirrors")
			continue
		}
		if len(mirrors) == 0 {
			c.logger.Debug("Waiting for mirror tasks ...")
			time.Sleep(3 * time.Second)
			continue
		}
		var pairs []string
		for _, mirror := range mirrors {
			pairs = append(pairs, fmt.Sprintf("%s=%s", mirror.Source, mirror.Destination))
		}
		if err := wait.PollUntilContextTimeout(ctx, 1*time.Second, 3*time.Minute, true, func(ctx context.Context) (done bool, err error) {
			if errFromMirror := c.quayIOImageHelper.ImageMirror(pairs, c.options); errFromMirror != nil {
				return false, nil
			}
			return true, nil
		}); err != nil {
			// TODO use "--force" on long stale images with errors even after retries, ideally, only for the failed ones
			c.logger.WithError(err).Warn("Failed to mirror even with retries")
		}
	}
}

func NewMirrorConsumer(mirrorStore MirrorStore, quayIOImageHelper QuayIOImageHelper, registryConfig string, dryRun bool) *MirrorConsumerController {
	return &MirrorConsumerController{
		quayIOImageHelper: quayIOImageHelper,
		mirrorStore:       mirrorStore,
		options: OCImageMirrorOptions{
			RegistryConfig:  registryConfig,
			ContinueOnError: true,
			MaxPerRegistry:  20,
			BatchSize:       10,
			DryRun:          dryRun,
		},
		logger: logrus.WithField("subComponent", "mirrorController"),
	}
}
