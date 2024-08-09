package main

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api"
	podscalerv2 "github.com/openshift/ci-tools/pkg/pod-scaler/v2"
)

func TestMetadataQueryMappingRoundTripping(t *testing.T) {
	metas := []podscalerv2.FullMetadata{
		{
			Metadata: api.Metadata{
				Org:     "org",
				Repo:    "repo",
				Branch:  "branch",
				Variant: "variant",
			},
			Target:    "target",
			Step:      "step",
			Pod:       "target-step",
			Container: "container",
		},
		{
			Metadata: api.Metadata{
				Org:     "org",
				Repo:    "repo",
				Branch:  "branch",
				Variant: "variant",
			},
			Pod:       "pod-build",
			Container: "container",
		},
		{
			Metadata: api.Metadata{
				Org:     "org",
				Repo:    "repo",
				Branch:  "branch",
				Variant: "variant",
			},
			Target:    "target",
			Pod:       "target",
			Container: "container",
		},
		{
			Metadata: api.Metadata{
				Org:     "org",
				Repo:    "repo",
				Branch:  "branch",
				Variant: "variant",
			},
			Container: "rpm-repo",
		},
		{
			Target:    "target",
			Container: "container",
		},
	}

	for _, meta := range metas {
		ptr := &meta
		original := *ptr
		for name, mapping := range endpoints() {
			if !mapping.matches(&meta) {
				continue
			}
			nodes := mapping.nodesFromMeta(&meta)
			if nodes == nil {
				continue
			}
			if diff := cmp.Diff(original, meta); diff != "" {
				t.Fatalf("%s: mutated meta: %v", name, diff)
			}
			r, err := http.NewRequest(http.MethodGet, "whatever.com", &bytes.Buffer{})
			if err != nil {
				t.Fatalf("%s: could not make request: %v", name, err)
			}
			q := r.URL.Query()
			for _, node := range nodes {
				q.Set(node.Field, node.Name)
			}
			r.URL.RawQuery = q.Encode()
			roundTripped, err := mapping.metadataFromQuery(&fakeWriter{}, r)
			if err != nil {
				t.Fatalf("%s: could not get round tripped meta: %v", name, err)
			}
			if diff := cmp.Diff(original, roundTripped); diff != "" {
				t.Fatalf("%s: did not round-trip meta: %v", name, diff)
			}
		}
	}
}

type fakeWriter struct{}

func (w *fakeWriter) Header() http.Header        { return nil }
func (w *fakeWriter) Write([]byte) (int, error)  { return 0, nil }
func (w *fakeWriter) WriteHeader(statusCode int) {}
