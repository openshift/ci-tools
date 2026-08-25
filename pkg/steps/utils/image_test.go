package utils

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes/scheme"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func init() {
	if err := imagev1.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to register imagev1 scheme: %v", err))
	}
}

func TestResolveOfficialInputFrom(t *testing.T) {
	specPull := "quay-proxy.ci.openshift.org/openshift/ci@sha256:abc"
	base := api.ImageStreamTagReference{Namespace: "ocp", Name: "4.22", Tag: "hyperkube"}
	tests := []struct {
		name     string
		base     api.ImageStreamTagReference
		objects  []runtime.Object
		wantOK   bool
		wantFrom *coreapi.ObjectReference
	}{
		{name: "official ocp 5.0", base: api.ImageStreamTagReference{Namespace: "ocp", Name: "5.0", Tag: "cli"}, wantOK: true, wantFrom: &coreapi.ObjectReference{Kind: "DockerImage", Name: api.QuayImageReference(api.ImageStreamTagReference{Namespace: "ocp", Name: "5.0", Tag: "cli"})}},
		{name: "non-official namespace", base: api.ImageStreamTagReference{Namespace: "ci", Name: "5.0", Tag: "cli"}, wantOK: false},
		{
			name: "ocp builder spec quay-proxy digest",
			base: api.ImageStreamTagReference{Namespace: "ocp", Name: "builder", Tag: "rhel-9-golang-1.22-openshift-4.17"},
			objects: []runtime.Object{&imagev1.ImageStream{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ocp", Name: "builder"},
				Spec: imagev1.ImageStreamSpec{Tags: []imagev1.TagReference{{
					Name:      "rhel-9-golang-1.22-openshift-4.17",
					Reference: true,
					From: &coreapi.ObjectReference{
						Kind: "DockerImage",
						Name: "quay-proxy.ci.openshift.org/openshift/ci@sha256:47f4267a177f47b7a1cf44d652a452d668ee1fc72ed0490560db4292449eebfe",
					},
				}}},
				Status: imagev1.ImageStreamStatus{Tags: []imagev1.NamedTagEventList{{
					Tag: "rhel-9-golang-1.22-openshift-4.17",
					Items: []imagev1.TagEvent{{
						DockerImageReference: "quay-proxy.ci.openshift.org/openshift/ci@sha256:47f4267a177f47b7a1cf44d652a452d668ee1fc72ed0490560db4292449eebfe",
					}},
				}}},
			}},
			wantOK: true,
			wantFrom: &coreapi.ObjectReference{
				Kind: "DockerImage",
				Name: "quay-proxy.ci.openshift.org/openshift/ci@sha256:47f4267a177f47b7a1cf44d652a452d668ee1fc72ed0490560db4292449eebfe",
			},
		},
		{
			name: "spec docker",
			base: base,
			objects: []runtime.Object{&imagev1.ImageStream{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ocp", Name: "4.22"},
				Spec: imagev1.ImageStreamSpec{Tags: []imagev1.TagReference{{
					Name: "hyperkube",
					From: &coreapi.ObjectReference{Kind: "DockerImage", Name: specPull},
				}}},
			}},
			wantOK:   true,
			wantFrom: &coreapi.ObjectReference{Kind: "DockerImage", Name: specPull},
		},
		{
			name: "spec image stream image",
			base: api.ImageStreamTagReference{Namespace: "ocp", Name: "4.22", Tag: "cli"},
			objects: []runtime.Object{&imagev1.ImageStream{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ocp", Name: "4.22"},
				Spec: imagev1.ImageStreamSpec{Tags: []imagev1.TagReference{{
					Name: "cli",
					From: &coreapi.ObjectReference{Kind: "ImageStreamImage", Name: "4.22@sha256:deadbeef", Namespace: "ocp"},
				}}},
			}},
			wantOK:   true,
			wantFrom: &coreapi.ObjectReference{Kind: "ImageStreamImage", Name: "4.22@sha256:deadbeef", Namespace: "ocp"},
		},
		{
			name:     "quay fallback",
			base:     base,
			wantOK:   true,
			wantFrom: &coreapi.ObjectReference{Kind: "DockerImage", Name: api.QuayImageReference(base)},
		},
		{
			name: "skip stale registry.ci ocp spec",
			base: api.ImageStreamTagReference{Namespace: "ocp", Name: "4.16", Tag: "base-rhel9"},
			objects: []runtime.Object{&imagev1.ImageStream{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ocp", Name: "4.16"},
				Spec: imagev1.ImageStreamSpec{Tags: []imagev1.TagReference{{
					Name: "base-rhel9",
					From: &coreapi.ObjectReference{Kind: "DockerImage", Name: "registry.ci.openshift.org/ocp/4.16@sha256:dead"},
				}}},
			}},
			wantOK:   true,
			wantFrom: &coreapi.ObjectReference{Kind: "DockerImage", Name: api.QuayImageReference(api.ImageStreamTagReference{Namespace: "ocp", Name: "4.16", Tag: "base-rhel9"})},
		},
		{
			name: "stable stream in job namespace",
			base: api.ImageStreamTagReference{Namespace: "ocp", Name: api.StableImageStream, Tag: "cli"},
			objects: []runtime.Object{&imagev1.ImageStream{
				ObjectMeta: metav1.ObjectMeta{Namespace: "job-ns", Name: api.StableImageStream},
				Spec:       imagev1.ImageStreamSpec{Tags: []imagev1.TagReference{{Name: "cli"}}},
				Status: imagev1.ImageStreamStatus{
					PublicDockerImageRepository: "registry/job-ns/stable",
					Tags:                        []imagev1.NamedTagEventList{{Tag: "cli", Items: []imagev1.TagEvent{{Image: "sha256:1111"}}}},
				},
			}},
			wantOK:   true,
			wantFrom: &coreapi.ObjectReference{Kind: "ImageStreamTag", Name: "stable:cli", Namespace: "job-ns"},
		},
		{
			name: "versioned ocp stream not redirected to job stable",
			base: api.ImageStreamTagReference{Namespace: "ocp", Name: "5.0", Tag: "cli"},
			objects: []runtime.Object{&imagev1.ImageStream{
				ObjectMeta: metav1.ObjectMeta{Namespace: "job-ns", Name: api.StableImageStream},
				Spec:       imagev1.ImageStreamSpec{Tags: []imagev1.TagReference{{Name: "cli"}}},
				Status: imagev1.ImageStreamStatus{
					PublicDockerImageRepository: "registry/job-ns/stable",
					Tags:                        []imagev1.NamedTagEventList{{Tag: "cli", Items: []imagev1.TagEvent{{Image: "sha256:1111"}}}},
				},
			}},
			wantOK:   true,
			wantFrom: &coreapi.ObjectReference{Kind: "DockerImage", Name: api.QuayImageReference(api.ImageStreamTagReference{Namespace: "ocp", Name: "5.0", Tag: "cli"})},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fakectrlruntimeclient.NewClientBuilder().WithScheme(scheme.Scheme).WithRuntimeObjects(tt.objects...).Build()
			from, ok, err := ResolveOfficialInputFrom(context.Background(), client, "job-ns", tt.base)
			if err != nil {
				t.Fatalf("ResolveOfficialInputFrom() error = %v", err)
			}
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if diff := cmp.Diff(tt.wantFrom, from); diff != "" {
				t.Fatalf("from mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestPullSpecForImageStreamTag(t *testing.T) {
	specDigest := "quay-proxy.ci.openshift.org/openshift/ci@sha256:47f4267a177f47b7a1cf44d652a452d668ee1fc72ed0490560db4292449eebfe"
	is := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ocp", Name: "builder"},
		Spec: imagev1.ImageStreamSpec{Tags: []imagev1.TagReference{{
			Name:      "rhel-9-golang-1.22-openshift-4.17",
			Reference: true,
			From:      &coreapi.ObjectReference{Kind: "DockerImage", Name: specDigest},
		}}},
		Status: imagev1.ImageStreamStatus{Tags: []imagev1.NamedTagEventList{{
			Tag:   "rhel-9-golang-1.22-openshift-4.17",
			Items: []imagev1.TagEvent{{DockerImageReference: specDigest}},
		}}},
	}
	tests := []struct {
		name  string
		isTag *imagev1.ImageStreamTag
		want  string
	}{
		{
			name: "reference tag",
			isTag: &imagev1.ImageStreamTag{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ocp", Name: "builder:rhel-9-golang-1.22-openshift-4.17"},
			},
			want: specDigest,
		},
		{
			name: "stale local image",
			isTag: &imagev1.ImageStreamTag{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ocp", Name: "builder:rhel-9-golang-1.22-openshift-4.17"},
				Image:      imagev1.Image{ObjectMeta: metav1.ObjectMeta{Name: "sha256:950393761142fa66698e9ba1d679643c88194d78a99308aa814fef6de92a8bfe"}},
			},
			want: specDigest,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, PullSpecForImageStreamTag("registry.ci.openshift.org", is, tt.isTag)); diff != "" {
				t.Fatalf("pull spec mismatch (-want +got):\n%s", diff)
			}
		})
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

func TestImportTagWithRetryDelaysRecoversAfterVirtualTwoMinuteOutage(t *testing.T) {
	const namespace = "release-import"
	var elapsed time.Duration
	client := &outageImageImportClient{
		Client: fakectrlruntimeclient.NewClientBuilder().Build(),
		now:    func() time.Duration { return elapsed },
	}
	delays := exponentialImageImportRetryDelays(9)
	var logs bytes.Buffer
	logger := logrus.StandardLogger()
	originalOutput := logger.Out
	logger.SetOutput(&logs)
	defer logger.SetOutput(originalOutput)

	pullSpec, err := importTagWithRetryDelays(context.Background(), client, namespace, "release", "latest", "quay.io/openshift/release:latest", 9, delays, func(_ context.Context, delay time.Duration) error {
		elapsed += delay
		return nil
	}, true, nil)
	if err != nil {
		t.Fatalf("expected import to recover: %v", err)
	}
	if pullSpec != "quay.io/openshift/release@sha256:resolved" {
		t.Fatalf("unexpected resolved pull spec %q", pullSpec)
	}
	if client.attempts != 8 {
		t.Fatalf("expected 8 import attempts, got %d", client.attempts)
	}
	if elapsed != 127*time.Second {
		t.Fatalf("expected virtual recovery after 127s, got %s", elapsed)
	}
	if !strings.Contains(logs.String(), "Image stream import did not succeed, retrying") {
		t.Fatalf("expected retry log evidence, got %q", logs.String())
	}
}

func TestImportTagWithRetryDelaysDoesNotRetryPermanentErrors(t *testing.T) {
	permanentErr := errors.New("malformed source")
	client := &outageImageImportClient{
		Client:       fakectrlruntimeclient.NewClientBuilder().Build(),
		permanentErr: permanentErr,
	}
	_, err := ImportTagWithRetryDelays(context.Background(), client, "ns", "release", "latest", "not a valid source", exponentialImageImportRetryDelays(9), nil)
	if !errors.Is(err, permanentErr) {
		t.Fatalf("expected permanent error, got %v", err)
	}
	if client.attempts != 1 {
		t.Fatalf("expected permanent error after one attempt, got %d", client.attempts)
	}
}

type outageImageImportClient struct {
	ctrlruntimeclient.Client
	now          func() time.Duration
	permanentErr error
	attempts     int
}

func (c *outageImageImportClient) Create(ctx context.Context, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.CreateOption) error {
	streamImport, ok := obj.(*imagev1.ImageStreamImport)
	if !ok {
		return c.Client.Create(ctx, obj, opts...)
	}
	c.attempts++
	if c.permanentErr != nil {
		return c.permanentErr
	}
	if c.now() >= 2*time.Minute {
		streamImport.Status.Images = []imagev1.ImageImportStatus{{Image: &imagev1.Image{DockerImageReference: "quay.io/openshift/release@sha256:resolved"}}}
	}
	return nil
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
		name            string
		client          ctrlruntimeclient.Client
		obj             *imagev1.ImageStream
		tags            sets.Set[string]
		waitForSpecTags bool
		expected        bool
		expectedErr     error
		expectedCount   int
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
			name:   "transient reimport failure keeps waiting",
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
		{
			name:   "wait for requested tag not yet visible in spec",
			client: bcc(fakectrlruntimeclient.NewClientBuilder().Build()),
			obj: &imagev1.ImageStream{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "is",
					Namespace: "imported",
				},
				Spec: imagev1.ImageStreamSpec{
					Tags: []imagev1.TagReference{{Name: "tag1"}},
				},
				Status: imagev1.ImageStreamStatus{
					PublicDockerImageRepository: "registry",
					Tags: []imagev1.NamedTagEventList{{
						Tag:   "tag1",
						Items: []imagev1.TagEvent{{Image: "some"}},
					}},
				},
			},
			tags:            sets.New[string]("tag1", "cli"),
			waitForSpecTags: true,
			expected:        false,
		},
	}

	for _, testCase := range testCases {
		e := getEvaluator(context.Background(), testCase.client, testCase.obj.Namespace, testCase.obj.Name, testCase.tags, testCase.waitForSpecTags, nil)
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

func TestWaitForImportingISTagSpecTimeout(t *testing.T) {
	const (
		namespace  = "test-namespace"
		streamName = "stable"
	)
	client := fakectrlruntimeclient.NewClientBuilder().WithScheme(scheme.Scheme).WithRuntimeObjects(&imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: streamName},
	}).Build()

	err := WaitForImportingISTag(context.Background(), client, namespace, streamName, nil, sets.New("cli"), 100*time.Millisecond, nil, WaitForSpecTags())
	if err == nil {
		t.Fatal("expected timeout while the requested tag remains absent from the spec")
	}
	if !strings.Contains(err.Error(), "timed out waiting for the condition") {
		t.Fatalf("expected bounded wait timeout, got: %v", err)
	}
}

func TestImportEvaluatorContinuesAfterTransientReimportFailure(t *testing.T) {
	const (
		namespace  = "test-namespace"
		streamName = "stable"
	)
	from := &coreapi.ObjectReference{Kind: "DockerImage", Name: "quay.io/openshift/release:latest"}
	failing := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: streamName},
		Spec: imagev1.ImageStreamSpec{Tags: []imagev1.TagReference{{
			Name: "cli",
			From: from,
		}}},
		Status: imagev1.ImageStreamStatus{Tags: []imagev1.NamedTagEventList{{
			Tag: "cli",
			Conditions: []imagev1.TagEventCondition{{
				Message: "Internal error occurred: registry unavailable",
			}},
		}}},
	}
	recovered := failing.DeepCopy()
	recovered.Status.PublicDockerImageRepository = "registry.example.com/test-namespace/stable"
	recovered.Status.Tags = []imagev1.NamedTagEventList{{
		Tag: "cli",
		Items: []imagev1.TagEvent{{
			Image:                "sha256:resolved",
			DockerImageReference: "quay.io/openshift/release@sha256:resolved",
		}},
	}}
	client := &transientReimportClient{
		Client: fakectrlruntimeclient.NewClientBuilder().WithScheme(scheme.Scheme).WithRuntimeObjects(failing).Build(),
	}
	var logs bytes.Buffer
	logger := logrus.StandardLogger()
	originalOutput := logger.Out
	logger.SetOutput(&logs)
	defer logger.SetOutput(originalOutput)

	evaluate := getEvaluator(context.Background(), client, namespace, streamName, sets.New("cli"), false, nil)
	done, err := evaluate(failing)
	if err != nil {
		t.Fatalf("expected transient failure to remain retryable: %v", err)
	}
	if done {
		t.Fatal("expected evaluator to continue polling after transient reimport failure")
	}
	if client.importAttempts != 1 {
		t.Fatalf("expected one failed reimport attempt, got %d", client.importAttempts)
	}
	if !strings.Contains(logs.String(), "continuing to wait") {
		t.Fatalf("expected transient reimport warning, got %q", logs.String())
	}
	done, err = evaluate(recovered)
	if err != nil {
		t.Fatalf("expected recovered import to succeed: %v", err)
	}
	if !done {
		t.Fatal("expected evaluator to finish after the import recovered")
	}
}

