package imagestreamtagwrapper

import (
	"context"
	"os"
	"testing"

	"github.com/pmezard/go-difflib/difflib"

	"k8s.io/apimachinery/pkg/runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"

	imagev1 "github.com/openshift/api/image/v1"
)

func TestGetImageStreamTag(t *testing.T) {
	rawImageStream, err := os.ReadFile("testdata/imagestream.yaml")
	if err != nil {
		t.Fatalf("failed to read imagestream from disk: %v", err)
	}
	imageStream := &imagev1.ImageStream{}
	if err := yaml.Unmarshal(rawImageStream, imageStream); err != nil {
		t.Fatalf("failed to unmarshal imagestream: %v", err)
	}
	rawImages, err := os.ReadFile("testdata/images.yaml")
	if err != nil {
		t.Fatalf("failed to read images from disk: %v", err)
	}
	images := &imagev1.ImageList{}
	if err := yaml.Unmarshal(rawImages, images); err != nil {
		t.Fatalf("failed to unmarshal images: %v", err)
	}
	rawImageStreamTags, err := os.ReadFile("testdata/imagestreamtags.yaml")
	if err != nil {
		t.Fatalf("failed to read imagestreamtags from disk: %v", err)
	}
	imageStreamTags := &imagev1.ImageStreamTagList{}
	if err := yaml.Unmarshal(rawImageStreamTags, imageStreamTags); err != nil {
		t.Fatalf("failed to unmarshal imagestreamtags: %v", err)
	}

	scheme := runtime.NewScheme()
	if err := imagev1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to register imagev1 to scheme: %v", err)
	}

	client := &imagestreamtagwrapper{
		Client: fakectrlruntimeclient.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(imageStream, images).Build(),
	}
	ctx := context.Background()

	for _, imageStreamTag := range imageStreamTags.Items {
		imageStreamTag := imageStreamTag
		t.Run(imageStreamTag.Name, func(t *testing.T) {
			t.Parallel()
			key := ctrlruntimeclient.ObjectKey{Namespace: "ci-op-jpdy23wx", Name: imageStreamTag.Name}
			result := &imagev1.ImageStreamTag{}
			if err := client.Get(ctx, key, result); err != nil {
				t.Fatalf("failed to retrieve object from client: %v", err)
			}

			// Fixtures have this. Its completely irrelevant except when the data is on the wire
			// but adding it here means these tests will pass with arbitraty inputs.
			gvks, _, err := scheme.ObjectKinds(result)
			if err != nil {
				t.Fatalf("failed to get objectKinds from scheme: %v", err)
			}
			result.APIVersion, result.Kind = gvks[0].ToAPIVersionAndKind()

			// We have to serialize back because ISTs have embedded raw json
			serializedInput, err := yaml.Marshal(imageStreamTag)
			if err != nil {
				t.Fatalf("failed to serialize input: %v", err)
			}
			serializedOutput, err := yaml.Marshal(result)
			if err != nil {
				t.Fatalf("failed to serialize output: %v", err)
			}
			diff := difflib.UnifiedDiff{
				A:        difflib.SplitLines(string(serializedInput)),
				B:        difflib.SplitLines(string(serializedOutput)),
				FromFile: "expected",
				ToFile:   "actual",
				Context:  3,
			}
			diffStr, err := difflib.GetUnifiedDiffString(diff)
			if err != nil {
				t.Fatalf("failed to get diff: %v", err)
			}

			if diffStr != "" {
				t.Fatalf("objects differ:\n---\n%s\n---\n", diffStr)
			}
		})
	}
}
