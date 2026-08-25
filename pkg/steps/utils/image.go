package utils

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	coreapi "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilnet "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/kubernetes"
	"github.com/openshift/ci-tools/pkg/metrics"
	"github.com/openshift/ci-tools/pkg/util"
)

func ImageDigestFor(client ctrlruntimeclient.Client, namespace func() string, name, tag string) func() (string, error) {
	return func() (string, error) {
		is := &imagev1.ImageStream{}
		if err := client.Get(context.TODO(), ctrlruntimeclient.ObjectKey{Namespace: namespace(), Name: name}, is); err != nil {
			return "", fmt.Errorf("could not retrieve output imagestream: %w", err)
		}
		var registry string
		if len(is.Status.PublicDockerImageRepository) > 0 {
			registry = is.Status.PublicDockerImageRepository
		} else if len(is.Status.DockerImageRepository) > 0 {
			registry = is.Status.DockerImageRepository
		} else {
			return "", fmt.Errorf("image stream %s has no accessible image registry value", name)
		}
		ref, image := FindStatusTag(is, tag)
		if len(image) > 0 {
			return fmt.Sprintf("%s@%s", registry, image), nil
		}
		if ref == nil && !hasSpecTag(is, tag) {
			return "", fmt.Errorf("image stream %q has no tag %q in spec or status", name, tag)
		}
		return fmt.Sprintf("%s:%s", registry, tag), nil
	}
}

func hasSpecTag(is *imagev1.ImageStream, tag string) bool {
	for _, t := range is.Spec.Tags {
		if t.Name == tag {
			return true
		}
	}
	return false
}

// FindSpecTag returns the spec tag's From reference when present.
func FindSpecTag(is *imagev1.ImageStream, tag string) *coreapi.ObjectReference {
	for _, t := range is.Spec.Tags {
		if t.Name != tag {
			continue
		}
		return t.From
	}
	return nil
}

// OfficialImageTagFrom returns an import source from spec, then status, then quay-proxy.
func OfficialImageTagFrom(source *imagev1.ImageStream, base api.ImageStreamTagReference) *coreapi.ObjectReference {
	if source != nil {
		if ref := FindSpecTag(source, base.Tag); ref != nil && ref.Name != "" && (ref.Kind != "DockerImage" || !strings.HasPrefix(ref.Name, api.ServiceDomainAPPCIRegistry+"/ocp/")) {
			return ref
		}
		if ref, _ := FindStatusTag(source, base.Tag); ref != nil && ref.Name != "" && (ref.Kind != "DockerImage" || !strings.HasPrefix(ref.Name, api.ServiceDomainAPPCIRegistry+"/ocp/")) {
			return ref
		}
	}
	return &coreapi.ObjectReference{Kind: "DockerImage", Name: api.QuayImageReference(base)}
}

// TagImportReferencePolicy returns Source for external DockerImage refs, Local for in-cluster tags.
func TagImportReferencePolicy(from *coreapi.ObjectReference) imagev1.TagReferencePolicyType {
	if from != nil && from.Kind == "DockerImage" {
		name := from.Name
		if strings.HasPrefix(name, api.QCIAPPCIDomain) || strings.HasPrefix(name, api.QuayOpenShiftCIRepo) || strings.HasPrefix(name, "quay.io/") {
			return imagev1.SourceTagReferencePolicy
		}
	}
	return imagev1.LocalTagReferencePolicy
}

// PullSpecForImageStreamTag returns a pull spec for an imagestream tag, resolving reference-only
// tags via spec/status/quay-proxy when the tag has no local image content.
func PullSpecForImageStreamTag(registryURL string, source *imagev1.ImageStream, isTag *imagev1.ImageStreamTag) string {
	nameParts := strings.SplitN(isTag.Name, ":", 2)
	if len(nameParts) == 2 && source != nil {
		for _, t := range source.Spec.Tags {
			if t.Name == nameParts[1] && t.Reference && t.From != nil && t.From.Kind == "DockerImage" && t.From.Name != "" {
				return t.From.Name
			}
		}
	}
	if isTag.Image.ObjectMeta.Name != "" {
		return registryURL + "/" + isTag.Namespace + "/" + strings.Split(isTag.Name, ":")[0] + "@" + isTag.Image.ObjectMeta.Name
	}
	if len(nameParts) != 2 {
		return ""
	}
	base := api.ImageStreamTagReference{Namespace: isTag.Namespace, Name: nameParts[0], Tag: nameParts[1]}
	ref := OfficialImageTagFrom(source, base)
	switch ref.Kind {
	case "DockerImage":
		return ref.Name
	case "ImageStreamImage":
		if ref.Namespace != "" {
			return registryURL + "/" + ref.Namespace + "/" + ref.Name
		}
		return registryURL + "/" + isTag.Namespace + "/" + ref.Name
	default:
		return ref.Name
	}
}

