package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/openshift/ci-tools/pkg/api/vault"
	"github.com/openshift/ci-tools/pkg/steps"
)

// https://github.com/openshift/ci-tools/blob/7af2e075f381ecae1562d1406bad2c86a23e72a3/vendor/k8s.io/api/core/v1/types.go#L5748-L5749
const secretKeyValidationRegexString = `^[a-zA-Z0-9\.\-_]+$`

var secretKeyValidationRegex = regexp.MustCompile(secretKeyValidationRegexString)

type kvKeyValidator struct {
	kvMountPath string
	upstream    http.RoundTripper
}

func (k *kvKeyValidator) RoundTrip(r *http.Request) (*http.Response, error) {
	l := logrus.WithFields(logrus.Fields{
		"method": r.Method,
		"path":   r.URL.Path,
	})
	l.Debug("Received request")
	if (r.Method != http.MethodPut && r.Method != http.MethodPost && r.Method != http.MethodPatch) || !strings.HasPrefix(r.URL.Path, "/v1/"+k.kvMountPath) {
		return k.upstream.RoundTrip(r)
	}
	l.Debug("Checking if kv keys in request are valid")

	requestBodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		logrus.WithError(err).Error("failed to read request body")
		return newResponse(http.StatusInternalServerError, r, "failed to read request body"), nil
	}

	var body simpleKVUpdateRequestBody
	if err := json.Unmarshal(requestBodyBytes, &body); err != nil {
		logrus.WithError(err).WithField("raw-body", string(requestBodyBytes)).Error("failed to unmarshal request body")
		return newResponse(http.StatusInternalServerError, r, "failed to deserialize request body"), nil
	}

	var errs []string
	for key, value := range body.Data {
		if key == vault.SecretSyncTargetNamepaceKey || key == vault.SecretSyncTargetNameKey {
			if valueErrs := validation.IsDNS1123Label(value); len(valueErrs) > 0 {
				errs = append(errs, fmt.Sprintf("value of key %s is invalid: %v", key, valueErrs))
			}
			continue
		}
		if key == vault.SecretSyncTargetClusterKey {
			continue
		}
		if !secretKeyValidationRegex.MatchString(key) {
			errs = append(errs, fmt.Sprintf("key %s is invalid: must match regex %s", key, secretKeyValidationRegexString))
		}
	}

	if err := steps.ValidateSecretInStep(body.Data[vault.SecretSyncTargetNamepaceKey], body.Data[vault.SecretSyncTargetNameKey]); err != nil {
		errs = append(errs, fmt.Sprintf("secret %s in namespace %s cannot be used in a step: %s", body.Data[vault.SecretSyncTargetNameKey], body.Data[vault.SecretSyncTargetNamepaceKey], err.Error()))
	}

	if len(errs) > 0 {
		return newResponse(400, r, errs...), nil
	}

	r.Body = io.NopCloser(bytes.NewBuffer(requestBodyBytes))
	return k.upstream.RoundTrip(r)
}

func newResponse(statusCode int, req *http.Request, errs ...string) *http.Response {
	var body []byte
	// We have to properly encode this, otherwise the UI just prints an "Error: [object Object]" which is
	// not particularly helpful
	headers := http.Header{}
	if len(errs) > 0 {
		respError := errorResponse{Errors: errs}
		var err error
		body, err = json.Marshal(respError)
		if err != nil {
			// Fall back to just directly putting the errors into the body
			body = []byte(strings.Join(errs, "\n"))
			logrus.WithError(err).Error("failed to serialize vault error response")
		} else {
			headers.Set("Content-Type", "application/json")
		}
	}
	return &http.Response{
		StatusCode:    statusCode,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Body:          io.NopCloser(bytes.NewBuffer(body)),
		ContentLength: int64(len(body)),
		Request:       req,
		Header:        headers,
	}
}

// errorResponse is the raw structure of errors when they're returned by the
// HTTP API.
// This is copied from github.com/hashicorp/vault/api/response.go because
// they don't have json tags there, resulting in an upper-cases json field
// in the response which makes the UI just diplay `Error: [object Object]`
type errorResponse struct {
	Errors []string `json:"errors"`
}

type simpleKVUpdateRequestBody struct {
	Data map[string]string `json:"data"`
}
