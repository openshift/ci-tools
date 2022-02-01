package main

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"

	rbacapi "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/kubernetes/scheme"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/pod-utils/downwardapi"
	"k8s.io/utils/diff"
	"k8s.io/utils/pointer"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/secrets"
	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	"github.com/openshift/ci-tools/pkg/testhelper"
	utilgzip "github.com/openshift/ci-tools/pkg/util/gzip"
)

func init() {
	if err := imagev1.AddToScheme(scheme.Scheme); err != nil {
		panic(fmt.Sprintf("failed to add imagev1 to scheme: %v", err))
	}
}

func TestProwMetadata(t *testing.T) {
	tests := []struct {
		id             string
		jobSpec        *api.JobSpec
		namespace      string
		customMetadata map[string]string
	}{
		{
			id: "generate metadata",
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Refs: &prowapi.Refs{
						Org:  "some-org",
						Repo: "some-repo",
					},
					ExtraRefs: []prowapi.Refs{
						{
							Org:  "some-extra-org",
							Repo: "some-extra-repo",
						},
					},
					ProwJobID: "some-prow-job-id",
				},
			},
			namespace:      "some-namespace",
			customMetadata: nil,
		},
		{
			id: "generate metadata with a custom metadata file",
			jobSpec: &api.JobSpec{
				JobSpec: downwardapi.JobSpec{
					Refs: &prowapi.Refs{
						Org:  "another-org",
						Repo: "another-repo",
					},
					ExtraRefs: []prowapi.Refs{
						{
							Org:  "another-extra-org",
							Repo: "another-extra-repo",
						},
						{
							Org:  "another-extra-org2",
							Repo: "another-extra-repo2",
						},
					},
					ProwJobID: "another-prow-job-id",
				},
			},
			namespace: "another-namespace",
			customMetadata: map[string]string{
				"custom-field1": "custom-value1",
				"custom-field2": "custom-value2",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			err := verifyMetadata(tc.jobSpec, tc.namespace, tc.customMetadata)
			if err != nil {
				t.Fatalf("error while running test: %v", err)
			}
		})
	}
}

func verifyMetadata(jobSpec *api.JobSpec, namespace string, customMetadata map[string]string) error {
	tempDir, err := ioutil.TempDir("", "")
	if err != nil {
		return fmt.Errorf("Unable to create temporary directory: %v", err)
	}
	defer os.RemoveAll(tempDir)
	if err := os.Setenv("ARTIFACTS", tempDir); err != nil {
		return err
	}

	metadataFile := filepath.Join(tempDir, "metadata.json")

	// Verify without custom metadata
	c := secrets.NewDynamicCensor()
	o := &options{
		jobSpec:   jobSpec,
		namespace: namespace,
		censor:    &c,
	}

	if err := o.writeMetadataJSON(); err != nil {
		return fmt.Errorf("error while writing metadata JSON: %v", err)
	}

	metadataFileContents, err := ioutil.ReadFile(metadataFile)
	if err != nil {
		return fmt.Errorf("error reading metadata file: %v", err)
	}

	var writtenMetadata prowResultMetadata
	if err := json.Unmarshal(metadataFileContents, &writtenMetadata); err != nil {
		return fmt.Errorf("error parsing prow metadata: %v", err)
	}

	expectedMetadata := prowResultMetadata{
		Revision:      "1",
		Repo:          fmt.Sprintf("%s/%s", jobSpec.Refs.Org, jobSpec.Refs.Repo),
		Repos:         map[string]string{fmt.Sprintf("%s/%s", jobSpec.Refs.Org, jobSpec.Refs.Repo): ""},
		Pod:           jobSpec.ProwJobID,
		WorkNamespace: namespace,
	}

	for _, extraRef := range jobSpec.ExtraRefs {
		expectedMetadata.Repos[fmt.Sprintf("%s/%s", extraRef.Org, extraRef.Repo)] = ""
	}

	if !reflect.DeepEqual(expectedMetadata, writtenMetadata) {
		return fmt.Errorf("written metadata does not match expected metadata: %s", cmp.Diff(expectedMetadata, writtenMetadata))
	}

	testArtifactDirectory := filepath.Join(tempDir, "test-artifact-directory")
	if os.Mkdir(testArtifactDirectory, os.FileMode(0755)) != nil {
		return fmt.Errorf("unable to create artifact directory under temporary directory")
	}

	if len(customMetadata) > 0 {
		testJSON, err := json.MarshalIndent(customMetadata, "", "")
		if err != nil {
			return fmt.Errorf("error marshalling custom metadata: %v", err)
		}
		err = ioutil.WriteFile(filepath.Join(testArtifactDirectory, "custom-prow-metadata.json"), testJSON, os.FileMode(0644))
		if err != nil {
			return fmt.Errorf("unable to create custom metadata file: %v", err)
		}
	}

	// Write a bunch of empty files that should be ignored
	var errs []error
	errs = append(errs, ioutil.WriteFile(filepath.Join(testArtifactDirectory, "a-ignore1.txt"), []byte(``), os.FileMode(0644)))
	errs = append(errs, ioutil.WriteFile(filepath.Join(testArtifactDirectory, "b-ignore1.txt"), []byte(`{"invalid-field1": "invalid-value1"}`), os.FileMode(0644)))
	errs = append(errs, ioutil.WriteFile(filepath.Join(testArtifactDirectory, "d-ignore1.txt"), []byte(``), os.FileMode(0644)))
	errs = append(errs, ioutil.WriteFile(filepath.Join(testArtifactDirectory, "e-ignore1.txt"), []byte(`{"invalid-field2": "invalid-value2"}`), os.FileMode(0644)))
	if err := utilerrors.NewAggregate(errs); err != nil {
		return fmt.Errorf("one or more of the empty *ignore files failed to write: %v", err)
	}

	if err := o.writeMetadataJSON(); err != nil {
		return fmt.Errorf("error while writing metadata JSON: %v", err)
	}

	metadataFileContents, err = ioutil.ReadFile(metadataFile)
	if err != nil {
		return fmt.Errorf("error reading metadata file (second revision): %v", err)
	}

	if err = json.Unmarshal(metadataFileContents, &writtenMetadata); err != nil {
		return fmt.Errorf("error parsing prow metadata (second revision): %v", err)
	}

	revision := "1"
	if len(customMetadata) > 0 {
		revision = "2"
	}

	expectedMetadata.Revision = revision
	expectedMetadata.Metadata = customMetadata
	if !reflect.DeepEqual(expectedMetadata, writtenMetadata) {
		return fmt.Errorf("written metadata does not match expected metadata (second revision): %s", cmp.Diff(expectedMetadata, writtenMetadata))
	}

	return nil
}