// ResolveOfficialInputFrom resolves official ocp inputs: stable in job ns, then spec/status/quay on source IS.
// When ok is false, callers use QuayImageReference with Source policy (e.g. 4.23, 5.0).
func ResolveOfficialInputFrom(ctx context.Context, client ctrlruntimeclient.Client, jobNamespace string, base api.ImageStreamTagReference) (*coreapi.ObjectReference, bool, error) {
	if !api.RefersToOfficialImage(base.Namespace, api.WithoutOKD) {
		return nil, false, nil
	}
	if base.Name == api.StableImageStream || api.IsReleaseStream(base.Name) {
		stable := &imagev1.ImageStream{}
		if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: jobNamespace, Name: api.StableImageStream}, stable); err == nil {
			if _, exists, _ := util.ResolvePullSpec(stable, base.Tag, true); exists {
				return &coreapi.ObjectReference{
					Kind:      "ImageStreamTag",
					Namespace: jobNamespace,
					Name:      fmt.Sprintf("%s:%s", api.StableImageStream, base.Tag),
				}, true, nil
			}
		} else if !kerrors.IsNotFound(err) {
			return nil, false, fmt.Errorf("get stable imagestream in %s: %w", jobNamespace, err)
		}
	}
	source := &imagev1.ImageStream{}
	if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: base.Namespace, Name: base.Name}, source); err != nil && !kerrors.IsNotFound(err) {
		return nil, false, fmt.Errorf("get source imagestream %s: %w", base.ISTagName(), err)
	}
	return OfficialImageTagFrom(source, base), true, nil
}

// FindStatusTag returns an object reference to a tag if
// it exists in the ImageStream's Spec
func FindStatusTag(is *imagev1.ImageStream, tag string) (*coreapi.ObjectReference, string) {
	for _, t := range is.Status.Tags {
		if t.Tag != tag {
			continue
		}
		if len(t.Items) == 0 {
			return nil, ""
		}
		if len(t.Items[0].Image) == 0 {
			return &coreapi.ObjectReference{
				Kind: "DockerImage",
				Name: t.Items[0].DockerImageReference,
			}, ""
		}
		return &coreapi.ObjectReference{
			Kind:      "ImageStreamImage",
			Namespace: is.Namespace,
			Name:      fmt.Sprintf("%s@%s", is.Name, t.Items[0].Image),
		}, t.Items[0].Image
	}
	return nil, ""
}

const DefaultImageImportTimeout = 45 * time.Minute

type imageTagImporter func(context.Context, ctrlruntimeclient.Client, string, string, string, string, int, *metrics.MetricsAgent) (string, error)

func getEvaluator(ctx context.Context, client ctrlruntimeclient.Client, ns, name string, tags sets.Set[string], waitForSpecTags bool, metricsAgent *metrics.MetricsAgent) func(obj runtime.Object) (bool, error) {
	return getEvaluatorWithImporter(ctx, client, ns, name, tags, waitForSpecTags, metricsAgent, ImportTagWithRetries)
}

