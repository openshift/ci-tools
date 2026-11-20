package release

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	imagev1 "github.com/openshift/api/image/v1"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/steps/loggingclient"
	testhelperkube "github.com/openshift/ci-tools/pkg/testhelper/kubernetes"
)

const testCLIImage = "quay.io/test/cli:latest"

func TestReleaseExtractRetryClassification(t *testing.T) {
	testCases := []struct {
		name           string
		errorText      string
		wantSuccess    bool
		wantAttempts   int
		wantDiagnostic string
	}{
		{
			name:           "authentication markers in URL do not suppress GOAWAY retry",
			errorText:      `Get "https://unauthorized.registry.example/v2/forbidden/repo/blobs/sha256:abc": http2: server sent GOAWAY and closed the connection; LastStreamID=9, ErrCode=NO_ERROR`,
			wantSuccess:    true,
			wantAttempts:   2,
			wantDiagnostic: "Release extraction error is retryable; retrying in 5s.",
		},
		{
			name:           "HTTP 500 response body parse error is retryable",
			errorText:      `error parsing HTTP 500 response body: invalid character '<' looking for beginning of value: "<html>"`,
			wantSuccess:    true,
			wantAttempts:   2,
			wantDiagnostic: "Release extraction error is retryable; retrying in 5s.",
		},
		{
			name:           "HTTP 502 response body parse error is retryable",
			errorText:      `error parsing HTTP 502 response body: invalid character '<' looking for beginning of value: "<html>"`,
			wantSuccess:    true,
			wantAttempts:   2,
			wantDiagnostic: "Release extraction error is retryable; retrying in 5s.",
		},
		{
			name:           "HTTP 503 HTML response body parse error is retryable",
			errorText:      `error parsing HTTP 503 response body: invalid character '<' looking for beginning of value: "<html><title>503 Service Unavailable</title>"`,
			wantSuccess:    true,
			wantAttempts:   2,
			wantDiagnostic: "Release extraction error is retryable; retrying in 5s.",
		},
		{
			name:           "HTTP 504 response body parse error is retryable",
			errorText:      `error parsing HTTP 504 response body: invalid character '<' looking for beginning of value: "<html>"`,
			wantSuccess:    true,
			wantAttempts:   2,
			wantDiagnostic: "Release extraction error is retryable; retrying in 5s.",
		},
		{
			name:           "eligible unexpected HTTP status is retryable",
			errorText:      "received unexpected HTTP status: 503 Service Unavailable",
			wantSuccess:    true,
			wantAttempts:   2,
			wantDiagnostic: "Release extraction error is retryable; retrying in 5s.",
		},
		{
			name:           "connection reset is retryable",
			errorText:      "read tcp 10.0.0.1:443: connection reset by peer",
			wantSuccess:    true,
			wantAttempts:   2,
			wantDiagnostic: "Release extraction error is retryable; retrying in 5s.",
		},
		{
			name:           "TLS handshake timeout is retryable",
			errorText:      "net/http: TLS handshake timeout",
			wantSuccess:    true,
			wantAttempts:   2,
			wantDiagnostic: "Release extraction error is retryable; retrying in 5s.",
		},
		{
			name:           "dial timeout is retryable",
			errorText:      "dial tcp 10.0.0.1:443: i/o timeout",
			wantSuccess:    true,
			wantAttempts:   2,
			wantDiagnostic: "Release extraction error is retryable; retrying in 5s.",
		},
		{
			name:           "request wait timeout is retryable",
			errorText:      "net/http: request canceled while waiting for connection",
			wantSuccess:    true,
			wantAttempts:   2,
			wantDiagnostic: "Release extraction error is retryable; retrying in 5s.",
		},
		{
			name:           "insufficient scope is immediate",
			errorText:      "insufficient_scope: authorization failed",
			wantAttempts:   1,
			wantDiagnostic: "Release extraction error is not retryable; failing immediately.",
		},
		{
			name:           "authorization failure takes precedence over GOAWAY",
			errorText:      "authorization failed: token rejected; http2: server sent GOAWAY and closed the connection; ErrCode=NO_ERROR",
			wantAttempts:   1,
			wantDiagnostic: "Release extraction error is not retryable; failing immediately.",
		},
		{
			name:           "authorization failure takes precedence over transient HTTP response",
			errorText:      `insufficient_scope: authorization failed; error parsing HTTP 503 response body: invalid character '<' looking for beginning of value`,
			wantAttempts:   1,
			wantDiagnostic: "Release extraction error is not retryable; failing immediately.",
		},
		{
			name:           "manifest failure takes precedence over GOAWAY",
			errorText:      "manifest unknown: requested manifest does not exist; http2: server sent GOAWAY and closed the connection; ErrCode=NO_ERROR",
			wantAttempts:   1,
			wantDiagnostic: "Release extraction error is not retryable; failing immediately.",
		},
		{
			name:           "malformed payload takes precedence over GOAWAY",
			errorText:      "invalid image-references: unexpected end of JSON input; http2: server sent GOAWAY and closed the connection; ErrCode=NO_ERROR",
			wantAttempts:   1,
			wantDiagnostic: "Release extraction error is not retryable; failing immediately.",
		},
		{
			name:           "HTTP 499 response body parse error is immediate",
			errorText:      `error parsing HTTP 499 response body: invalid character '<' looking for beginning of value`,
			wantAttempts:   1,
			wantDiagnostic: "Release extraction error is not retryable; failing immediately.",
		},
		{
			name:           "HTTP 505 response body parse error is immediate",
			errorText:      `error parsing HTTP 505 response body: invalid character '<' looking for beginning of value`,
			wantAttempts:   1,
			wantDiagnostic: "Release extraction error is not retryable; failing immediately.",
		},
		{
			name:           "HTTP 5000 response body parse error is immediate",
			errorText:      `error parsing HTTP 5000 response body: invalid character '<' looking for beginning of value`,
			wantAttempts:   1,
			wantDiagnostic: "Release extraction error is not retryable; failing immediately.",
		},
		{
			name:           "ineligible unexpected HTTP status is immediate",
			errorText:      "received unexpected HTTP status: 501 Not Implemented",
			wantAttempts:   1,
			wantDiagnostic: "Release extraction error is not retryable; failing immediately.",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			result := runReleaseExtractCommandWithError(t, testCase.errorText, 2, releaseExtractInitialBackoffSeconds)
			if testCase.wantSuccess && result.err != nil {
				t.Fatalf("release extraction failed: %v\n%s", result.err, result.output)
			}
			if !testCase.wantSuccess && result.err == nil {
				t.Fatalf("release extraction unexpectedly succeeded:\n%s", result.output)
			}
			if result.attempts != testCase.wantAttempts {
				t.Errorf("attempts: got %d, want %d", result.attempts, testCase.wantAttempts)
			}
			wantBackoffs := []string(nil)
			if testCase.wantSuccess {
				wantBackoffs = []string{"5"}
			}
			if strings.Join(result.backoffs, ",") != strings.Join(wantBackoffs, ",") {
				t.Errorf("backoffs: got %v, want %v", result.backoffs, wantBackoffs)
			}
			if !strings.Contains(result.output, testCase.wantDiagnostic) {
				t.Errorf("missing diagnostic %q in output:\n%s", testCase.wantDiagnostic, result.output)
			}
			if testCase.wantSuccess {
				if got, want := result.finalOutput, "complete-image-references\n"; got != want {
					t.Errorf("final output: got %q, want %q", got, want)
				}
			} else if result.finalExists {
				t.Errorf("non-retryable failure published output: %q", result.finalOutput)
			}
			if len(result.temporaryFiles) != 0 {
				t.Errorf("temporary files were not cleaned up: %v", result.temporaryFiles)
			}
			if result.rawErrorPath == "" {
				t.Error("fake oc did not report its stderr capture path")
			} else if result.rawErrorExists {
				t.Errorf("raw stderr capture was not cleaned up: %s", result.rawErrorPath)
			}
		})
	}
}