func TestGetResolverInfo(t *testing.T) {
	testCases := []struct {
		name     string
		opt      *options
		jobSpec  *api.JobSpec
		expected *api.Metadata
	}{{
		name: "Only JobSpec Refs",
		opt:  &options{},
		jobSpec: &api.JobSpec{
			JobSpec: downwardapi.JobSpec{
				Refs: &prowapi.Refs{
					Org:     "testOrganization",
					Repo:    "testRepo",
					BaseRef: "testBranch",
				},
			},
		},
		expected: &api.Metadata{
			Org:    "testOrganization",
			Repo:   "testRepo",
			Branch: "testBranch",
		},
	}, {
		name: "JobSpec Refs w/ variant set via flag",
		opt:  &options{variant: "v2"},
		jobSpec: &api.JobSpec{
			JobSpec: downwardapi.JobSpec{
				Refs: &prowapi.Refs{
					Org:     "testOrganization",
					Repo:    "testRepo",
					BaseRef: "testBranch",
				},
			},
		},
		expected: &api.Metadata{
			Org:     "testOrganization",
			Repo:    "testRepo",
			Branch:  "testBranch",
			Variant: "v2",
		},
	}, {
		name: "Ref with ExtraRefs",
		opt:  &options{},
		jobSpec: &api.JobSpec{
			JobSpec: downwardapi.JobSpec{
				Refs: &prowapi.Refs{
					Org:     "testOrganization",
					Repo:    "testRepo",
					BaseRef: "testBranch",
				},
				ExtraRefs: []prowapi.Refs{{
					Org:     "anotherOrganization",
					Repo:    "anotherRepo",
					BaseRef: "anotherBranch",
				}},
			},
		},
		expected: &api.Metadata{
			Org:    "testOrganization",
			Repo:   "testRepo",
			Branch: "testBranch",
		},
	}, {
		name: "Incomplete refs not used",
		opt:  &options{},
		jobSpec: &api.JobSpec{
			JobSpec: downwardapi.JobSpec{
				Refs: &prowapi.Refs{
					Org:     "testOrganization",
					BaseRef: "testBranch",
				},
				ExtraRefs: []prowapi.Refs{{
					Org:     "anotherOrganization",
					Repo:    "anotherRepo",
					BaseRef: "anotherBranch",
				}},
			},
		},
		expected: &api.Metadata{
			Org:    "anotherOrganization",
			Repo:   "anotherRepo",
			Branch: "anotherBranch",
		},
	}, {
		name: "Refs with single field overridden by options",
		opt: &options{
			repo:    "anotherRepo",
			variant: "v2",
		},
		jobSpec: &api.JobSpec{
			JobSpec: downwardapi.JobSpec{
				Refs: &prowapi.Refs{
					Org:     "testOrganization",
					Repo:    "testRepo",
					BaseRef: "testBranch",
				},
			},
		},
		expected: &api.Metadata{
			Org:     "testOrganization",
			Repo:    "anotherRepo",
			Branch:  "testBranch",
			Variant: "v2",
		},
	}, {
		name: "Only options",
		opt: &options{
			org:     "testOrganization",
			repo:    "testRepo",
			branch:  "testBranch",
			variant: "v2",
		},
		jobSpec: &api.JobSpec{},
		expected: &api.Metadata{
			Org:     "testOrganization",
			Repo:    "testRepo",
			Branch:  "testBranch",
			Variant: "v2",
		},
	}, {
		name: "All fields overridden by options",
		opt: &options{
			org:     "anotherOrganization",
			repo:    "anotherRepo",
			branch:  "anotherBranch",
			variant: "v2",
		},
		jobSpec: &api.JobSpec{
			JobSpec: downwardapi.JobSpec{
				Refs: &prowapi.Refs{
					Org:     "testOrganization",
					Repo:    "testRepo",
					BaseRef: "testBranch",
				},
			},
		},
		expected: &api.Metadata{
			Org:     "anotherOrganization",
			Repo:    "anotherRepo",
			Branch:  "anotherBranch",
			Variant: "v2",
		},
	}}
	for _, testCase := range testCases {
		actual := testCase.opt.getResolverInfo(testCase.jobSpec)
		if !reflect.DeepEqual(actual, testCase.expected) {
			t.Errorf("%s: Actual does not match expected:\n%s", testCase.name, diff.ObjectReflectDiff(testCase.expected, actual))
		}
	}
}

