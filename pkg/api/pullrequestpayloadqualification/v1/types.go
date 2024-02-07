package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"

	"github.com/openshift/ci-tools/pkg/api"
)

const (
	PullRequestPayloadQualificationRunLabel = "pullrequestpayloadqualificationruns.ci.openshift.io"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:resource:shortName=prpqr

// PullRequestPayloadQualificationRun represents the intent to run a battery of OCP release
// payload validating jobs
type PullRequestPayloadQualificationRun struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	// Spec is considered immutable and should be entirely created by the requestor
	Spec   PullRequestPayloadTestSpec   `json:"spec"`
	Status PullRequestPayloadTestStatus `json:"status,omitempty"`
}

// PullRequestPayloadTestSpec specifies for which PR the payload qualification run was requested
// and the list of individual jobs that should be executed.
type PullRequestPayloadTestSpec struct {
	// PullRequests specifies the code to be tested. Immutable and required.
	PullRequests []PullRequestUnderTest `json:"pullRequests"`
	// Jobs specifies the jobs to be executed. Immutable.
	Jobs PullRequestPayloadJobSpec `json:"jobs"`
	// InitialPayloadBase specifies the base payload pullspec for the "initial" release payload
	InitialPayloadBase string `json:"initial,omitempty"`
	// PayloadOverrides specifies overrides to the base payload.
	PayloadOverrides PayloadOverrides `json:"payload,omitempty"`
}

// PayloadOverrides allows overrides to the base payload.
type PayloadOverrides struct {
	// BasePullSpec specifies the base payload pullspec for the "latest" release payload
	// (alternate from the default of the 4.x CI payload) to layer changes on top of.
	BasePullSpec string `json:"base,omitempty"`
	// ImageTagOverrides allow specific image tags to be overridden
	ImageTagOverrides []ImageTagOverride `json:"tags,omitempty"`
}

// ImageTagOverride describes a specific image name that should be overridden with the provided tag
type ImageTagOverride struct {
	// Name is the name of the image like "machine-os-content"
	Name string `json:"name"`
	// Tag is the tag to override the image with like "4.16-art-latest-2024-02-05-071231"
	Tag string `json:"tag"`
}

// PullRequestUnderTest describes the state of the repo that will be under test
// This is a combination of the PR revision and base ref revision. Tested code
// is the specific revision of the PR merged into the base branch with
// a specific branch as a HEAD
type PullRequestUnderTest struct {
	// Org is something like "openshift" in github.com/openshift/kubernetes
	Org string `json:"org"`
	// Repo is something like "kubernetes" in github.com/openshift/kubernetes
	Repo string `json:"repo"`
	// BaseRef identifies the target branch for the PR
	BaseRef string `json:"baseRef"`
	// BaseSHA identifies the HEAD of BaseRef at the time
	BaseSHA string `json:"baseSHA"`

	PullRequest PullRequest `json:"pr"`
}

// PullRequest identifies a pull request in a repository
type PullRequest struct {
	Number int    `json:"number"`
	Author string `json:"author"`
	SHA    string `json:"sha"`
	Title  string `json:"title"`
}

// PullRequestPayloadJobSpec specifies the list of jobs that should be executed
// together with information about the data source (Release Controller Config)
// used to make the list
type PullRequestPayloadJobSpec struct {
	// ReleaseControllerConfig specifies the source of the selected jobs
	ReleaseControllerConfig ReleaseControllerConfig `json:"releaseControllerConfig"`
	// Jobs is a list of jobs to be executed. This list should be fully specified
	// when the custom resource is created and should not be changed afterwards.
	Jobs []ReleaseJobSpec `json:"releaseJobSpec"`
}

// ReleaseControllerConfig captures which Release Controller configuration to
// use to extract the list of jobs.
type ReleaseControllerConfig struct {
	// OCP is an OCP version, such as "4.10"
	OCP string `json:"ocp"`
	// Release is a release type, such as "nightly" or "ci"
	Release string `json:"release"`
	// Specifier specifies which jobs were selected from the release controller configs. Example: "informing"
	Specifier string `json:"specifier"`
	// Revision is a git revision of the release controller configuration files. Optional.
	Revision string `json:"revision,omitempty"`
}

// CIOperatorMetadata describes the source repo for which a config is written
type CIOperatorMetadata struct {
	Org     string `json:"org"`
	Repo    string `json:"repo"`
	Branch  string `json:"branch"`
	Variant string `json:"variant,omitempty"`
}

// ReleaseJobSpec identifies the release payload one qualification test to execute. In this context,
// "test" means one item in the specified ci-operator configuration file. This structure corresponds
// to a single configured Prowjob (like "periodic-ci-openshift-release-master-ci-4.9-e2e-gcp") and
// serves as a specification for dynamically building a one-off Prowjob that runs the identical test
// as the configured one.
type ReleaseJobSpec struct {
	// CIOperatorConfig identifies the ci-operator configuration with the test
	CIOperatorConfig CIOperatorMetadata `json:"ciOperatorConfig"`
	// Test is the name of the test in the ci-operator configuration
	Test string `json:"test"`
	// AggregatedCount is a number that specifies how many instances of the job will run in parallel.
	// When the value is 0 it means that the job is not run as aggregated and 1 means that
	// the job is aggregated with a single execution.
	AggregatedCount int `json:"aggregatedCount,omitempty"`
}

// PullRequestPayloadTestStatus provides runtime data, such as references to submitted ProwJobs,
// whether all jobs are submitted, finished, etc.
type PullRequestPayloadTestStatus struct {
	Conditions []metav1.Condition            `json:"conditions,omitempty"`
	Jobs       []PullRequestPayloadJobStatus `json:"jobs,omitempty"`
}

// PullRequestPayloadJobStatus is a reference to a Prowjob submitted for a single item
// from the list of jobs to be submitted
type PullRequestPayloadJobStatus struct {
	// ReleaseJobName is a name of the job that corresponds to the name corresponding to the
	// ReleaseJobSpec tuple. This name is inferred from ReleaseJobSpec data and corresponds to
	// the name which the user would see in e.g. release-controller
	ReleaseJobName string `json:"jobName"`
	// ProwJob is a name of the submitted ProwJob resource
	ProwJob string `json:"prowJob"`

	Status prowv1.ProwJobStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PullRequestPayloadQualificationRunList is a list of PullRequestPayloadQualificationRun resources
type PullRequestPayloadQualificationRunList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []PullRequestPayloadQualificationRun `json:"items"`
}

// JobName maps the name in the spec to the corresponding Prow job name.
// It matches the `ReleaseJobName` value in the status.
func (s *ReleaseJobSpec) JobName(prefix string) string {
	mwt := api.MetadataWithTest{
		Metadata: api.Metadata{
			Org:     s.CIOperatorConfig.Org,
			Repo:    s.CIOperatorConfig.Repo,
			Branch:  s.CIOperatorConfig.Branch,
			Variant: s.CIOperatorConfig.Variant,
		},
		Test: s.Test,
	}
	return mwt.JobName(prefix)
}
