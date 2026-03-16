package manifestpusher

import (
	"context"
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

func TestImportManifestList(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := imagev1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add imagev1 to scheme: %v", err)
	}

	var testCases = []struct {
		name          string
		targetRef     string
		digest        string
		wantNamespace string
		wantName      string
		wantTag       string
		wantPullSpec  string
		wantErr       bool
	}{
		{
			name:          "creates ImageStreamImport with correct parameters",
			targetRef:     "ci-op-abc123/pipeline:src",
			digest:        "sha256:deadbeef",
			wantNamespace: "ci-op-abc123",
			wantName:      "pipeline",
			wantTag:       "src",
			wantPullSpec:  "registry/ci-op-abc123/pipeline:src@sha256:deadbeef",
		},
		{
			name:      "returns error for invalid reference",
			targetRef: "invalid",
			digest:    "sha256:deadbeef",
			wantErr:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var createdImport *imagev1.ImageStreamImport
			client := &importCapturingClient{
				Client:          fakectrlruntimeclient.NewClientBuilder().WithScheme(scheme).Build(),
				capturedImports: &createdImport,
			}
			pusher := manifestPusher{
				logger:      logrus.NewEntry(logrus.New()),
				registryURL: "registry",
				client:      client,
			}
			err := pusher.importManifestList(tc.targetRef, tc.digest)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error but got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if createdImport == nil {
				t.Fatal("expected ImageStreamImport to be created, but it was not")
			}
			if createdImport.Namespace != tc.wantNamespace {
				t.Errorf("namespace: want %q, got %q", tc.wantNamespace, createdImport.Namespace)
			}
			if createdImport.Name != tc.wantName {
				t.Errorf("name: want %q, got %q", tc.wantName, createdImport.Name)
			}
			if !createdImport.Spec.Import {
				t.Error("expected Spec.Import to be true")
			}
			if len(createdImport.Spec.Images) != 1 {
				t.Fatalf("expected 1 image spec, got %d", len(createdImport.Spec.Images))
			}
			img := createdImport.Spec.Images[0]
			if img.To.Name != tc.wantTag {
				t.Errorf("tag: want %q, got %q", tc.wantTag, img.To.Name)
			}
			if img.From.Name != tc.wantPullSpec {
				t.Errorf("pullSpec: want %q, got %q", tc.wantPullSpec, img.From.Name)
			}
			if img.ImportPolicy.ImportMode != imagev1.ImportModePreserveOriginal {
				t.Errorf("importMode: want %q, got %q", imagev1.ImportModePreserveOriginal, img.ImportPolicy.ImportMode)
			}
			if !img.ImportPolicy.Insecure {
				t.Error("expected ImportPolicy.Insecure to be true")
			}
		})
	}
}

// importCapturingClient wraps a fake client to capture ImageStreamImport
// creates and simulate the server populating the Status field.
type importCapturingClient struct {
	ctrlruntimeclient.Client
	capturedImports **imagev1.ImageStreamImport
}

func (c *importCapturingClient) Create(ctx context.Context, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.CreateOption) error {
	if isi, ok := obj.(*imagev1.ImageStreamImport); ok {
		*c.capturedImports = isi.DeepCopy()
		isi.Status = imagev1.ImageStreamImportStatus{
			Images: []imagev1.ImageImportStatus{
				{
					Image: &imagev1.Image{
						ObjectMeta:           metav1.ObjectMeta{Name: "sha256:deadbeef"},
						DockerImageReference: isi.Spec.Images[0].From.Name,
					},
				},
			},
		}
		return nil
	}
	return c.Client.Create(ctx, obj, opts...)
}

func TestParseImageStreamRef(t *testing.T) {
	var testCases = []struct {
		name            string
		targetImageRef  string
		wantNamespace   string
		wantImageStream string
		wantTag         string
		wantError       bool
	}{
		{
			name:            "valid reference",
			targetImageRef:  "ci-op-abc123/pipeline:src",
			wantNamespace:   "ci-op-abc123",
			wantImageStream: "pipeline",
			wantTag:         "src",
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

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			namespace, imageStream, tag, err := parseImageStreamRef(tc.targetImageRef)
			if tc.wantError {
				if err == nil {
					t.Fatal("expected error but got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(tc.wantNamespace, namespace); diff != "" {
				t.Fatalf("namespace mismatch (-want +got): %s", diff)
			}
			if diff := cmp.Diff(tc.wantImageStream, imageStream); diff != "" {
				t.Fatalf("imageStream mismatch (-want +got): %s", diff)
			}
			if diff := cmp.Diff(tc.wantTag, tag); diff != "" {
				t.Fatalf("tag mismatch (-want +got): %s", diff)
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
