package sentry

import "strings"

type ErrType string

// IsInstance will tell you whether a given error is an instance
// of this ErrType
func (e ErrType) IsInstance(err error) bool {
	return strings.Contains(err.Error(), string(e))
}

// Error gets the error message for this ErrType
func (e ErrType) Error() string {
	return string(e)
}
