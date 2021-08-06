package main

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/google/go-cmp/cmp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/test-infra/prow/git/localgit"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func init() {
	if err := imagev1.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to register imagev1 scheme: %v", err))
	}
}

func TestTagsToDelete(t *testing.T) {
	ocp48Stream := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "4.8",
			Namespace: "ocp",
		},
		Status: imagev1.ImageStreamStatus{
			Tags: []imagev1.NamedTagEventList{
				{Tag: "some-component"},
				{Tag: "machine-os-content"},
				{Tag: "bar"},
			},
		},
	}

	origin48Stream := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "4.8",
			Namespace: "origin",
		},
		Status: imagev1.ImageStreamStatus{
			Tags: []imagev1.NamedTagEventList{
				{Tag: "some-component"},
				{Tag: "bar"},
				{Tag: "machine-os-content"},
				{Tag: "not-mirror-from-ocp"},
			},
		},
	}

	ciSomeStream := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "some-tool",
			Namespace: "ci",
		},
		Status: imagev1.ImageStreamStatus{
			Tags: []imagev1.NamedTagEventList{
				{Tag: "latest"},
				{Tag: "test"},
			},
		},
	}

	testCases := []struct {
		name            string
		client          ctrlruntimeclient.Client
		promotedTags    map[api.ImageStreamTagReference]api.Metadata
		toIgnore        []*regexp.Regexp
		imageStreamRefs []ImageStreamRef
		expected        map[api.ImageStreamTagReference]interface{}
		expectedError   error
	}{
		{
			name:   "basic case",
			client: fakeclient.NewFakeClient(ocp48Stream.DeepCopy(), ciSomeStream.DeepCopy(), origin48Stream.DeepCopy()),
			promotedTags: map[api.ImageStreamTagReference]api.Metadata{
				{
					Namespace: "ci",
					Name:      "some-tool",
					Tag:       "latest",
				}: {},
				{
					Namespace: "ocp",
					Name:      "4.8",
					Tag:       "some-component",
				}: {},
				{
					Namespace: "origin",
					Name:      "4.8",
					Tag:       "not-mirror-from-ocp",
				}: {},
			},
			toIgnore: []*regexp.Regexp{
				regexp.MustCompile(`^ocp/\S+:machine-os-content$`),
			},
			imageStreamRefs: []ImageStreamRef{
				{
					Namespace:   "origin",
					Name:        "4.8",
					ExcludeTags: []string{"machine-os-content"},
				},
			},
			expected: map[api.ImageStreamTagReference]interface{}{
				{
					Namespace: "ci",
					Name:      "some-tool",
					Tag:       "test",
				}: nil,
				{
					Namespace: "ocp",
					Name:      "4.8",
					Tag:       "bar",
				}: nil,
				{
					Namespace: "origin",
					Name:      "4.8",
					Tag:       "machine-os-content",
				}: nil,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual, actualError := tagsToDelete(context.TODO(), tc.client, tc.promotedTags, tc.toIgnore, tc.imageStreamRefs)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expectedError, actualError, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
		})
	}
}

