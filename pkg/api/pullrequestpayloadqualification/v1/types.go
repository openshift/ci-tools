package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/ci-tools/pkg/api"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PullRequestPayloadQualificationRun represents the intent to run a battery of OCP release
// payload validating jobs
type PullRequestPayloadQualificationRun struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	// Spec is considered immutable and should be entirely created by the requestor
	Spec   PullRequestPayloadTestSpec   `json:"spec"`
	Status PullRequestPayloadTestStatus `json:"status"`
}

// PullRequestPayloadTestSpec specifies for which PR the payload qualification run was requested
// and the list of individual jobs that should be executed.
type PullRequestPayloadTestSpec struct {
	// PullRequest specifies the code to be tested. Immutable and required.
	PullRequest PullRequestUnderTest `json:"pullRequest"`
	// Jobs specifies the jobs to be executed. Immutable.
	Jobs PullRequestPayloadJobSpec `json:"jobs"`
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

// ReleaseJobSpec identifies the release payload one qualification test to execute. In this context,
// "test" means one item in the specified ci-operator configuration file. This structure corresponds
// to a single configured Prowjob (like "periodic-ci-openshift-release-master-ci-4.9-e2e-gcp") and
// serves as a specification for dynamically building a one-off Prowjob that runs the identical test
// as the configured one.
type ReleaseJobSpec struct {
	// CIOperatorConfig identifies the ci-operator configuration with the test
	CIOperatorConfig api.Metadata `json:"ciOperatorConfig"`
	// Test is the name of the test in the ci-operator configuration
	Test string `json:"test"`
}

// PullRequestPayloadTestStatus provides runtime data, such as references to submitted ProwJobs,
// whether all jobs are submitted, finished, etc.
type PullRequestPayloadTestStatus struct {
	Conditions []metav1.Condition            `json:"conditions"`
	Jobs       []PullRequestPayloadJobStatus `json:"jobs"`
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
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PullRequestPayloadQualificationRunList is a list of PullRequestPayloadQualificationRun resources
type PullRequestPayloadQualificationRunList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []PullRequestPayloadQualificationRun `json:"items"`
}
