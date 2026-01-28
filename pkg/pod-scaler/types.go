package pod_scaler

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/openhistogram/circonusllhist"
	"github.com/prometheus/common/model"
	"github.com/sirupsen/logrus"

	prowv1 "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/kube"

	buildv1 "github.com/openshift/api/build/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/jobconfig"
	"github.com/openshift/ci-tools/pkg/steps"
	"github.com/openshift/ci-tools/pkg/steps/release"
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
	Data map[model.Fingerprint]*circonusllhist.HistogramWithoutLookups `json:"data"`
	// DataByMetaData indexes the metric data by the full set of labels.
	// The list of fingerprintTimes is guaranteed to be unique for any set of labels
	// and will never contain more than twenty-five items.
	DataByMetaData map[FullMetadata][]FingerprintTime `json:"data_by_meta_data"`
}

// FingerprintTime holds both the fingerprint for referencing the data, and the time at which it was added for later pruning
type FingerprintTime struct {
	// Fingerprint provides a hash-capable representation of a Metric.
	Fingerprint model.Fingerprint `json:"fingerprint"`
	// Added is the time which this was sourced. This is useful for later pruning of stale data.
	Added time.Time `json:"added"`
}

// Record adds the data in the matrix to the cache and records that the given cluster has
// successfully had this time range queried.
func (q *CachedQuery) Record(clusterName string, r TimeRange, matrix model.Matrix, logger *logrus.Entry) {
	q.RangesByCluster[clusterName] = coalesce(append(q.RangesByCluster[clusterName], r))

	for _, stream := range matrix {
		fingerprint := stream.Metric.Fingerprint()
		meta := metadataFromMetric(stream.Metric)
		// Metrics are unique in our dataset, so if we've already seen this metric/fingerprint,
		// we're guaranteed to already have recorded it in the indices, and we just need to add
		// the new data. This case will occur if one metric/fingerprint shows up in more than
		// one query range.
		seen := false
		var hist *circonusllhist.Histogram
		if existing, exists := q.Data[fingerprint]; exists {
			hist = existing.Histogram()
			seen = true
		} else {
			hist = circonusllhist.New(circonusllhist.NoLookup())
		}
		for _, value := range stream.Values {
			if math.IsNaN(float64(value.Value)) {
				continue
			}
			err := hist.RecordValue(float64(value.Value))
			if err != nil {
				logger.WithError(err).Warn("Failed to insert data into histogram. This should never happen.")
			}
		}
		q.Data[fingerprint] = circonusllhist.NewHistogramWithoutLookups(hist)
		if !seen {
			ft := FingerprintTime{
				Fingerprint: fingerprint,
				Added:       r.End, // We use the end time from the range as the added time, it is sufficient for pruning
			}
			q.DataByMetaData[meta] = append(q.DataByMetaData[meta], ft)
		}
	}
}

func metadataFromMetric(metric model.Metric) FullMetadata {
	rawMeta := FullMetadata{
		Metadata: api.Metadata{
			Org:     oneOf(metric, LabelNameOrg, ProwLabelNameOrg),
			Repo:    oneOf(metric, LabelNameRepo, ProwLabelNameRepo),
			Branch:  oneOf(metric, LabelNameBranch, ProwLabelNameBranch),
			Variant: string(metric[LabelNameVariant]),
		},
		Target:    oneOf(metric, LabelNameTarget, ProwLabelNameContext),
		Step:      string(metric[LabelNameStep]),
		Pod:       string(metric[LabelNamePod]),
		Container: string(metric[LabelNameContainer]),
	}
	// we know RPM repos, release Pods and Build Pods do not differ by target, so
	// we can remove those fields when we know we're looking at one of those
	_, buildPod := metric[LabelNameBuild]
	_, releasePod := metric[LabelNameRelease]
	value, set := metric[LabelNameApp]
	rpmRepoPod := set && value == model.LabelValue(steps.RPMRepoName)
	if buildPod || releasePod || rpmRepoPod {
		rawMeta.Target = ""
	}
	// RPM repo Pods are generated for a Deployment, so the name is random and not relevant
	if rpmRepoPod {
		rawMeta.Pod = ""
	}
	// we know the name for ProwJobs is not important
	if _, prowJob := metric[ProwLabelNameCreated]; prowJob {
		rawMeta.Pod = ""
		if rawMeta.Target == "" {
			// periodic and postsubmit jobs do not have a context, but we can try to
			// extract a useful name for the job by processing the full name, with the
			// caveat that labels have a finite length limit and the most specific data
			// is in the suffix of the job name, so we will alias jobs here whose names
			// are too long
			rawMeta.Target = syntheticContextFromJob(rawMeta.Metadata, metric)
		}
	}
	return rawMeta
}

