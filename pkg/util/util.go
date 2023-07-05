package util

import (
	"sort"

	"golang.org/x/exp/constraints"
)

// Zero returns the zero value of a type
func Zero[T any]() (_ T) {
	return
}

// CopyMap creates a new map copying all key/values from its argument
func CopyMap[K comparable, V any](m map[K]V) map[K]V {
	ret := make(map[K]V, len(m))
	for k, v := range m {
		ret[k] = v
	}
	return ret
}

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

// RemoveIf retains only elements which are not selected by a predicate
// The slice is modified in place and (potentially a subset) is returned.
func RemoveIf[T any](s []T, p func(T) bool) []T {
	i := 0
	for _, x := range s {
		if !p(x) {
			s[i] = x
			i++
		}
	}
	return s[:i]
}

// IsBitSet tests if a single specific bit is set in a value
func IsBitSet[T constraints.Integer](x, b T) bool {
	return x&b != Zero[T]()
}

// PopCount returns the number of "true" argument values.
func PopCount[T comparable](xs ...T) (ret uint) {
	for _, x := range xs {
		if x != Zero[T]() {
			ret += 1
		}
	}
	return
}