const rawConfig = `tag_specification:
  name: '4.0'
  namespace: ocp
promotion:
  name: '4.0'
  namespace: ocp
  additional_images:
    artifacts: artifacts
  excluded_images:
  - machine-os-content
base_images:
  base:
    name: '4.0'
    namespace: ocp
    tag: base
  base-machine:
    name: fedora
    namespace: openshift
    tag: '29'
  machine-os-content-base:
    name: '4.0'
    namespace: ocp
    tag: machine-os-content
binary_build_commands: make build WHAT='cmd/hypershift vendor/k8s.io/kubernetes/cmd/hyperkube'
canonical_go_repository: github.com/openshift/origin
images:
- dockerfile_path: images/template-service-broker/Dockerfile.rhel
  from: base
  to: template-service-broker
  inputs:
    bin:
      as:
      - builder
- dockerfile_path: images/cli/Dockerfile.rhel
  from: base
  to: cli
  inputs:
    bin:
      as:
      - builder
- dockerfile_path: images/hypershift/Dockerfile.rhel
  from: base
  to: hypershift
  inputs:
    bin:
      as:
      - builder
- dockerfile_path: images/hyperkube/Dockerfile.rhel
  from: base
  to: hyperkube
  inputs:
    bin:
      as:
      - builder
- dockerfile_path: images/tests/Dockerfile.rhel
  from: cli
  to: tests
  inputs:
    bin:
      as:
      - builder
- context_dir: images/deployer/
  dockerfile_path: Dockerfile.rhel
  from: cli
  to: deployer
- context_dir: images/recycler/
  dockerfile_path: Dockerfile.rhel
  from: cli
  to: recycler
- dockerfile_path: images/sdn/Dockerfile.rhel
  from: base
  to: node # TODO: SDN
  inputs:
    bin:
      as:
      - builder
- context_dir: images/os/
  from: base
  inputs:
    base-machine-with-rpms:
      as:
      - builder
    machine-os-content-base:
      as:
      -  registry.svc.ci.openshift.org/openshift/origin-v4.0:machine-os-content
  to: machine-os-content
raw_steps:
- pipeline_image_cache_step:
    commands: mkdir -p _output/local/releases; touch _output/local/releases/CHECKSUM;
      echo $'FROM bin AS bin\nFROM rpms AS rpms\nFROM centos:7\nCOPY --from=bin /go/src/github.com/openshift/origin/_output/local/releases
      /srv/zips/\nCOPY --from=rpms /go/src/github.com/openshift/origin/_output/local/releases/rpms/*
      /srv/repo/' > _output/local/releases/Dockerfile; make build-cross
    from: bin
    to: bin-cross
- project_directory_image_build_step:
    from: base
    inputs:
      bin-cross:
        as:
        - bin
        paths:
        - destination_dir: .
          source_path: /go/src/github.com/openshift/origin/_output/local/releases/Dockerfile
      rpms:
        as:
        - rpms
      src: {}
    optional: true
    to: artifacts
- output_image_tag_step:
    from: artifacts
    optional: true
    to:
      name: stable
      tag: artifacts
- rpm_image_injection_step:
    from: base
    to: base-with-rpms
- rpm_image_injection_step:
    from: base-machine
    to: base-machine-with-rpms
resources:
  '*':
    limits:
      memory: 6Gi
    requests:
      cpu: 100m
      memory: 200Mi
  bin:
    limits:
      memory: 12Gi
    requests:
      cpu: '3'
      memory: 8Gi
  bin-cross:
    limits:
      memory: 12Gi
    requests:
      cpu: '3'
      memory: 8Gi
  cmd:
    limits:
      memory: 11Gi
    requests:
      cpu: '3'
      memory: 8Gi
  integration:
    limits:
      memory: 18Gi
    requests:
      cpu: '3'
      memory: 14Gi
  rpms:
    limits:
      memory: 10Gi
    requests:
      cpu: '3'
      memory: 8Gi
  unit:
    limits:
      memory: 14Gi
    requests:
      cpu: '3'
      memory: 11Gi
  verify:
    limits:
      memory: 12Gi
    requests:
      cpu: '3'
      memory: 8Gi
rpm_build_commands: make build-rpms
build_root:
  image_stream_tag:
    name: src-cache-origin
    namespace: ci
    tag: master
tests:
- as: cmd
  commands: TMPDIR=/tmp/volume ARTIFACT_DIR=/tmp/artifacts JUNIT_REPORT=1
    KUBERNETES_SERVICE_HOST= make test-cmd -k
  container:
    from: bin
    memory_backed_volume:
      size: 4Gi
- as: unit
  commands: ARTIFACT_DIR=/tmp/artifacts JUNIT_REPORT=1 TEST_KUBE=true KUBERNETES_SERVICE_HOST=
    hack/test-go.sh
  container:
    from: src
- as: integration
  commands: GOMAXPROCS=8 TMPDIR=/tmp/volume ARTIFACT_DIR=/tmp/artifacts JUNIT_REPORT=1
    KUBERNETES_SERVICE_HOST= make test-integration
  container:
    from: bin
    memory_backed_volume:
      size: 4Gi
- as: verify
  commands: ARTIFACT_DIR=/tmp/artifacts JUNIT_REPORT=1 KUBERNETES_SERVICE_HOST= make
    verify -k
  container:
    from: bin
- as: e2e-aws
  commands: TEST_SUITE=openshift/conformance/parallel run-tests
  openshift_installer:
    cluster_profile: aws
- as: e2e-aws-all
  commands: TEST_SUITE=openshift/conformance run-tests
  openshift_installer:
    cluster_profile: aws
- as: e2e-aws-builds
  commands: TEST_SUITE=openshift/build run-tests
  openshift_installer:
    cluster_profile: aws
- as: e2e-aws-image-ecosystem
  commands: TEST_SUITE=openshift/image-ecosystem run-tests
  openshift_installer:
    cluster_profile: aws
- as: e2e-aws-image-registry
  commands: TEST_SUITE=openshift/image-registry run-tests
  openshift_installer:
    cluster_profile: aws
- as: e2e-aws-serial
  commands: TEST_SUITE=openshift/conformance/serial run-tests
  openshift_installer:
    cluster_profile: aws
- as: launch-aws
  commands: sleep 7200 & wait
  openshift_installer:
    cluster_profile: aws
- as: e2e-upi-aws
  commands: TEST_SUITE=openshift/conformance/serial run-tests
  openshift_installer_upi:
    cluster_profile: aws
- as: e2e-upi-src-vsphere
  commands: make tests
  openshift_installer_upi_src:
    cluster_profile: vsphere
`

