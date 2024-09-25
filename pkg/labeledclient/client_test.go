package labeledclient

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"

	coreapi "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	v1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/pod-utils/downwardapi"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/steps"
)

func TestCreate(t *testing.T) {
	tests := []struct {
		name     string
		obj      ctrlruntimeclient.Object
		expected map[string]string
	}{
		{
			name: "Object without existing labels",
			obj: &coreapi.Pod{
				ObjectMeta: meta.ObjectMeta{
					Name: "test-pod",
				},
			},
			expected: map[string]string{
				steps.LabelMetadataOrg:     "test-org",
				steps.LabelMetadataRepo:    "test-repo",
				steps.LabelMetadataTarget:  "images",
				steps.LabelMetadataBranch:  "",
				steps.LabelMetadataVariant: "",
				steps.LabelJobType:         "periodic",
				steps.LabelJobID:           "",
				steps.LabelJobName:         "test-job",
				steps.CreatedByCILabel:     "true",
				"OPENSHIFT_CI":             "true",
			},
		},
		{
			name: "Object with existing labels",
			obj: &coreapi.Pod{
				ObjectMeta: meta.ObjectMeta{
					Name: "test-pod",
					Labels: map[string]string{
						"existing": "label",
						"another":  "label",
					},
				},
			},
			expected: map[string]string{
				"existing":                 "label",
				"another":                  "label",
				steps.LabelMetadataOrg:     "test-org",
				steps.LabelMetadataRepo:    "test-repo",
				steps.LabelMetadataTarget:  "images",
				steps.LabelMetadataBranch:  "",
				steps.LabelMetadataVariant: "",
				steps.LabelJobType:         "periodic",
				steps.LabelJobID:           "",
				steps.LabelJobName:         "test-job",
				steps.CreatedByCILabel:     "true",
				"OPENSHIFT_CI":             "true",
			},
		},
		{
			name: "Object with the same labels",
			obj: &coreapi.Pod{
				ObjectMeta: meta.ObjectMeta{
					Name: "test-pod",
					Labels: map[string]string{
						steps.LabelMetadataOrg:     "test-org",
						steps.LabelMetadataRepo:    "test-repo",
						steps.LabelMetadataTarget:  "images",
						steps.LabelMetadataBranch:  "",
						steps.LabelMetadataVariant: "",
						steps.LabelJobType:         "periodic",
						steps.LabelJobID:           "",
						steps.LabelJobName:         "test-job",
						steps.CreatedByCILabel:     "true",
						"OPENSHIFT_CI":             "true",
					},
				},
			},
			expected: map[string]string{
				steps.LabelMetadataOrg:     "test-org",
				steps.LabelMetadataRepo:    "test-repo",
				steps.LabelMetadataTarget:  "images",
				steps.LabelMetadataBranch:  "",
				steps.LabelMetadataVariant: "",
				steps.LabelJobType:         "periodic",
				steps.LabelJobID:           "",
				steps.LabelJobName:         "test-job",
				steps.CreatedByCILabel:     "true",
				"OPENSHIFT_CI":             "true",
			},
		},
	}
	for _, tt := range tests {
		var jobSpec = &api.JobSpec{
			Metadata: api.Metadata{
				Org:  "test-org",
				Repo: "test-repo",
			},
			Target: "[images]",
			JobSpec: downwardapi.JobSpec{
				Type: v1.PeriodicJob,
				Job:  "test-job",
			},
		}
		t.Run(tt.name, func(t *testing.T) {
			c := &client{
				upstream: fakeclient.NewClientBuilder().Build(),
				jobSpec:  jobSpec,
			}
			_ = c.Create(context.TODO(), tt.obj)
			if diff := cmp.Diff(tt.expected, tt.obj.GetLabels()); diff != "" {
				t.Errorf("Labels differ from expected:\n%s", diff)
			}
		})
	}
}