func TestReleaseExtractRetriesWhenPullSpecContainsAuthMarker(t *testing.T) {
	const pullSpec = "quay.io/acme/forbidden@sha256:abc"
	result := runReleaseExtractCommandWithPullSpecAndError(t, pullSpec, `unable to parse image quay.io/acme/forbidden@sha256:abc: http2: server sent GOAWAY and closed the connection; LastStreamID=9, ErrCode=NO_ERROR`, 2, releaseExtractInitialBackoffSeconds)
	if result.err != nil {
		t.Fatalf("release extraction failed: %v\n%s", result.err, result.output)
	}
	if result.attempts != 2 {
		t.Errorf("attempts: got %d, want 2", result.attempts)
	}
	if got, want := strings.Join(result.backoffs, ","), "5"; got != want {
		t.Errorf("backoffs: got %q, want %q", got, want)
	}
	if !strings.Contains(result.output, "Release extraction succeeded on attempt 2/2.") {
		t.Errorf("missing second-attempt success diagnostic in output:\n%s", result.output)
	}
	if got, want := result.finalOutput, "complete-image-references\n"; got != want {
		t.Errorf("final output: got %q, want %q", got, want)
	}
	if len(result.temporaryFiles) != 0 {
		t.Errorf("temporary files were not cleaned up: %v", result.temporaryFiles)
	}
}