var parsedConfig = &api.ReleaseBuildConfiguration{
	InputConfiguration: api.InputConfiguration{
		BaseImages: map[string]api.ImageStreamTagReference{
			"base": {
				Name:      "4.0",
				Namespace: "ocp",
				Tag:       "base",
			},
			"base-machine": {
				Name:      "fedora",
				Namespace: "openshift",
				Tag:       "29",
			},
			"machine-os-content-base": {
				Name:      "4.0",
				Namespace: "ocp",
				Tag:       "machine-os-content",
			},
		},
		BuildRootImage: &api.BuildRootImageConfiguration{
			ImageStreamTagReference: &api.ImageStreamTagReference{
				Name:      "src-cache-origin",
				Namespace: "ci",
				Tag:       "master",
			},
		},
		ReleaseTagConfiguration: &api.ReleaseTagConfiguration{
			Name:      "4.0",
			Namespace: "ocp",
		},
	},
	BinaryBuildCommands:     `make build WHAT='cmd/hypershift vendor/k8s.io/kubernetes/cmd/hyperkube'`,
	TestBinaryBuildCommands: "",
	RpmBuildCommands:        "make build-rpms",
	RpmBuildLocation:        "",
	CanonicalGoRepository:   pointer.StringPtr("github.com/openshift/origin"),
	Images: []api.ProjectDirectoryImageBuildStepConfiguration{{
		From: "base",
		To:   "template-service-broker",
		ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
			DockerfilePath: "images/template-service-broker/Dockerfile.rhel",
			Inputs:         map[string]api.ImageBuildInputs{"bin": {As: []string{"builder"}}},
		},
	}, {
		From: "base",
		To:   "cli",
		ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
			DockerfilePath: "images/cli/Dockerfile.rhel",
			Inputs:         map[string]api.ImageBuildInputs{"bin": {As: []string{"builder"}}},
		},
	}, {
		From: "base",
		To:   "hypershift",
		ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
			DockerfilePath: "images/hypershift/Dockerfile.rhel",
			Inputs:         map[string]api.ImageBuildInputs{"bin": {As: []string{"builder"}}},
		},
	}, {
		From: "base",
		To:   "hyperkube",
		ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
			DockerfilePath: "images/hyperkube/Dockerfile.rhel",
			Inputs:         map[string]api.ImageBuildInputs{"bin": {As: []string{"builder"}}},
		},
	}, {
		From: "cli",
		To:   "tests",
		ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
			DockerfilePath: "images/tests/Dockerfile.rhel",
			Inputs:         map[string]api.ImageBuildInputs{"bin": {As: []string{"builder"}}},
		},
	}, {
		From: "cli",
		To:   "deployer",
		ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
			DockerfilePath: "Dockerfile.rhel",
			ContextDir:     "images/deployer/",
		},
	}, {
		From: "cli",
		To:   "recycler",
		ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
			DockerfilePath: "Dockerfile.rhel",
			ContextDir:     "images/recycler/",
		},
	}, {
		From: "base",
		To:   "node",
		ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
			DockerfilePath: "images/sdn/Dockerfile.rhel",
			Inputs:         map[string]api.ImageBuildInputs{"bin": {As: []string{"builder"}}},
		},
	}, {
		From: "base",
		To:   "machine-os-content",
		ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
			ContextDir: "images/os/",
			Inputs: map[string]api.ImageBuildInputs{
				"base-machine-with-rpms":  {As: []string{"builder"}},
				"machine-os-content-base": {As: []string{"registry.svc.ci.openshift.org/openshift/origin-v4.0:machine-os-content"}},
			},
		},
	}},
	RawSteps: []api.StepConfiguration{{
		PipelineImageCacheStepConfiguration: &api.PipelineImageCacheStepConfiguration{
			From:     "bin",
			To:       "bin-cross",
			Commands: `mkdir -p _output/local/releases; touch _output/local/releases/CHECKSUM; echo $'FROM bin AS bin\nFROM rpms AS rpms\nFROM centos:7\nCOPY --from=bin /go/src/github.com/openshift/origin/_output/local/releases /srv/zips/\nCOPY --from=rpms /go/src/github.com/openshift/origin/_output/local/releases/rpms/* /srv/repo/' > _output/local/releases/Dockerfile; make build-cross`,
		},
	}, {
		ProjectDirectoryImageBuildStepConfiguration: &api.ProjectDirectoryImageBuildStepConfiguration{
			From: "base",
			To:   "artifacts",
			ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
				Inputs: map[string]api.ImageBuildInputs{
					"bin-cross": {As: []string{"bin"}, Paths: []api.ImageSourcePath{{DestinationDir: ".", SourcePath: "/go/src/github.com/openshift/origin/_output/local/releases/Dockerfile"}}},
					"rpms":      {As: []string{"rpms"}},
					"src":       {},
				},
			},
			Optional: true,
		},
	}, {
		OutputImageTagStepConfiguration: &api.OutputImageTagStepConfiguration{
			From:     "artifacts",
			To:       api.ImageStreamTagReference{Name: "stable", Tag: "artifacts"},
			Optional: true,
		},
	}, {
		RPMImageInjectionStepConfiguration: &api.RPMImageInjectionStepConfiguration{
			From: "base",
			To:   "base-with-rpms",
		},
	}, {
		RPMImageInjectionStepConfiguration: &api.RPMImageInjectionStepConfiguration{
			From: "base-machine",
			To:   "base-machine-with-rpms",
		},
	}},
	PromotionConfiguration: &api.PromotionConfiguration{
		Namespace:        "ocp",
		Name:             "4.0",
		AdditionalImages: map[string]string{"artifacts": "artifacts"},
		ExcludedImages:   []string{"machine-os-content"},
	},
	Resources: map[string]api.ResourceRequirements{
		"*":           {Limits: map[string]string{"memory": "6Gi"}, Requests: map[string]string{"cpu": "100m", "memory": "200Mi"}},
		"bin":         {Limits: map[string]string{"memory": "12Gi"}, Requests: map[string]string{"cpu": "3", "memory": "8Gi"}},
		"bin-cross":   {Limits: map[string]string{"memory": "12Gi"}, Requests: map[string]string{"cpu": "3", "memory": "8Gi"}},
		"cmd":         {Limits: map[string]string{"memory": "11Gi"}, Requests: map[string]string{"cpu": "3", "memory": "8Gi"}},
		"integration": {Limits: map[string]string{"memory": "18Gi"}, Requests: map[string]string{"cpu": "3", "memory": "14Gi"}},
		"rpms":        {Limits: map[string]string{"memory": "10Gi"}, Requests: map[string]string{"cpu": "3", "memory": "8Gi"}},
		"unit":        {Limits: map[string]string{"memory": "14Gi"}, Requests: map[string]string{"cpu": "3", "memory": "11Gi"}},
		"verify":      {Limits: map[string]string{"memory": "12Gi"}, Requests: map[string]string{"cpu": "3", "memory": "8Gi"}},
	},
	Tests: []api.TestStepConfiguration{{
		As:       "cmd",
		Commands: `TMPDIR=/tmp/volume ARTIFACT_DIR=/tmp/artifacts JUNIT_REPORT=1 KUBERNETES_SERVICE_HOST= make test-cmd -k`,
		ContainerTestConfiguration: &api.ContainerTestConfiguration{
			From: "bin",
			MemoryBackedVolume: &api.MemoryBackedVolume{
				Size: "4Gi",
			},
		},
	}, {
		As:       "unit",
		Commands: `ARTIFACT_DIR=/tmp/artifacts JUNIT_REPORT=1 TEST_KUBE=true KUBERNETES_SERVICE_HOST= hack/test-go.sh`,
		ContainerTestConfiguration: &api.ContainerTestConfiguration{
			From: "src",
		},
	}, {
		As:       "integration",
		Commands: `GOMAXPROCS=8 TMPDIR=/tmp/volume ARTIFACT_DIR=/tmp/artifacts JUNIT_REPORT=1 KUBERNETES_SERVICE_HOST= make test-integration`,
		ContainerTestConfiguration: &api.ContainerTestConfiguration{
			From: "bin",
			MemoryBackedVolume: &api.MemoryBackedVolume{
				Size: "4Gi",
			},
		},
	}, {
		As:       "verify",
		Commands: `ARTIFACT_DIR=/tmp/artifacts JUNIT_REPORT=1 KUBERNETES_SERVICE_HOST= make verify -k`,
		ContainerTestConfiguration: &api.ContainerTestConfiguration{
			From: "bin",
		},
	}, {
		As:       "e2e-aws",
		Commands: `TEST_SUITE=openshift/conformance/parallel run-tests`,
		OpenshiftInstallerClusterTestConfiguration: &api.OpenshiftInstallerClusterTestConfiguration{
			ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: "aws"},
		},
	}, {
		As:       "e2e-aws-all",
		Commands: `TEST_SUITE=openshift/conformance run-tests`,
		OpenshiftInstallerClusterTestConfiguration: &api.OpenshiftInstallerClusterTestConfiguration{
			ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: "aws"},
		},
	}, {
		As:       "e2e-aws-builds",
		Commands: `TEST_SUITE=openshift/build run-tests`,
		OpenshiftInstallerClusterTestConfiguration: &api.OpenshiftInstallerClusterTestConfiguration{
			ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: "aws"},
		},
	}, {
		As:       "e2e-aws-image-ecosystem",
		Commands: `TEST_SUITE=openshift/image-ecosystem run-tests`,
		OpenshiftInstallerClusterTestConfiguration: &api.OpenshiftInstallerClusterTestConfiguration{
			ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: "aws"},
		},
	}, {
		As:       "e2e-aws-image-registry",
		Commands: `TEST_SUITE=openshift/image-registry run-tests`,
		OpenshiftInstallerClusterTestConfiguration: &api.OpenshiftInstallerClusterTestConfiguration{
			ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: "aws"},
		},
	}, {
		As:       "e2e-aws-serial",
		Commands: `TEST_SUITE=openshift/conformance/serial run-tests`,
		OpenshiftInstallerClusterTestConfiguration: &api.OpenshiftInstallerClusterTestConfiguration{
			ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: "aws"},
		},
	}, {
		As:       "launch-aws",
		Commands: `sleep 7200 & wait`,
		OpenshiftInstallerClusterTestConfiguration: &api.OpenshiftInstallerClusterTestConfiguration{
			ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: "aws"},
		},
	}, {
		As:       "e2e-upi-aws",
		Commands: `TEST_SUITE=openshift/conformance/serial run-tests`,
		OpenshiftInstallerUPIClusterTestConfiguration: &api.OpenshiftInstallerUPIClusterTestConfiguration{
			ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: "aws"},
		},
	}, {
		As:       "e2e-upi-src-vsphere",
		Commands: `make tests`,
		OpenshiftInstallerUPISrcClusterTestConfiguration: &api.OpenshiftInstallerUPISrcClusterTestConfiguration{
			ClusterTestConfiguration: api.ClusterTestConfiguration{ClusterProfile: "vsphere"},
		},
	}},
}

