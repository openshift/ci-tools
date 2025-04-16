package slices

import (
	stdslices "slices"
)

// uniqueAdd adds v to s. It returns a slice and a boolean to indicate whether
// the element has been inserted or not.
func UniqueAdd[S ~[]E, E comparable](s S, v E) (S, bool) {
	if i := stdslices.Index(s, v); i == -1 {
		return append(s, v), true
	}
	return s, false
}

// UniqueAddFunc adds v to s by using f as a comparison function.
// It returns a slice and a boolean to indicate whether the element has been inserted or not.
func UniqueAddFunc[S ~[]E, E comparable](s S, v E, f func(e E) bool) (S, bool) {
	if i := stdslices.IndexFunc(s, f); i == -1 {
		return append(s, v), true
	}
	return s, false
}

// Delete removes v from s. It returns the slice and a boolean
// to indicate whether the element has been removed or not.
func Delete[S ~[]E, E comparable](s S, v E) (S, bool) {
	if i := stdslices.Index(s, v); i != -1 {
		return stdslices.Delete(s, i, i+1), true
	}
	return s, false
}
