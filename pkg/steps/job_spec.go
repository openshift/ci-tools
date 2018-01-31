package steps

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// JobSpec is a superset of the upstream spec, but
// we do not import it as importing test-infra is a
// massive hassle.
type JobSpec struct {
	Type    ProwJobType `json:"type,omitempty"`
	Job     string      `json:"job,omitempty"`
	BuildId string      `json:"buildid,omitempty"`

	Refs Refs `json:"refs,omitempty"`

	// rawSpec is the serialized form of the Spec
	rawSpec   string
	identifer string
}

type ProwJobType string

const (
	PresubmitJob  ProwJobType = "presubmit"
	PostsubmitJob             = "postsubmit"
	PeriodicJob               = "periodic"
	BatchJob                  = "batch"
)

type Pull struct {
	Number int    `json:"number,omitempty"`
	Author string `json:"author,omitempty"`
	SHA    string `json:"sha,omitempty"`
}

type Refs struct {
	Org  string `json:"org,omitempty"`
	Repo string `json:"repo,omitempty"`

	BaseRef string `json:"base_ref,omitempty"`
	BaseSHA string `json:"base_sha,omitempty"`

	Pulls []Pull `json:"pulls,omitempty"`
}

func (r Refs) String() string {
	rs := []string{fmt.Sprintf("%s:%s", r.BaseRef, r.BaseSHA)}
	for _, pull := range r.Pulls {
		rs = append(rs, fmt.Sprintf("%d:%s", pull.Number, pull.SHA))
	}
	return strings.Join(rs, ",")
}

func (s *JobSpec) Identifier() string {
	if s.identifer != "" {
		return s.identifer
	}

	// Object names can't be too long so we truncate
	// the hash. This increases chances of collision
	// but we can tolerate it as our input space is
	// tiny.
	s.identifer = fmt.Sprintf("%x", sha256.Sum256([]byte(s.rawSpec)))[40:]
	return s.identifer
}

// ResolveSpecFromEnv will determine the Refs being
// tested in by parsing Prow environment variable contents
func ResolveSpecFromEnv() (*JobSpec, error) {
	specEnv, ok := os.LookupEnv("JOB_SPEC")
	if !ok {
		return nil, errors.New("$JOB_SPEC unset")
	}

	spec := &JobSpec{}
	if err := json.Unmarshal([]byte(specEnv), spec); err != nil {
		return nil, fmt.Errorf("malformed $JOB_SPEC: %v", err)
	}

	spec.rawSpec = specEnv

	return spec, nil
}