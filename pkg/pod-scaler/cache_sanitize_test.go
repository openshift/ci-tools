package pod_scaler

import (
	"testing"

	"github.com/openhistogram/circonusllhist"
	"github.com/prometheus/common/model"
)

func TestCachedQuery_Repair(t *testing.T) {
	opts := DefaultSanitizeOptions()

	t.Run("memory orphan and spike", func(t *testing.T) {
		fpGood := model.Fingerprint(1)
		fpOrphan := model.Fingerprint(2)
		fpSpike := model.Fingerprint(3)

		goodInner := circonusllhist.New()
		for i := 0; i < 20; i++ {
			if err := goodInner.RecordValue(1e8); err != nil {
				t.Fatalf("RecordValue: %v", err)
			}
		}
		goodHist := circonusllhist.NewHistogramWithoutLookups(goodInner)

		spikeInner := circonusllhist.New()
		for i := 0; i < 20; i++ {
			if err := spikeInner.RecordValue(1e8); err != nil {
				t.Fatalf("RecordValue: %v", err)
			}
		}
		if err := spikeInner.RecordValue(float64(30 * 1024 * 1024 * 1024)); err != nil {
			t.Fatalf("RecordValue spike: %v", err)
		}
		spikeHist := circonusllhist.NewHistogramWithoutLookups(spikeInner)

		meta := FullMetadata{Container: "test"}
		query := &CachedQuery{
			Data: map[model.Fingerprint]*circonusllhist.HistogramWithoutLookups{
				fpGood:  goodHist,
				fpSpike: spikeHist,
			},
			DataByMetaData: map[FullMetadata][]FingerprintTime{
				meta: {
					{Fingerprint: fpGood},
					{Fingerprint: fpOrphan},
					{Fingerprint: fpSpike},
				},
			},
		}

		stats := query.Repair("container_memory_working_set_bytes", opts)
		if stats.RemovedOrphanRefs != 2 {
			t.Fatalf("RemovedOrphanRefs = %d, want 2", stats.RemovedOrphanRefs)
		}
		if stats.RemovedCorruptValues != 1 {
			t.Fatalf("RemovedCorruptValues = %d, want 1", stats.RemovedCorruptValues)
		}
		if len(query.Data) != 1 {
			t.Fatalf("histogram count = %d, want 1", len(query.Data))
		}
		if len(query.DataByMetaData[meta]) != 1 {
			t.Fatalf("meta refs = %d, want 1", len(query.DataByMetaData[meta]))
		}
	})

	t.Run("zero baseline skips ratio check", func(t *testing.T) {
		fp := model.Fingerprint(11)
		meta := FullMetadata{Container: "idle"}

		inner := circonusllhist.New()
		for i := 0; i < 20; i++ {
			if err := inner.RecordValue(0); err != nil {
				t.Fatalf("RecordValue: %v", err)
			}
		}
		if err := inner.RecordValue(0.5); err != nil {
			t.Fatalf("RecordValue: %v", err)
		}

		query := &CachedQuery{
			Data: map[model.Fingerprint]*circonusllhist.HistogramWithoutLookups{
				fp: circonusllhist.NewHistogramWithoutLookups(inner),
			},
			DataByMetaData: map[FullMetadata][]FingerprintTime{
				meta: {{Fingerprint: fp}},
			},
		}

		stats := query.Repair("container_cpu_usage_seconds_total", opts)
		if stats.RemovedCorruptValues != 0 {
			t.Fatalf("RemovedCorruptValues = %d, want 0", stats.RemovedCorruptValues)
		}
		if len(query.Data) != 1 {
			t.Fatalf("histogram count = %d, want 1", len(query.Data))
		}
	})

	t.Run("cpu corrupt histogram", func(t *testing.T) {
		fp := model.Fingerprint(10)
		meta := FullMetadata{Container: "test"}

		inner := circonusllhist.New()
		for i := 0; i < 20; i++ {
			if err := inner.RecordValue(0.5); err != nil {
				t.Fatalf("RecordValue: %v", err)
			}
		}
		if err := inner.RecordValue(50); err != nil {
			t.Fatalf("RecordValue spike: %v", err)
		}

		query := &CachedQuery{
			Data: map[model.Fingerprint]*circonusllhist.HistogramWithoutLookups{
				fp: circonusllhist.NewHistogramWithoutLookups(inner),
			},
			DataByMetaData: map[FullMetadata][]FingerprintTime{
				meta: {{Fingerprint: fp}},
			},
		}

		stats := query.Repair("container_cpu_usage_seconds_total", opts)
		if stats.RemovedCorruptValues != 1 {
			t.Fatalf("RemovedCorruptValues = %d, want 1", stats.RemovedCorruptValues)
		}
		if len(query.Data) != 0 {
			t.Fatalf("histogram count = %d, want 0", len(query.Data))
		}
		if len(query.DataByMetaData) != 0 {
			t.Fatalf("meta refs = %d, want 0", len(query.DataByMetaData))
		}
	})
}
