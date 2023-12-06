package api

import (
	"reflect"
	"testing"

	"github.com/ghodss/yaml"
	"github.com/google/go-cmp/cmp"
)

func TestOverlay(t *testing.T) {
	tests := []struct {
		name      string
		base      string
		overlay   string
		want      *ReleaseBuildConfiguration
		wantInput *InputConfiguration
	}{
		{
			name:      "empty",
			base:      "{}",
			overlay:   "{}",
			want:      &ReleaseBuildConfiguration{},
			wantInput: &InputConfiguration{},
		},
		{
			name:    "empty",
			base:    `{}`,
			overlay: `{"base_images":{"test":{"name":"test-1"}}}`,
			want: &ReleaseBuildConfiguration{
				InputConfiguration: InputConfiguration{
					BaseImages: map[string]ImageStreamTagReference{
						"test": {Name: "test-1"},
					},
				},
			},
			wantInput: &InputConfiguration{
				BaseImages: map[string]ImageStreamTagReference{
					"test": {Name: "test-1"},
				},
			},
		},
		{
			name:    "overwrite",
			base:    `{"base_images":{"test":{"name":"test-0"}}}`,
			overlay: `{"base_images":{"test":{"name":"test-1"}}}`,
			want: &ReleaseBuildConfiguration{
				InputConfiguration: InputConfiguration{
					BaseImages: map[string]ImageStreamTagReference{
						"test": {Name: "test-1"},
					},
				},
			},
			wantInput: &InputConfiguration{
				BaseImages: map[string]ImageStreamTagReference{
					"test": {Name: "test-1"},
				},
			},
		},
		{
			name:    "map merge",
			base:    `{"base_images":{"test-0":{"name":"test-0"}}}`,
			overlay: `{"base_images":{"test-1":{"name":"test-1"}}}`,
			want: &ReleaseBuildConfiguration{
				InputConfiguration: InputConfiguration{
					BaseImages: map[string]ImageStreamTagReference{
						"test-0": {Name: "test-0"},
						"test-1": {Name: "test-1"},
					},
				},
			},
			wantInput: &InputConfiguration{
				BaseImages: map[string]ImageStreamTagReference{
					"test-1": {Name: "test-1"},
				},
			},
		},
		{
			name:    "map merge by field",
			base:    `{"base_images":{"test-0":{"name":"test-0","namespace":"0"}}}`,
			overlay: `{"base_images":{"test-0":{"name":"test-0","namespace":null}}}`,
			want: &ReleaseBuildConfiguration{
				InputConfiguration: InputConfiguration{
					BaseImages: map[string]ImageStreamTagReference{
						"test-0": {Name: "test-0"},
					},
				},
			},
			wantInput: &InputConfiguration{
				BaseImages: map[string]ImageStreamTagReference{
					"test-0": {Name: "test-0"},
				},
			},
		},
		{
			name:    "skips missing key",
			base:    `{"build_root":{}}`,
			overlay: `{}`,
			want: &ReleaseBuildConfiguration{
				InputConfiguration: InputConfiguration{
					BuildRootImage: &BuildRootImageConfiguration{},
				},
			},
			wantInput: &InputConfiguration{},
		},
		{
			name:    "clears with explicit null",
			base:    `{"build_root":{}}`,
			overlay: `{"build_root":null}`,
			want: &ReleaseBuildConfiguration{
				InputConfiguration: InputConfiguration{},
			},
			wantInput: &InputConfiguration{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &ReleaseBuildConfiguration{}
			input := &InputConfiguration{}
			if err := yaml.Unmarshal([]byte(tt.base), config); err != nil {
				t.Fatal(err)
			}
			if err := yaml.Unmarshal([]byte(tt.overlay), config); err != nil {
				t.Fatal(err)
			}
			if err := yaml.Unmarshal([]byte(tt.overlay), input); err != nil {
				t.Fatal(err)
			}
			if got := input; !reflect.DeepEqual(got, tt.wantInput) {
				t.Errorf("input:\n%#v\n%#v", got, tt.wantInput)
			}
			if got := config; !reflect.DeepEqual(got, tt.want) {
				t.Errorf("config:\n%#v\n%#v", got, tt.want)
			}
		})
	}
}

func TestBuildsImage(t *testing.T) {
	conf := ReleaseBuildConfiguration{
		Images: []ProjectDirectoryImageBuildStepConfiguration{
			{To: "this-image-is-in-the-images-field"},
		},
	}
	for _, tc := range []struct {
		name  string
		image string
		want  bool
	}{{
		name:  "not in `images`",
		image: "this-image-is-not-in-the-images-field",
	}, {
		name:  "in `images`",
		image: "this-image-is-in-the-images-field",
		want:  true,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			if ret := conf.BuildsImage(tc.image); ret != tc.want {
				t.Errorf("got %v, want %v", ret, tc.want)
			}
		})
	}
}

