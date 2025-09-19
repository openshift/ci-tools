package utils

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	coreapi "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes/scheme"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func init() {
	if err := imagev1.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to register imagev1 scheme: %v", err))
	}
}

func TestReimportTag(t *testing.T) {
	var testCases = []struct {
		name                        string
		client                      ctrlruntimeclient.Client
		ns, is, tag, sourcePullSpec string
		expect                      string
		expectedErr                 error
		expectedCount               int
	}{
		{
			name:           "happy path",
			client:         bcc(fakectrlruntimeclient.NewClientBuilder().Build()),
			ns:             "imported",
			is:             "is",
			tag:            "tag",
			sourcePullSpec: "sourcePullSpec",
			expect:         "dockerImageReference",
			expectedCount:  1,
		},
		{
			name:           "imported on the 2nd try",
			client:         bcc(fakectrlruntimeclient.NewClientBuilder().Build()),
			ns:             "imported-2nd",
			is:             "is",
			tag:            "tag",
			sourcePullSpec: "sourcePullSpec",
			expectedCount:  2,
		},
		{
			name:           "timeout",
			client:         bcc(fakectrlruntimeclient.NewClientBuilder().Build()),
			ns:             "timeout",
			is:             "is",
			tag:            "tag",
			sourcePullSpec: "sourcePullSpec",
			expectedErr:    fmt.Errorf("unable to import tag timeout/is:tag even after (3) imports: timed out waiting for the condition"),
			expectedCount:  3,
		},
	}

	for _, testCase := range testCases {
		actual, actualErr := ImportTagWithRetries(context.Background(), testCase.client, testCase.ns, testCase.is, testCase.tag, testCase.sourcePullSpec, 3, nil)
		if diff := cmp.Diff(testCase.expectedErr, actualErr, testhelper.EquateErrorMessage); diff != "" {
			t.Errorf("%s: actualErr does not match expectedErr, diff: %s", testCase.name, diff)
		}
		if diff := cmp.Diff(testCase.expect, actual); diff != "" {
			t.Errorf("%s: actual does not match expected, diff: %s", testCase.name, diff)
		}
		if c, match := testCase.client.(*imageStreamImportStatusSettingClient); match {
			actualCount := c.Count(testCase.ns)
			if diff := cmp.Diff(testCase.expectedCount, actualCount); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", testCase.name, diff)
			}
		}
	}
}

func bcc(upstream ctrlruntimeclient.Client) ctrlruntimeclient.Client {
	c := &imageStreamImportStatusSettingClient{
		Client: upstream,
		count:  map[string]int{},
	}
	return c
}

type imageStreamImportStatusSettingClient struct {
	ctrlruntimeclient.Client
	count map[string]int
}

func (client *imageStreamImportStatusSettingClient) Count(name string) int {
	var ret = 0
	for k, v := range client.count {
		if strings.HasPrefix(k, name) {
			ret = ret + v
		}
	}
	return ret
}

func (client *imageStreamImportStatusSettingClient) Create(ctx context.Context, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.CreateOption) error {
	if asserted, match := obj.(*imagev1.ImageStreamImport); match {
		if _, ok := client.count[asserted.Namespace]; !ok {
			client.count[asserted.Namespace] = 1
		} else {
			client.count[asserted.Namespace] = client.count[asserted.Namespace] + 1
		}
		if asserted.Namespace == "imported" {
			asserted.Status = imagev1.ImageStreamImportStatus{
				Images: []imagev1.ImageImportStatus{
					{
						Image: &imagev1.Image{
							DockerImageReference: "dockerImageReference",
						},
					},
				},
			}
		}
		if asserted.Namespace == "imported-2nd" {
			if client.count[asserted.Namespace] == 2 {
				asserted.Status = imagev1.ImageStreamImportStatus{
					Images: []imagev1.ImageImportStatus{
						{
							Image: &imagev1.Image{},
						},
					},
				}
			}
		}
		if asserted.Namespace == "some error" {
			return errors.New("some error")
		}
	}
	return nil
}

