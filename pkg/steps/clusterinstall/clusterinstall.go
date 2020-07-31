package clusterinstall

import (
	"context"
	"fmt"
	"github.com/openshift/ci-tools/pkg/steps/utils"
	"strings"

	"github.com/ghodss/yaml"

	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	coreclientset "k8s.io/client-go/kubernetes/typed/core/v1"

	templateapi "github.com/openshift/api/template/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/junit"
	"github.com/openshift/ci-tools/pkg/results"
	"github.com/openshift/ci-tools/pkg/steps"
)

type e2eTestStep struct {
	config     api.OpenshiftInstallerClusterTestConfiguration
	testConfig api.TestStepConfiguration

	secretClient coreclientset.SecretsGetter
	jobSpec      *api.JobSpec

	step api.Step
	nestedSubTests
}

type nestedSubTests interface {
	SubTests() []*junit.TestCase
}

// E2ETestStep installs a cluster and then runs end-to-end tests against it.
func E2ETestStep(
	config api.OpenshiftInstallerClusterTestConfiguration,
	testConfig api.TestStepConfiguration,
	params api.Parameters,
	podClient steps.PodClient,
	templateClient steps.TemplateClient,
	secretClient coreclientset.SecretsGetter,
	artifactDir string,
	jobSpec *api.JobSpec,
	resources api.ResourceConfiguration,
) (api.Step, error) {
	var template *templateapi.Template
	if err := yaml.Unmarshal([]byte(installTemplateE2E), &template); err != nil {
		return nil, fmt.Errorf("the embedded template is invalid: %w", err)
	}

	template.Name = testConfig.As

	if config.Upgrade {
		overrides := make(map[string]string)
		for i := range template.Parameters {
			p := &template.Parameters[i]
			switch p.Name {
			case "JOB_NAME_SAFE":
				if !params.HasInput(p.Name) {
					overrides[p.Name] = testConfig.As
				}
			case "TEST_COMMAND":
				p.Value = testConfig.Commands
			case "CLUSTER_TYPE":
				p.Value = strings.Split(string(config.ClusterProfile), "-")[0]
			}
		}

		// ensure we depend on the release image
		name := utils.ReleaseImageEnv(api.InitialImageStream)
		template.Parameters = append(template.Parameters, templateapi.Parameter{
			Required: true,
			Name:     name,
		})

		// ensure the installer image points to the initial state
		name = utils.StableImageEnv("installer")
		if !params.HasInput(name) {
			overrides[name] = "stable-initial:installer"
		}
		template.Parameters = append(template.Parameters, templateapi.Parameter{
			Required: true,
			Name:     name,
		})

		// set install initial release true for use in the template
		name = "INSTALL_INITIAL_RELEASE"
		template.Parameters = append(template.Parameters, templateapi.Parameter{
			Required: true,
			Name:     name,
			Value:    "true",
		})

		params = api.NewOverrideParameters(params, overrides)
	}

	step := steps.TemplateExecutionStep(template, params, podClient, templateClient, artifactDir, jobSpec, resources)
	subTests, ok := step.(nestedSubTests)
	if !ok {
		return nil, fmt.Errorf("unexpected %T", step)
	}

	return &e2eTestStep{
		config:     config,
		testConfig: testConfig,

		secretClient: secretClient,
		jobSpec:      jobSpec,

		step:           step,
		nestedSubTests: subTests,
	}, nil
}

func (s *e2eTestStep) Inputs() (api.InputDefinition, error) {
	return nil, nil
}

func (s *e2eTestStep) Run(ctx context.Context) error {
	return results.ForReason("installing_cluster").ForError(s.run(ctx))
}

func (s *e2eTestStep) run(ctx context.Context) error {
	if _, err := s.secretClient.Secrets(s.jobSpec.Namespace()).Get(context.TODO(), fmt.Sprintf("%s-cluster-profile", s.testConfig.As), meta.GetOptions{}); err != nil {
		return results.ForReason("missing_cluster_profile").WithError(err).Errorf("could not find required secret: %v", err)
	}
	return s.step.Run(ctx)
}

func (s *e2eTestStep) Requires() []api.StepLink {
	links := s.step.Requires()
	if s.config.Upgrade {
		links = append([]api.StepLink{api.ReleasePayloadImageLink("initial")}, links...)
	}
	return links
}

func (s *e2eTestStep) Creates() []api.StepLink {
	return nil
}

func (s *e2eTestStep) Provides() api.ParameterMap {
	return nil
}

func (s *e2eTestStep) Name() string { return s.testConfig.As }

func (s *e2eTestStep) Description() string {
	if s.config.Upgrade {
		return fmt.Sprintf("Run cluster install and upgrade %s", s.testConfig.As)
	}
	return fmt.Sprintf("Run cluster install %s", s.testConfig.As)
}