func TestIsPipelineImage(t *testing.T) {
	conf := ReleaseBuildConfiguration{
		InputConfiguration: InputConfiguration{
			BaseImages: map[string]ImageStreamTagReference{
				"base-img": {},
			},
			BaseRPMImages: map[string]ImageStreamTagReference{
				"base-rpm-img": {},
			},
		},
		BinaryBuildCommands:     "make",
		TestBinaryBuildCommands: "make test-bin",
		RpmBuildCommands:        "make rpms",
		Images:                  []ProjectDirectoryImageBuildStepConfiguration{{To: "img"}},
	}
	for _, tc := range []struct {
		name string
		want bool
	}{
		{name: "base-img", want: true},
		{name: "base-rpm-img", want: true},
		{name: "root", want: true},
		{name: "root-org.repo", want: true},
		{name: "bin", want: true},
		{name: "bin-org.repo", want: true},
		{name: "test-bin", want: true},
		{name: "rpms", want: true},
		{name: "img"},
		{name: "404"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if ret := conf.IsPipelineImage(tc.name); ret != tc.want {
				t.Errorf("want %v, got %v", tc.want, ret)
			}
		})
	}
}

func TestBundleName(t *testing.T) {
	if BundleName(0) != "ci-bundle0" {
		t.Errorf("Expected %s, got %s", "ci-bundle0", BundleName(0))
	}
	if BundleName(1) != "ci-bundle1" {
		t.Errorf("Expected %s, got %s", "ci-bundle1", BundleName(1))
	}
}

func TestIsBundleImage(t *testing.T) {
	config := ReleaseBuildConfiguration{
		Operator: &OperatorStepConfiguration{
			Bundles: []Bundle{{As: "my-bundle"}, {As: ""}},
		},
	}
	testCases := []struct {
		name     string
		expected bool
	}{{
		name:     BundleName(0),
		expected: true,
	}, {
		name:     BundleName(1),
		expected: true,
	}, {
		name:     "my-bundle",
		expected: true,
	}, {
		name:     "not-a-bundle",
		expected: false,
	}, {
		name:     "",
		expected: false,
	}}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if config.IsBundleImage(testCase.name) != testCase.expected {
				t.Errorf("Expected %t, got %t", testCase.expected, config.IsBundleImage(testCase.name))
			}
		})
	}
}

func TestInputImageTagStepConfiguration(t *testing.T) {
	baseImage := ImageStreamTagReference{
		Name:      "image",
		Namespace: "ns",
		Tag:       "tag",
	}

	otherImage := ImageStreamTagReference{
		Name:      "image2",
		Namespace: "ns",
		Tag:       "tag",
	}

	baseConfig := InputImageTagStepConfiguration{
		InputImage: InputImage{
			To:        "TO",
			BaseImage: baseImage,
		},
	}

	testCases := []struct {
		name                     string
		config                   InputImageTagStepConfiguration
		sources                  []ImageStreamSource
		inputImage               *InputImage
		matches                  bool
		expectedFormattedSources string
	}{{
		name:   "test step sources",
		config: baseConfig,
		sources: []ImageStreamSource{
			{
				SourceType: ImageStreamSourceTest,
				Name:       "test1",
			},
			{
				SourceType: ImageStreamSourceTest,
				Name:       "test2",
			},
		},
	}, {
		name:   "test inputImage matches",
		config: baseConfig,
		inputImage: &InputImage{
			To:        "TO",
			BaseImage: baseImage,
		},
		matches: true,
	}, {
		name:   "test inputImage doesn't match",
		config: baseConfig,
		inputImage: &InputImage{
			To:        "TO",
			BaseImage: otherImage,
		},
		matches: false,
	}, {
		name:   "test output formatted sources",
		config: baseConfig,
		sources: []ImageStreamSource{
			{
				SourceType: ImageStreamSourceRoot,
			},
			{
				SourceType: ImageStreamSourceBase,
				Name:       "os",
			},
			{
				SourceType: ImageStreamSourceBaseRpm,
				Name:       "rpms",
			},
			{
				SourceType: ImageStreamSourceTest,
				Name:       "test1",
			},
			{
				SourceType: ImageStreamSourceTest,
				Name:       "test2",
			},
		},
		expectedFormattedSources: "root|base_image: os|base_rpm_image: rpms|test steps: test1,test2",
	}}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			testCase.config.AddSources(testCase.sources...)

			if diff := cmp.Diff(testCase.sources, testCase.config.Sources); diff != "" {
				t.Errorf("Unexpected sources: %v", diff)
			}
			if testCase.inputImage != nil {
				if testCase.config.Matches(*testCase.inputImage) != testCase.matches {
					t.Errorf("Expected matches to be %t but was %t", testCase.matches, !testCase.matches)
				}
			}
			if testCase.expectedFormattedSources != "" {
				if diff := cmp.Diff(testCase.expectedFormattedSources, testCase.config.FormattedSources()); diff != "" {
					t.Errorf("Unexpected formatted sources : %v", diff)
				}
			}
		})
	}
}
