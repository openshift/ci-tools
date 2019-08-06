package api

import (
	"encoding/json"
	"fmt"

	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/test-infra/prow/pod-utils/downwardapi"
)

// JobSpec is a superset of the upstream spec, but
// we do not import it as importing test-infra is a
// massive hassle.
type JobSpec struct {
	downwardapi.JobSpec `json:",inline"`

	// rawSpec is the serialized form of the Spec
	rawSpec string

	// these fields allow the job to be targeted at a location
	Namespace     string
	BaseNamespace string

	// if set, any new artifacts will be a child of this object
	owner *meta.OwnerReference
}

func (s *JobSpec) RawSpec() string {
	return s.rawSpec
}

func (s *JobSpec) Owner() *meta.OwnerReference {
	return s.owner
}

func (s *JobSpec) SetOwner(owner *meta.OwnerReference) {
	s.owner = owner
}

// Inputs returns the definition of the job as an input to
// the execution graph.
func (s *JobSpec) Inputs() InputDefinition {
	spec := &JobSpec{
		JobSpec: downwardapi.JobSpec{
			Refs: s.Refs,
		},
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		panic(err)
	}
	return InputDefinition{string(raw)}
}

// ResolveSpecFromEnv will determine the Refs being
// tested in by parsing Prow environment variable contents
func ResolveSpecFromEnv() (*JobSpec, error) {
	apiSpec, err := downwardapi.ResolveSpecFromEnv()
	if err != nil {
		return nil, fmt.Errorf("malformed $JOB_SPEC: %v", err)
	}
	raw, err := json.Marshal(apiSpec)
	if err != nil {
		panic(err)
	}
	return &JobSpec{
		JobSpec: *apiSpec,
		rawSpec: string(raw),
	}, nil
}
