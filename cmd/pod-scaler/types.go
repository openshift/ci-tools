package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/openhistogram/circonusllhist"
	"github.com/prometheus/common/model"
	"github.com/sirupsen/logrus"

	"github.com/openshift/ci-tools/pkg/api"
)

const (
	ProwLabelNameCreated model.LabelName = "label_created_by_prow"
	ProwLabelNameContext model.LabelName = "label_prow_k8s_io_context"
	ProwLabelNameJob     model.LabelName = "label_prow_k8s_io_job"
	ProwLabelNameType    model.LabelName = "label_prow_k8s_io_type"
	ProwLabelNameOrg     model.LabelName = "label_prow_k8s_io_refs_org"
	ProwLabelNameRepo    model.LabelName = "label_prow_k8s_io_refs_repo"
	ProwLabelNameBranch  model.LabelName = "label_prow_k8s_io_refs_base_ref"

	LabelNameRehearsal model.LabelName = "label_ci_openshift_org_rehearse"
	LabelNameCreated   model.LabelName = "label_created_by_ci"
	LabelNameOrg       model.LabelName = "label_ci_openshift_io_metadata_org"
	LabelNameRepo      model.LabelName = "label_ci_openshift_io_metadata_repo"
	LabelNameBranch    model.LabelName = "label_ci_openshift_io_metadata_branch"
	LabelNameVariant   model.LabelName = "label_ci_openshift_io_metadata_variant"
	LabelNameTarget    model.LabelName = "label_ci_openshift_io_metadata_target"
	LabelNameStep      model.LabelName = "label_ci_openshift_io_metadata_step"
	LabelNamePod       model.LabelName = "pod"
	LabelNameContainer model.LabelName = "container"
	LabelNameBuild     model.LabelName = "label_openshift_io_build_name"
	LabelNameRelease   model.LabelName = "label_ci_openshift_io_release"
	LabelNameApp       model.LabelName = "label_app"
)

// CachedQuery stores digested data for a query across clusters, as well as indices
// for the data to access it by the fully specific set of labels as well as a smaller
// set that uses the step for context only.
type CachedQuery struct {
	// Query is the query we executed against Prometheus to get this data.
	Query string `json:"query"`
	// RangesByCluster stores time ranges for which we've succeeded in getting this
	// data fromm Prometheus servers on the clusters we're querying.
	RangesByCluster map[string][]TimeRange `json:"ranges_by_cluster"`
	// Data holds the digested metric data, indexed by the metric fingerprint.
	// We digest data into log-linear histograms to allow for aggregation while
	// saving enormous amounts of space and incurring only minimal accuracy loss:
	// https://www.circonus.com/2018/11/the-problem-with-percentiles-aggregation-brings-aggravation/
	Data map[model.Fingerprint]*circonusllhist.Histogram `json:"data"`
	// DataByMetaData indexes the metric data by the full set of labels.
	// The list of fingerprints is guaranteed to be unique for any set of labels
	// and will never contain more than fifty items.
	DataByMetaData map[FullMetadata][]model.Fingerprint `json:"data_by_meta_data"`
}

// TimeRange describes a range of time, inclusive.
type TimeRange struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// FullMetadata identifies a container by all of the relevant labels, creating the
// most specific set of labels for any given container in our system. We can be
// certain that containers with the same labels at this level of specificity will
// execute with similar usage.
type FullMetadata struct {
	// Metadata identifies the ci-operator configuration for which this container ran.
	api.Metadata `json:"api_metadata"`
	// Target is the ci-operator --target for which this container ran.
	Target string `json:"target"`
	// Step is the multi-stage step for which this container ran, if any.
	Step string `json:"step,omitempty"`
	// Pod is the name of the pod which executed.
	Pod string `json:"pod"`
	// Container is the name of the container which executed.
	Container string `json:"container"`
}

func (m FullMetadata) LogFields() logrus.Fields {
	fields := api.LogFieldsFor(m.Metadata)
	fields["target"] = m.Target
	fields["step"] = m.Step
	fields["pod"] = m.Pod
	fields["container"] = m.Container
	return fields
}

func (m FullMetadata) StepMetadata() StepMetadata {
	return StepMetadata{
		Step:      m.Step,
		Container: m.Container,
	}
}

func (m *FullMetadata) String() string {
	suffix := ""
	if m.Metadata.Variant != "" {
		suffix = fmt.Sprintf("[%s]", m.Metadata.Variant)
	}
	return fmt.Sprintf("%s/%s@%s%s %s - %s[%s]", m.Metadata.Org, m.Metadata.Repo, m.Metadata.Branch, suffix, m.Target, m.Step, m.Container)
}

// MarshalText allows us to use this struct as a key in a JSON map when marshalling.
func (m FullMetadata) MarshalText() (text []byte, err error) {
	// We need a type alias to call json.Marshal on here, or the call will recurse
	// as json.Marshal will call MarshalText() if possible.
	type withoutMethod FullMetadata
	return json.Marshal(withoutMethod(m))
}

// UnmarshalText allows us to use this struct as a key in a JSON map when unmarshalling.
func (m *FullMetadata) UnmarshalText(text []byte) error {
	// We need a type alias to call json.Unmarshal on here, or the call will recurse
	// as json.Marshal will call UnmarshalText() if possible.
	type withoutMethod FullMetadata
	return json.Unmarshal(text, (*withoutMethod)(m))
}

// StepMetadata identifies a container running in the context of a step, where we
// expect that this container will execute in a similar way across all of the jobs
// for which it runs.
type StepMetadata struct {
	// Step is the multi-stage step for which this container ran, if any.
	Step string `json:"step,omitempty"`
	// Container is the name of the container which executed.
	Container string `json:"container"`
}

func (m *StepMetadata) String() string {
	return fmt.Sprintf("%s[%s]", m.Step, m.Container)
}

// MarshalText allows us to use this struct as a key in a JSON map when marshalling.
func (m StepMetadata) MarshalText() (text []byte, err error) {
	// We need a type alias to call json.Unmarshal on here, or the call will recurse
	// as json.Marshal will call UnmarshalText() if possible.
	type withoutMethod StepMetadata
	return json.Marshal(withoutMethod(m))
}

// UnmarshalText allows us to use this struct as a key in a JSON map when unmarshalling.
func (m *StepMetadata) UnmarshalText(text []byte) error {
	// We need a type alias to call json.Unmarshal on here, or the call will recurse
	// as json.Marshal will call UnmarshalText() if possible.
	type withoutMethod StepMetadata
	return json.Unmarshal(text, (*withoutMethod)(m))
}

func (m *StepMetadata) LogFields() logrus.Fields {
	return logrus.Fields{
		"step":      m.Step,
		"container": m.Container,
	}
}
