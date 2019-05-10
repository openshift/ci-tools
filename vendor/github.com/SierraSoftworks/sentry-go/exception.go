package sentry

import (
	"reflect"
	"regexp"
)

// NewExceptionInfo creates a new ExceptionInfo object which can
// then be populated with information about an exception which
// occurred before being passed to the Exception() method for
// submission to Sentry.
func NewExceptionInfo() *ExceptionInfo {
	ex := &ExceptionInfo{
		Type:       "unknown",
		Value:      "An unknown error has occurred",
		StackTrace: StackTrace(),
	}

	return ex
}

// An ExceptionInfo describes the details of an exception that occurred within
// your application.
type ExceptionInfo struct {
	Type       string           `json:"type"`
	Value      string           `json:"value"`
	Module     string           `json:"module,omitempty"`
	ThreadID   string           `json:"thread_id,omitempty"`
	Mechanism  string           `json:"mechanism,omitempty"`
	StackTrace StackTraceOption `json:"stacktrace,omitempty"`
}

// ForError updates an ExceptionInfo object with information sourced
// from an error.
func (e *ExceptionInfo) ForError(err error) *ExceptionInfo {
	e.Type = reflect.TypeOf(err).String()
	e.Value = err.Error()

	if e.StackTrace == nil {
		e.StackTrace = StackTrace().ForError(err)
	} else {
		e.StackTrace.ForError(err)
	}

	if m := errorMsgPattern.FindStringSubmatch(err.Error()); m != nil {
		e.Module = m[1]
		e.Value = m[2]
	}

	return e
}

// ExceptionForError allows you to include the details of an error which
// occurred within your application as part of the event you send to Sentry.
func ExceptionForError(err error) Option {
	exceptions := []*ExceptionInfo{}

	for err != nil {
		exceptions = append([]*ExceptionInfo{NewExceptionInfo().ForError(err)}, exceptions...)

		if causer, ok := err.(interface {
			Cause() error
		}); ok {
			err = causer.Cause()
		} else {
			err = nil
		}
	}

	return &exceptionOption{
		Exceptions: exceptions,
	}
}

// Exception allows you to include the details of an exception which occurred
// within your application as part of the event you send to Sentry.
func Exception(info *ExceptionInfo) Option {
	return &exceptionOption{
		Exceptions: []*ExceptionInfo{info},
	}
}

var errorMsgPattern = regexp.MustCompile(`\A(\w+): (.+)\z`)

type exceptionOption struct {
	Exceptions []*ExceptionInfo `json:"values"`
}

func (o *exceptionOption) Class() string {
	return "exception"
}

func (o *exceptionOption) Merge(old Option) Option {
	if old, ok := old.(*exceptionOption); ok {
		return &exceptionOption{
			Exceptions: append(old.Exceptions, o.Exceptions...),
		}
	}

	return o
}

func (o *exceptionOption) Finalize() {
	for _, ex := range o.Exceptions {
		if ex.StackTrace != nil {
			if finalize, ok := ex.StackTrace.(FinalizableOption); ok {
				finalize.Finalize()
			}
		}
	}
}