func oneOf(metric model.Metric, labels ...model.LabelName) string {
	for _, label := range labels {
		if value, set := metric[label]; set {
			return string(value)
		}
	}
	return ""
}

func syntheticContextFromJob(meta api.Metadata, metric model.Metric) string {
	job, jobLabeled := metric[ProwLabelNameJob]
	if !jobLabeled {
		// this should not happen, but if it does, we can't deduce a job name
		return ""
	}
	jobType, typeLabeled := metric[ProwLabelNameType]
	if !typeLabeled {
		// this should not happen, but if it does, we can't deduce a job name
		return ""
	}
	if prowv1.ProwJobType(jobType) == prowv1.PeriodicJob && meta.Repo == "" {
		// this periodic has no repo associated with it, no use to strip any prefix
		return string(job)
	}
	var prefix string
	switch prowv1.ProwJobType(jobType) {
	case prowv1.PresubmitJob, prowv1.BatchJob:
		prefix = jobconfig.PresubmitPrefix
	case prowv1.PostsubmitJob:
		prefix = jobconfig.PostsubmitPrefix
	case prowv1.PeriodicJob:
		prefix = jobconfig.PeriodicPrefix
	default:
		// this should not happen, but if it does, we can't deduce a job name
		return ""
	}
	namePrefix := meta.JobName(prefix, "")
	if len(namePrefix) >= len(job) {
		// the job label truncated away any useful information we would have had
		return ""
	}
	return strings.TrimPrefix(string(job), namePrefix)
}

func MetadataFor(labels map[string]string, pod, container string) FullMetadata {
	metric := labelsToMetric(labels)
	metric[LabelNamePod] = model.LabelValue(pod)
	metric[LabelNameContainer] = model.LabelValue(container)
	return metadataFromMetric(metric)
}

func labelsToMetric(labels map[string]string) model.Metric {
	mapping := map[string]model.LabelName{
		kube.CreatedByProw:         ProwLabelNameCreated,
		kube.ContextAnnotation:     ProwLabelNameContext,
		kube.ProwJobAnnotation:     ProwLabelNameJob,
		kube.ProwJobTypeLabel:      ProwLabelNameType,
		kube.OrgLabel:              ProwLabelNameOrg,
		kube.RepoLabel:             ProwLabelNameRepo,
		kube.BaseRefLabel:          ProwLabelNameBranch,
		steps.LabelMetadataOrg:     LabelNameOrg,
		steps.LabelMetadataRepo:    LabelNameRepo,
		steps.LabelMetadataBranch:  LabelNameBranch,
		steps.LabelMetadataVariant: LabelNameVariant,
		steps.LabelMetadataTarget:  LabelNameTarget,
		steps.LabelMetadataStep:    LabelNameStep,
		buildv1.BuildLabel:         LabelNameBuild,
		release.Label:              LabelNameRelease,
		steps.AppLabel:             LabelNameApp,
	}
	output := model.Metric{}
	for key, value := range labels {
		mapped, recorded := mapping[key]
		if recorded {
			output[mapped] = model.LabelValue(value)
		}
	}
	return output
}

