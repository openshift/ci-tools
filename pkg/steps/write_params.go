package steps

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/sirupsen/logrus"

	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/results"
)

type writeParametersStep struct {
	params    *api.DeferredParameters
	paramFile string
}

var safeEnv = regexp.MustCompile(`^[a-zA-Z0-9_\-\.]*$`)

func (s *writeParametersStep) Inputs() (api.InputDefinition, error) {
	return nil, nil
}

func (*writeParametersStep) Validate() error { return nil }

func (s *writeParametersStep) Run(_ context.Context, o *api.RunOptions) error {
	return results.ForReason("writing_parameters").ForError(s.run())
}

func (s *writeParametersStep) run() error {
	logrus.Infof("Writing parameters to %s", s.paramFile)
	var params []string

	values, err := s.params.Map()
	if err != nil {
		return fmt.Errorf("failed to resolve parameters: %w", err)
	}
	for k, v := range values {
		if safeEnv.MatchString(v) {
			params = append(params, fmt.Sprintf("%s=%s", k, v))
			continue
		}
		params = append(params, fmt.Sprintf("%s='%s'", k, strings.Replace(strings.Replace(v, "\\", "\\\\", -1), "'", "\\'", -1)))
	}

	sort.Strings(params)

	params = append(params, "")
	return os.WriteFile(s.paramFile, []byte(strings.Join(params, "\n")), 0640)
}

func (s *writeParametersStep) Requires() []api.StepLink {
	return []api.StepLink{api.AllStepsLink()}
}

func (s *writeParametersStep) Creates() []api.StepLink {
	return nil
}

func (s *writeParametersStep) Provides() api.ParameterMap {
	return nil
}

func (s *writeParametersStep) Name() string { return "parameters/write" }

func (s *writeParametersStep) Description() string { return "Write the job parameters to disk" }

func (s *writeParametersStep) Objects() []ctrlruntimeclient.Object {
	return nil
}

func WriteParametersStep(params *api.DeferredParameters, paramFile string) api.Step {
	return &writeParametersStep{
		params:    params,
		paramFile: paramFile,
	}
}
