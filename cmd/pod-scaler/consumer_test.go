package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/openhistogram/circonusllhist"
	"github.com/prometheus/common/model"
	"github.com/sirupsen/logrus"

	podscaler "github.com/openshift/ci-tools/pkg/pod-scaler"
)

type stubCache struct {
	updated    time.Time
	updatedErr error
	payload    []byte
	loadErr    error
}

func (s *stubCache) load(_ context.Context, _ string) (io.ReadCloser, error) {
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	return io.NopCloser(bytes.NewReader(s.payload)), nil
}

func (s *stubCache) store(_ context.Context, _ string) (io.WriteCloser, error) {
	return nil, errors.New("store not implemented in test stub")
}

func (s *stubCache) lastUpdated(_ context.Context, _ string) (time.Time, error) {
	return s.updated, s.updatedErr
}

func newTestCacheReloader(t *testing.T, cache Cache) *cacheReloader {
	t.Helper()
	return &cacheReloader{
		name:   t.Name(),
		cache:  cache,
		logger: logrus.WithField("test", t.Name()),
		lock:   &sync.RWMutex{},
	}
}

func marshalCachedQuery(t *testing.T, query *podscaler.CachedQuery) []byte {
	t.Helper()
	raw, err := json.Marshal(query)
	if err != nil {
		t.Fatalf("marshal cache query: %v", err)
	}
	return raw
}

func reloadOnce(t *testing.T, reloader *cacheReloader) []*podscaler.CachedQuery {
	t.Helper()
	ch := make(chan *podscaler.CachedQuery, 1)
	reloader.subscribe(ch)
	reloader.reload()
	return drainReloadNotifications(ch)
}

func drainReloadNotifications(ch <-chan *podscaler.CachedQuery) []*podscaler.CachedQuery {
	var got []*podscaler.CachedQuery
	for {
		select {
		case query := <-ch:
			got = append(got, query)
		default:
			return got
		}
	}
}

func seedLastUpdated(t *testing.T, reloader *cacheReloader, at time.Time) {
	t.Helper()
	reloader.lock.Lock()
	reloader.lastUpdated = at
	reloader.lock.Unlock()
}