func TestReleaseExtractRetriesAreBoundedAndPreserveFinalExitStatus(t *testing.T) {
	result := runReleaseExtractCommand(t, "always_goaway", releaseExtractAttempts, releaseExtractInitialBackoffSeconds, nil)
	if result.err == nil {
		t.Fatalf("release extraction unexpectedly succeeded:\n%s", result.output)
	}
	var exitError *exec.ExitError
	if !errors.As(result.err, &exitError) {
		t.Fatalf("expected an exit error, got %T: %v", result.err, result.err)
	}
	if exitError.ExitCode() != 23 {
		t.Errorf("exit code: got %d, want final attempt's 23", exitError.ExitCode())
	}
	if result.attempts != releaseExtractAttempts {
		t.Errorf("attempts: got %d, want %d", result.attempts, releaseExtractAttempts)
	}
	if got, want := strings.Join(result.backoffs, ","), "5,10"; got != want {
		t.Errorf("backoffs: got %q, want %q", got, want)
	}
	for _, diagnostic := range []string{
		"Release extraction failed on attempt 3/3 with exit code 23.",
		"Release extraction error is retryable, but all 3 attempts are exhausted; returning exit code 23.",
	} {
		if !strings.Contains(result.output, diagnostic) {
			t.Errorf("missing diagnostic %q in output:\n%s", diagnostic, result.output)
		}
	}
	if result.finalExists {
		t.Errorf("partial final output was published: %q", result.finalOutput)
	}
	if len(result.temporaryFiles) != 0 {
		t.Errorf("temporary files were not cleaned up: %v", result.temporaryFiles)
	}
}

func TestReleaseExtractDoesNotLogCommandStderr(t *testing.T) {
	const sensitiveMarker = "SENSITIVE-OC-STDERR-MARKER-token=secret-value"
	testCases := []struct {
		name            string
		scenario        string
		errorText       string
		wantAttempts    int
		wantExitCode    int
		wantDiagnostics []string
	}{
		{
			name:         "retry",
			scenario:     "configured_error_then_success",
			errorText:    sensitiveMarker + ": http2: server sent GOAWAY and closed the connection; LastStreamID=9, ErrCode=NO_ERROR",
			wantAttempts: 2,
			wantDiagnostics: []string{
				"Release extraction attempt 1/3.",
				"Release extraction failed on attempt 1/3 with exit code 47.",
				"Release extraction error is retryable; retrying in 5s.",
				"Release extraction attempt 2/3.",
				"Release extraction succeeded on attempt 2/3.",
			},
		},
		{
			name:         "final exhaustion",
			scenario:     "configured_error_always",
			errorText:    sensitiveMarker + ": http2: server sent GOAWAY and closed the connection; LastStreamID=9, ErrCode=NO_ERROR",
			wantAttempts: 3,
			wantExitCode: 53,
			wantDiagnostics: []string{
				"Release extraction attempt 1/3.",
				"Release extraction error is retryable; retrying in 5s.",
				"Release extraction error is retryable; retrying in 10s.",
				"Release extraction failed on attempt 3/3 with exit code 53.",
				"Release extraction error is retryable, but all 3 attempts are exhausted; returning exit code 53.",
			},
		},
		{
			name:         "immediate failure",
			scenario:     "configured_error_always",
			errorText:    sensitiveMarker + ": unauthorized: authentication required",
			wantAttempts: 1,
			wantExitCode: 51,
			wantDiagnostics: []string{
				"Release extraction attempt 1/3.",
				"Release extraction failed on attempt 1/3 with exit code 51.",
				"Release extraction error is not retryable; failing immediately.",
			},
		},
		{
			name:         "success",
			scenario:     "configured_success_stderr",
			errorText:    sensitiveMarker,
			wantAttempts: 1,
			wantDiagnostics: []string{
				"Release extraction attempt 1/3.",
				"Release extraction succeeded on attempt 1/3.",
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			result := runReleaseExtractCommandFixture(t, testCase.scenario, testCase.errorText, "quay.io/test/release@sha256:abc", releaseExtractAttempts, releaseExtractInitialBackoffSeconds, nil)
			if strings.Contains(result.output, sensitiveMarker) {
				t.Errorf("sensitive marker from oc stderr was logged:\n%s", result.output)
			}
			if result.attempts != testCase.wantAttempts {
				t.Errorf("attempts: got %d, want %d", result.attempts, testCase.wantAttempts)
			}
			if testCase.wantExitCode == 0 {
				if result.err != nil {
					t.Errorf("release extraction failed: %v\n%s", result.err, result.output)
				}
			} else {
				var exitError *exec.ExitError
				if !errors.As(result.err, &exitError) {
					t.Fatalf("expected an exit error, got %T: %v", result.err, result.err)
				}
				if got := exitError.ExitCode(); got != testCase.wantExitCode {
					t.Errorf("exit code: got %d, want %d", got, testCase.wantExitCode)
				}
			}
			for _, diagnostic := range testCase.wantDiagnostics {
				if !strings.Contains(result.output, diagnostic) {
					t.Errorf("missing diagnostic %q in output:\n%s", diagnostic, result.output)
				}
			}
			if len(result.temporaryFiles) != 0 {
				t.Errorf("temporary files were not cleaned up: %v", result.temporaryFiles)
			}
			if result.rawErrorPath == "" {
				t.Error("fake oc did not report its stderr capture path")
			} else if result.rawErrorExists {
				t.Errorf("raw stderr capture was not cleaned up: %s", result.rawErrorPath)
			}
		})
	}
}

