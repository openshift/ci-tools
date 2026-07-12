package main

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/openhistogram/circonusllhist"
	"github.com/prometheus/common/model"
	"github.com/sirupsen/logrus"

	podscaler "github.com/openshift/ci-tools/pkg/pod-scaler"
)

type bytesLoader struct {
	payload []byte
}

func (b *bytesLoader) load(_ context.Context, _ string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(b.payload)), nil
}

func corruptMemoryCachedQuery(t *testing.T) *podscaler.CachedQuery {
	t.Helper()
	fp := model.Fingerprint(99)
	inner := circonusllhist.New()
	for i := 0; i < 20; i++ {
		if err := inner.RecordValue(1e8); err != nil {
			t.Fatalf("RecordValue: %v", err)
		}
	}
	if err := inner.RecordValue(float64(30 * 1024 * 1024 * 1024)); err != nil {
		t.Fatalf("RecordValue spike: %v", err)
	}
	return &podscaler.CachedQuery{
		Data: map[model.Fingerprint]*circonusllhist.HistogramWithoutLookups{
			fp: circonusllhist.NewHistogramWithoutLookups(inner),
		},
		DataByMetaData: map[podscaler.FullMetadata][]podscaler.FingerprintTime{
			{Container: "test"}: {{Fingerprint: fp}},
		},
	}
}

func TestLoadCache(t *testing.T) {
	t.Run("repairs corrupt histograms", func(t *testing.T) {
		raw := marshalCachedQuery(t, corruptMemoryCachedQuery(t))

		got, err := LoadCache(&bytesLoader{payload: raw}, MetricNameMemoryWorkingSet, logrus.WithField("test", t.Name()))
		if err != nil {
			t.Fatalf("LoadCache() error = %v", err)
		}
		if len(got.Data) != 0 {
			t.Fatalf("expected corrupt histogram removed, got %d entries", len(got.Data))
		}
		if len(got.DataByMetaData) != 0 {
			t.Fatalf("expected orphan metadata removed, got %d entries", len(got.DataByMetaData))
		}
	})
}