func getEvaluatorWithImporter(ctx context.Context, client ctrlruntimeclient.Client, ns, name string, tags sets.Set[string], waitForSpecTags bool, metricsAgent *metrics.MetricsAgent, importer imageTagImporter) func(obj runtime.Object) (bool, error) {
	return func(obj runtime.Object) (bool, error) {
		switch stream := obj.(type) {
		case *imagev1.ImageStream:
			checkedTags := sets.New[string]()
			for i, tag := range stream.Spec.Tags {
				if tags.Len() > 0 {
					if tags.Has(tag.Name) {
						checkedTags.Insert(tag.Name)
					} else {
						continue
					}
				}
				_, exist, condition := util.ResolvePullSpec(stream, tag.Name, true)
				if !exist {
					logrus.WithField("conditionMessage", condition.Message).Debugf("Waiting to import tag[%d] on imagestream %s/%s:%s ...", i, stream.Namespace, stream.Name, tag.Name)
					if strings.Contains(condition.Message, "Internal error occurred") {
						if tag.From == nil {
							// should never happen
							return false, fmt.Errorf("failed to determine the source of the tag %s/%s:%s", stream.Namespace, stream.Name, tag.Name)
						}
						if tag.From.Kind != "DockerImage" {
							// should never happen
							return false, fmt.Errorf("failed to import tag %s/%s:%s from an unexpected tag source %v", stream.Namespace, stream.Name, tag.Name, *tag.From)
						}
						if tag.From.Name == "" {
							// should never happen
							return false, fmt.Errorf("failed to import tag %s/%s:%s from an empty source", stream.Namespace, stream.Name, tag.Name)
						}
						if _, err := importer(ctx, client, ns, name, tag.Name, tag.From.Name, api.ImageStreamImportRetries, metricsAgent); err != nil {
							if isTransientImageImportError(err) {
								logrus.WithField("error_class", "transient_import_exhausted").Warnf("Failed to reimport tag %s/%s:%s after a transient registry error, continuing to wait", stream.Namespace, stream.Name, tag.Name)
								return false, nil
							}
							return false, fmt.Errorf("failed to reimport the tag %s/%s:%s: %w", stream.Namespace, stream.Name, tag.Name, err)
						}
					}
					return false, nil
				}
			}
			if diff := tags.Difference(checkedTags); diff.Len() > 0 {
				l := diff.UnsortedList()
				sort.Strings(l)
				if waitForSpecTags {
					logrus.Debugf("Waiting for tag definition(s) [%s] on image stream %s/%s ...", strings.Join(l, ","), stream.Namespace, stream.Name)
					return false, nil
				}
				return false, fmt.Errorf("failed to import tag(s) [%s] on image stream %s/%s because of missing definition in the spec", strings.Join(l, ","), stream.Namespace, stream.Name)
			}
			return true, nil
		default:
			return false, fmt.Errorf("imagestream %s/%s got an event that did not contain an imagestream: %v", ns, name, obj)
		}
	}
}

type importWaitOptions struct {
	waitForSpecTags bool
}

// ImportWaitOption configures an image stream tag import wait.
type ImportWaitOption func(*importWaitOptions)

// WaitForSpecTags keeps watching when requested tags are not yet visible in
// the image stream spec. The import timeout also bounds spec visibility.
func WaitForSpecTags() ImportWaitOption {
	return func(options *importWaitOptions) {
		options.waitForSpecTags = true
	}
}

// WaitForImportingISTag waits for the tags on the image stream are imported.
func WaitForImportingISTag(ctx context.Context, client ctrlruntimeclient.WithWatch, ns, name string, into *imagev1.ImageStream, tags sets.Set[string], timeout time.Duration, metricsAgent *metrics.MetricsAgent, optionFns ...ImportWaitOption) error {
	options := importWaitOptions{}
	for _, optionFn := range optionFns {
		optionFn(&options)
	}
	startTime := time.Now()

	obj := into
	if obj == nil {
		obj = &imagev1.ImageStream{}
	}
	err := kubernetes.WaitForConditionOnObject(ctx, client, ctrlruntimeclient.ObjectKey{Namespace: ns, Name: name}, &imagev1.ImageStreamList{}, obj, getEvaluator(ctx, client, ns, name, tags, options.waitForSpecTags, metricsAgent), timeout)

	completionTime := time.Now()
	duration := completionTime.Sub(startTime)

	for tag := range tags {
		metricsAgent.Record(&metrics.TagImportEvent{
			Namespace:       ns,
			ImageStreamName: name,
			TagName:         tag,
			FullTagName:     ns + "/" + name + ":" + tag,
			StartTime:       startTime,
			CompletionTime:  completionTime,
			DurationSeconds: duration.Seconds(),
			Success:         err == nil,
			Error: func() string {
				if err != nil {
					return err.Error()
				}
				return ""
			}(),
		})
	}

	return err
}

type importRetrySleep func(context.Context, time.Duration) error

