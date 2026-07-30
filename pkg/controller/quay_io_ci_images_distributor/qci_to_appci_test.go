package quay_io_ci_images_distributor

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestLoadConfigQCIToAppCIImages(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    map[string]Source
		wantErr bool
	}{
		{
			name: "derive QCI float from target key",
			raw: `qciToAppCIImages:
  ci/foo:latest: {}
`,
			want: map[string]Source{
				"ci/foo:latest": {Image: api.QuayImageReference(api.ImageStreamTagReference{Namespace: "ci", Name: "foo", Tag: "latest"})},
			},
		},
		{
			name: "explicit image",
			raw: `qciToAppCIImages:
  ci/foo:latest:
    image: quay-proxy.ci.openshift.org/openshift/ci:ci_foo_custom
`,
			want: map[string]Source{
				"ci/foo:latest": {Image: "quay-proxy.ci.openshift.org/openshift/ci:ci_foo_custom"},
			},
		},
		{
			name: "rejects non-QCI explicit image",
			raw: `qciToAppCIImages:
  ci/foo:latest:
    image: registry.redhat.io/ubi9/ubi:latest
`,
			wantErr: true,
		},
		{
			name: "rejects namespace name tag source fields",
			raw: `qciToAppCIImages:
  ci/foo:latest:
    namespace: ci
    name: foo
    tag: latest
`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := LoadConfig([]byte(tt.raw))
			if (err != nil) != tt.wantErr {
				t.Fatalf("LoadConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if diff := cmp.Diff(tt.want, got.QCIToAppCIImages); diff != "" {
				t.Errorf("QCIToAppCIImages mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

type fakeQuayHelper struct {
	info map[string]ImageInfo
	errs map[string]error
}

func (f *fakeQuayHelper) ImageInfo(image string, _ OCImageInfoOptions) (ImageInfo, error) {
	if err := f.errs[image]; err != nil {
		return ImageInfo{}, err
	}
	return f.info[image], nil
}

func (f *fakeQuayHelper) ImageMirror(_ []string, _ OCImageMirrorOptions) error {
	return nil
}

type putFailStore struct {
	MirrorStore
	failFor string
}

func (s *putFailStore) Put(tasks ...MirrorTask) error {
	for _, t := range tasks {
		if t.Destination == s.failFor {
			return fmt.Errorf("store full")
		}
	}
	return s.MirrorStore.Put(tasks...)
}

func TestMirrorQCIToAppCI(t *testing.T) {
	const (
		src   = "quay-proxy.ci.openshift.org/openshift/ci:ci_foo_latest"
		dest  = "registry.ci.openshift.org/ci/foo:latest"
		src2  = "quay-proxy.ci.openshift.org/openshift/ci:ci_bar_latest"
		dest2 = "registry.ci.openshift.org/ci/bar:latest"
	)
	derived := api.QuayImageReference(api.ImageStreamTagReference{Namespace: "ci", Name: "foo", Tag: "latest"})
	tests := []struct {
		name      string
		mirrors   map[string]Source
		info      map[string]ImageInfo
		errs      map[string]error
		failPut   string
		wantTasks []MirrorTask
		wantErr   bool
	}{
		{
			name:    "enqueues when digests differ",
			mirrors: map[string]Source{"ci/foo:latest": {Image: src}},
			info: map[string]ImageInfo{
				src:  {Digest: "sha256:aaa"},
				dest: {Digest: "sha256:bbb"},
			},
			wantTasks: []MirrorTask{{Source: src, Destination: dest, Owner: "qciToAppCIImages"}},
		},
		{
			name:    "skips when digests match",
			mirrors: map[string]Source{"ci/foo:latest": {Image: src}},
			info: map[string]ImageInfo{
				src:  {Digest: "sha256:aaa"},
				dest: {Digest: "sha256:aaa"},
			},
			wantTasks: []MirrorTask{},
		},
		{
			name:      "skips when source missing",
			mirrors:   map[string]Source{"ci/foo:latest": {Image: src}},
			info:      map[string]ImageInfo{dest: {Digest: "sha256:bbb"}},
			wantTasks: []MirrorTask{},
		},
		{
			name:    "derives source from target key",
			mirrors: map[string]Source{"ci/foo:latest": {}},
			info:    map[string]ImageInfo{derived: {Digest: "sha256:aaa"}},
			wantTasks: []MirrorTask{{
				Source:      derived,
				Destination: dest,
				Owner:       "qciToAppCIImages",
			}},
		},
		{
			name: "continues after put failure",
			mirrors: map[string]Source{
				"ci/foo:latest": {Image: src},
				"ci/bar:latest": {Image: src2},
			},
			info: map[string]ImageInfo{
				src:  {Digest: "sha256:aaa"},
				src2: {Digest: "sha256:bbb"},
			},
			failPut: dest,
			wantTasks: []MirrorTask{{
				Source: src2, Destination: dest2, Owner: "qciToAppCIImages",
			}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMirrorStore()
			if tt.failPut != "" {
				store = &putFailStore{MirrorStore: store, failFor: tt.failPut}
			}
			err := MirrorQCIToAppCI(store, logrus.WithField("test", tt.name), &fakeQuayHelper{info: tt.info, errs: tt.errs}, OCImageInfoOptions{}, tt.mirrors)
			if (err != nil) != tt.wantErr {
				t.Fatalf("MirrorQCIToAppCI() error = %v, wantErr %v", err, tt.wantErr)
			}
			got, _, showErr := store.Show(10)
			if showErr != nil {
				t.Fatalf("Show() error = %v", showErr)
			}
			normalized := make([]MirrorTask, 0, len(got))
			for _, task := range got {
				normalized = append(normalized, MirrorTask{Source: task.Source, Destination: task.Destination, Owner: task.Owner})
			}
			if diff := cmp.Diff(tt.wantTasks, normalized); diff != "" {
				t.Errorf("tasks mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
