package main

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/client-go/kubernetes/scheme"

	hivev1 "github.com/openshift/hive/apis/hive/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func poolFilesWithSortedPaths(m map[api.VersionBounds][]string) map[api.VersionBounds][]string {
	out := make(map[api.VersionBounds][]string, len(m))
	for k, v := range m {
		cp := append([]string(nil), v...)
		if len(cp) > 1 {
			sort.Strings(cp)
		}
		out[k] = cp
	}
	return out
}

func TestMergeVersionStreams(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want string
	}{
		{"both empty", "", "", ""},
		{"left empty", "", "4-stable", "4-stable"},
		{"right empty", "4-stable", "", "4-stable"},
		{"equal non-empty", "4-stable", "4-stable", "4-stable"},
		{"distinct non-empty lex max", "4-dev-preview", "4-stable", "4-stable"},
		{"distinct non-empty lex max reverse", "4-stable", "4-dev-preview", "4-stable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeVersionStreams(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("mergeVersionStreams(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.want)
			}
			gotRev := mergeVersionStreams(tt.b, tt.a)
			if gotRev != tt.want {
				t.Errorf("mergeVersionStreams(%q, %q) = %q, want %q (commutative)", tt.b, tt.a, gotRev, tt.want)
			}
		})
	}
}

func TestMergeCollidingBounds(t *testing.T) {
	bEmpty := api.VersionBounds{Lower: "4.7.0-0", Upper: "4.8.0-0", Stream: ""}
	bDev := api.VersionBounds{Lower: "4.7.0-0", Upper: "4.8.0-0", Stream: "4-dev-preview"}
	bStable := api.VersionBounds{Lower: "4.7.0-0", Upper: "4.8.0-0", Stream: "4-stable"}
	ps := "quay.io/openshift-release-dev/ocp-release:4.7.60-x86_64"
	psOther := "quay.io/openshift-release-dev/ocp-release:4.7.59-x86_64"
	t.Run("merges same pullspec different stream", func(t *testing.T) {
		pools := map[api.VersionBounds][]string{
			bEmpty:  {"a.yaml"},
			bStable: {"b.yaml"},
		}
		pull := map[api.VersionBounds]string{
			bEmpty:  ps,
			bStable: ps,
		}
		gotPools, gotPull := mergeCollidingBounds(pools, pull)
		wantBounds := api.VersionBounds{Lower: "4.7.0-0", Upper: "4.8.0-0", Stream: "4-stable"}
		wantPools := map[api.VersionBounds][]string{
			wantBounds: {"a.yaml", "b.yaml"},
		}
		wantPull := map[api.VersionBounds]string{wantBounds: ps}
		if diff := cmp.Diff(wantPull, gotPull); diff != "" {
			t.Errorf("pullspecs differ: %s", diff)
		}
		if diff := cmp.Diff(wantPools, poolFilesWithSortedPaths(gotPools)); diff != "" {
			t.Errorf("pools differ: %s", diff)
		}
	})
	t.Run("merges three streams to lexicographic max", func(t *testing.T) {
		pools := map[api.VersionBounds][]string{
			bEmpty:  {"a.yaml"},
			bDev:    {"b.yaml"},
			bStable: {"c.yaml"},
		}
		pull := map[api.VersionBounds]string{bEmpty: ps, bDev: ps, bStable: ps}
		gotPools, gotPull := mergeCollidingBounds(pools, pull)
		wantBounds := api.VersionBounds{Lower: "4.7.0-0", Upper: "4.8.0-0", Stream: "4-stable"}
		wantPools := map[api.VersionBounds][]string{
			wantBounds: {"a.yaml", "b.yaml", "c.yaml"},
		}
		wantPull := map[api.VersionBounds]string{wantBounds: ps}
		if diff := cmp.Diff(wantPull, gotPull); diff != "" {
			t.Errorf("pullspecs differ: %s", diff)
		}
		if diff := cmp.Diff(wantPools, poolFilesWithSortedPaths(gotPools)); diff != "" {
			t.Errorf("pools differ: %s", diff)
		}
	})
	t.Run("does not merge when pullspec differs", func(t *testing.T) {
		pools := map[api.VersionBounds][]string{
			bEmpty:  {"a.yaml"},
			bStable: {"b.yaml"},
		}
		pull := map[api.VersionBounds]string{bEmpty: psOther, bStable: ps}
		gotPools, gotPull := mergeCollidingBounds(pools, pull)
		if len(gotPools) != 2 || len(gotPull) != 2 {
			t.Fatalf("got %d pool groups, %d pullspecs, want 2 each", len(gotPools), len(gotPull))
		}
		if diff := cmp.Diff(pools, gotPools); diff != "" {
			t.Errorf("pools differ: %s", diff)
		}
		if diff := cmp.Diff(pull, gotPull); diff != "" {
			t.Errorf("pullspecs differ: %s", diff)
		}
	})
	t.Run("single group unchanged", func(t *testing.T) {
		pools := map[api.VersionBounds][]string{bStable: {"x.yaml"}}
		pull := map[api.VersionBounds]string{bStable: ps}
		gotPools, gotPull := mergeCollidingBounds(pools, pull)
		if diff := cmp.Diff(pools, gotPools); diff != "" {
			t.Errorf("pools differ: %s", diff)
		}
		if diff := cmp.Diff(pull, gotPull); diff != "" {
			t.Errorf("pullspecs differ: %s", diff)
		}
	})
}

