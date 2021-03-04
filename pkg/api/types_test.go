package api

import (
	"reflect"
	"testing"

	"github.com/ghodss/yaml"
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
		{name: "bin", want: true},
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
			Bundles: []Bundle{{As: "my-bundle"}},
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
	}}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if config.IsBundleImage(testCase.name) != testCase.expected {
				t.Errorf("Expected %t, got %t", testCase.expected, config.IsBundleImage(testCase.name))
			}
		})
	}
}