func TestGetEvaluator(t *testing.T) {
	var testCases = []struct {
		name          string
		client        ctrlruntimeclient.Client
		obj           *imagev1.ImageStream
		tags          sets.Set[string]
		expected      bool
		expectedErr   error
		expectedCount int
	}{
		{
			name:   "happy path",
			client: bcc(fakectrlruntimeclient.NewClientBuilder().Build()),
			obj: &imagev1.ImageStream{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "is",
					Namespace: "imported",
				},
				Spec: imagev1.ImageStreamSpec{
					Tags: []imagev1.TagReference{
						{Name: "cli", From: &coreapi.ObjectReference{Kind: "DockerImage", Name: "reg.com/ns/n:t"}},
					},
				},
				Status: imagev1.ImageStreamStatus{
					PublicDockerImageRepository: "registry",
					Tags: []imagev1.NamedTagEventList{
						{
							Tag: "cli",
							Items: []imagev1.TagEvent{
								{
									Image: "some",
								},
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name:   "not imported",
			client: bcc(fakectrlruntimeclient.NewClientBuilder().Build()),
			obj: &imagev1.ImageStream{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "is",
					Namespace: "some",
				},
				Spec: imagev1.ImageStreamSpec{
					Tags: []imagev1.TagReference{
						{Name: "cli"},
					},
				},
			},
			expected: false,
		},
		{
			name:   "reimport with error",
			client: bcc(fakectrlruntimeclient.NewClientBuilder().Build()),
			obj: &imagev1.ImageStream{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "is",
					Namespace: "some error",
				},
				Spec: imagev1.ImageStreamSpec{
					Tags: []imagev1.TagReference{
						{Name: "cli", From: &coreapi.ObjectReference{Kind: "DockerImage", Name: "reg.com/ns/n:t"}},
					},
				},
				Status: imagev1.ImageStreamStatus{
					PublicDockerImageRepository: "registry",
					Tags: []imagev1.NamedTagEventList{
						{
							Tag: "cli",
							Conditions: []imagev1.TagEventCondition{
								{
									Message: "Internal error occurred: a and b",
								},
							},
						},
					},
				},
			},
			expected:      false,
			expectedErr:   fmt.Errorf("failed to reimport the tag some error/is:cli: unable to import tag some error/is:cli at import (0): some error"),
			expectedCount: 1,
		},
		{
			name:   "nil-from error",
			client: bcc(fakectrlruntimeclient.NewClientBuilder().Build()),
			obj: &imagev1.ImageStream{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "is",
					Namespace: "ns",
				},
				Spec: imagev1.ImageStreamSpec{
					Tags: []imagev1.TagReference{
						{Name: "cli"},
					},
				},
				Status: imagev1.ImageStreamStatus{
					PublicDockerImageRepository: "registry",
					Tags: []imagev1.NamedTagEventList{
						{
							Tag: "cli",
							Conditions: []imagev1.TagEventCondition{
								{
									Message: "Internal error occurred: a and b",
								},
							},
						},
					},
				},
			},
			expected:    false,
			expectedErr: fmt.Errorf("failed to determine the source of the tag ns/is:cli"),
		},
		{
			name:   "no-name error",
			client: bcc(fakectrlruntimeclient.NewClientBuilder().Build()),
			obj: &imagev1.ImageStream{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "is",
					Namespace: "ns",
				},
				Spec: imagev1.ImageStreamSpec{
					Tags: []imagev1.TagReference{
						{Name: "cli", From: &coreapi.ObjectReference{Kind: "DockerImage"}},
					},
				},
				Status: imagev1.ImageStreamStatus{
					PublicDockerImageRepository: "registry",
					Tags: []imagev1.NamedTagEventList{
						{
							Tag: "cli",
							Conditions: []imagev1.TagEventCondition{
								{
									Message: "Internal error occurred: a and b",
								},
							},
						},
					},
				},
			},
			expected:    false,
			expectedErr: fmt.Errorf("failed to import tag ns/is:cli from an empty source"),
		},
		{
			name:   "unknown-kind error",
			client: bcc(fakectrlruntimeclient.NewClientBuilder().Build()),
			obj: &imagev1.ImageStream{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "is",
					Namespace: "ns",
				},
				Spec: imagev1.ImageStreamSpec{
					Tags: []imagev1.TagReference{
						{Name: "cli", From: &coreapi.ObjectReference{Kind: "UnknownKind"}},
					},
				},
				Status: imagev1.ImageStreamStatus{
					PublicDockerImageRepository: "registry",
					Tags: []imagev1.NamedTagEventList{
						{
							Tag: "cli",
							Conditions: []imagev1.TagEventCondition{
								{
									Message: "Internal error occurred: a and b",
								},
							},
						},
					},
				},
			},
			expected:    false,
			expectedErr: fmt.Errorf("failed to import tag ns/is:cli from an unexpected tag source {UnknownKind      }"),
		},
		{
			name:   "happy path with 2 tags",
			client: bcc(fakectrlruntimeclient.NewClientBuilder().Build()),
			obj: &imagev1.ImageStream{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "is",
					Namespace: "imported",
				},
				Spec: imagev1.ImageStreamSpec{
					Tags: []imagev1.TagReference{
						{Name: "tag1"},
						{Name: "tag2"},
						{Name: "tag3"},
					},
				},
				Status: imagev1.ImageStreamStatus{
					PublicDockerImageRepository: "registry",
					Tags: []imagev1.NamedTagEventList{
						{
							Tag: "tag1",
							Items: []imagev1.TagEvent{
								{
									Image: "some",
								},
							},
						},
						{
							Tag: "tag3",
							Items: []imagev1.TagEvent{
								{
									Image: "some",
								},
							},
						},
					},
				},
			},
			tags:     sets.New[string]("tag1", "tag3"),
			expected: true,
		},
		{
			name:   "failed with 2 tags",
			client: bcc(fakectrlruntimeclient.NewClientBuilder().Build()),
			obj: &imagev1.ImageStream{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "is",
					Namespace: "imported",
				},
				Spec: imagev1.ImageStreamSpec{
					Tags: []imagev1.TagReference{
						{Name: "tag1"},
						{Name: "tag2"},
						{Name: "tag3"},
					},
				},
				Status: imagev1.ImageStreamStatus{
					PublicDockerImageRepository: "registry",
					Tags: []imagev1.NamedTagEventList{
						{
							Tag: "tag1",
							Items: []imagev1.TagEvent{
								{
									Image: "some",
								},
							},
						},
						{
							Tag: "tag2",
							Items: []imagev1.TagEvent{
								{
									Image: "some",
								},
							},
						},
					},
				},
			},
			tags: sets.New[string]("tag1", "tag3"),
		},
		{
			name:   "failed with 1 tag not in spec",
			client: bcc(fakectrlruntimeclient.NewClientBuilder().Build()),
			obj: &imagev1.ImageStream{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "is",
					Namespace: "imported",
				},
				Spec: imagev1.ImageStreamSpec{
					Tags: []imagev1.TagReference{
						{Name: "tag1"},
						{Name: "tag2"},
						{Name: "tag3"},
					},
				},
				Status: imagev1.ImageStreamStatus{
					PublicDockerImageRepository: "registry",
					Tags: []imagev1.NamedTagEventList{
						{
							Tag: "tag1",
							Items: []imagev1.TagEvent{
								{
									Image: "some",
								},
							},
						},
						{
							Tag: "tag3",
							Items: []imagev1.TagEvent{
								{
									Image: "some",
								},
							},
						},
					},
				},
			},
			tags:        sets.New[string]("tag1", "m-tag1", "m-tag2"),
			expected:    false,
			expectedErr: fmt.Errorf("failed to import tag(s) [m-tag1,m-tag2] on image stream imported/is because of missing definition in the spec"),
		},
	}

	for _, testCase := range testCases {
		e := getEvaluator(context.Background(), testCase.client, testCase.obj.Namespace, testCase.obj.Name, testCase.tags, nil)
		actual, actualErr := e(testCase.obj)
		if diff := cmp.Diff(testCase.expectedErr, actualErr, testhelper.EquateErrorMessage); diff != "" {
			t.Errorf("%s: actualErr does not match expectedErr, diff: %s", testCase.name, diff)
		}
		if actualErr == nil {
			if diff := cmp.Diff(testCase.expected, actual, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", testCase.name, diff)
			}
		}
		if c, match := testCase.client.(*imageStreamImportStatusSettingClient); match {
			actualCount := c.Count(testCase.obj.Namespace)
			if diff := cmp.Diff(testCase.expectedCount, actualCount); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", testCase.name, diff)
			}
		}
	}
}