func TestReleaseExtractPublishesAtomically(t *testing.T) {
	const previous = "previous-image-references"
	result := runReleaseExtractCommand(t, "atomic_then_success", releaseExtractAttempts, releaseExtractInitialBackoffSeconds, ptrTo(previous))
	if result.err != nil {
		t.Fatalf("release extraction failed: %v\n%s", result.err, result.output)
	}
	if result.attempts != 2 {
		t.Errorf("attempts: got %d, want 2", result.attempts)
	}
	if got, want := result.finalOutput, "complete-image-references\n"; got != want {
		t.Errorf("final output: got %q, want %q", got, want)
	}
	if len(result.temporaryFiles) != 0 {
		t.Errorf("temporary files were not cleaned up: %v", result.temporaryFiles)
	}

	result = runReleaseExtractCommand(t, "manifest", releaseExtractAttempts, releaseExtractInitialBackoffSeconds, ptrTo(previous))
	if result.err == nil {
		t.Fatalf("release extraction unexpectedly succeeded:\n%s", result.output)
	}
	if got := strings.TrimSuffix(result.finalOutput, "\n"); got != previous {
		t.Errorf("failed extraction changed existing final output: got %q, want %q", got, previous)
	}
	if len(result.temporaryFiles) != 0 {
		t.Errorf("temporary files were not cleaned up after failure: %v", result.temporaryFiles)
	}
}