func TestGenerateMappings(t *testing.T) {
	testCases := []struct {
		name            string
		promotedTags    map[api.ImageStreamTagReference]api.Metadata
		mappingConfig   *OpenshiftMappingConfig
		imageStreamRefs []ImageStreamRef
		expected        map[string]map[string][]string
	}{
		{
			name: "basic case",
			promotedTags: map[api.ImageStreamTagReference]api.Metadata{
				{
					Namespace: "ci",
					Name:      "some-tool",
					Tag:       "latest",
				}: {},
				{
					Namespace: "origin",
					Name:      "4.8",
					Tag:       "bar",
				}: {},
				{
					Namespace: "origin",
					Name:      "4.8",
					Tag:       "foo",
				}: {},
				{
					Namespace: "origin",
					Name:      "4.9",
					Tag:       "bar",
				}: {},
				{
					Namespace: "origin",
					Name:      "4.9",
					Tag:       "some",
				}: {},
				{
					Namespace: "ocp",
					Name:      "4.8",
					Tag:       "ocp-some",
				}: {},
				{
					Namespace: "ocp",
					Name:      "4.8",
					Tag:       "ironic-ipa-downloader",
				}: {},
			},
			mappingConfig: &OpenshiftMappingConfig{
				SourceNamespace: "origin",
				TargetNamespace: "openshift",
				SourceRegistry:  "registry.ci.openshift.org",
				TargetRegistry:  "quay.io",
				Images: map[string][]string{
					"4.8": {"4.8", "4.8.0"},
					"4.9": {"4.9", "4.9.0", "latest"},
				},
			},
			imageStreamRefs: []ImageStreamRef{
				{
					Namespace:   "origin",
					Name:        "4.8",
					ExcludeTags: []string{"ironic-ipa-downloader"},
				},
			},
			expected: map[string]map[string][]string{
				"mapping_origin_4_8": {
					"registry.ci.openshift.org/origin/4.8:bar": {"quay.io/openshift/origin-bar:4.8", "quay.io/openshift/origin-bar:4.8.0"},
					"registry.ci.openshift.org/origin/4.8:foo": {"quay.io/openshift/origin-foo:4.8", "quay.io/openshift/origin-foo:4.8.0"},
					"registry.ci.openshift.org/origin/4.8:ocp-some": {
						"quay.io/openshift/origin-ocp-some:4.8",
						"quay.io/openshift/origin-ocp-some:4.8.0",
					},
				},
				"mapping_origin_4_9": {
					"registry.ci.openshift.org/origin/4.9:bar": {
						"quay.io/openshift/origin-bar:4.9",
						"quay.io/openshift/origin-bar:4.9.0",
						"quay.io/openshift/origin-bar:latest",
					},
					"registry.ci.openshift.org/origin/4.9:some": {
						"quay.io/openshift/origin-some:4.9",
						"quay.io/openshift/origin-some:4.9.0",
						"quay.io/openshift/origin-some:latest",
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := generateMappings(tc.promotedTags, tc.mappingConfig, tc.imageStreamRefs)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
		})
	}
}

func init() {
	if err := imagev1.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to register imagev1 scheme: %v", err))
	}
}

type fakeRefGetter struct {
	ref string
	err error
}

func (f *fakeRefGetter) GetRef(_, _, _ string) (string, error) {
	return f.ref, f.err
}

func TestCheckImageStreamTags(t *testing.T) {

	ciSomeToolLatestISTag := &imagev1.ImageStreamTag{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ci",
			Name:      "some-tool:latest",
		},
		Image: imagev1.Image{
			DockerImageMetadata: runtime.RawExtension{
				Raw: []byte(`{
  "Architecture": "amd64",
  "Config": {
    "Cmd": [
      "/bin/bash"
    ],
    "Env": [
      "foo=bar",
      "OPENSHIFT_BUILD_NAME=cluster-openshift-apiserver-operator",
      "OPENSHIFT_BUILD_NAMESPACE=ci-op-q19q0441",
      "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
      "container=oci"
    ],
    "Hostname": "1a6370b10892",
    "Labels": {
      "architecture": "x86_64",
      "authoritative-source-url": "registry.access.redhat.com",
      "build-date": "2020-03-10T10:38:13.657446",
      "com.redhat.build-host": "cpt-1004.osbs.prod.upshift.rdu2.redhat.com",
      "com.redhat.component": "ubi7-container",
      "com.redhat.license_terms": "https://www.redhat.com/en/about/red-hat-end-user-license-agreements#UBI",
      "description": "The Universal Base Image is designed and engineered to be the base layer for all of your containerized applications, middleware and utilities. This base image is freely redistributable, but Red Hat only supports Red Hat technologies through subscriptions for Red Hat products. This image is maintained by Red Hat and updated regularly.",
      "distribution-scope": "public",
      "io.k8s.description": "This is the base image from which all OpenShift images inherit.",
      "io.k8s.display-name": "OpenShift Base",
      "io.openshift.build.commit.author": "",
      "io.openshift.build.commit.date": "",
      "io.openshift.build.commit.id": "ist-commit",
      "io.openshift.build.commit.message": "",
      "io.openshift.build.commit.ref": "master",
      "io.openshift.build.name": "",
      "io.openshift.build.namespace": "",
      "io.openshift.build.source-context-dir": "",
      "io.openshift.build.source-location": "https://github.com/openshift/cluster-openshift-apiserver-operator",
      "io.openshift.release.operator": "true",
      "io.openshift.tags": "base rhel7",
      "maintainer": "Red Hat, Inc.",
      "name": "ubi7",
      "release": "358",
      "summary": "Provides the latest release of the Red Hat Universal Base Image 7.",
      "url": "https://access.redhat.com/containers/#/registry.access.redhat.com/ubi7/images/7.7-358",
      "vcs-ref": "96d6c74347445e0687267165a1a7d8f2c98dd3a1",
      "vcs-type": "git",
      "vcs-url": "https://github.com/openshift/cluster-openshift-apiserver-operator",
      "vendor": "Red Hat, Inc.",
      "version": "7.7"
    }
  },
  "Container": "6bd17be0fb0bb25b80f46d98b276b30a9db8a8363509c03a0f68d337a15fde16",
  "ContainerConfig": {
    "Entrypoint": [
      "/bin/sh",
      "-c",
      "#(imagebuilder)"
    ],
    "Env": [
      "foo=bar",
      "OPENSHIFT_BUILD_NAME=base",
      "OPENSHIFT_BUILD_NAMESPACE=ci-op-1t3559hx",
      "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
      "container=oci"
    ],
    "Hostname": "6bd17be0fb0b",
    "Image": "docker-registry.default.svc:5000/ci-op-q19q0441/pipeline@sha256:3823c45b9feec9e861d1fd7ef268e38717b3b94adae26dd76eba3ac5f8b8cab9",
    "Labels": {
      "architecture": "x86_64",
      "authoritative-source-url": "registry.access.redhat.com",
      "build-date": "2020-03-10T10:38:13.657446",
      "com.redhat.build-host": "cpt-1004.osbs.prod.upshift.rdu2.redhat.com",
      "com.redhat.component": "ubi7-container",
      "com.redhat.license_terms": "https://www.redhat.com/en/about/red-hat-end-user-license-agreements#UBI",
      "description": "The Universal Base Image is designed and engineered to be the base layer for all of your containerized applications, middleware and utilities. This base image is freely redistributable, but Red Hat only supports Red Hat technologies through subscriptions for Red Hat products. This image is maintained by Red Hat and updated regularly.",
      "distribution-scope": "public",
      "io.k8s.description": "This is the base image from which all OpenShift images inherit.",
      "io.k8s.display-name": "OpenShift Base",
      "io.openshift.build.commit.author": "",
      "io.openshift.build.commit.date": "",
      "io.openshift.build.commit.id": "6cdb5d360768d8f87a615286180e46784ae7d28f",
      "io.openshift.build.commit.message": "",
      "io.openshift.build.commit.ref": "master",
      "io.openshift.build.name": "",
      "io.openshift.build.namespace": "",
      "io.openshift.build.source-context-dir": "base/",
      "io.openshift.build.source-location": "https://github.com/openshift/images",
      "io.openshift.tags": "base rhel7",
      "maintainer": "Red Hat, Inc.",
      "name": "ubi7",
      "release": "358",
      "summary": "Provides the latest release of the Red Hat Universal Base Image 7.",
      "url": "https://access.redhat.com/containers/#/registry.access.redhat.com/ubi7/images/7.7-358",
      "vcs-ref": "6cdb5d360768d8f87a615286180e46784ae7d28f",
      "vcs-type": "git",
      "vcs-url": "https://github.com/openshift/images",
      "vendor": "Red Hat, Inc.",
      "version": "7.7"
    }
  },
  "Created": "2020-04-15T21:42:11Z",
  "DockerVersion": "1.13.1",
  "Identifier": "sha256:b30dd86077b7f7e70ec31d06cf51f0394ccab4b85d0abbaea80f1bbb71ef2fe9",
  "Size": 113678077,
  "apiVersion": "1.0",
  "kind": "DockerImage"
}
`),
			},
		},
	}

	testCases := []struct {
		name         string
		promotedTags map[api.ImageStreamTagReference]api.Metadata
		client       ctrlruntimeclient.Client
		refGetter    refGetter
		expected     []error
	}{
		{
			name: "basic case",
			promotedTags: map[api.ImageStreamTagReference]api.Metadata{
				{
					Namespace: "ci",
					Name:      "some-tool",
					Tag:       "latest",
				}: {},
				{
					Namespace: "origin",
					Name:      "4.8",
					Tag:       "bar",
				}: {},
			},
			client:    fakeclient.NewClientBuilder().WithObjects(ciSomeToolLatestISTag.DeepCopy()).Build(),
			refGetter: &fakeRefGetter{ref: "ist-commit", err: nil},
		},
		{
			name: "commit is wrong",
			promotedTags: map[api.ImageStreamTagReference]api.Metadata{
				{
					Namespace: "ci",
					Name:      "some-tool",
					Tag:       "latest",
				}: {
					Org:    "openshift",
					Repo:   "ci-tools",
					Branch: "master",
				},
			},
			client:    fakeclient.NewClientBuilder().WithObjects(ciSomeToolLatestISTag.DeepCopy()).Build(),
			refGetter: &fakeRefGetter{ref: "not-ist-commit", err: nil},
			expected:  []error{fmt.Errorf("the isTag some-tool:latest in namespace ci is not built from the head not-ist-commit of openshift/ci-tools/master")},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := checkImageStreamTags(context.TODO(), tc.client, tc.promotedTags, tc.refGetter)
			if diff := cmp.Diff(tc.expected, actual, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
		})
	}
}

