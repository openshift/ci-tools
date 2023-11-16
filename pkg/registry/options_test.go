package registry

import (
	"compress/gzip"
	"os"
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/util/diff"
	prowapi "k8s.io/test-infra/prow/apis/prowjobs/v1"
	"k8s.io/test-infra/prow/pod-utils/downwardapi"
	"k8s.io/utils/pointer"

	"github.com/openshift/ci-tools/pkg/api"
	utilgzip "github.com/openshift/ci-tools/pkg/util/gzip"
)

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

func TestGetResolverInfo(t *testing.T) {
	testCases := []struct {
		name     string
		opt      *Options
		jobSpec  *api.JobSpec
		expected *api.Metadata
	}{{
		name: "Only JobSpec Refs",
		opt:  &Options{},
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
		opt:  &Options{variant: "v2"},
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
		opt:  &Options{},
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
			Org:    "testOrganization,anotherOrganization",
			Repo:   "testRepo,anotherRepo",
			Branch: "testBranch,anotherBranch",
		},
	}, {
		name: "Incomplete refs not used",
		opt:  &Options{},
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
		opt: &Options{
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
		opt: &Options{
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
		opt: &Options{
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
		actual := testCase.opt.GetResolverInfo(testCase.jobSpec)
		if !reflect.DeepEqual(actual, testCase.expected) {
			t.Errorf("%s: Actual does not match expected:\n%s", testCase.name, diff.ObjectReflectDiff(testCase.expected, actual))
		}
	}
}

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
				temp, err := os.CreateTemp("", "")
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
					if err := os.WriteFile(path, []byte(testCase.config), 0664); err != nil {
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
			config, err := (&Options{configSpecPath: path}).loadConfig(nil)
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