func TestSortedBounds(t *testing.T) {
	m := map[api.VersionBounds]string{
		{Lower: "4.12.0-0", Upper: "4.13.0-0", Stream: "4-stable"}: "a",
		{Lower: "4.11.0-0", Upper: "4.12.0-0", Stream: ""}:         "b",
		{Lower: "4.11.0-0", Upper: "4.12.0-0", Stream: "4-stable"}: "c",
	}
	got := sortedBounds(m)
	want := []api.VersionBounds{
		{Lower: "4.11.0-0", Upper: "4.12.0-0", Stream: ""},
		{Lower: "4.11.0-0", Upper: "4.12.0-0", Stream: "4-stable"},
		{Lower: "4.12.0-0", Upper: "4.13.0-0", Stream: "4-stable"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("sortedBounds() differs: %s", diff)
	}
}

func TestClusterImageSetFileIsCurrent(t *testing.T) {
	ps := "quay.io/openshift-release-dev/ocp-release:4.7.60-x86_64"
	canon := api.VersionBounds{Lower: "4.7.0-0", Upper: "4.8.0-0", Stream: "4-stable"}
	outputDir := t.TempDir()
	path := clusterImageSetYAMLPath(outputDir, ps, canon)
	boundsToPullspec := map[api.VersionBounds]string{canon: ps}

	t.Run("stream-only annotation drift at canonical path", func(t *testing.T) {
		annot := map[string]string{
			versionLowerLabel: "4.7.0-0",
			versionUpperLabel: "4.8.0-0",
		}
		bounds, err := labelsToBounds(annot)
		if err != nil {
			t.Fatal(err)
		}
		imageset := hivev1.ClusterImageSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:        nameFromPullspec(ps, canon),
				Annotations: annot,
			},
			Spec: hivev1.ClusterImageSetSpec{ReleaseImage: ps},
		}
		if !clusterImageSetFileIsCurrent(path, outputDir, imageset, bounds, boundsToPullspec) {
			t.Fatal("expected file current so it is not queued for deletion before in-place rewrite")
		}
	})
	t.Run("exact bounds key", func(t *testing.T) {
		annot := map[string]string{
			versionLowerLabel:  "4.7.0-0",
			versionUpperLabel:  "4.8.0-0",
			versionStreamLabel: "4-stable",
		}
		bounds, err := labelsToBounds(annot)
		if err != nil {
			t.Fatal(err)
		}
		imageset := hivev1.ClusterImageSet{
			ObjectMeta: metav1.ObjectMeta{Name: nameFromPullspec(ps, canon), Annotations: annot},
			Spec:       hivev1.ClusterImageSetSpec{ReleaseImage: ps},
		}
		if !clusterImageSetFileIsCurrent(path, outputDir, imageset, bounds, boundsToPullspec) {
			t.Fatal("expected current")
		}
	})
	t.Run("wrong release image", func(t *testing.T) {
		annot := map[string]string{versionLowerLabel: "4.7.0-0", versionUpperLabel: "4.8.0-0"}
		bounds, err := labelsToBounds(annot)
		if err != nil {
			t.Fatal(err)
		}
		imageset := hivev1.ClusterImageSet{
			ObjectMeta: metav1.ObjectMeta{Annotations: annot},
			Spec:       hivev1.ClusterImageSetSpec{ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.7.59-x86_64"},
		}
		if clusterImageSetFileIsCurrent(path, outputDir, imageset, bounds, boundsToPullspec) {
			t.Fatal("expected not current")
		}
	})
	t.Run("stream drift but non-canonical path", func(t *testing.T) {
		annot := map[string]string{versionLowerLabel: "4.7.0-0", versionUpperLabel: "4.8.0-0"}
		bounds, err := labelsToBounds(annot)
		if err != nil {
			t.Fatal(err)
		}
		imageset := hivev1.ClusterImageSet{
			ObjectMeta: metav1.ObjectMeta{Annotations: annot},
			Spec:       hivev1.ClusterImageSetSpec{ReleaseImage: ps},
		}
		wrongPath := filepath.Join(outputDir, "other_clusterimageset.yaml")
		if clusterImageSetFileIsCurrent(wrongPath, outputDir, imageset, bounds, boundsToPullspec) {
			t.Fatal("expected not current when path does not match canonical output file")
		}
	})
}

