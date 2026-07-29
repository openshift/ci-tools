package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/openshift/ci-tools/pkg/release/candidate"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestRun(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(candidate.Release{
			Name:     "4.21.99",
			Phase:    "Accepted",
			PullSpec: "quay.io/openshift-release-dev/ocp-release:4.21.99-multi",
		}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	if err := addSchemes(); err != nil {
		t.Fatal(err)
	}

	poolDir := t.TempDir()
	imagesetDir := t.TempDir()

	copyDir(t, "testdata/run/input/pools", poolDir)
	copyDir(t, "testdata/run/input/imagesets", imagesetDir)

	o := options{
		poolDir:              poolDir,
		outputDir:            imagesetDir,
		releaseControllerURL: server.URL + "/api/v1/releasestream",
	}
	if err := run(o, server.Client()); err != nil {
		t.Fatal(err)
	}

	for _, dir := range []struct {
		path   string
		prefix string
	}{
		{imagesetDir, "imagesets-"},
		{poolDir, "pools-"},
	} {
		entries, err := os.ReadDir(dir.path)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			data, err := os.ReadFile(filepath.Join(dir.path, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			testhelper.CompareWithFixture(t, data, testhelper.WithPrefix(dir.prefix+e.Name()+"-"))
		}
	}
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
}
