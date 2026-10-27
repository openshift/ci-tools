package gsmproxy

import (
	"context"
	"net/http"

	"github.com/sirupsen/logrus"

	gsmvalidation "github.com/openshift/ci-tools/pkg/gsm-validation"
)

// auditRecord collects request details that inner handlers learn, such as the verified caller.
type auditRecord struct {
	username string
}

type auditRecordContextKey struct{}

// recordAuditUsername stores the verified caller's username for the request's audit log line.
// It does nothing when the request is not wrapped by Audit.
func recordAuditUsername(ctx context.Context, username string) {
	if record, ok := ctx.Value(auditRecordContextKey{}).(*auditRecord); ok {
		record.username = username
	}
}

// statusRecorder remembers the status code written by the wrapped handler.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(body []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(body)
}

// Audit wraps a collection endpoint and writes one info-level log line per request with the
// action, the collection (only if its name is valid), the verified username (only if the token
// was accepted), the HTTP status, and an outcome derived from it. It never logs tokens, groups,
// or response bodies. Register it outside RequireCollectionAccess so it sees every outcome.
func Audit(action string, logger *logrus.Entry, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		record := &auditRecord{}
		recorder := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r.WithContext(context.WithValue(r.Context(), auditRecordContextKey{}, record)))

		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		fields := logrus.Fields{
			"action":  action,
			"status":  status,
			"outcome": auditOutcome(status),
		}
		if collection := r.PathValue("collection"); gsmvalidation.ValidateCollectionName(collection) {
			fields["collection"] = collection
		}
		if record.username != "" {
			fields["user"] = record.username
		}
		logger.WithFields(fields).Info("audit")
	})
}

// auditOutcome maps an HTTP status to a short, searchable outcome name.
func auditOutcome(status int) string {
	switch {
	case status >= 200 && status < 300:
		return "success"
	case status == http.StatusBadRequest:
		return "bad_request"
	case status == http.StatusUnauthorized:
		return "unauthenticated"
	case status == http.StatusForbidden:
		return "forbidden"
	case status >= 500:
		return "error"
	default:
		return "other"
	}
}