func TestReleaseExtractSIGKILLDoesNotExposeStderrInArtifacts(t *testing.T) {
	const sensitiveMarker = "SENSITIVE-OC-STDERR-MARKER-token=secret-value"
	result := runReleaseExtractCommandFixture(t, "kill_parent", sensitiveMarker, "quay.io/test/release@sha256:abc", releaseExtractAttempts, releaseExtractInitialBackoffSeconds, nil)

	var exitError *exec.ExitError
	if !errors.As(result.err, &exitError) {
		t.Fatalf("expected extraction shell to be killed, got %T: %v", result.err, result.err)
	}
	waitStatus, ok := exitError.Sys().(syscall.WaitStatus)
	if !ok || waitStatus.Signal() != syscall.SIGKILL {
		t.Fatalf("extraction shell termination: got %v, want SIGKILL", exitError)
	}
	if result.rawErrorPath == "" {
		t.Fatal("fake oc did not report its stderr capture path")
	}
	if got, want := filepath.Dir(result.rawErrorPath), "/tmp"; got != want {
		t.Errorf("raw stderr directory: got %q, want %q", got, want)
	}
	rawError, err := os.ReadFile(result.rawErrorPath)
	if err != nil {
		t.Fatalf("read raw stderr capture: %v", err)
	}
	if !strings.Contains(string(rawError), sensitiveMarker) {
		t.Errorf("raw stderr capture does not contain sensitive marker: %q", rawError)
	}

	if result.finalExists {
		t.Errorf("partial output was published after SIGKILL: %q", result.finalOutput)
	}
	if len(result.artifactFiles) != 1 {
		t.Fatalf("artifact files after SIGKILL: got %v, want only the stdout temporary file", result.artifactFiles)
	}
	artifactFile := result.artifactFiles[0]
	if !strings.HasPrefix(filepath.Base(artifactFile), "release-images-latest.tmp.") {
		t.Errorf("artifact file after SIGKILL: got %q, want stdout temporary file", artifactFile)
	}
	artifactData, err := os.ReadFile(artifactFile)
	if err != nil {
		t.Fatalf("read artifact file after SIGKILL: %v", err)
	}
	if got, want := string(artifactData), "partial-image-references\n"; got != want {
		t.Errorf("stdout temporary file: got %q, want %q", got, want)
	}
	if strings.Contains(string(artifactData), sensitiveMarker) {
		t.Errorf("sensitive stderr marker was exposed in artifact file %q", artifactFile)
	}
}

type releaseExtractResult struct {
	output         string
	err            error
	attempts       int
	backoffs       []string
	finalExists    bool
	finalOutput    string
	temporaryFiles []string
	artifactFiles  []string
	rawErrorPath   string
	rawErrorExists bool
}

func runReleaseExtractCommand(t *testing.T, scenario string, attempts, initialBackoffSeconds int, existingOutput *string) releaseExtractResult {
	t.Helper()
	return runReleaseExtractCommandFixture(t, scenario, "", "quay.io/test/release@sha256:abc", attempts, initialBackoffSeconds, existingOutput)
}

func runReleaseExtractCommandWithError(t *testing.T, errorText string, attempts, initialBackoffSeconds int) releaseExtractResult {
	t.Helper()
	return runReleaseExtractCommandWithPullSpecAndError(t, "quay.io/test/release@sha256:abc", errorText, attempts, initialBackoffSeconds)
}

func runReleaseExtractCommandWithPullSpecAndError(t *testing.T, pullSpec, errorText string, attempts, initialBackoffSeconds int) releaseExtractResult {
	t.Helper()
	return runReleaseExtractCommandFixture(t, "configured_error_then_success", errorText, pullSpec, attempts, initialBackoffSeconds, nil)
}