func UncoveredRanges(r TimeRange, coverage []TimeRange) []TimeRange {
	var covered []TimeRange
	for _, extent := range coverage {
		startsInside := within(extent.Start, r)
		endsInside := within(extent.End, r)
		switch {
		case startsInside && endsInside:
			covered = append(covered, extent)
		case startsInside && !endsInside:
			covered = append(covered, TimeRange{
				Start: extent.Start,
				End:   r.End,
			})
		case !startsInside && endsInside:
			covered = append(covered, TimeRange{
				Start: r.Start,
				End:   extent.End,
			})
		case extent.Start.Before(r.Start) && extent.End.After(r.End):
			covered = append(covered, TimeRange{
				Start: r.Start,
				End:   r.End,
			})
		}
	}
	sort.Slice(covered, func(i, j int) bool {
		return covered[i].Start.Before(covered[j].Start)
	})
	covered = coalesce(covered)

	if len(covered) == 0 {
		return []TimeRange{r}
	}
	var uncovered []TimeRange
	if !covered[0].Start.Equal(r.Start) {
		uncovered = append(uncovered, TimeRange{Start: r.Start, End: covered[0].Start})
	}
	for i := 0; i < len(covered)-1; i++ {
		uncovered = append(uncovered, TimeRange{Start: covered[i].End, End: covered[i+1].Start})
	}
	if !covered[len(covered)-1].End.Equal(r.End) {
		uncovered = append(uncovered, TimeRange{Start: covered[len(covered)-1].End, End: r.End})
	}
	return uncovered
}

// within determines if the time falls within the range
func within(t time.Time, r TimeRange) bool {
	return (r.Start.Equal(t) || r.Start.Before(t)) && (r.End.Equal(t) || r.End.After(t))
}

// coalesce minimizes the number of timeRanges that are needed to describe a set of times.
// The output is sorted by start time of the remaining ranges.
func coalesce(input []TimeRange) []TimeRange {
	for {
		coalesced := coalesceOnce(input)
		if len(coalesced) == len(input) {
			sort.Slice(coalesced, func(i, j int) bool {
				return coalesced[i].Start.Before(coalesced[j].Start)
			})
			return coalesced
		}
		input = coalesced
	}
}

func coalesceOnce(input []TimeRange) []TimeRange {
	for i := 0; i < len(input); i++ {
		for j := i; j < len(input); j++ {
			var coalesced *TimeRange
			if input[i].End.Equal(input[j].Start) {
				coalesced = &TimeRange{
					Start: input[i].Start,
					End:   input[j].End,
				}
			}
			if input[i].Start.Equal(input[j].End) {
				coalesced = &TimeRange{
					Start: input[j].Start,
					End:   input[i].End,
				}
			}
			if coalesced != nil {
				return append(input[:i], append(input[i+1:j], append(input[j+1:], *coalesced)...)...)
			}
		}
	}
	return input
}

// Prune ensures that no identifying set of labels contains more than twenty-five entries,
// as well as removing any data that was added more than 90 days ago.
// We know that an entry fingerprint can only exist for one fully-qualified label set,
// but if the label set contains a multi-stage step, it will also be referenced in
// the additional per-step index.
func (q *CachedQuery) Prune() {
	ninetyDaysAgo := time.Now().Add(-90 * 24 * time.Hour)
	q.prune(ninetyDaysAgo)
}

func (q *CachedQuery) prune(pruneBefore time.Time) {
	for meta, values := range q.DataByMetaData {
		var toRemove []FingerprintTime
		// First, prune to a max of 25 entries
		if num := len(values); num > 25 {
			toRemove = append(toRemove, values[0:num-25]...)
			q.DataByMetaData[meta] = values[num-25:]
		}
		// Next, remove any data older than the requested date
		for i := len(q.DataByMetaData[meta]) - 1; i >= 0; i-- {
			data := q.DataByMetaData[meta][i]
			if data.Added.Before(pruneBefore) {
				toRemove = append(toRemove, data)
				q.DataByMetaData[meta] = append(q.DataByMetaData[meta][:i], q.DataByMetaData[meta][i+1:]...)
			}
		}
		if len(toRemove) == 0 {
			continue
		}
		for _, item := range toRemove {
			delete(q.Data, item.Fingerprint)
		}
	}
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
	// Measured indicates if this pod was marked as measured (for accurate resource measurement)
	Measured bool `json:"measured,omitempty"`
	// WorkloadType is the type of workload (e.g., "builds", "tests") from the ci-workload label
	WorkloadType string `json:"workload_type,omitempty"`
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
