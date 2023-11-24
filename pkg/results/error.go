package results

import (
	"errors"
	"fmt"
)

// Error holds a message and a child, allowing for an error
// The common use-case here will be to wrap errors from callsites:
//
//	if err := doSomething(data); err != nil {
//	    return results.ForReason(results.ReasonFoo).WithError(err).Errorf("could not do something for data: %v", data)
//	}
type Error struct {
	reason  Reason
	message string
	wrapped error
}

// Error makes an Error an error
func (e *Error) Error() string {
	return e.message
}

// Unwrap allows nesting of errors
func (e *Error) Unwrap() error {
	return e.wrapped
}

// Is allows us to say we are an Error
func (e *Error) Is(target error) bool {
	_, is := target.(*Error)
	return is
}

// Reasons provides the chains of error reasons.
// Each item in the return value is a single chain divided by colons.  Aggregate
// errors — those whose type provides an `Errors` method returning a list of
// errors — are recursively expanded, generating a separate chain for each
// child.
func Reasons(errs ...error) (ret []string) {
	for _, err := range errs {
		switch err := err.(type) {
		case *Error:
			children := Reasons(err.Unwrap())
			if len(children) == 0 {
				ret = append(ret, string(err.reason))
				break
			}
			for _, r := range children {
				ret = append(ret, fmt.Sprintf("%s:%s", err.reason, r))
			}
		case interface{ Errors() []error }:
			ret = append(ret, Reasons(err.Errors()...)...)
		case interface{ Unwrap() error }:
			ret = append(ret, Reasons(err.Unwrap())...)
		}
	}
	return
}

// BuilderWithReason starts the builder chain
type BuilderWithReason struct {
	Error
}

// ForReason is a constructor for an Error from a Reason. We expect
// users to then add a child and a error message to this Error.
func ForReason(reason Reason) *BuilderWithReason {
	if reason == "" {
		// we don't want to publish metrics with an empty label, so
		// we enforce that there's some default (if useless) value
		reason = ReasonUnknown
	}
	return &BuilderWithReason{
		Error: Error{
			reason: reason,
		},
	}
}

// BuilderWithReasonAndError adds a child error to the builder
type BuilderWithReasonAndError struct {
	Error
}

// WithError is a builder that adds a child to the Error. We
// expect users to continue to build the Error by adding a message.
func (e *BuilderWithReason) WithError(err error) *BuilderWithReasonAndError {
	b := &BuilderWithReasonAndError{
		Error: e.Error,
	}
	b.wrapped = err
	return b
}

// Errorf is a builder that adds in the main error to an Error.
// This is expected to be the final builder/producer in a chain,
// so we return an error and not an Error
func (e *BuilderWithReasonAndError) Errorf(format string, args ...interface{}) error {
	e.message = fmt.Sprintf(format, args...)
	return &e.Error
}

// ForError is a constructor for when a caller does not want to add
// a child but instead wants a simple error. For instance, wrapping
// the outcome of a function that doesn't return an Error itself:
//
//	err := results.ForReason(results.ReasonLoadingArgs).ForError(doSomething())
func (e *BuilderWithReason) ForError(err error) error {
	if err == nil {
		return nil
	}
	e.wrapped = err
	e.message = err.Error()
	return &e.Error
}

// DefaultReason is a constructor that adds a reason if needed, when we
// want to ensure that consumers downstream of a callsite have an Error.
//
// annotated := DefaultReason(doSomething())
func DefaultReason(err error) error {
	if errors.Is(err, &Error{}) {
		return err
	}

	return ForReason(ReasonUnknown).ForError(err)
}