func runReleaseExtractCommandFixture(t *testing.T, scenario, errorText, pullSpec string, attempts, initialBackoffSeconds int, existingOutput *string) releaseExtractResult {
	t.Helper()

	workDir := t.TempDir()
	artifactDir := filepath.Join(workDir, "artifacts")
	binDir := filepath.Join(workDir, "bin")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatalf("create artifact directory: %v", err)
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create bin directory: %v", err)
	}

	const target = "release-images-latest"
	finalPath := filepath.Join(artifactDir, target)
	if existingOutput != nil {
		if err := os.WriteFile(finalPath, []byte(*existingOutput), 0o600); err != nil {
			t.Fatalf("write existing output: %v", err)
		}
	}

	attemptFile := filepath.Join(workDir, "attempts")
	sleepFile := filepath.Join(workDir, "sleeps")
	rawErrorPathFile := filepath.Join(workDir, "raw-error-path")
	writeExecutable(t, filepath.Join(binDir, "oc"), fakeReleaseExtractOC)
	writeExecutable(t, filepath.Join(binDir, "sleep"), fakeReleaseExtractSleep)

	command := exec.Command("bash", "-c", "set -euo pipefail\n"+releaseExtractCommand(pullSpec, target, attempts, initialBackoffSeconds))
	command.Env = append(os.Environ(),
		"ARTIFACT_DIR="+artifactDir,
		"FAKE_OC_SCENARIO="+scenario,
		"FAKE_OC_ERROR="+errorText,
		"FAKE_OC_ATTEMPT_FILE="+attemptFile,
		"FAKE_OC_STDERR_PATH_FILE="+rawErrorPathFile,
		"FAKE_FINAL_PATH="+finalPath,
		"FAKE_SLEEP_FILE="+sleepFile,
		"PATH="+binDir+":"+os.Getenv("PATH"),
	)
	output, commandErr := command.CombinedOutput()

	result := releaseExtractResult{output: string(output), err: commandErr}
	if data, err := os.ReadFile(attemptFile); err == nil {
		result.attempts, err = strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			t.Fatalf("parse attempt count: %v", err)
		}
	} else if !os.IsNotExist(err) {
		t.Fatalf("read attempt count: %v", err)
	}
	if data, err := os.ReadFile(sleepFile); err == nil {
		result.backoffs = strings.Fields(string(data))
	} else if !os.IsNotExist(err) {
		t.Fatalf("read backoffs: %v", err)
	}
	if data, err := os.ReadFile(finalPath); err == nil {
		result.finalExists = true
		result.finalOutput = string(data)
	} else if !os.IsNotExist(err) {
		t.Fatalf("read final output: %v", err)
	}
	if data, err := os.ReadFile(rawErrorPathFile); err == nil {
		result.rawErrorPath = strings.TrimSpace(string(data))
		if _, err := os.Stat(result.rawErrorPath); err == nil {
			result.rawErrorExists = true
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat raw stderr capture: %v", err)
		}
		t.Cleanup(func() {
			if err := os.Remove(result.rawErrorPath); err != nil && !os.IsNotExist(err) {
				t.Errorf("remove raw stderr capture: %v", err)
			}
		})
	} else if !os.IsNotExist(err) {
		t.Fatalf("read raw stderr path: %v", err)
	}
	for _, pattern := range []string{target + ".tmp.*", target + ".stderr.*"} {
		matches, err := filepath.Glob(filepath.Join(artifactDir, pattern))
		if err != nil {
			t.Fatalf("glob temporary files: %v", err)
		}
		result.temporaryFiles = append(result.temporaryFiles, matches...)
	}
	entries, err := os.ReadDir(artifactDir)
	if err != nil {
		t.Fatalf("read artifact directory: %v", err)
	}
	for _, entry := range entries {
		result.artifactFiles = append(result.artifactFiles, filepath.Join(artifactDir, entry.Name()))
	}
	return result
}

func writeExecutable(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatalf("write executable %s: %v", path, err)
	}
}

func ptrTo(value string) *string { return &value }

const fakeReleaseExtractOC = `#!/usr/bin/env bash
set -euo pipefail
if [[ "$#" -lt 3 || "$1" != "adm" || "$2" != "release" || "$3" != "extract" ]]; then
	echo "unexpected oc invocation: $*" >&2
	exit 99
fi

attempt=0
if [[ -f "${FAKE_OC_ATTEMPT_FILE}" ]]; then
	attempt="$(<"${FAKE_OC_ATTEMPT_FILE}")"
fi
attempt=$((attempt + 1))
echo "${attempt}" > "${FAKE_OC_ATTEMPT_FILE}"
readlink /proc/self/fd/2 > "${FAKE_OC_STDERR_PATH_FILE}"

case "${FAKE_OC_SCENARIO}" in
	configured_error_then_success)
		if (( attempt == 1 )); then
			echo "partial-image-references"
			printf '%s\n' "${FAKE_OC_ERROR}" >&2
			exit 47
		fi
		;;
	configured_error_always)
		echo "partial-image-references-${attempt}"
		printf '%s\n' "${FAKE_OC_ERROR}" >&2
		exit $((50 + attempt))
		;;
	configured_success_stderr)
		printf '%s\n' "${FAKE_OC_ERROR}" >&2
		;;
	kill_parent)
		echo "partial-image-references"
		printf '%s\n' "${FAKE_OC_ERROR}" >&2
		kill -KILL "${PPID}"
		exit 0
		;;
	wrapped_goaway_then_success)
		if (( attempt == 1 )); then
			echo "partial-image-references"
			echo 'error: unable to parse image: cannot retrieve image configuration for manifest sha256:abc in manifest list sha256:def: Get "https://registry.example/v2/repo/blobs/sha256:abc": http2: server sent GOAWAY and closed the connection; LastStreamID=9, ErrCode=NO_ERROR, debug=""' >&2
			exit 17
		fi
		;;
	http_503_then_success)
		if (( attempt == 1 )); then
			echo "partial-image-references"
			echo "received unexpected HTTP status: 503 Service Unavailable" >&2
			exit 18
		fi
		;;
	reset_then_success)
		if (( attempt == 1 )); then
			echo "partial-image-references"
			echo "read tcp 10.0.0.1:443: connection reset by peer" >&2
			exit 19
		fi
		;;
	always_goaway)
		echo "partial-image-references-${attempt}"
		echo "final transient error attempt ${attempt}: http2: server sent GOAWAY and closed the connection; LastStreamID=9, ErrCode=NO_ERROR" >&2
		exit $((20 + attempt))
		;;
	atomic_then_success)
		if (( attempt == 1 )); then
			echo "partial-image-references"
			echo "http2: server sent GOAWAY and closed the connection; LastStreamID=9, ErrCode=NO_ERROR" >&2
			exit 24
		fi
		if [[ "$(<"${FAKE_FINAL_PATH}")" != "previous-image-references" ]]; then
			echo "final output changed before successful extraction" >&2
			exit 90
		fi
		;;
	unauthorized)
		echo "partial-image-references"
		echo "unauthorized: authentication required" >&2
		exit 41
		;;
	forbidden_with_goaway)
		echo "partial-image-references"
		echo "forbidden: requested access denied; http2: server sent GOAWAY and closed the connection; ErrCode=NO_ERROR" >&2
		exit 42
		;;
	digest)
		echo "partial-image-references"
		echo "digest mismatch: expected sha256:abc, got sha256:def" >&2
		exit 43
		;;
	manifest)
		echo "partial-image-references"
		echo "manifest unknown: requested manifest does not exist" >&2
		exit 44
		;;
	malformed)
		echo "partial-image-references"
		echo "invalid image-references: unexpected end of JSON input" >&2
		exit 45
		;;
	http_501)
		echo "partial-image-references"
		echo "received unexpected HTTP status: 501 Not Implemented" >&2
		exit 46
		;;
	*)
		echo "unknown scenario ${FAKE_OC_SCENARIO}" >&2
		exit 98
		;;
esac

echo "complete-image-references"
`