func sleepForImageImportRetry(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func exponentialImageImportRetryDelays(attempts int) []time.Duration {
	if attempts < 2 {
		return nil
	}
	delays := make([]time.Duration, 0, attempts-1)
	delay := time.Second
	for len(delays) < attempts-1 {
		delays = append(delays, delay)
		delay *= 2
	}
	return delays
}

// ReleaseImportRetryDelays returns the extended, jittered retry schedule used
// by both phases of release import. Jitter prevents concurrent jobs from
// synchronizing their requests during a registry or API outage.
func ReleaseImportRetryDelays() []time.Duration {
	delays := exponentialImageImportRetryDelays(9)
	for i := range delays {
		delays[i] = wait.Jitter(delays[i], 0.1)
	}
	return delays
}

type transientImageImportError struct {
	err error
}

func (e *transientImageImportError) Error() string { return e.err.Error() }
func (e *transientImageImportError) Unwrap() error { return e.err }

func isTransientImageImportError(err error) bool {
	var transientErr *transientImageImportError
	return errors.As(err, &transientErr)
}

func isRetryableImageImportAPIError(err error) bool {
	return utilnet.IsConnectionReset(err) ||
		utilnet.IsProbableEOF(err) ||
		utilnet.IsTimeout(err) ||
		kerrors.IsConflict(err) ||
		kerrors.IsTooManyRequests(err) ||
		kerrors.IsServerTimeout(err) ||
		kerrors.IsTimeout(err) ||
		kerrors.IsInternalError(err) ||
		kerrors.IsServiceUnavailable(err) ||
		kerrors.IsUnexpectedServerError(err)
}

func imageImportRetryErrorClass(err error) string {
	switch {
	case err == nil:
		return "status_not_ready"
	case utilnet.IsConnectionReset(err):
		return "connection_reset"
	case utilnet.IsProbableEOF(err):
		return "connection_closed"
	case utilnet.IsTimeout(err):
		return "network_timeout"
	default:
		reason := kerrors.ReasonForError(err)
		if reason == meta.StatusReasonUnknown {
			return "api_error"
		}
		return string(reason)
	}
}

func imageImportRetryDelay(err error, configured time.Duration) time.Duration {
	if seconds, suggested := kerrors.SuggestsClientDelay(err); suggested {
		serverDelay := time.Duration(seconds) * time.Second
		if serverDelay > configured {
			return serverDelay
		}
	}
	return configured
}

// ImportTagWithRetries imports image with retries
func ImportTagWithRetries(ctx context.Context, client ctrlruntimeclient.Client, ns, name, tag, sourcePullSpec string, retries int, metricsAgent *metrics.MetricsAgent) (string, error) {
	if retries < 1 {
		return importTagWithRetryDelays(ctx, client, ns, name, tag, sourcePullSpec, nil, sleepForImageImportRetry, false, metricsAgent, 0)
	}
	return importTagWithRetryDelays(ctx, client, ns, name, tag, sourcePullSpec, exponentialImageImportRetryDelays(retries), sleepForImageImportRetry, false, metricsAgent, retries)
}

// ImportTagWithRetryDelays imports an image using the provided delays between attempts.
func ImportTagWithRetryDelays(ctx context.Context, client ctrlruntimeclient.Client, ns, name, tag, sourcePullSpec string, retryDelays []time.Duration, metricsAgent *metrics.MetricsAgent) (string, error) {
	return importTagWithRetryDelays(ctx, client, ns, name, tag, sourcePullSpec, retryDelays, sleepForImageImportRetry, true, metricsAgent, len(retryDelays)+1)
}

func importTagWithRetryDelays(ctx context.Context, client ctrlruntimeclient.Client, ns, name, tag, sourcePullSpec string, retryDelays []time.Duration, sleep importRetrySleep, logRetries bool, metricsAgent *metrics.MetricsAgent, attempts int) (string, error) {
	if sourcePullSpec == "" {
		return "", fmt.Errorf("sourcePullSpec cannot be empty")
	}
	if attempts != len(retryDelays)+1 && attempts != 0 {
		return "", fmt.Errorf("invalid image import retry policy: %d attempts require %d delays, got %d", attempts, attempts-1, len(retryDelays))
	}
	for _, delay := range retryDelays {
		if delay < 0 {
			return "", fmt.Errorf("invalid image import retry policy: delay %s must not be negative", delay)
		}
	}
	startTime := time.Now()
	var pullSpec string
	step := 0
	retryCount := 0
	logger := logrus.WithField("tag", fmt.Sprintf(" %s/%s:%s", ns, name, tag))
	var importErr error
	if attempts < 1 {
		importErr = wait.ErrWaitTimeout
	}
	for step < attempts {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("unable to import tag %s/%s:%s before import (%d): %w", ns, name, tag, step, err)
		}
		logger.WithField("step", step).Debug("Retrying importing tag ...")
		retryCount = step
		streamImport := &imagev1.ImageStreamImport{
			ObjectMeta: meta.ObjectMeta{
				Namespace: ns,
				Name:      name,
			},
			Spec: imagev1.ImageStreamImportSpec{
				Import: true,
				Images: []imagev1.ImageImportSpec{
					{
						To: &coreapi.LocalObjectReference{
							Name: tag,
						},
						From: coreapi.ObjectReference{
							Kind: "DockerImage",
							Name: sourcePullSpec,
						},
						ImportPolicy:    imagev1.TagImportPolicy{ImportMode: imagev1.ImportModePreserveOriginal},
						ReferencePolicy: imagev1.TagReferencePolicy{Type: imagev1.SourceTagReferencePolicy},
					},
				},
			},
		}
		step = step + 1
		attemptErr := client.Create(ctx, streamImport)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", fmt.Errorf("unable to import tag %s/%s:%s at import (%d): %w", ns, name, tag, step-1, ctxErr)
		}
		if attemptErr != nil {
			if !isRetryableImageImportAPIError(attemptErr) {
				return "", fmt.Errorf("unable to import tag %s/%s:%s at import (%d): %w", ns, name, tag, step-1, attemptErr)
			}
			logger.WithFields(logrus.Fields{"error_class": imageImportRetryErrorClass(attemptErr), "step": step - 1}).Debug("Transient image stream import API error")
		} else if len(streamImport.Status.Images) == 0 {
			logger.WithField("step", step-1).Debug("Imports' status has no images")
		} else {
			image := streamImport.Status.Images[0]
			if image.Image != nil {
				pullSpec = image.Image.DockerImageReference
				logrus.Debugf("Imported tag %s/%s:%s at import (%d)", ns, name, tag, step-1)
				importErr = nil
				break
			}
			if image.Status.Reason != "" || image.Status.Status == meta.StatusFailure {
				statusErr := &kerrors.StatusError{ErrStatus: image.Status}
				if !isRetryableImageImportAPIError(statusErr) {
					return "", fmt.Errorf("unable to import tag %s/%s:%s at import (%d): %w", ns, name, tag, step-1, statusErr)
				}
				attemptErr = statusErr
			}
			logger.WithField("step", step-1).Debug("Imports' status' image is nil")
		}

		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", fmt.Errorf("unable to import tag %s/%s:%s after import (%d): %w", ns, name, tag, step-1, ctxErr)
		}
		if step == attempts {
			exhaustionErr := errors.Join(wait.ErrWaitTimeout, attemptErr)
			importErr = &transientImageImportError{err: exhaustionErr}
			logger.WithFields(logrus.Fields{"attempts": step, "error_class": imageImportRetryErrorClass(attemptErr)}).Error("Image stream import retry attempts exhausted")
			break
		}
		delay := imageImportRetryDelay(attemptErr, retryDelays[step-1])
		if logRetries {
			logger.WithFields(logrus.Fields{"attempt": step, "delay": delay, "error_class": imageImportRetryErrorClass(attemptErr)}).Warn("Image stream import did not succeed, retrying")
		}
		if err := sleep(ctx, delay); err != nil {
			return "", fmt.Errorf("unable to import tag %s/%s:%s while waiting to retry import (%d): %w", ns, name, tag, step-1, err)
		}
	}
	if importErr != nil {
		var conditionMsg string
		imagestream := imagev1.ImageStream{}
		if err := client.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: ns, Name: name}, &imagestream); err != nil {
			logger.WithError(err).Debug("Failed to get image stream for the tag")
		} else {
			for _, t := range imagestream.Status.Tags {
				if t.Tag == tag {
					if len(t.Conditions) > 0 {
						conditionMsg = t.Conditions[0].Message
					}
					break
				}
			}
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", fmt.Errorf("unable to import tag %s/%s:%s while collecting terminal status after %d imports: %w", ns, name, tag, step, ctxErr)
		}
		if conditionMsg == "" {
			return "", fmt.Errorf("unable to import tag %s/%s:%s even after (%d) imports: %w", ns, name, tag, step, importErr)
		}
		return "", fmt.Errorf("unable to import tag %s/%s:%s with message %s on the image stream even after (%d) imports: %w", ns, name, tag, conditionMsg, step, importErr)
	}

	completionTime := time.Now()
	duration := completionTime.Sub(startTime)
	metricsAgent.Record(&metrics.TagImportEvent{
		Namespace:       ns,
		ImageStreamName: name,
		TagName:         tag,
		FullTagName:     ns + "/" + name + ":" + tag,
		SourceImage:     sourcePullSpec,
		SourceImageKind: "DockerImage",
		StartTime:       startTime,
		CompletionTime:  completionTime,
		DurationSeconds: duration.Seconds(),
		RetryCount:      retryCount,
		Success:         true,
	})

	return pullSpec, nil
}