const configWithInvalidField = `
tests:
- as: e2e-aws-multistage
  steps:
    invalid_field: bad
    cluster_profile: aws
    workflow: origin-e2e-aws
`

func TestConfig(t *testing.T) {
	var testCases = []struct {
		name          string
		config        string
		asFile        bool
		asEnv         bool
		compressEnv   bool
		expected      *api.ReleaseBuildConfiguration
		isGzipped     bool
		expectedError bool
	}{
		{
			name:          "loading config from file works",
			config:        rawConfig,
			asFile:        true,
			expected:      parsedConfig,
			expectedError: false,
		},
		{
			name:          "loading config from gzipped file works",
			config:        rawConfig,
			asFile:        true,
			expected:      parsedConfig,
			isGzipped:     true,
			expectedError: false,
		},
		{
			name:          "loading config from env works",
			config:        rawConfig,
			asEnv:         true,
			expected:      parsedConfig,
			expectedError: false,
		},
		{
			name:          "loading config from compressed env works",
			config:        rawConfig,
			asEnv:         true,
			compressEnv:   true,
			expected:      parsedConfig,
			expectedError: false,
		},
		{
			name:          "no file or env fails to load config",
			config:        rawConfig,
			asEnv:         true,
			expected:      parsedConfig,
			expectedError: false,
		},
		{
			name:          "extra fields results in error",
			config:        configWithInvalidField,
			asEnv:         true,
			expected:      nil,
			expectedError: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var path string
			if testCase.asFile {
				temp, err := ioutil.TempFile("", "")
				if err != nil {
					t.Fatalf("%s: failed to create temp config file: %v", testCase.name, err)
				}
				defer func() {
					if err := os.Remove(temp.Name()); err != nil {
						t.Fatalf("%s: failed to remove temp config file: %v", testCase.name, err)
					}
				}()
				path = temp.Name()

				if testCase.isGzipped {
					w := gzip.NewWriter(temp)
					if _, err := w.Write([]byte(testCase.config)); err != nil {
						t.Fatalf("%s: failed to populate temp config file with gzipped data: %v", testCase.name, err)
					}
					w.Close()
				} else {
					if err := ioutil.WriteFile(path, []byte(testCase.config), 0664); err != nil {
						t.Fatalf("%s: failed to populate temp config file: %v", testCase.name, err)
					}
				}
			}
			if testCase.asEnv {
				config := testCase.config
				if testCase.compressEnv {
					var err error
					config, err = utilgzip.CompressStringAndBase64(config)
					if err != nil {
						t.Fatalf("Failed to compress config: %v", err)
					}
				}
				if err := os.Setenv("CONFIG_SPEC", config); err != nil {
					t.Fatalf("%s: failed to populate env var: %v", testCase.name, err)
				}
			}
			config, err := (&options{configSpecPath: path}).loadConfig(nil)
			if err == nil && testCase.expectedError {
				t.Errorf("%s: expected an error, but got none", testCase.name)
			}
			if err != nil && !testCase.expectedError {
				t.Errorf("%s: expected no error, but got one: %v", testCase.name, err)
			}
			if actual, expected := config, testCase.expected; !reflect.DeepEqual(actual, expected) {
				t.Errorf("%s: didn't get correct config: %v", testCase.name, diff.ObjectReflectDiff(actual, expected))
			}

		})
	}
}

