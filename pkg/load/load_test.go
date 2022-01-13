package load

import (
	"compress/gzip"
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/ghodss/yaml"
	"github.com/google/go-cmp/cmp"

	"k8s.io/apimachinery/pkg/util/diff"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/utils/pointer"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/registry"
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
			config, err := Config(path, "", "", nil, nil)
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

func TestRegistry(t *testing.T) {
	defaultStr := "test parameter default"
	var (
		expectedReferences = registry.ReferenceByName{
			"ipi-deprovision-deprovision": {
				As:       "ipi-deprovision-deprovision",
				From:     "installer",
				Commands: "openshift-cluster destroy\n",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m", "memory": "2Gi"},
				},
			},
			"ipi-deprovision-must-gather": {
				As:       "ipi-deprovision-must-gather",
				From:     "installer",
				Commands: "gather\n",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m", "memory": "2Gi"},
				},
			},
			"ipi-install-install": {
				As:       "ipi-install-install",
				From:     "installer",
				Commands: "openshift-cluster install\n",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m", "memory": "2Gi"},
				},
				Environment: []api.StepParameter{
					{Name: "TEST_PARAMETER", Default: &defaultStr},
				},
				Observers: []string{"resourcewatcher"},
			},
			"ipi-install-rbac": {
				As:       "ipi-install-rbac",
				From:     "installer",
				Commands: "setup-rbac\n",
				Resources: api.ResourceRequirements{
					Requests: api.ResourceList{"cpu": "1000m", "memory": "2Gi"},
				},
			},
		}

		deprovisionRef       = `ipi-deprovision-deprovision`
		deprovisionGatherRef = `ipi-deprovision-must-gather`
		installRef           = `ipi-install-install`
		installRBACRef       = `ipi-install-rbac`
		installChain         = `ipi-install`

		chainDefault   = "test parameter set by chain"
		defaultEmpty   = ""
		expectedChains = registry.ChainByName{
			"ipi-install": api.RegistryChain{
				As: "ipi-install",
				Steps: []api.TestStep{
					{
						Reference: &installRBACRef,
					}, {
						Reference: &installRef,
					},
				},
			},
			"ipi-install-empty-parameter": {
				As:          "ipi-install-empty-parameter",
				Steps:       []api.TestStep{{Chain: &installChain}},
				Environment: []api.StepParameter{{Name: "TEST_PARAMETER", Default: &defaultEmpty}},
			},
			"ipi-install-with-parameter": api.RegistryChain{
				As:    "ipi-install-with-parameter",
				Steps: []api.TestStep{{Chain: &installChain}},
				Environment: []api.StepParameter{{
					Name:    "TEST_PARAMETER",
					Default: &chainDefault,
				}},
			},
			"ipi-deprovision": api.RegistryChain{
				As: "ipi-deprovision",
				Steps: []api.TestStep{
					{
						Reference: &deprovisionGatherRef,
					}, {
						Reference: &deprovisionRef,
					},
				},
			},
		}

		deprovisionChain = `ipi-deprovision`

		expectedWorkflows = registry.WorkflowByName{
			"ipi": {
				Pre: []api.TestStep{{
					Chain: &installChain,
				}},
				Post: []api.TestStep{{
					Chain: &deprovisionChain,
				}},
				Observers: &api.Observers{Disable: []string{"resourcewatcher"}},
			},
			"ipi-changed": {
				Pre: []api.TestStep{{
					Chain: &installChain,
				}},
				Post: []api.TestStep{{
					Chain: &deprovisionChain,
				}},
				Observers: &api.Observers{Disable: []string{"resourcewatcher"}},
			},
		}

		expectedObservers = registry.ObserverByName{
			"resourcewatcher": {
				Name:      "resourcewatcher",
				FromImage: &api.ImageStreamTagReference{Namespace: "ocp", Name: "resourcewatcher", Tag: "latest"},
				Commands:  "#!/bin/bash\n\nsleep 300",
			},
		}

		testCases = []struct {
			name          string
			registryDir   string
			flags         RegistryFlag
			references    registry.ReferenceByName
			chains        registry.ChainByName
			workflows     registry.WorkflowByName
			observers     registry.ObserverByName
			expectedError bool
		}{{
			name:          "Read registry",
			registryDir:   "../../test/multistage-registry/registry",
			references:    expectedReferences,
			chains:        expectedChains,
			workflows:     expectedWorkflows,
			observers:     expectedObservers,
			expectedError: false,
		}, {
			name:        "Read configmap style registry",
			registryDir: "../../test/multistage-registry/configmap",
			flags:       RegistryFlat,
			references: registry.ReferenceByName{
				"ipi-install-install": {
					As:       "ipi-install-install",
					From:     "installer",
					Commands: "openshift-cluster install\n",
					Resources: api.ResourceRequirements{
						Requests: api.ResourceList{"cpu": "1000m", "memory": "2Gi"},
					},
					Environment: []api.StepParameter{
						{Name: "TEST_PARAMETER", Default: &defaultStr},
					},
				},
			},
			chains:        registry.ChainByName{},
			workflows:     registry.WorkflowByName{},
			observers:     registry.ObserverByName{},
			expectedError: false,
		}, {
			name:          "Read registry with ref where name and filename don't match",
			registryDir:   "../../test/multistage-registry/invalid-filename",
			references:    nil,
			chains:        nil,
			workflows:     nil,
			expectedError: true,
		}, {
			name:          "Read registry where ref has an extra, invalid field",
			registryDir:   "../../test/multistage-registry/invalid-field",
			references:    nil,
			chains:        nil,
			workflows:     nil,
			expectedError: true,
		}, {
			name:          "Read registry where ref has command containing trap without grace period specified",
			registryDir:   "../../test/multistage-registry/trap-without-grace-period",
			references:    nil,
			chains:        nil,
			workflows:     nil,
			expectedError: true,
		}, {
			name:          "Read registry where ref has best effort defined without timeout",
			registryDir:   "../../test/multistage-registry/best-effort-without-timeout",
			references:    nil,
			chains:        nil,
			workflows:     nil,
			expectedError: true,
		}}
	)

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			references, chains, workflows, _, _, observers, err := Registry(testCase.registryDir, testCase.flags)
			if err == nil && testCase.expectedError == true {
				t.Error("got no error when error was expected")
			}
			if err != nil && testCase.expectedError == false {
				t.Errorf("got error when error wasn't expected: %v", err)
			}
			if !reflect.DeepEqual(references, testCase.references) {
				t.Errorf("output references different from expected: %s", diff.ObjectReflectDiff(references, testCase.references))
			}
			if !reflect.DeepEqual(chains, testCase.chains) {
				t.Errorf("output chains different from expected: %s", diff.ObjectReflectDiff(chains, testCase.chains))
			}
			if !reflect.DeepEqual(workflows, testCase.workflows) {
				t.Errorf("output workflows different from expected: %s", diff.ObjectReflectDiff(workflows, testCase.workflows))
			}
			if !reflect.DeepEqual(observers, testCase.observers) {
				t.Errorf("output observers different from expected: %s", diff.ObjectReflectDiff(observers, testCase.observers))
			}
		})
	}
	// set up a temporary directory registry with a broken component
	temp, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatalf("failed to create temp step registry: %v", err)
	}
	defer func() {
		if err := os.RemoveAll(temp); err != nil {
			t.Fatalf("failed to remove temp step registry: %v", err)
		}
	}()

	// create directory with slightly incorrect path based on ref name
	path := filepath.Join(temp, "ipi/deprovision/gather")
	err = os.MkdirAll(path, 0755)
	if err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}
	fileData, err := yaml.Marshal(expectedChains[deprovisionGatherRef])
	if err != nil {
		t.Fatalf("failed to marshal %s into a yaml []byte: %v", deprovisionGatherRef, err)
	}

	if err := ioutil.WriteFile(filepath.Join(path, deprovisionGatherRef), fileData, 0664); err != nil {
		t.Fatalf("failed to populate temp reference file: %v", err)
	}
	_, _, _, _, _, _, err = Registry(temp, RegistryFlag(0))
	if err == nil {
		t.Error("got no error when expecting error on incorrect reference name")
	}
}

