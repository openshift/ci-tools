package main

import (
	"compress/gzip"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/utils/diff"
	"k8s.io/utils/pointer"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/testhelper"
	utilgzip "github.com/openshift/ci-tools/pkg/util/gzip"
)

const rawConfig = `tag_specification:
  name: '4.0'
  namespace: ocp
promotion:
  to:
  - name: '4.0'
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
  items:
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
	Images: api.ImageConfiguration{Items: []api.ProjectDirectoryImageBuildStepConfiguration{{
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
	}}},
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
		Targets: []api.PromotionTarget{{
			Namespace:        "ocp",
			Name:             "4.0",
			AdditionalImages: map[string]string{"artifacts": "artifacts"},
			ExcludedImages:   []string{"machine-os-content"},
		}},
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

type fakeGcsReader struct {
	content []byte
}

func (b fakeGcsReader) Read(filePath string) ([]byte, error) {
	return b.content, nil
}

func TestConfig(t *testing.T) {
	var testCases = []struct {
		name          string
		config        string
		asFile        bool
		asEnv         bool
		asGcsUrlEnv   bool
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
			name:          "loading config from GCS URL env works",
			config:        rawConfig,
			asGcsUrlEnv:   true,
			expected:      parsedConfig,
			expectedError: false,
		},
		{
			name:          "loading config from compressed GCS URL env works",
			config:        rawConfig,
			asGcsUrlEnv:   true,
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
			var gcsReader fakeGcsReader
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
			if testCase.asGcsUrlEnv {
				config := testCase.config
				if testCase.compressEnv {
					var err error
					config, err = utilgzip.CompressStringAndBase64(config)
					if err != nil {
						t.Fatalf("Failed to compress config: %v", err)
					}
				}
				gcsReader = fakeGcsReader{content: []byte(config)}
				if err := os.Setenv("CONFIG_SPEC_GCS_URL", "some-path"); err != nil {
					t.Fatalf("%s: failed to populate env var: %v", testCase.name, err)
				}
			}
			config, err := (&options{configSpecPath: path}).loadConfig(nil, gcsReader)
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
		{
			id:          "quoted param with spaces",
			inputParams: stringSlice{[]string{`ARGS="--test 1 --value text"`}},
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
				"ARGS":       "--test 1 --value text",
				"OTHERPARAM": "OTHERVAL",
			},
		},
	}

	t.Parallel()

	for _, tc := range testCases {
		tc := tc
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

func TestApplyEnvOverrides(t *testing.T) {
	testCases := []struct {
		id             string
		envVars        map[string]string
		expectedParams map[string]string
		testConfig     []api.TestStepConfiguration
		expectedErrs   []string
	}{
		{
			id: "Apply overrides",
			envVars: map[string]string{
				"MULTISTAGE_PARAM_OVERRIDE_PARAM1": "VAL1",
				"MULTISTAGE_PARAM_OVERRIDE_PARAM2": "VAL2",
				"XXX_OVERRIDE_PARAM2":              "VAL3",
			},
			expectedParams: map[string]string{
				"MULTISTAGE_PARAM_OVERRIDE_PARAM1": "VAL1",
				"MULTISTAGE_PARAM_OVERRIDE_PARAM2": "VAL2",
			},
			testConfig: []api.TestStepConfiguration{
				{
					MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
						Environment: map[string]string{
							"MULTISTAGE_PARAM_OVERRIDE_PARAM1": "VAL1",
							"MULTISTAGE_PARAM_OVERRIDE_PARAM2": "VAL2",
						},
					},
				},
			},
		},
		{
			id: "No OVERRIDE_ prefix",
			envVars: map[string]string{
				"PARAM1": "VAL1",
			},
			expectedParams: map[string]string{},
			testConfig: []api.TestStepConfiguration{
				{
					MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
						Environment: map[string]string{},
					},
				},
			},
		},
		{
			id: "test environment variable contains an equal sign",
			envVars: map[string]string{
				"MULTISTAGE_PARAM_OVERRIDE_PARAM1": "VAL2",
				"MULTISTAGE_PARAM_OVERRIDE_PARAM2": "VAL=2",
				"MULTISTAGE_PARAM_OVERRIDE_PARAM3": "VAL2",
			},
			expectedParams: map[string]string{
				"MULTISTAGE_PARAM_OVERRIDE_PARAM1": "VAL2",
				"MULTISTAGE_PARAM_OVERRIDE_PARAM2": "VAL=2",
				"MULTISTAGE_PARAM_OVERRIDE_PARAM3": "VAL2",
			},
			testConfig: []api.TestStepConfiguration{
				{
					MultiStageTestConfigurationLiteral: &api.MultiStageTestConfigurationLiteral{
						Environment: map[string]string{
							"MULTISTAGE_PARAM_OVERRIDE_PARAM1": "VAL2",
							"MULTISTAGE_PARAM_OVERRIDE_PARAM2": "VAL=2",
							"MULTISTAGE_PARAM_OVERRIDE_PARAM3": "VAL2",
						},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.id, func(t *testing.T) {
			for k, v := range tc.envVars {
				t.Setenv(k, v)
			}

			configSpec := api.ReleaseBuildConfiguration{
				Tests: tc.testConfig,
			}

			o := &options{
				configSpec: &configSpec,
			}

			applyEnvOverrides(o)

			actualParams := make(map[string]string)

			for _, test := range o.configSpec.Tests {
				if test.MultiStageTestConfigurationLiteral != nil {
					for name, val := range test.MultiStageTestConfigurationLiteral.Environment {
						actualParams[name] = val
					}
				}
			}

			if diff := cmp.Diff(tc.expectedParams, actualParams); diff != "" {
				t.Errorf("actual does not match expected, diff: %s", diff)
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

func TestHandleTargetAdditionalSuffix(t *testing.T) {
	testCases := []struct {
		name                   string
		targetAdditionalSuffix string
		targets                stringSlice
		expectedTargets        stringSlice
		tests                  []api.TestStepConfiguration
		expectedTests          []api.TestStepConfiguration
		jobSpec                api.JobSpec
		expectedJobSpec        api.JobSpec
	}{
		{
			name:            "no target-additional-suffix set",
			targets:         stringSlice{[]string{"e2e"}},
			expectedTargets: stringSlice{[]string{"e2e"}},
			tests:           []api.TestStepConfiguration{{As: "e2e"}},
			expectedTests:   []api.TestStepConfiguration{{As: "e2e"}},
			jobSpec:         api.JobSpec{Target: "e2e"},
			expectedJobSpec: api.JobSpec{Target: "e2e"},
		},
		{
			name:                   "basic target-additional-suffix set",
			targetAdditionalSuffix: "1",
			targets:                stringSlice{[]string{"e2e"}},
			expectedTargets:        stringSlice{[]string{"e2e-1"}},
			tests:                  []api.TestStepConfiguration{{As: "e2e"}},
			expectedTests:          []api.TestStepConfiguration{{As: "e2e-1"}},
			jobSpec:                api.JobSpec{Target: "e2e"},
			expectedJobSpec:        api.JobSpec{Target: "e2e-1", TargetAdditionalSuffix: "1"},
		},
		{
			name:                   "target-additional-suffix set with multiple targets",
			targetAdditionalSuffix: "1",
			targets:                stringSlice{[]string{"e2e", "unit"}},
			expectedTargets:        stringSlice{[]string{"e2e-1", "unit-1"}},
			tests:                  []api.TestStepConfiguration{{As: "e2e"}, {As: "unit"}},
			expectedTests:          []api.TestStepConfiguration{{As: "e2e-1"}, {As: "unit-1"}},
			jobSpec:                api.JobSpec{Target: "e2e"},
			expectedJobSpec:        api.JobSpec{Target: "e2e-1", TargetAdditionalSuffix: "1"},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			o := &options{
				targetAdditionalSuffix: tc.targetAdditionalSuffix,
				configSpec: &api.ReleaseBuildConfiguration{
					Tests: tc.tests,
				},
				jobSpec: &tc.jobSpec,
				targets: tc.targets,
			}

			handleTargetAdditionalSuffix(o)

			if diff := cmp.Diff(tc.expectedTargets.values, o.targets.values); diff != "" {
				t.Fatalf("expectedTargets differ from actual, diff: %s", diff)
			}
			if diff := cmp.Diff(tc.expectedTests, o.configSpec.Tests); diff != "" {
				t.Fatalf("expectedTests differ from actual, diff: %s", diff)
			}
			if diff := cmp.Diff(tc.expectedJobSpec.Target, o.jobSpec.Target); diff != "" {
				t.Fatalf("expectedJobSpec Target differs from actual, diff: %s", diff)
			}
		})
	}
}
