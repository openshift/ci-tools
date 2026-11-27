// Package execution submits ProwJobs and tracks their completion in the infrastructure cluster.
package execution

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilnet "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	prowapi "sigs.k8s.io/prow/pkg/apis/prowjobs/v1"
	"sigs.k8s.io/prow/pkg/pjutil"
)

// Client creates ProwJobs and reads their latest status from the Prow cluster.
type Client interface {
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
	client  Client
	pending []pendingJob
	errors  []error
}

// jobLogger identifies a job consistently from submission through completion.
func jobLogger(job *prowapi.ProwJob) *logrus.Entry {
	return logrus.WithFields(pjutil.ProwJobFields(job)).WithField("namespace", job.Namespace)
}

// SubmitAndWait submits every job and waits for accepted submissions, aggregating submission and job failures.
// It polls at interval and stops waiting when ctx is canceled without deleting submitted jobs.
func SubmitAndWait(ctx context.Context, pjclient Client, jobs []*prowapi.ProwJob, interval time.Duration) error {
	execution := jobExecution{client: pjclient}
	execution.submit(ctx, jobs)
	return execution.wait(ctx, interval)
}

// submit creates all requested jobs, retaining uncertain submissions for confirmation during polling.
func (e *jobExecution) submit(ctx context.Context, jobs []*prowapi.ProwJob) {
	for _, job := range jobs {
		if err := ctx.Err(); err != nil {
			e.errors = append(e.errors, fmt.Errorf("submitting ProwJobs: %w", err))
			break
		}
		submitted := job.DeepCopy()
		logger := jobLogger(submitted)
		if err := e.client.Create(ctx, submitted); err != nil {
			createErr := fmt.Errorf("submitting %s (%s/%s): %w", submitted.Spec.Job, submitted.Namespace, submitted.Name, err)
			var status apierrors.APIStatus
			if errors.As(err, &status) && !retryableError(err) {
				logger.WithError(err).Error("ProwJob submission rejected")
				e.errors = append(e.errors, createErr)
				continue
			}
			// The server may have committed the create before the response was lost.
			logger.WithError(err).Warn("ProwJob submission unconfirmed; will check by name")
			e.pending = append(e.pending, pendingJob{job: job, createErr: createErr})
			continue
		}
		logger.Info("Submitted ProwJob")
		e.pending = append(e.pending, pendingJob{job: submitted})
	}
}

// wait polls outstanding jobs and returns all observed submission, read, and completion errors.
func (e *jobExecution) wait(ctx context.Context, interval time.Duration) error {
	if len(e.pending) > 0 {
		logrus.WithField("job_count", len(e.pending)).Info("Waiting for ProwJobs to complete")
		if err := wait.PollUntilContextCancel(ctx, interval, true, e.poll); err != nil {
			e.errors = append(e.errors, fmt.Errorf("waiting for ProwJobs: %w", err))
		}
	}
	return utilerrors.NewAggregate(e.errors)
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

// poll retains jobs that need another observation. Only cancellation stops the batch.
func (e *jobExecution) poll(ctx context.Context) (bool, error) {
	pending := e.pending[:0]
	for _, entry := range e.pending {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		done, err := entry.refresh(ctx, e.client)
		if err != nil {
			if !done {
				return false, err
			}
			if entry.createErr != nil {
				e.errors = append(e.errors, entry.createErr)
			}
			e.errors = append(e.errors, err)
		}
		if !done {
			pending = append(pending, entry)
		}
	}
	e.pending = pending
	return len(e.pending) == 0, nil
}

// refresh updates the last observation and returns whether tracking is finished. An error
// is final for this job unless the read was interrupted by context cancellation.
func (p *pendingJob) refresh(ctx context.Context, pjclient Client) (bool, error) {
	key := client.ObjectKeyFromObject(p.job)
	logger := jobLogger(p.job)
	var job prowapi.ProwJob
	if err := pjclient.Get(ctx, key, &job); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, ctxErr
		}
		if retryableError(err) {
			logger.WithError(err).Warn("Retrying ProwJob status read on next poll")
			return false, nil
		}
		logger.WithError(err).Error("Cannot read ProwJob")
		return true, fmt.Errorf("reading %s (%s): %w", p.job.Spec.Job, key, err)
	}
	logger = logger.WithFields(logrus.Fields{"state": job.Status.State, "url": job.Status.URL})
	if p.createErr != nil {
		logger.Info("Confirmed ProwJob submission")
		p.createErr = nil
	}
	previousState := p.job.Status.State
	p.job = &job
	if !job.Complete() {
		if previousState != job.Status.State {
			logger.Info("ProwJob state changed")
		}
		return false, nil
	}
	if job.Status.State != prowapi.SuccessState {
		logger.Error("ProwJob failed")
		return true, fmt.Errorf("%s (%s) finished with state %s", job.Spec.Job, key, job.Status.State)
	}
	logger.Info("ProwJob succeeded")
	return true, nil
}
