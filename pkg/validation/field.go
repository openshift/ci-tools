package validation

import (
	"fmt"
)

// fieldPath contains the full path to the current field being validated.
type fieldPath string

func (f fieldPath) addField(name string) fieldPath {
	if f == "" {
		return fieldPath(name)
	}
	return fieldPath(fmt.Sprintf("%s.%s", f, name))
}

func (f fieldPath) addIndex(i int) fieldPath {
	if f == "" {
		panic("no previous field name")
	}
	return fieldPath(fmt.Sprintf("%s[%d]", f, i))
}

func (f fieldPath) errorf(format string, args ...interface{}) error {
	args = append([]interface{}{f}, args...)
	return fmt.Errorf("%s: "+format, args...)
}
