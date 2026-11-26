package prowjobrunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilnet "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/pjutil"

	"github.com/openshift/ci-tools/pkg/util"
)

// prowJobClient creates ProwJobs and reads their latest status from the Prow cluster.
type prowJobClient interface {
	Create(context.Context, client.Object, ...client.CreateOption) error
	Get(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error
}

// pendingJob holds the last observed job and any submission error awaiting confirmation by name.
type pendingJob struct {
	job       *prowapi.ProwJob
	createErr error
}

// jobExecution tracks submitted jobs until Prow marks them complete.
type jobExecution struct {
	client  prowJobClient
	pending []pendingJob
	errors  []error
}

// newProwJobClient loads KUBECONFIG or in-cluster credentials and creates a ProwJob client.
func newProwJobClient() (client.Client, error) {
	config, err := util.LoadClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("loading Prow cluster credentials: %w", err)
	}
	scheme := runtime.NewScheme()
	if err := prowapi.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("registering ProwJob API: %w", err)
	}
	pjclient, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("creating ProwJob client: %w", err)
	}
	return pjclient, nil
}

// execute submits every job and waits for all successful submissions, aggregating submission and job failures.
func execute(ctx context.Context, pjclient prowJobClient, jobs []*prowapi.ProwJob, interval time.Duration) error {
	execution := jobExecution{client: pjclient}
	for _, job := range jobs {
		if err := ctx.Err(); err != nil {
			execution.errors = append(execution.errors, fmt.Errorf("submitting ProwJobs: %w", err))
			break
		}
		submitted := job.DeepCopy()
		logger := logrus.WithFields(pjutil.ProwJobFields(submitted)).WithField("namespace", submitted.Namespace)
		if err := pjclient.Create(ctx, submitted); err != nil {
			createErr := fmt.Errorf("submitting %s (%s/%s): %w", submitted.Spec.Job, submitted.Namespace, submitted.Name, err)
			var status apierrors.APIStatus
			if errors.As(err, &status) && !retryableError(err) {
				logger.WithError(err).Error("ProwJob submission rejected")
				execution.errors = append(execution.errors, createErr)
				continue
			}
			// The server may have committed the create before the response was lost.
			logger.WithError(err).Warn("ProwJob submission unconfirmed; will check by name")
			execution.pending = append(execution.pending, pendingJob{job: job, createErr: createErr})
			continue
		}
		logger.Info("Submitted ProwJob")
		execution.pending = append(execution.pending, pendingJob{job: submitted})
	}
	if len(execution.pending) > 0 {
		logrus.WithField("job_count", len(execution.pending)).Info("Waiting for ProwJobs to complete")
		if err := wait.PollUntilContextCancel(ctx, interval, true, execution.poll); err != nil {
			execution.errors = append(execution.errors, fmt.Errorf("waiting for ProwJobs: %w", err))
		}
	}
	return utilerrors.NewAggregate(execution.errors)
}

// retryableError identifies temporary API server and transport errors for which another read can succeed.
func retryableError(err error) bool {
	var status apierrors.APIStatus
	if errors.As(err, &status) {
		code := status.Status().Code
		return code >= http.StatusInternalServerError || code == http.StatusRequestTimeout ||
			apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) || apierrors.IsTooManyRequests(err)
	}
	return utilnet.IsTimeout(err) || utilnet.IsConnectionRefused(err) || utilnet.IsHTTP2ConnectionLost(err) ||
		utilnet.IsProbableEOF(err) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

// poll retries transient reads and records per-job errors or completed results. Only cancellation stops the batch.
func (e *jobExecution) poll(ctx context.Context) (bool, error) {
	pending := e.pending[:0]
	for _, entry := range e.pending {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		previous := entry.job
		key := client.ObjectKeyFromObject(previous)
		logger := logrus.WithFields(pjutil.ProwJobFields(previous)).WithField("namespace", key.Namespace)
		var job prowapi.ProwJob
		if err := e.client.Get(ctx, key, &job); err != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			if retryableError(err) {
				logger.WithError(err).Warn("Retrying ProwJob status read on next poll")
				pending = append(pending, entry)
				continue
			}
			logger.WithError(err).Error("Cannot read ProwJob")
			if entry.createErr != nil {
				e.errors = append(e.errors, entry.createErr)
			}
			e.errors = append(e.errors, fmt.Errorf("reading %s (%s): %w", previous.Spec.Job, key, err))
			continue
		}
		logger = logger.WithFields(logrus.Fields{"state": job.Status.State, "url": job.Status.URL})
		if entry.createErr != nil {
			logger.Info("Confirmed ProwJob submission")
		}
		if !job.Complete() {
			if previous.Status.State != job.Status.State {
				logger.Info("ProwJob state changed")
			}
			pending = append(pending, pendingJob{job: &job})
			continue
		}
		if job.Status.State != prowapi.SuccessState {
			logger.Error("ProwJob failed")
			e.errors = append(e.errors, fmt.Errorf("%s (%s) finished with state %s", job.Spec.Job, key, job.Status.State))
			continue
		}
		logger.Info("ProwJob succeeded")
	}
	e.pending = pending
	return len(e.pending) == 0, nil
}