func TestErrWroteJUnit(t *testing.T) {
	// this simulates the error chain bubbling up to the top of the call chain
	rootCause := errors.New("failure")
	reasonedErr := results.ForReason("something").WithError(rootCause).Errorf("couldn't do it: %v", rootCause)
	withJunit := &errWroteJUnit{wrapped: reasonedErr}
	defaulted := results.DefaultReason(withJunit)

	if !errors.Is(defaulted, &errWroteJUnit{}) {
		t.Error("expected the top-level error to still expose that we wrote jUnit")
	}
	testhelper.Diff(t, "reasons", results.Reasons(defaulted), []string{"something"})
}

func TestBuildPartialGraph(t *testing.T) {
	testCases := []struct {
		name           string
		input          []api.Step
		targetName     string
		expectedErrors []error
	}{
		{
			name: "Missing input image results in human-readable error",
			input: []api.Step{
				steps.InputImageTagStep(
					&api.InputImageTagStepConfiguration{InputImage: api.InputImage{To: api.PipelineImageStreamTagReferenceRoot}},
					loggingclient.New(fakectrlruntimeclient.NewFakeClient(&imagev1.ImageStreamTag{ObjectMeta: metav1.ObjectMeta{Name: ":"}})),
					nil,
				),
				steps.SourceStep(api.SourceStepConfiguration{From: api.PipelineImageStreamTagReferenceRoot, To: api.PipelineImageStreamTagReferenceSource}, api.ResourceConfiguration{}, nil, &api.JobSpec{}, nil, nil),
				steps.ProjectDirectoryImageBuildStep(
					api.ProjectDirectoryImageBuildStepConfiguration{
						From: api.PipelineImageStreamTagReferenceSource,
						ProjectDirectoryImageBuildInputs: api.ProjectDirectoryImageBuildInputs{
							Inputs: map[string]api.ImageBuildInputs{"cli": {Paths: []api.ImageSourcePath{{DestinationDir: ".", SourcePath: "/usr/bin/oc"}}}},
						},
						To: api.PipelineImageStreamTagReference("oc-bin-image"),
					},
					&api.ReleaseBuildConfiguration{}, api.ResourceConfiguration{}, nil, nil, nil,
				),
				steps.OutputImageTagStep(api.OutputImageTagStepConfiguration{From: api.PipelineImageStreamTagReference("oc-bin-image")}, nil, nil),
				steps.ImagesReadyStep(steps.OutputImageTagStep(api.OutputImageTagStepConfiguration{From: api.PipelineImageStreamTagReference("oc-bin-image")}, nil, nil).Creates()),
			},
			targetName: "[images]",
			expectedErrors: []error{
				errors.New("steps are missing dependencies"),
				errors.New(`step [output::] is missing dependencies: <&api.internalImageStreamLink{name:"stable"}>, <&api.internalImageStreamTagLink{name:"pipeline", tag:"oc-bin-image", unsatisfiableError:""}>`),
				errors.New(`step oc-bin-image is missing dependencies: "cli" is neither an imported nor a built image`),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			graph, err := api.BuildPartialGraph(tc.input, []string{tc.targetName})
			if err != nil {
				t.Fatalf("failed to build graph: %v", err)
			}

			// Apparently we only coincidentally validate the graph during the topologicalSort we do prior to printing it
			_, errs := graph.TopologicalSort()
			testhelper.Diff(t, "errors", errs, tc.expectedErrors, testhelper.EquateErrorMessage)
		})
	}
}

type fakeValidationStep struct {
	name string
	err  error
}

func (*fakeValidationStep) Inputs() (api.InputDefinition, error) { return nil, nil }
func (*fakeValidationStep) Run(ctx context.Context) error        { return nil }
func (*fakeValidationStep) Requires() []api.StepLink             { return nil }
func (*fakeValidationStep) Creates() []api.StepLink              { return nil }
func (f *fakeValidationStep) Name() string                       { return f.name }
func (*fakeValidationStep) Description() string                  { return "" }
func (*fakeValidationStep) Provides() api.ParameterMap           { return nil }
func (f *fakeValidationStep) Validate() error                    { return f.err }
func (*fakeValidationStep) Objects() []ctrlruntimeclient.Object  { return nil }

func TestValidateSteps(t *testing.T) {
	valid0 := fakeValidationStep{name: "valid0"}
	valid1 := fakeValidationStep{name: "valid1"}
	valid2 := fakeValidationStep{name: "valid2"}
	valid3 := fakeValidationStep{name: "valid3"}
	invalid0 := fakeValidationStep{
		name: "invalid0",
		err:  errors.New("invalid0"),
	}
	for _, tc := range []struct {
		name     string
		expected bool
		steps    api.OrderedStepList
	}{{
		name:     "empty graph",
		expected: true,
	}, {
		name:     "valid graph",
		expected: true,
		steps: api.OrderedStepList{{
			Step: &valid0,
			Children: []*api.StepNode{
				{Step: &valid1},
				{Step: &valid2},
			},
		}, {
			Step: &valid3,
		}},
	}, {
		name: "invalid graph",
		steps: api.OrderedStepList{{
			Step: &valid0,
			Children: []*api.StepNode{
				{Step: &valid1},
				{Step: &valid2},
			},
		}, {
			Step: &invalid0,
			Children: []*api.StepNode{
				{Step: &valid1},
				{Step: &valid2},
			},
		}},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSteps(tc.steps)
			if (err == nil) != tc.expected {
				t.Errorf("got %v, want %v", err == nil, tc.expected)
			}
		})
	}
}

