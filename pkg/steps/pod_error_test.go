package steps

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"testing"

	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type podStepTypedNilCause struct{}

func (*podStepTypedNilCause) Error() string { panic("typed nil cause must not be formatted") }

func TestPodStepErrorContract(t *testing.T) {
	cause := errors.New("pod failed")
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "test", Name: "failed-pod"}}
	err := fmt.Errorf("outer context: %w", &PodStepError{Pod: pod, err: cause})

	var podStepErr *PodStepError
	if !errors.As(err, &podStepErr) {
		t.Fatalf("expected errors.As to find PodStepError in %v", err)
	}
	if podStepErr.Pod != pod {
		t.Fatalf("expected observed pod to be retained, got %#v", podStepErr.Pod)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("expected errors.Is to find wrapped cause in %v", err)
	}
}

func TestPodStepErrorNilSafety(t *testing.T) {
	var nilWrapper *PodStepError
	var typedNilCause *podStepTypedNilCause
	for _, testCase := range []struct {
		name string
		err  *PodStepError
	}{
		{name: "nil receiver", err: nilWrapper},
		{name: "nil cause", err: &PodStepError{}},
		{name: "typed nil cause", err: &PodStepError{err: typedNilCause}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := testCase.err.Error(); got != "pod step failed" {
				t.Fatalf("Error() = %q, want %q", got, "pod step failed")
			}
			if got := testCase.err.Unwrap(); got != nil {
				t.Fatalf("Unwrap() = %v, want nil", got)
			}
		})
	}
}

func TestPodCleanupErrorLogIsSanitized(t *testing.T) {
	const (
		internalHost = "api.internal.example.test"
		podName      = "release-images-latest"
		stepName     = "release"
	)
	transportErr := fmt.Errorf(`Delete "https://%s:6443/api/v1/namespaces/ci/pods/%s": %w`, internalHost, podName, syscall.ECONNREFUSED)

	var logs bytes.Buffer
	logger := logrus.StandardLogger()
	originalOutput, originalFormatter := logger.Out, logger.Formatter
	logger.SetOutput(&logs)
	logger.SetFormatter(&logrus.TextFormatter{DisableColors: true, DisableTimestamp: true})
	defer func() {
		logger.SetOutput(originalOutput)
		logger.SetFormatter(originalFormatter)
	}()

	logPodCleanupError(stepName, podName, transportErr)
	output := logs.String()
	for _, sensitive := range []string{internalHost, "https://", "/api/v1/"} {
		if strings.Contains(output, sensitive) {
			t.Fatalf("cleanup log contains sensitive transport detail %q: %s", sensitive, output)
		}
	}
	for _, safe := range []string{"error_class=connection_refused", "pod=" + podName, "step=" + stepName} {
		if !strings.Contains(output, safe) {
			t.Fatalf("cleanup log is missing safe field %q: %s", safe, output)
		}
	}
}

func TestPodCleanupErrorClass(t *testing.T) {
	for _, testCase := range []struct {
		name string
		err  error
		want string
	}{
		{name: "connection refused", err: syscall.ECONNREFUSED, want: "connection_refused"},
		{name: "HTTP2 connection lost", err: errors.New("http2: client connection lost"), want: "http2_connection_lost"},
		{name: "API timeout", err: context.DeadlineExceeded, want: "api_timeout"},
		{name: "unknown", err: errors.New("cleanup failed"), want: "unknown"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := podCleanupErrorClass(testCase.err); got != testCase.want {
				t.Fatalf("podCleanupErrorClass() = %q, want %q", got, testCase.want)
			}
		})
	}
}
