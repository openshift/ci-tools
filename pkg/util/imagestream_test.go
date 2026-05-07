package util

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	imageapi "github.com/openshift/api/image/v1"
)

func TestResolvePullSpec(t *testing.T) {
	const tag = "cli"
	testCases := []struct {
		name string
		is   *imageapi.ImageStream
		want string
		ok   bool
	}{
		{
			name: "source digest-only status resolves",
			is: &imageapi.ImageStream{
				Spec: imageapi.ImageStreamSpec{Tags: []imageapi.TagReference{{
					Name:            tag,
					ReferencePolicy: imageapi.TagReferencePolicy{Type: imageapi.SourceTagReferencePolicy},
				}}},
				Status: imageapi.ImageStreamStatus{
					DockerImageRepository: "image-registry.openshift-image-registry.svc:5000/ns/stable",
					Tags: []imageapi.NamedTagEventList{{Tag: tag, Items: []imageapi.TagEvent{{
						DockerImageReference: "quay.io/org/repo@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
						Image:                "",
					}}}},
				},
			},
			want: "quay.io/org/repo@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			ok:   true,
		},
		{
			name: "local policy needs image id",
			is: &imageapi.ImageStream{
				Spec: imageapi.ImageStreamSpec{Tags: []imageapi.TagReference{{
					Name:            tag,
					ReferencePolicy: imageapi.TagReferencePolicy{Type: imageapi.LocalTagReferencePolicy},
				}}},
				Status: imageapi.ImageStreamStatus{
					Tags: []imageapi.NamedTagEventList{{Tag: tag, Items: []imageapi.TagEvent{{
						DockerImageReference: "quay.io/org/repo@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
						Image:                "",
					}}}},
				},
			},
			ok: false,
		},
		{
			name: "source uses spec digest when status empty",
			is: &imageapi.ImageStream{
				Spec: imageapi.ImageStreamSpec{Tags: []imageapi.TagReference{{
					Name: tag,
					From: &corev1.ObjectReference{
						Kind: "DockerImage",
						Name: "registry.example/ns/repo@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
					},
					ReferencePolicy: imageapi.TagReferencePolicy{Type: imageapi.SourceTagReferencePolicy},
				}}},
				Status: imageapi.ImageStreamStatus{
					Tags: []imageapi.NamedTagEventList{{Tag: tag, Items: []imageapi.TagEvent{}, Conditions: []imageapi.TagEventCondition{{
						Type: imageapi.ImportSuccess, Status: corev1.ConditionTrue,
					}}}},
				},
			},
			want: "registry.example/ns/repo@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			ok:   true,
		},
		{
			name: "source uses spec digest when status ref empty",
			is: &imageapi.ImageStream{
				Spec: imageapi.ImageStreamSpec{Tags: []imageapi.TagReference{{
					Name: tag,
					From: &corev1.ObjectReference{
						Kind: "DockerImage",
						Name: "registry.example/ns/repo@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
					},
					ReferencePolicy: imageapi.TagReferencePolicy{Type: imageapi.SourceTagReferencePolicy},
				}}},
				Status: imageapi.ImageStreamStatus{
					Tags: []imageapi.NamedTagEventList{{Tag: tag, Items: []imageapi.TagEvent{{Image: "", DockerImageReference: ""}}}},
				},
			},
			want: "registry.example/ns/repo@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			ok:   true,
		},
		{
			name: "unset policy without hints does not resolve digest-only",
			is: &imageapi.ImageStream{
				Spec: imageapi.ImageStreamSpec{Tags: []imageapi.TagReference{{Name: tag}}},
				Status: imageapi.ImageStreamStatus{
					Tags: []imageapi.NamedTagEventList{{Tag: tag, Items: []imageapi.TagEvent{{
						DockerImageReference: "quay.io/org/repo@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
						Image:                "",
					}}}},
				},
			},
			ok: false,
		},
		{
			name: "unset policy with preserve original resolves digest-only",
			is: &imageapi.ImageStream{
				Spec: imageapi.ImageStreamSpec{Tags: []imageapi.TagReference{{
					Name: tag,
					ImportPolicy: imageapi.TagImportPolicy{
						ImportMode: imageapi.ImportModePreserveOriginal,
					},
				}}},
				Status: imageapi.ImageStreamStatus{
					Tags: []imageapi.NamedTagEventList{{Tag: tag, Items: []imageapi.TagEvent{{
						DockerImageReference: "quay.io/org/repo@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
						Image:                "",
					}}}},
				},
			},
			want: "quay.io/org/repo@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			ok:   true,
		},
		{
			name: "source import failed does not resolve",
			is: &imageapi.ImageStream{
				Spec: imageapi.ImageStreamSpec{Tags: []imageapi.TagReference{{
					Name:            tag,
					ReferencePolicy: imageapi.TagReferencePolicy{Type: imageapi.SourceTagReferencePolicy},
				}}},
				Status: imageapi.ImageStreamStatus{
					Tags: []imageapi.NamedTagEventList{{Tag: tag, Items: []imageapi.TagEvent{{
						DockerImageReference: "quay.io/org/repo@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
						Image:                "",
					}}, Conditions: []imageapi.TagEventCondition{{
						Type: imageapi.ImportSuccess, Status: corev1.ConditionFalse,
					}}}},
				},
			},
			ok: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok, _ := ResolvePullSpec(tc.is, tag, true)
			if ok != tc.ok {
				t.Fatalf("ResolvePullSpec() ok = %t, want %t", ok, tc.ok)
			}
			if got != tc.want {
				t.Fatalf("ResolvePullSpec() pullSpec = %q, want %q", got, tc.want)
			}
		})
	}
}