func TestGitRefGetter(t *testing.T) {
	localgit, clients, err := localgit.New()
	if err != nil {
		t.Fatalf("failed to create localgit: %v", err)
	}
	defer func() {
		if err := localgit.Clean(); err != nil {
			t.Errorf("localgit cleanup failed: %v", err)
		}
	}()

	testCases := []struct {
		name          string
		org           string
		repo          string
		branch        string
		expected      string
		expectedError error
	}{
		{
			name:   "basic case",
			org:    "org",
			repo:   "repo",
			branch: "branch",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if err := localgit.MakeFakeRepo(tc.org, tc.repo); err != nil {
				t.Fatalf("makeFakeRepo: %v", err)
			}
			if err := localgit.CheckoutNewBranch(tc.org, tc.repo, tc.branch); err != nil {
				t.Fatalf("CheckoutNewBranch: %v", err)
			}
			rc, err := clients.ClientFor(tc.org, tc.repo)
			if err != nil {
				t.Fatalf("ClientFor: %v", err)
			}
			expected, err := rc.ShowRef(tc.branch)
			if err != nil {
				t.Fatalf("ShowRef: %v", err)
			}

			refGetter := gitRefGetter{clone: clients.ClientFor}
			actual, actualError := refGetter.GetRef(tc.org, tc.repo, tc.branch)
			if diff := cmp.Diff(expected, actual); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
			if diff := cmp.Diff(tc.expectedError, actualError, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
		})
	}
}
