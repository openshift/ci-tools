package promotionreconciler

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/prow/pkg/github"
	"sigs.k8s.io/yaml"

	imagev1 "github.com/openshift/api/image/v1"

	cioperatorapi "github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/controller/promotionreconciler/prowjobreconciler"
	controllerutil "github.com/openshift/ci-tools/pkg/controller/util"
	"github.com/openshift/ci-tools/pkg/load/agents"
	"github.com/openshift/ci-tools/pkg/testhelper"
)

func init() {
	if err := imagev1.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to register imagev1 scheme: %v", err))
	}
}

func TestCommitForIST(t *testing.T) {
	testCases := []struct {
		name           string
		srcFile        string
		expectedCommit string
	}{
		{
			name:           "normal",
			srcFile:        "testdata/imagestreamtag.yaml",
			expectedCommit: "96d6c74347445e0687267165a1a7d8f2c98dd3a1",
		},
		{
			name:           "source location has .git suffix",
			srcFile:        "testdata/ist_with_git_suffix.yaml",
			expectedCommit: "71e03eafe37b34af3768c8fcae077885d29e16f7",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rawImageStreamTag, err := os.ReadFile(tc.srcFile)
			if err != nil {
				t.Fatalf("failed to read imagestreamtag fixture: %v", err)
			}
			ist := &imagev1.ImageStreamTag{}
			if err := yaml.Unmarshal(rawImageStreamTag, ist); err != nil {
				t.Fatalf("failed to unmarshal imagestreamTag: %v", err)
			}
			commit, err := commitForIST(ist, fakectrlruntimeclient.NewClientBuilder().Build())
			if err != nil {
				t.Fatalf("failed to get ref for ist: %v", err)
			}
			if commit != tc.expectedCommit {
				t.Errorf("expected commit to be %s , was %q", tc.expectedCommit, commit)
			}
		})
	}
}

type fakeGithubClient struct {
	getGef func(string, string, string) (string, error)
}

func (fghc fakeGithubClient) GetRef(org, repo, ref string) (string, error) {
	return fghc.getGef(org, repo, ref)
}

