package manifestpusher

import (
	"testing"

	"github.com/estesp/manifest-tool/v2/pkg/types"
	"github.com/google/go-cmp/cmp"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	buildv1 "github.com/openshift/api/build/v1"
	imagev1 "github.com/openshift/api/image/v1"
)

func TestManifestEntries(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := imagev1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add imagev1 to scheme: %v", err)
	}

	var testCases = []struct {
		name      string
		builds    []buildv1.Build
		targetRef string
		objects   []ctrlruntimeclient.Object
		want      []types.ManifestEntry
	}{
		{
			name:      "returns current entries when imagestreamtag is missing",
			targetRef: "ns/pipeline:src",
			builds: []buildv1.Build{
				{
					Spec: buildv1.BuildSpec{
						CommonSpec: buildv1.CommonSpec{
							NodeSelector: map[string]string{nodeArchitectureLabel: "amd64"},
							Output: buildv1.BuildOutput{
								To: &corev1.ObjectReference{Namespace: "ns", Name: "pipeline:src-amd64"},
							},
						},
					},
				},
			},
			want: []types.ManifestEntry{
				{
					Image: "registry/ns/pipeline:src-amd64",
					Platform: ocispec.Platform{
						OS:           "linux",
						Architecture: "amd64",
					},
				},
			},
		},
		{
			name:      "returns current entries when imagestreamtag has no manifests",
			targetRef: "ns/pipeline:src",
			builds: []buildv1.Build{
				{
					Spec: buildv1.BuildSpec{
						CommonSpec: buildv1.CommonSpec{
							NodeSelector: map[string]string{nodeArchitectureLabel: "amd64"},
							Output: buildv1.BuildOutput{
								To: &corev1.ObjectReference{Namespace: "ns", Name: "pipeline:src-amd64"},
							},
						},
					},
				},
			},
			objects: []ctrlruntimeclient.Object{
				&imagev1.ImageStreamTag{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "pipeline:src"},
					Image:      imagev1.Image{},
				},
			},
			want: []types.ManifestEntry{
				{
					Image: "registry/ns/pipeline:src-amd64",
					Platform: ocispec.Platform{
						OS:           "linux",
						Architecture: "amd64",
					},
				},
			},
		},
		{
			name:      "returns current and existing manifest entries",
			targetRef: "ns/pipeline:src",
			builds: []buildv1.Build{
				{
					Spec: buildv1.BuildSpec{
						CommonSpec: buildv1.CommonSpec{
							NodeSelector: map[string]string{nodeArchitectureLabel: "amd64"},
							Output: buildv1.BuildOutput{
								To: &corev1.ObjectReference{Namespace: "ns", Name: "pipeline:src-amd64"},
							},
						},
					},
				},
			},
			objects: []ctrlruntimeclient.Object{
				&imagev1.ImageStreamTag{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "pipeline:src"},
					Image: imagev1.Image{
						DockerImageManifests: []imagev1.ImageManifest{
							{Digest: "sha256:arm64digest", OS: "linux", Architecture: "arm64"},
						},
					},
				},
			},
			want: []types.ManifestEntry{
				{
					Image: "registry/ns/pipeline:src-amd64",
					Platform: ocispec.Platform{
						OS:           "linux",
						Architecture: "amd64",
					},
				},
				{
					Image: "registry/ns/pipeline:src@sha256:arm64digest",
					Platform: ocispec.Platform{
						OS:           "linux",
						Architecture: "arm64",
					},
				},
			},
		},
		{
			name:      "skips existing manifests for rebuilt architecture",
			targetRef: "ns/pipeline:src",
			builds: []buildv1.Build{
				{
					Spec: buildv1.BuildSpec{
						CommonSpec: buildv1.CommonSpec{
							NodeSelector: map[string]string{nodeArchitectureLabel: "amd64"},
							Output: buildv1.BuildOutput{
								To: &corev1.ObjectReference{Namespace: "ns", Name: "pipeline:src-amd64"},
							},
						},
					},
				},
			},
			objects: []ctrlruntimeclient.Object{
				&imagev1.ImageStreamTag{
					ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "pipeline:src"},
					Image: imagev1.Image{
						DockerImageManifests: []imagev1.ImageManifest{
							{Digest: "sha256:existing-amd64", OS: "linux", Architecture: "amd64"},
							{Digest: "sha256:existing-arm64", OS: "linux", Architecture: "arm64"},
						},
					},
				},
			},
			want: []types.ManifestEntry{
				{
					Image: "registry/ns/pipeline:src-amd64",
					Platform: ocispec.Platform{
						OS:           "linux",
						Architecture: "amd64",
					},
				},
				{
					Image: "registry/ns/pipeline:src@sha256:existing-arm64",
					Platform: ocispec.Platform{
						OS:           "linux",
						Architecture: "arm64",
					},
				},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			client := fakectrlruntimeclient.NewClientBuilder().WithScheme(scheme).WithObjects(testCase.objects...).Build()
			pusher := manifestPusher{
				logger:      logrus.NewEntry(logrus.New()),
				registryURL: "registry",
				client:      client,
			}
			actual, err := pusher.manifestEntries(testCase.builds, testCase.targetRef)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(testCase.want, actual); diff != "" {
				t.Fatalf("manifestEntries mismatch (-want +got): %s", diff)
			}
		})
	}
}

func TestSplitImageStreamTagRef(t *testing.T) {
	var testCases = []struct {
		name           string
		targetImageRef string
		wantNamespace  string
		wantName       string
		wantError      bool
	}{
		{
			name:           "valid reference",
			targetImageRef: "ci/pipeline:src",
			wantNamespace:  "ci",
			wantName:       "pipeline:src",
		},
		{
			name:           "invalid missing slash",
			targetImageRef: "pipeline:src",
			wantError:      true,
		},
		{
			name:           "invalid missing tag",
			targetImageRef: "ci/pipeline",
			wantError:      true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			namespace, name, err := splitImageStreamTagRef(testCase.targetImageRef)
			if testCase.wantError {
				if err == nil {
					t.Fatalf("expected error but got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(testCase.wantNamespace, namespace); diff != "" {
				t.Fatalf("namespace mismatch (-want +got): %s", diff)
			}
			if diff := cmp.Diff(testCase.wantName, name); diff != "" {
				t.Fatalf("name mismatch (-want +got): %s", diff)
			}
		})
	}
}