const fakeReleaseExtractSleep = `#!/usr/bin/env bash
set -euo pipefail
echo "$1" >> "${FAKE_SLEEP_FILE}"
`

func TestResolveCLIImageFromStreamWaitsForSpecVisibility(t *testing.T) {
	const (
		namespace  = "test-namespace"
		streamName = "stable-initial"
	)
	client := newReleaseImportGuardClient(t, namespace, streamName,
		&imagev1.ImageStream{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: streamName}},
		&imagev1.ImageStreamTag{
			ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: streamName + ":cli"},
			Tag: &imagev1.TagReference{From: &corev1.ObjectReference{
				Kind: "DockerImage",
				Name: testCLIImage,
			}},
		},
	)
	jobSpec := &api.JobSpec{}
	jobSpec.SetNamespace(namespace)
	step := &importReleaseStep{client: client, jobSpec: jobSpec}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := step.resolveCLIImageFromStream(ctx, streamName)
	if err != nil {
		t.Fatalf("resolve CLI image from stream: %v", err)
	}
	assertTestCLIReference(t, got)
	if client.imageStreamWatchCount != 1 {
		t.Fatalf("expected the shared import wait to use one ImageStream watch, got %d", client.imageStreamWatchCount)
	}
}

func TestExtractAndTagCLIImageWaitsForSpecVisibility(t *testing.T) {
	const (
		namespace  = "test-namespace"
		streamName = "stable"
		targetCLI  = "release-images-latest-cli"
	)
	client := newReleaseImportGuardClient(t, namespace, streamName,
		&imagev1.ImageStream{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: streamName}},
		&imagev1.ImageStreamTag{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: streamName + ":cli"}},
	)
	jobSpec := &api.JobSpec{}
	jobSpec.SetNamespace(namespace)
	step := &importReleaseStep{name: api.LatestReleaseName, client: client, jobSpec: jobSpec}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := step.extractAndTagCLIImage(ctx, targetCLI, streamName)
	if err != nil {
		t.Fatalf("extract and tag CLI image: %v", err)
	}
	assertTestCLIReference(t, got)
	if client.imageStreamWatchCount != 1 {
		t.Fatalf("expected the shared import wait to use one ImageStream watch, got %d", client.imageStreamWatchCount)
	}
	if len(client.CreatedPods) != 1 || client.CreatedPods[0].Name != targetCLI {
		t.Fatalf("expected CLI extractor pod %q to be created, got %#v", targetCLI, client.CreatedPods)
	}
}