func TestArchitectureForBounds(t *testing.T) {
	tests := []struct {
		name    string
		bounds  api.VersionBounds
		want    api.ReleaseArchitecture
		wantErr bool
	}{
		{"4.7 uses amd64", api.VersionBounds{Lower: "4.7.0-0", Upper: "4.8.0-0"}, api.ReleaseArchitectureAMD64, false},
		{"4.11 uses amd64", api.VersionBounds{Lower: "4.11.0-0", Upper: "4.12.0-0"}, api.ReleaseArchitectureAMD64, false},
		{"4.12 uses multi", api.VersionBounds{Lower: "4.12.0-0", Upper: "4.13.0-0"}, api.ReleaseArchitectureMULTI, false},
		{"4.13 uses multi", api.VersionBounds{Lower: "4.13.0-0", Upper: "4.14.0-0"}, api.ReleaseArchitectureMULTI, false},
		{"4.21 uses multi", api.VersionBounds{Lower: "4.21.0-0", Upper: "4.22.0-0"}, api.ReleaseArchitectureMULTI, false},
		{"5.0 uses multi", api.VersionBounds{Lower: "5.0.0-0", Upper: "5.1.0-0"}, api.ReleaseArchitectureMULTI, false},
		{"5.1 uses multi", api.VersionBounds{Lower: "5.1.0-0", Upper: "5.2.0-0"}, api.ReleaseArchitectureMULTI, false},
		{"v4.12 uses multi", api.VersionBounds{Lower: "v4.12.0-0", Upper: "4.13.0-0"}, api.ReleaseArchitectureMULTI, false},
		{"major 3 uses amd64", api.VersionBounds{Lower: "3.12.0-0", Upper: "4.0.0-0"}, api.ReleaseArchitectureAMD64, false},
		{"unparseable lower fails", api.VersionBounds{Lower: "bad", Upper: "4.8.0-0"}, "", true},
		{"single segment fails", api.VersionBounds{Lower: "4", Upper: "4.8.0-0"}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := architectureForBounds(tt.bounds)
			if (err != nil) != tt.wantErr {
				t.Errorf("architectureForBounds() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("architectureForBounds() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEnsureLabels(t *testing.T) {
	testCases := []struct {
		name             string
		given            hivev1.ClusterPool
		expected         hivev1.ClusterPool
		expectedModified bool
	}{
		{
			name: "basic case",
			given: hivev1.ClusterPool{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"owner": "dpp",
					},
				},
			},
			expected: hivev1.ClusterPool{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"owner": "dpp",
					},
				},
				Spec: hivev1.ClusterPoolSpec{
					Labels: map[string]string{"tp.openshift.io/owner": "dpp"},
				},
			},
			expectedModified: true,
		},
		{
			name: "not modified",
			given: hivev1.ClusterPool{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"owner": "dpp",
					},
				},
				Spec: hivev1.ClusterPoolSpec{
					Labels: map[string]string{"tp.openshift.io/owner": "dpp"},
				},
			},
			expected: hivev1.ClusterPool{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"owner": "dpp",
					},
				},
				Spec: hivev1.ClusterPoolSpec{
					Labels: map[string]string{"tp.openshift.io/owner": "dpp"},
				},
			},
		},
		{
			name: "modified",
			given: hivev1.ClusterPool{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"owner": "dpp",
					},
				},
				Spec: hivev1.ClusterPoolSpec{
					Labels: map[string]string{"tp.openshift.io/owner": "not-dpp"},
				},
			},
			expected: hivev1.ClusterPool{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"owner": "dpp",
					},
				},
				Spec: hivev1.ClusterPoolSpec{
					Labels: map[string]string{"tp.openshift.io/owner": "dpp"},
				},
			},
			expectedModified: true,
		},
		{
			name: "given has no labels",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual, actualModified := ensureLabels(tc.given)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("%s differs from expected:\n%s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expectedModified, actualModified); diff != "" {
				t.Errorf("%s differs from expected:\n%s", tc.name, diff)
			}
		})
	}
}

