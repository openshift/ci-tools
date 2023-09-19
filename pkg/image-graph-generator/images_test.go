package imagegraphgenerator

import (
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/api"
)

func TestOperator_UpdateImage(t *testing.T) {
	type args struct {
		image    api.ProjectDirectoryImageBuildStepConfiguration
		c        *api.ReleaseBuildConfiguration
		branchID string
	}
	tests := []struct {
		name     string
		images   map[string]string
		args     args
		expected map[string]Image
		wantErr  bool
	}{
		{
			name: "basic case",
			args: args{
				image: api.ProjectDirectoryImageBuildStepConfiguration{
					From: "os",
					To:   "test-image",
				},
				c: &api.ReleaseBuildConfiguration{
					PromotionConfiguration: &api.PromotionConfiguration{
						Targets: []api.PromotionTarget{{
							Namespace: "test-ns",
							Name:      "test-is",
						}},
					},
				},
				branchID: "0x12345",
			},
			expected: map[string]Image{
				"1": {
					ID:             "1",
					Name:           "test-ns/test-is:test-image",
					ImageStreamRef: "test-is",
					Namespace:      "test-ns",
					Branches:       map[string]interface{}{"id": string("0x12345")},
				},
			},
		},
		{
			name: "basic case - update",
			args: args{
				image: api.ProjectDirectoryImageBuildStepConfiguration{
					From: "root",
					To:   "test-image",
				},
				c: &api.ReleaseBuildConfiguration{
					PromotionConfiguration: &api.PromotionConfiguration{
						Targets: []api.PromotionTarget{{
							Namespace: "test-ns",
							Name:      "test-is",
						}},
					},
				},
				branchID: "0x12345",
			},
			images: map[string]string{
				"test-ns/test-is:test-image": "1",
			},
			expected: map[string]Image{
				"1": {
					ID:             "1",
					Name:           "test-ns/test-is:test-image",
					ImageStreamRef: "test-is",
					Namespace:      "test-ns",
					Branches:       map[string]interface{}{"id": string("0x12345")},
					FromRoot:       true,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fc := NewFakeClient()
			o := &Operator{
				c:      fc,
				images: tt.images,
			}
			if err := o.UpdateImage(tt.args.image, tt.args.c.BaseImages, tt.args.c.PromotionConfiguration.Targets[0], tt.args.branchID); (err != nil) != tt.wantErr {
				t.Errorf("Operator.UpdateImage() error = %v, wantErr %v", err, tt.wantErr)
			}
			if diff := cmp.Diff(fc.images, tt.expected); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}

func Test_extractImageFromURL(t *testing.T) {
	tests := []struct {
		name     string
		imageURL string
		want     *imageInfo
	}{
		{
			name:     "basic case",
			imageURL: "registry.ci.openshift.org/ocp/builder:rhel-8-golang-1.20-openshift-4.14",
			want: &imageInfo{
				registry:  "registry.ci.openshift.org",
				namespace: "ocp",
				name:      "builder",
				tag:       "rhel-8-golang-1.20-openshift-4.14",
			},
		},
		{
			name:     "wrong case",
			imageURL: "golang:${GOLANG_VERSION}",
			want:     nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractImageFromURL(tt.imageURL); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("extractImageFromURL() = %v, want %v", got, tt.want)
			}
		})
	}
}