func TestLoadLeaseCredentials(t *testing.T) {
	dir, err := ioutil.TempDir("", "test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	leaseServerCredentialsFile := filepath.Join(dir, "leaseServerCredentialsFile")
	if err := ioutil.WriteFile(leaseServerCredentialsFile, []byte("ci-new:secret-new"), 0644); err != nil {
		t.Fatal(err)
	}

	leaseServerCredentialsInvalidFile := filepath.Join(dir, "leaseServerCredentialsInvalidFile")
	if err := ioutil.WriteFile(leaseServerCredentialsInvalidFile, []byte("no-colon"), 0644); err != nil {
		t.Fatal(err)
	}

	testCases := []struct {
		name                       string
		leaseServerCredentialsFile string
		expectedUsername           string
		passwordGetterVerify       func(func() []byte) error
		expectedErr                error
	}{
		{
			name:                       "valid credential file",
			leaseServerCredentialsFile: leaseServerCredentialsFile,
			expectedUsername:           "ci-new",
			passwordGetterVerify: func(passwordGetter func() []byte) error {
				p := string(passwordGetter())
				if diff := cmp.Diff("secret-new", p); diff != "" {
					return fmt.Errorf("actual does not match expected, diff: %s", diff)
				}
				return nil
			},
		},
		{
			name:                       "wrong credential file",
			leaseServerCredentialsFile: leaseServerCredentialsInvalidFile,
			expectedErr:                fmt.Errorf("got invalid content of lease server credentials file which must be of the form '<username>:<passwrod>'"),
		},
	}

	for _, tc := range testCases {

		t.Run(tc.name, func(t *testing.T) {
			username, passwordGetter, err := loadLeaseCredentials(tc.leaseServerCredentialsFile)
			if diff := cmp.Diff(tc.expectedUsername, username); diff != "" {
				t.Errorf("actual does not match expected, diff: %s", diff)
			}
			if diff := cmp.Diff(tc.expectedErr, err, testhelper.EquateErrorMessage); diff != "" {
				t.Errorf("actualError does not match expectedError, diff: %s", diff)
			}
			if tc.passwordGetterVerify != nil {
				if err := tc.passwordGetterVerify(passwordGetter); err != nil {
					t.Errorf("unexpcected error: %v", err)
				}
			}
		})
	}
}

func TestExcludeContextCancelledErrors(t *testing.T) {
	testCases := []struct {
		id       string
		errs     []error
		expected []error
	}{
		{
			id: "no context cancelled errors, no changes expected",
			errs: []error{
				results.ForReason("step_failed").WithError(errors.New("oopsie")).Errorf("step foo failed: oopsie"),
				results.ForReason("step_failed").WithError(errors.New("oopsie")).Errorf("step bar failed: oopsie"),
			},
			expected: []error{
				results.ForReason("step_failed").WithError(errors.New("oopsie")).Errorf("step foo failed: oopsie"),
				results.ForReason("step_failed").WithError(errors.New("oopsie")).Errorf("step bar failed: oopsie"),
			},
		},
		{
			id: "context cancelled errors, changes expected",
			errs: []error{
				results.ForReason("step_failed").WithError(errors.New("oopsie")).Errorf("step foo failed: oopsie"),
				results.ForReason("step_failed").WithError(context.Canceled).Errorf("step bar failed: %v", context.Canceled),
			},
			expected: []error{
				results.ForReason("step_failed").WithError(errors.New("oopsie")).Errorf("step foo failed: oopsie"),
			},
		},
	}

	for _, tc := range testCases {
		actualErrs := excludeContextCancelledErrors(tc.errs)
		if diff := cmp.Diff(actualErrs, tc.expected, testhelper.EquateErrorMessage); diff != "" {
			t.Fatal(diff)
		}
	}
}

func TestMultiStageParams(t *testing.T) {
	testCases := []struct {
		id             string
		inputParams    stringSlice
		expectedParams map[string]string
		testConfig     []api.TestStepConfiguration
		expectedErrs   []string
	}{
		{
			id:          "Add params",
			inputParams: stringSlice{[]string{"PARAM1=VAL1", "PARAM2=VAL2"}},
			testConfig: []api.TestStepConfiguration{
				{
					MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
						Environment: map[string]string{
							"OTHERPARAM": "OTHERVAL",
						},
					},
				},
			},
			expectedParams: map[string]string{
				"PARAM1":     "VAL1",
				"PARAM2":     "VAL2",
				"OTHERPARAM": "OTHERVAL",
			},
		},
		{
			id:          "Override existing param",
			inputParams: stringSlice{[]string{"PARAM1=NEWVAL", "PARAM2=VAL2"}},
			testConfig: []api.TestStepConfiguration{
				{
					MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
						Environment: map[string]string{
							"PARAM1": "VAL1",
						},
					},
				},
			},
			expectedParams: map[string]string{
				"PARAM1": "NEWVAL",
				"PARAM2": "VAL2",
			},
		},
		{
			id:             "invalid params",
			inputParams:    stringSlice{[]string{"PARAM1", "PARAM2"}},
			expectedParams: map[string]string{},
			expectedErrs: []string{
				"could not parse multi-stage-param: PARAM1 is not in the format key=value",
				"could not parse multi-stage-param: PARAM2 is not in the format key=value",
			},
		},
	}

	t.Parallel()

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			t.Parallel()

			configSpec := api.ReleaseBuildConfiguration{
				Tests: tc.testConfig,
			}

			o := &options{
				multiStageParamOverrides: tc.inputParams,
				configSpec:               &configSpec,
			}

			errs := overrideMultiStageParams(o)
			actualParams := make(map[string]string)

			for _, test := range o.configSpec.Tests {
				if test.MultiStageTestConfigurationLiteral != nil {
					for name, val := range test.MultiStageTestConfigurationLiteral.Environment {
						actualParams[name] = val
					}
				}

				if test.MultiStageTestConfiguration != nil {
					for name, val := range test.MultiStageTestConfiguration.Environment {
						actualParams[name] = val
					}
				}
			}

			if errs == nil {
				if diff := cmp.Diff(tc.expectedParams, actualParams); diff != "" {
					t.Errorf("actual does not match expected, diff: %s", diff)
				}
			}

			var expectedErr error
			if len(tc.expectedErrs) > 0 {
				var errorsList []error
				for _, err := range tc.expectedErrs {
					errorsList = append(errorsList, errors.New(err))
				}
				expectedErr = utilerrors.NewAggregate(errorsList)
			}
			if diff := cmp.Diff(errs, expectedErr, testhelper.EquateErrorMessage); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}