type transientReimportClient struct {
	ctrlruntimeclient.Client
	importAttempts int
}

func (c *transientReimportClient) Create(ctx context.Context, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.CreateOption) error {
	if _, ok := obj.(*imagev1.ImageStreamImport); ok {
		c.importAttempts++
		return errors.New("registry unavailable")
	}
	return c.Client.Create(ctx, obj, opts...)
}

func TestImageDigestForSpecTagWithoutFrom(t *testing.T) {
	is := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "pipeline"},
		Spec: imagev1.ImageStreamSpec{
			Tags: []imagev1.TagReference{{Name: "pending"}},
		},
		Status: imagev1.ImageStreamStatus{
			PublicDockerImageRepository: "registry/ns/pipeline",
		},
	}
	client := fakectrlruntimeclient.NewClientBuilder().WithScheme(scheme.Scheme).WithRuntimeObjects(is).Build()
	got, err := ImageDigestFor(client, func() string { return "ns" }, "pipeline", "pending")()
	if err != nil {
		t.Fatalf("ImageDigestFor() error = %v", err)
	}
	if got != "registry/ns/pipeline:pending" {
		t.Fatalf("ImageDigestFor() = %q, want %q", got, "registry/ns/pipeline:pending")
	}
}

func TestImageDigestForMissingTag(t *testing.T) {
	is := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "pipeline"},
		Status: imagev1.ImageStreamStatus{
			PublicDockerImageRepository: "registry/ns/pipeline",
		},
	}
	client := fakectrlruntimeclient.NewClientBuilder().WithScheme(scheme.Scheme).WithRuntimeObjects(is).Build()
	_, err := ImageDigestFor(client, func() string { return "ns" }, "pipeline", "missing")()
	if err == nil {
		t.Fatal("ImageDigestFor() expected error for missing tag")
	}
}
