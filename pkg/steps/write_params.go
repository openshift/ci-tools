package steps

import (
	"fmt"
	"io/ioutil"
	"log"
	"regexp"
	"sort"
	"strings"

	"github.com/openshift/ci-operator/pkg/api"
)

type writeParametersStep struct {
	params    *DeferredParameters
	paramFile string
	jobSpec   *JobSpec
}

var safeEnv = regexp.MustCompile(`^[a-zA-Z0-9_\-\.]*$`)

func (s *writeParametersStep) Run(dry bool) error {
	log.Printf("Writing parameters to %s", s.paramFile)
	var params []string

	values, err := s.params.Map()
	if err != nil {
		return err
	}
	for k, v := range values {
		if safeEnv.MatchString(v) {
			params = append(params, fmt.Sprintf("%s=%s", k, v))
			continue
		}
		params = append(params, fmt.Sprintf("%s='%s'", k, strings.Replace(strings.Replace(v, "\\", "\\\\", -1), "'", "\\'", -1)))
	}

	sort.Strings(params)

	if dry {
		log.Printf("\n%s", strings.Join(params, "\n"))
		return nil
	}
	params = append(params, "")
	return ioutil.WriteFile(s.paramFile, []byte(strings.Join(params, "\n")), 0640)
}

func (s *writeParametersStep) Done() (bool, error) {
	return false, nil
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

func WriteParametersStep(params *DeferredParameters, paramFile string, jobSpec *JobSpec) api.Step {
	return &writeParametersStep{
		params:    params,
		paramFile: paramFile,
		jobSpec:   jobSpec,
	}
}