func TestReconcile(t *testing.T) {
	t.Parallel()
	const (
		commitOnIST = "ist-commit"
		ciOPOrg     = "ci-op-org"
		ciOpRepo    = "ci-op-repo"
		ciOpBranch  = "ci-op-branch"
	)
	testCases := []struct {
		name              string
		githubClient      func(owner, repo, ref string) (string, error)
		promotionDisabled bool
		verify            func(error, *prowjobreconciler.OrgRepoBranchCommit) error
	}{
		{
			name:         "404 getting commit for IST returns terminal error",
			githubClient: func(_, _, _ string) (string, error) { return "", fmt.Errorf("wrapped: %w", github.NewNotFound()) },
			verify: func(e error, _ *prowjobreconciler.OrgRepoBranchCommit) error {
				if !controllerutil.IsTerminal(e) {
					return fmt.Errorf("expected to get terminal error, got %w", e)
				}
				return nil
			},
		},
		{
			name:         "404 does not happen on an old tag",
			githubClient: func(_, _, _ string) (string, error) { return "", fmt.Errorf("wrapped: %w", github.NewNotFound()) },
			verify: func(e error, _ *prowjobreconciler.OrgRepoBranchCommit) error {
				if e != nil {
					return fmt.Errorf("unexpected error: %w", e)
				}
				return nil
			},
		},
		{
			name: "ErrTooManyRefs getting commit for IST returns terminal error",
			githubClient: func(_, _, _ string) (string, error) {
				return "", fmt.Errorf("wrapped: %w", github.GetRefTooManyResultsError{})
			},
			verify: func(e error, _ *prowjobreconciler.OrgRepoBranchCommit) error {
				if !controllerutil.IsTerminal(e) {
					return fmt.Errorf("expected to get terminal error, got %w", e)
				}
				return nil
			},
		},
		{
			name:         "IST up to date, nothing to do",
			githubClient: func(_, _, _ string) (string, error) { return commitOnIST, nil },
			verify: func(e error, req *prowjobreconciler.OrgRepoBranchCommit) error {
				if e != nil {
					return fmt.Errorf("expected error to be nil, was %w", e)
				}
				if req != nil {
					return fmt.Errorf("expected to not get a prowjob creation request, got %v", req)
				}
				return nil
			},
		},
		{
			name:              "Ist outdated, promotion disabled, no prowjob created",
			githubClient:      func(_, _, _ string) (string, error) { return "newer", nil },
			promotionDisabled: true,
			verify: func(e error, req *prowjobreconciler.OrgRepoBranchCommit) error {
				if e != nil {
					return fmt.Errorf("expected error to be nil, was %w", e)
				}
				if req != nil {
					return fmt.Errorf("expected no request, got %v", req)
				}
				return nil
			},
		},
		{
			name:         "Ist outdated, prowjob created",
			githubClient: func(_, _, _ string) (string, error) { return "newer", nil },
			verify: func(e error, req *prowjobreconciler.OrgRepoBranchCommit) error {
				if e != nil {
					return fmt.Errorf("expected error to be nil, was %w", e)
				}
				if req == nil {
					return errors.New("expected to get request, was nil")
				}
				expected := &prowjobreconciler.OrgRepoBranchCommit{
					Org:    ciOPOrg,
					Repo:   ciOpRepo,
					Branch: ciOpBranch,
					Commit: "newer",
				}
				if diff := cmp.Diff(req, expected); diff != "" {
					return fmt.Errorf("req differs from expected: %s", diff)
				}
				return nil
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			imageStreamTag := &imagev1.ImageStreamTag{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:         "namespace",
					Name:              "name:tag",
					CreationTimestamp: metav1.NewTime(time.Now()),
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

			var req *prowjobreconciler.OrgRepoBranchCommit

			client := fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(imageStreamTag).Build()
			since := 180 * 24 * time.Hour
			if tc.name == "404 does not happen on an old tag" {
				client = fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(&imagev1.ImageStreamTag{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:         "namespace",
						Name:              "name:tag",
						CreationTimestamp: metav1.NewTime(time.Now().Add(-(since + time.Hour))),
					},
				}).Build()
			}
			r := &reconciler{
				log:    logrus.NewEntry(logrus.New()),
				client: client,
				releaseBuildConfigs: func(_ string) ([]*cioperatorapi.ReleaseBuildConfiguration, error) {
					return []*cioperatorapi.ReleaseBuildConfiguration{{
						Metadata: cioperatorapi.Metadata{
							Org:    ciOPOrg,
							Repo:   ciOpRepo,
							Branch: ciOpBranch,
						},
						PromotionConfiguration: &cioperatorapi.PromotionConfiguration{
							Targets: []cioperatorapi.PromotionTarget{{
								Namespace:        "namespace",
								Name:             "name",
								AdditionalImages: map[string]string{"tag": ""},
								Disabled:         tc.promotionDisabled,
							}},
						},
					},
					}, nil
				},
				gitHubClient: fakeGithubClient{getGef: tc.githubClient},
				enqueueJob:   func(orbc prowjobreconciler.OrgRepoBranchCommit) { req = &orbc },
				since:        since,
			}

			err := r.reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{
				Namespace: "namespace",
				Name:      "name:tag",
			}}, r.log)

			if err := tc.verify(err, req); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestHandleCIOpConfigChange(t *testing.T) {
	var queue []prowjobreconciler.OrgRepoBranchCommit
	testCases := []struct {
		name                   string
		registryClient         ctrlruntimeclient.Client
		ciOperatorConfigGetter ciOperatorConfigGetter
		prowJobEnqueuer        prowjobreconciler.Enqueuer
		githubClient           githubClient
		delta                  agents.IndexDelta
		log                    *logrus.Entry
		expected               error
		expectedQue            []prowjobreconciler.OrgRepoBranchCommit
	}{
		{
			name: "empty delta",
		},
		{
			name:     "invalid identifier",
			delta:    agents.IndexDelta{Added: []*cioperatorapi.ReleaseBuildConfiguration{{}}},
			expected: fmt.Errorf("got an index delta event with a key that is not a valid namespace/name identifier: "),
		},
		{
			name:           "failed to get promotionConfig",
			delta:          agents.IndexDelta{IndexKey: "ns/is:tag", Added: []*cioperatorapi.ReleaseBuildConfiguration{{}}},
			registryClient: fakectrlruntimeclient.NewClientBuilder().Build(),
			ciOperatorConfigGetter: func(identifier string) ([]*cioperatorapi.ReleaseBuildConfiguration, error) {
				return nil, fmt.Errorf("some error")
			},
			expected: fmt.Errorf("failed to get promotionConfig for imagestreamtag ns/is:tag: query index: some error"),
		},
		{
			name:           "nil promotionConfig",
			delta:          agents.IndexDelta{IndexKey: "ns/is:tag", Added: []*cioperatorapi.ReleaseBuildConfiguration{{}}},
			registryClient: fakectrlruntimeclient.NewClientBuilder().Build(),
			ciOperatorConfigGetter: func(identifier string) ([]*cioperatorapi.ReleaseBuildConfiguration, error) {
				return nil, nil
			},
			expected: fmt.Errorf("nil promotionConfig for imagestreamtag ns/is:tag"),
		},
		{
			name:           "failed to get current git head",
			delta:          agents.IndexDelta{IndexKey: "ns/is:tag", Added: []*cioperatorapi.ReleaseBuildConfiguration{{}}},
			registryClient: fakectrlruntimeclient.NewClientBuilder().Build(),
			ciOperatorConfigGetter: func(identifier string) ([]*cioperatorapi.ReleaseBuildConfiguration, error) {
				return []*cioperatorapi.ReleaseBuildConfiguration{
					{Metadata: cioperatorapi.Metadata{
						Org:    "org",
						Repo:   "repo",
						Branch: "branch",
					}},
				}, nil
			},
			githubClient: fakeGithubClient{getGef: func(string, string, string) (string, error) { return "", fmt.Errorf("some error") }},
			expected:     fmt.Errorf("failed to get current git head for org/repo/branch and imageStreamTag ns/is:tag: failed to get sha for ref org/repo/heads/branch from github: some error"),
		},
		{
			name:           "got 404 from github",
			delta:          agents.IndexDelta{IndexKey: "ns/is:tag", Added: []*cioperatorapi.ReleaseBuildConfiguration{{}}},
			registryClient: fakectrlruntimeclient.NewClientBuilder().Build(),
			ciOperatorConfigGetter: func(identifier string) ([]*cioperatorapi.ReleaseBuildConfiguration, error) {
				return []*cioperatorapi.ReleaseBuildConfiguration{
					{Metadata: cioperatorapi.Metadata{
						Org:    "org",
						Repo:   "repo",
						Branch: "branch",
					}},
				}, nil
			},
			githubClient: fakeGithubClient{getGef: func(string, string, string) (string, error) { return "", github.GetRefTooManyResultsError{} }},
			expected:     fmt.Errorf("got 404 from github for org/repo/branch and imageStreamTag ns/is:tag, this likely means the repo or branch got deleted or we are not allowed to access it"),
		},
		{
			name:           "ignore istags from ns/build-cache",
			delta:          agents.IndexDelta{IndexKey: "build-cache/is:tag", Added: []*cioperatorapi.ReleaseBuildConfiguration{{}}},
			registryClient: fakectrlruntimeclient.NewClientBuilder().Build(),
			ciOperatorConfigGetter: func(identifier string) ([]*cioperatorapi.ReleaseBuildConfiguration, error) {
				return []*cioperatorapi.ReleaseBuildConfiguration{
					{Metadata: cioperatorapi.Metadata{
						Org:    "org",
						Repo:   "repo",
						Branch: "branch",
					}},
				}, nil
			},
			githubClient: fakeGithubClient{getGef: func(string, string, string) (string, error) { return "", github.GetRefTooManyResultsError{} }},
		},
		{
			name:           "basic case",
			delta:          agents.IndexDelta{IndexKey: "ns/is:tag", Added: []*cioperatorapi.ReleaseBuildConfiguration{{}}},
			registryClient: fakectrlruntimeclient.NewClientBuilder().Build(),
			ciOperatorConfigGetter: func(identifier string) ([]*cioperatorapi.ReleaseBuildConfiguration, error) {
				return []*cioperatorapi.ReleaseBuildConfiguration{
					{Metadata: cioperatorapi.Metadata{
						Org:    "org",
						Repo:   "repo",
						Branch: "branch",
					}},
				}, nil
			},
			githubClient: fakeGithubClient{getGef: func(string, string, string) (string, error) { return "ref", nil }},
			prowJobEnqueuer: func(input prowjobreconciler.OrgRepoBranchCommit) {
				queue = append(queue, input)
			},
			expectedQue: []prowjobreconciler.OrgRepoBranchCommit{{Org: "org", Repo: "repo", Branch: "branch", Commit: "ref"}},
		},
		{
			name:  "istag exists",
			delta: agents.IndexDelta{IndexKey: "ns/is:tag", Added: []*cioperatorapi.ReleaseBuildConfiguration{{}}},
			registryClient: fakectrlruntimeclient.NewClientBuilder().WithRuntimeObjects(
				&imagev1.ImageStreamTag{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "ns",
						Name:      "is:tag",
					},
				},
			).Build(),
			ciOperatorConfigGetter: func(identifier string) ([]*cioperatorapi.ReleaseBuildConfiguration, error) {
				return []*cioperatorapi.ReleaseBuildConfiguration{
					{Metadata: cioperatorapi.Metadata{
						Org:    "org",
						Repo:   "repo",
						Branch: "branch",
					}},
				}, nil
			},
			githubClient: fakeGithubClient{getGef: func(string, string, string) (string, error) { return "ref", nil }},
			prowJobEnqueuer: func(input prowjobreconciler.OrgRepoBranchCommit) {
				queue = append(queue, input)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			queue = nil
			actual := handleCIOpConfigChange(tc.registryClient, tc.ciOperatorConfigGetter, tc.prowJobEnqueuer, tc.githubClient, tc.delta, logrus.WithField("tc.name", tc.name))
			if diff := cmp.Diff(tc.expected, actual, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("actual import differs from expected: %s", diff)
			}
			if actual == nil {
				if diff := cmp.Diff(tc.expectedQue, queue); diff != "" {
					t.Errorf("actual import differs from expected: %s", diff)
				}
			}
		})
	}
}

func TestIgnored(t *testing.T) {
	testCases := []struct {
		name                string
		ignoredImageStreams []*regexp.Regexp
		request             reconcile.Request
		expected            bool
	}{
		{
			name:                "not ignored",
			ignoredImageStreams: []*regexp.Regexp{regexp.MustCompile(`^openshift-priv/.+`)},
			request: reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: "ns",
					Name:      "name",
				},
			},
		},
		{
			name:                "ignored",
			ignoredImageStreams: []*regexp.Regexp{regexp.MustCompile(`^openshift-priv/.+`)},
			request: reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: "openshift-priv",
					Name:      "name",
				},
			},
			expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := ignored(tc.request, tc.ignoredImageStreams)
			if diff := cmp.Diff(tc.expected, actual); diff != "" {
				t.Errorf("%s: actual does not match expected, diff: %s", tc.name, diff)
			}
		})
	}
}