func TestDependencyOverrides(t *testing.T) {
	testCases := []struct {
		id           string
		inputParams  stringSlice
		expectedDeps map[string]string
		testConfig   []api.TestStepConfiguration
		expectedErrs []string
	}{
		{
			id:          "Override dependency",
			inputParams: stringSlice{[]string{"OO_INDEX=registry.mystuff.com:5000/pushed/myimage"}},
			testConfig: []api.TestStepConfiguration{
				{
					MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
						Test: []api.LiteralTestStep{
							{
								As: "step1",
								Dependencies: []api.StepDependency{
									{
										Name: "ci-index",
										Env:  "OO_INDEX",
									},
									{
										Name: "cool-image",
										Env:  "OTHER_THING",
									},
								},
							},
						},
					},
				},
			},
			expectedDeps: map[string]string{
				"OO_INDEX":    "registry.mystuff.com:5000/pushed/myimage",
				"OTHER_THING": "cool-image",
			},
		},
		{
			id:          "No matching dependency for override, dependencies untouched",
			inputParams: stringSlice{[]string{"NOT_FOUND=NOT_UPDATES"}},
			testConfig: []api.TestStepConfiguration{
				{
					MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
						Test: []api.LiteralTestStep{
							{
								As: "step1",
								Dependencies: []api.StepDependency{
									{
										Name: "ci-index",
										Env:  "OO_INDEX",
									},
									{
										Name: "cool-image",
										Env:  "OTHER_THING",
									},
								},
							},
						},
					},
				},
			},
			expectedDeps: map[string]string{
				"OO_INDEX":    "ci-index",
				"OTHER_THING": "cool-image",
			},
		},
		{
			id:          "invalid params",
			inputParams: stringSlice{[]string{"NOT_GOOD", "ALSO_NOT_GOOD"}},
			expectedErrs: []string{
				"could not parse dependency-override-param: NOT_GOOD is not in the format key=value",
				"could not parse dependency-override-param: ALSO_NOT_GOOD is not in the format key=value",
			},
		},
		{
			id: "Override dependency using test-level dependency override",
			testConfig: []api.TestStepConfiguration{
				{
					MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
						DependencyOverrides: map[string]string{
							"OO_INDEX": "registry.mystuff.com:5000/pushed/myimage",
						},
						Test: []api.LiteralTestStep{
							{
								As: "step1",
								Dependencies: []api.StepDependency{
									{
										Name: "ci-index",
										Env:  "OO_INDEX",
									},
									{
										Name: "cool-image",
										Env:  "OTHER_THING",
									},
								},
							},
						},
					},
				},
			},
			expectedDeps: map[string]string{
				"OO_INDEX":    "registry.mystuff.com:5000/pushed/myimage",
				"OTHER_THING": "cool-image",
			},
		},
		{
			id:          "Input param dependency takes precedence",
			inputParams: stringSlice{[]string{"OO_INDEX=registry.mystuff.com:5000/pushed/myimage"}},
			testConfig: []api.TestStepConfiguration{
				{
					MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
						DependencyOverrides: map[string]string{
							"OO_INDEX": "registry.mystuff.com:5000/pushed/myimage2",
						},
						Test: []api.LiteralTestStep{
							{
								As: "step1",
								Dependencies: []api.StepDependency{
									{
										Name: "ci-index",
										Env:  "OO_INDEX",
									},
									{
										Name: "cool-image",
										Env:  "OTHER_THING",
									},
								},
							},
						},
					},
				},
			},
			expectedDeps: map[string]string{
				"OO_INDEX":    "registry.mystuff.com:5000/pushed/myimage",
				"OTHER_THING": "cool-image",
			},
		},
	}

	t.Parallel()

	for i := range testCases {
		tc := testCases[i]
		t.Run(tc.id, func(t *testing.T) {
			t.Parallel()

			configSpec := api.ReleaseBuildConfiguration{
				Tests: tc.testConfig,
			}

			o := &options{
				dependencyOverrides: tc.inputParams,
				configSpec:          &configSpec,
			}

			errs := overrideTestStepDependencyParams(o)
			actualDeps := make(map[string]string)

			for _, test := range o.configSpec.Tests {
				if test.MultiStageTestConfigurationLiteral != nil {
					for _, step := range test.MultiStageTestConfigurationLiteral.Test {
						for _, dependency := range step.Dependencies {
							if dependency.PullSpec != "" {
								actualDeps[dependency.Env] = dependency.PullSpec
							} else {
								actualDeps[dependency.Env] = dependency.Name
							}
						}
					}
				}
			}

			if errs == nil {
				if diff := cmp.Diff(tc.expectedDeps, actualDeps); diff != "" {
					t.Errorf("actual does not match expected, diff: %s", diff)
				}
			}

			var expectedErr error
			if len(tc.expectedErrs) > 0 {
				var errorsList []error
				for _, err := range tc.expectedErrs {
					errorsList = append(errorsList, errors.New(err))
				}
				expectedErr = utilerrors.NewAggregate(errorsList)
			}
			if diff := cmp.Diff(errs, expectedErr, testhelper.EquateErrorMessage); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}

func TestGenerateAuthorAccessRoleBinding(t *testing.T) {
	testCases := []struct {
		id       string
		authors  []string
		expected *rbacapi.RoleBinding
	}{
		{
			id:      "basic case",
			authors: []string{"a", "e"},
			expected: &rbacapi.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ci-op-author-access",
					Namespace: "ci-op-xxxx",
				},
				Subjects: []rbacapi.Subject{{Kind: "Group", Name: "a-group"}, {Kind: "Group", Name: "e-group"}},
				RoleRef: rbacapi.RoleRef{
					Kind: "ClusterRole",
					Name: "admin",
				},
			},
		},
		{
			id:      "no duplicated authors",
			authors: []string{"a", "a"},
			expected: &rbacapi.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ci-op-author-access",
					Namespace: "ci-op-xxxx",
				},
				Subjects: []rbacapi.Subject{{Kind: "Group", Name: "a-group"}},
				RoleRef: rbacapi.RoleRef{
					Kind: "ClusterRole",
					Name: "admin",
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.id, func(t *testing.T) {
			actual := generateAuthorAccessRoleBinding("ci-op-xxxx", tc.authors)
			if diff := cmp.Diff(tc.expected, actual, testhelper.RuntimeObjectIgnoreRvTypeMeta); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}
