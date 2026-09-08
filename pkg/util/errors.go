package util

import (
	"fmt"
	"reflect"
	"strings"
)

// IsNilError reports whether err is nil, including an interface that contains
// a typed nil value.
func IsNilError(err error) bool {
	if err == nil {
		return true
	}
	value := reflect.ValueOf(err)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// ErrorStringOrDefault returns fallback when err is nil or contains a typed
// nil value. Otherwise it returns the error text.
func ErrorStringOrDefault(err error, fallback string) string {
	if IsNilError(err) {
		return fallback
	}
	return err.Error()
}

func AppendLogToError(err error, log string) error {
	log = strings.TrimSpace(log)
	if len(log) == 0 {
		return err
	}
	return fmt.Errorf("%w\n\n%s", err, log)
}
