package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"

	"k8s.io/utils/pointer"

	"github.com/openshift/ci-tools/pkg/api"
)

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

func TestConfig(t *testing.T) {
	correctHandler := func(t *testing.T, jsonConfig []byte) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("org") != "openshift" {
				t.Errorf("%s: Org should equal openshift, but was %s", t.Name(), r.URL.Query().Get("org"))
			}
			if r.URL.Query().Get("repo") != "hyperkube" {
				t.Errorf("%s: Repo should equal hyperkube, but was %s", t.Name(), r.URL.Query().Get("repo"))
			}
			if r.URL.Query().Get("branch") != "master" {
				t.Errorf("%s: Branch should equal master, but was %s", t.Name(), r.URL.Query().Get("branch"))
			}
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write(jsonConfig); err != nil {
				t.Errorf("failed to write data: %v", err)
			}
		})
	}
	failingHandler := func(t *testing.T, jsonConfig []byte) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			if _, err := w.Write(jsonConfig); err != nil {
				t.Errorf("failed to write: %v", err)
			}
		})
	}
	var testCases = []struct {
		name           string
		handlerWrapper func(t *testing.T, jsonConfig []byte) http.Handler
		expected       *api.ReleaseBuildConfiguration
		expectedError  bool
	}{
		{
			name:           "getting config works",
			handlerWrapper: correctHandler,
			expected:       parsedConfig,
			expectedError:  false,
		},
		{
			name:           "function errors on non OK status",
			handlerWrapper: failingHandler,
			expected:       nil,
			expectedError:  true,
		},
	}

	jsonConfig, err := json.Marshal(parsedConfig)
	if err != nil {
		t.Fatalf("%s: Failed to marshal parsedConfig to JSON: %v", t.Name(), err)
	}
	metadata := api.Metadata{Org: "openshift", Repo: "hyperkube", Branch: "master"}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(testCase.handlerWrapper(t, jsonConfig))
			client := ResolverClient{Address: server.URL}
			config, err := client.Config(&metadata)
			if err == nil && testCase.expectedError {
				t.Errorf("%s: expected an error, but got none", testCase.name)
			}
			if err != nil && !testCase.expectedError {
				t.Errorf("%s: expected no error, but got one: %v", testCase.name, err)
			}
			if diff := cmp.Diff(config, testCase.expected); diff != "" {
				t.Errorf("%s: didn't get correct config: %v", testCase.name, diff)
			}
			server.Close()
		})
	}
}