func TestEnsureLabelsOnClusterPools(t *testing.T) {
	dir, err := os.MkdirTemp("", "TestEnsureLabelsOnClusterPools")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	testCases := []struct {
		name            string
		input           string
		output          string
		expected        error
		expectedContent string
	}{
		{
			name:   "basic case",
			input:  filepath.Join("testdata", "pools", "cvp-ocp-4-9-amd64-aws-us-west-2_clusterpool.yaml"),
			output: filepath.Join(dir, "cvp-ocp-4-9-amd64-aws-us-west-2_clusterpool.yaml"),
			expectedContent: `apiVersion: hive.openshift.io/v1
kind: ClusterPool
metadata:
  labels:
    architecture: amd64
    cloud: aws
    owner: cvp
    product: ocp
    region: us-west-2
    version: "4.9"
    version_lower: 4.9.0-0
    version_upper: 4.10.0-0
  name: cvp-ocp-4-9-amd64-aws-us-west-2
  namespace: cvp-cluster-pool
spec:
  baseDomain: cpaas-ci.devcluster.openshift.com
  hibernationConfig:
    resumeTimeout: 15m0s
  imageSetRef:
    name: ocp-release-4.9.57-x86-64-for-4.9.0-0-to-4.10.0-0
  installAttemptsLimit: 1
  installConfigSecretTemplateRef:
    name: install-config-aws-us-west-2
  labels:
    tp.openshift.io/owner: cvp
  maxSize: 10
  platform:
    aws:
      credentialsSecretRef:
        name: cvp-aws-credentials
      region: us-west-2
  pullSecretRef:
    name: pull-secret
  runningCount: 1
  size: 5
  skipMachinePools: true
status:
  ready: 0
  size: 0
  standby: 0
`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if err := addSchemes(); err != nil {
				t.Fatal("Failed to set up scheme")
			}
			s := json.NewSerializerWithOptions(json.DefaultMetaFactory, scheme.Scheme,
				scheme.Scheme, json.SerializerOptions{Yaml: true, Pretty: false, Strict: false})
			actual := ensureLabelsOnClusterPool(s, tc.input, tc.output)
			if diff := cmp.Diff(tc.expected, actual, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s differs from expected:\n%s", tc.name, diff)
			}
			if actual == nil {
				raw, err := os.ReadFile(tc.output)
				if err != nil {
					t.Errorf("failed to read file: %v", err)
				}
				if diff := cmp.Diff(tc.expectedContent, string(raw)); diff != "" {
					t.Errorf("%s differs from expected:\n%s", tc.name, diff)
				}
			}
		})
	}
}