func TestCachedQueryEmpty(t *testing.T) {
	testCases := []struct {
		name  string
		query *podscaler.CachedQuery
		want  bool
	}{
		{name: "nil", want: true},
		{name: "empty struct", query: &podscaler.CachedQuery{}, want: true},
		{name: "metadata present", query: &podscaler.CachedQuery{
			DataByMetaData: map[podscaler.FullMetadata][]podscaler.FingerprintTime{
				{Container: "test"}: {},
			},
		}, want: false},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cachedQueryEmpty(tc.query); got != tc.want {
				t.Fatalf("cachedQueryEmpty() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCacheReloader_reload(t *testing.T) {
	updatedAt := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	freshMeta := podscaler.FullMetadata{Container: "fresh"}
	fingerprint := model.Fingerprint(42)
	inner := circonusllhist.New()
	if err := inner.RecordValue(1e8); err != nil {
		t.Fatalf("RecordValue: %v", err)
	}
	freshPayload := marshalCachedQuery(t, &podscaler.CachedQuery{
		Data: map[model.Fingerprint]*circonusllhist.HistogramWithoutLookups{
			fingerprint: circonusllhist.NewHistogramWithoutLookups(inner),
		},
		DataByMetaData: map[podscaler.FullMetadata][]podscaler.FingerprintTime{
			freshMeta: {{Fingerprint: fingerprint}},
		},
	})
	emptyPayload := marshalCachedQuery(t, emptyCachedQuery())

	t.Run("initial load failure serves empty cache", func(t *testing.T) {
		reloader := newTestCacheReloader(t, &stubCache{
			updated: updatedAt,
			loadErr: errors.New("read failed"),
		})
		got := reloadOnce(t, reloader)
		if len(got) != 1 {
			t.Fatalf("reload notifications = %d, want 1", len(got))
		}
		if diff := cmp.Diff(map[podscaler.FullMetadata][]podscaler.FingerprintTime(nil), got[0].DataByMetaData); diff != "" {
			t.Fatalf("reload cache differs from expected empty cache, diff:\n%s", diff)
		}
		reloader.lock.RLock()
		lastUp := reloader.lastUpdated
		reloader.lock.RUnlock()
		if !lastUp.IsZero() {
			t.Fatalf("lastUpdated = %v, want zero after failed initial load", lastUp)
		}
	})

	t.Run("initial load failure retries on next reload", func(t *testing.T) {
		cache := &stubCache{
			updated: updatedAt,
			loadErr: errors.New("read failed"),
		}
		reloader := newTestCacheReloader(t, cache)
		if got := reloadOnce(t, reloader); len(got) != 1 {
			t.Fatalf("first reload notifications = %d, want 1", len(got))
		}
		cache.loadErr = nil
		cache.payload = freshPayload
		ch := make(chan *podscaler.CachedQuery, 1)
		reloader.subscribe(ch)
		reloader.reload()
		got := drainReloadNotifications(ch)
		if len(got) != 1 {
			t.Fatalf("retry reload notifications = %d, want 1", len(got))
		}
		if _, ok := got[0].DataByMetaData[freshMeta]; !ok {
			t.Fatalf("expected retry reload to deliver fresh metadata")
		}
	})

	t.Run("failed reload keeps previous cache", func(t *testing.T) {
		reloader := newTestCacheReloader(t, &stubCache{
			updated: updatedAt,
			loadErr: errors.New("read failed"),
		})
		seedLastUpdated(t, reloader, updatedAt)
		got := reloadOnce(t, reloader)
		if len(got) != 0 {
			t.Fatalf("reload notifications = %d, want 0", len(got))
		}
	})

	t.Run("empty reload keeps previous cache", func(t *testing.T) {
		reloader := newTestCacheReloader(t, &stubCache{
			updated: updatedAt.Add(time.Hour),
			payload: emptyPayload,
		})
		seedLastUpdated(t, reloader, updatedAt)
		got := reloadOnce(t, reloader)
		if len(got) != 0 {
			t.Fatalf("reload notifications = %d, want 0", len(got))
		}
	})

	t.Run("unchanged freshness skips reload", func(t *testing.T) {
		reloader := newTestCacheReloader(t, &stubCache{
			updated: updatedAt,
			payload: freshPayload,
		})
		seedLastUpdated(t, reloader, updatedAt)
		got := reloadOnce(t, reloader)
		if len(got) != 0 {
			t.Fatalf("reload notifications = %d, want 0", len(got))
		}
	})

	t.Run("newer update reloads cache snapshot", func(t *testing.T) {
		reloader := newTestCacheReloader(t, &stubCache{
			updated: updatedAt.Add(time.Hour),
			payload: freshPayload,
		})
		seedLastUpdated(t, reloader, updatedAt)
		got := reloadOnce(t, reloader)
		if len(got) != 1 {
			t.Fatalf("reload notifications = %d, want 1", len(got))
		}
		wantMeta := []podscaler.FullMetadata{freshMeta}
		gotMeta := make([]podscaler.FullMetadata, 0, len(got[0].DataByMetaData))
		for meta := range got[0].DataByMetaData {
			gotMeta = append(gotMeta, meta)
		}
		if diff := cmp.Diff(wantMeta, gotMeta); diff != "" {
			t.Fatalf("reloaded metadata differs from expected, diff:\n%s", diff)
		}
	})

	t.Run("pending cache delivered on subscribe", func(t *testing.T) {
		reloader := newTestCacheReloader(t, &stubCache{
			updated: updatedAt,
			payload: freshPayload,
		})
		reloader.reload()
		ch := make(chan *podscaler.CachedQuery, 1)
		reloader.subscribe(ch)
		got := drainReloadNotifications(ch)
		if len(got) != 1 {
			t.Fatalf("pending notifications = %d, want 1", len(got))
		}
		if _, ok := got[0].DataByMetaData[freshMeta]; !ok {
			t.Fatalf("expected pending cache to include fresh metadata")
		}
	})
}