func assertTestCLIReference(t *testing.T, ref *api.ImageStreamTagReference) {
	t.Helper()
	if ref == nil {
		t.Fatal("expected a CLI image reference, got nil")
	}
	if ref.Name != "quay.io/test/cli" || ref.Tag != "latest" {
		t.Fatalf("unexpected CLI image reference: %#v", ref)
	}
}

type releaseImportGuardClient struct {
	*testhelperkube.FakePodClient
	namespace             string
	streamName            string
	imageStreamWatchCount int
}

func (c *releaseImportGuardClient) Create(ctx context.Context, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.CreateOption) error {
	if pod, ok := obj.(*corev1.Pod); ok && len(pod.Spec.Containers) > 0 {
		pod.Status.ContainerStatuses = []corev1.ContainerStatus{{
			Name: pod.Spec.Containers[0].Name,
			State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
				Message: testCLIImage,
			}},
		}}
	}
	return c.FakePodExecutor.Create(ctx, obj, opts...)
}

func (c *releaseImportGuardClient) Update(ctx context.Context, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.UpdateOption) error {
	if streamTag, ok := obj.(*imagev1.ImageStreamTag); ok && streamTag.ResourceVersion == "" {
		existing := &imagev1.ImageStreamTag{}
		if err := c.FakePodExecutor.LoggingClient.Get(ctx, ctrlruntimeclient.ObjectKeyFromObject(streamTag), existing); err != nil {
			return err
		}
		streamTag.ResourceVersion = existing.ResourceVersion
	}
	return c.FakePodExecutor.LoggingClient.Update(ctx, obj, opts...)
}

func (c *releaseImportGuardClient) Watch(ctx context.Context, list ctrlruntimeclient.ObjectList, opts ...ctrlruntimeclient.ListOption) (watch.Interface, error) {
	if _, ok := list.(*corev1.PodList); ok {
		return c.FakePodExecutor.Watch(ctx, list, opts...)
	}
	if _, ok := list.(*imagev1.ImageStreamList); !ok {
		return c.FakePodExecutor.LoggingClient.Watch(ctx, list, opts...)
	}

	c.imageStreamWatchCount++
	stream := &imagev1.ImageStream{}
	key := ctrlruntimeclient.ObjectKey{Namespace: c.namespace, Name: c.streamName}
	if err := c.FakePodExecutor.LoggingClient.Get(ctx, key, stream); err != nil {
		return nil, err
	}
	stream.Spec.Tags = []imagev1.TagReference{{
		Name: "cli",
		From: &corev1.ObjectReference{Kind: "DockerImage", Name: testCLIImage},
	}}
	specOnly := stream.DeepCopy()
	stream.Status.Tags = []imagev1.NamedTagEventList{{
		Tag: "cli",
		Items: []imagev1.TagEvent{{
			DockerImageReference: "quay.io/test/cli@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}},
	}}
	if err := c.FakePodExecutor.LoggingClient.Update(ctx, stream); err != nil {
		return nil, err
	}
	// The first event exposes the spec tag; the second exposes import status.
	// Without the spec-aware shared evaluator, the initial empty list fails
	// before this watch can be established.
	events := make(chan watch.Event, 2)
	events <- watch.Event{Type: watch.Modified, Object: specOnly}
	events <- watch.Event{Type: watch.Modified, Object: stream.DeepCopy()}
	return watch.NewProxyWatcher(events), nil
}

func newReleaseImportGuardClient(t *testing.T, namespace, streamName string, objects ...runtime.Object) *releaseImportGuardClient {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core API to scheme: %v", err)
	}
	if err := imagev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add image API to scheme: %v", err)
	}
	loggingClient := loggingclient.New(fakectrlruntimeclient.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(objects...).
		WithIndex(&corev1.Pod{}, "metadata.name", func(obj ctrlruntimeclient.Object) []string { return []string{obj.GetName()} }).
		Build(), nil)
	return &releaseImportGuardClient{
		FakePodClient: &testhelperkube.FakePodClient{
			FakePodExecutor: &testhelperkube.FakePodExecutor{
				LoggingClient: loggingClient,
				AutoSchedule:  true,
			},
			PendingTimeout: time.Minute,
		},
		namespace:  namespace,
		streamName: streamName,
	}
}
