package steps

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"regexp"
	"sort"
	"strings"

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

func (s *writeParametersStep) Run(_ context.Context) error {
	return results.ForReason("writing_parameters").ForError(s.run())
}

func (s *writeParametersStep) run() error {
	log.Printf("Writing parameters to %s", s.paramFile)
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
	return ioutil.WriteFile(s.paramFile, []byte(strings.Join(params, "\n")), 0640)
}

func (s *writeParametersStep) Requires() []api.StepLink {
	return s.params.AllLinks()
}

func (s *writeParametersStep) Creates() []api.StepLink {
	return nil
}

func (s *writeParametersStep) Provides() (api.ParameterMap, api.StepLink) {
	return nil, nil
}

func (s *writeParametersStep) Name() string { return "parameters/write" }

func (s *writeParametersStep) Description() string { return "Write the job parameters to disk" }

func WriteParametersStep(params *api.DeferredParameters, paramFile string) api.Step {
	return &writeParametersStep{
		params:    params,
		paramFile: paramFile,
	}
}