func TestPartitionByRepo(t *testing.T) {
	var testCases = []struct {
		name   string
		input  filenameToConfig
		output ByOrgRepo
	}{
		{
			name:   "no input",
			input:  filenameToConfig{},
			output: ByOrgRepo{},
		},
		{
			name: "complex input",
			input: filenameToConfig{
				"a": api.ReleaseBuildConfiguration{Metadata: api.Metadata{
					Org:    "org",
					Repo:   "repo",
					Branch: "branch",
				}},
				"b": api.ReleaseBuildConfiguration{Metadata: api.Metadata{
					Org:    "org",
					Repo:   "repo",
					Branch: "other",
				}},
				"c": api.ReleaseBuildConfiguration{Metadata: api.Metadata{
					Org:     "org",
					Repo:    "repo",
					Branch:  "branch",
					Variant: "variant",
				}},
				"d": api.ReleaseBuildConfiguration{Metadata: api.Metadata{
					Org:    "org",
					Repo:   "other",
					Branch: "branch",
				}},
				"e": api.ReleaseBuildConfiguration{Metadata: api.Metadata{
					Org:    "other",
					Repo:   "repo",
					Branch: "branch",
				}},
			},
			output: ByOrgRepo{
				"org": map[string][]api.ReleaseBuildConfiguration{
					"repo": {{Metadata: api.Metadata{
						Org:    "org",
						Repo:   "repo",
						Branch: "branch",
					}}, {Metadata: api.Metadata{
						Org:    "org",
						Repo:   "repo",
						Branch: "other",
					}}, {Metadata: api.Metadata{
						Org:     "org",
						Repo:    "repo",
						Branch:  "branch",
						Variant: "variant",
					}}},
					"other": {{Metadata: api.Metadata{
						Org:    "org",
						Repo:   "other",
						Branch: "branch",
					}}},
				},
				"other": map[string][]api.ReleaseBuildConfiguration{
					"repo": {{Metadata: api.Metadata{
						Org:    "other",
						Repo:   "repo",
						Branch: "branch",
					}}},
				},
			},
		},
	}

	for _, testCase := range testCases {
		actual, expected := partitionByOrgRepo(testCase.input), testCase.output
		// The output slices are sorted based on the sorting of the input map so
		// this is more involded than just comparing two maps and ignoring their
		// order.
		if err := sortByOrgRepoSlices(&actual); err != nil {
			t.Fatalf("failed to sort actual: %v", err)
		}
		if err := sortByOrgRepoSlices(&expected); err != nil {
			t.Fatalf("failed to sort expected: %v", err)
		}
		if diff := cmp.Diff(actual, expected); diff != "" {
			t.Errorf("%s: did not get correct partitioned config: %s", testCase.name, diff)
		}
	}
}

func sortByOrgRepoSlices(in *ByOrgRepo) error {
	var errs []error
	for _, org := range *in {
		for _, repo := range org {
			sort.Slice(repo, func(i, j int) bool {
				iSerialized, err := json.Marshal(repo[i])
				if err != nil {
					errs = append(errs, err)
				}
				jSerialized, err := json.Marshal(repo[j])
				if err != nil {
					errs = append(errs, err)
				}
				return string(iSerialized) < string(jSerialized)
			})
		}
	}

	return utilerrors.NewAggregate(errs)
}
