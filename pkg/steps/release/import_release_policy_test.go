package release

import (
	"testing"

	imagev1 "github.com/openshift/api/image/v1"
)

func TestPayloadTagImpliesSource(t *testing.T) {
	preserve := imagev1.ImportModePreserveOriginal
	tests := []struct {
		name string
		tag  imagev1.TagReference
		want bool
	}{
		{name: "no hints", tag: imagev1.TagReference{Name: "x"}, want: false},
		{name: "reference true", tag: imagev1.TagReference{Name: "x", Reference: true}, want: true},
		{name: "preserve original", tag: imagev1.TagReference{Name: "x", ImportPolicy: imagev1.TagImportPolicy{ImportMode: preserve}}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := payloadTagImpliesSource(&tt.tag); got != tt.want {
				t.Fatalf("payloadTagImpliesSource() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMergePayloadReferencePolicy(t *testing.T) {
	source := imagev1.SourceTagReferencePolicy
	local := imagev1.LocalTagReferencePolicy
	preserve := imagev1.ImportModePreserveOriginal
	tests := []struct {
		name     string
		tag      imagev1.TagReference
		config   imagev1.TagReferencePolicyType
		wantType imagev1.TagReferencePolicyType
	}{
		{
			name:     "config overrides payload",
			tag:      imagev1.TagReference{Name: "x", ReferencePolicy: imagev1.TagReferencePolicy{Type: source}},
			config:   local,
			wantType: local,
		},
		{
			name:     "config local overrides reference-hint tag",
			tag:      imagev1.TagReference{Name: "x", Reference: true},
			config:   local,
			wantType: local,
		},
		{
			name:     "preserve payload source",
			tag:      imagev1.TagReference{Name: "x", ReferencePolicy: imagev1.TagReferencePolicy{Type: source}},
			config:   "",
			wantType: source,
		},
		{
			name:     "preserve payload local",
			tag:      imagev1.TagReference{Name: "x", ReferencePolicy: imagev1.TagReferencePolicy{Type: local}},
			config:   "",
			wantType: local,
		},
		{
			name:     "empty policy and no source hints defaults local",
			tag:      imagev1.TagReference{Name: "x"},
			config:   "",
			wantType: local,
		},
		{
			name: "reference true implies source when policy empty",
			tag: imagev1.TagReference{
				Name:      "x",
				Reference: true,
			},
			config:   "",
			wantType: source,
		},
		{
			name: "preserve original import implies source when policy empty",
			tag: imagev1.TagReference{
				Name: "x",
				ImportPolicy: imagev1.TagImportPolicy{
					ImportMode: preserve,
				},
			},
			config:   "",
			wantType: source,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergedPayloadReferencePolicy(tt.tag, tt.config)
			if got.ReferencePolicy.Type != tt.wantType {
				t.Fatalf("ReferencePolicy.Type = %q, want %q", got.ReferencePolicy.Type, tt.wantType)
			}
		})
	}
}
