package util

import (
	"sort"

	"golang.org/x/exp/constraints"
)

// SortSlice is a generic version of sort.Slice
func SortSlice[T constraints.Ordered](s []T) {
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
}

// Keys returns a slice with the keys in `m` in unspecified order
func Keys[K comparable, V any](m map[K]V) (ret []K) {
	ret = make([]K, 0, len(m))
	for k := range m {
		ret = append(ret, k)
	}
	return
}

// SortedKeys returns a slice with the keys in `m` in sorted order
func SortedKeys[K constraints.Ordered, V any](m map[K]V) (ret []K) {
	ret = Keys(m)
	SortSlice(ret)
	return
}

// Contains performs a linear search on `s` looking for `x`.
func Contains[T comparable](s []T, x T) bool {
	for _, y := range s {
		if x == y {
			return true
		}
	}
	return false
}
