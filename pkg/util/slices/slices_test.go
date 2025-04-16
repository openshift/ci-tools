package slices

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestUniqueAdd(t *testing.T) {
	for _, tc := range []struct {
		name         string
		s            []int
		e            int
		wantS        []int
		wantModified bool
	}{
		{
			name:         "Add unique element",
			s:            []int{},
			e:            1,
			wantS:        []int{1},
			wantModified: true,
		},
		{
			name:         "Add duplicated element",
			s:            []int{1},
			e:            1,
			wantS:        []int{1},
			wantModified: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotS, gotModified := UniqueAdd(tc.s, tc.e)

			if gotModified != tc.wantModified {
				t.Errorf("want %T got %T", tc.wantModified, gotModified)
			}

			if diff := cmp.Diff(gotS, tc.wantS); diff != "" {
				t.Errorf("unexpected slice:\n%s", diff)
			}
		})
	}
}

func TestUniqueAddFunc(t *testing.T) {
	type point struct {
		X int
		Y int
	}

	for _, tc := range []struct {
		name         string
		s            []point
		e            point
		wantS        []point
		wantModified bool
	}{
		{
			name:         "Add unique element",
			s:            []point{},
			e:            point{X: 1, Y: 2},
			wantS:        []point{{X: 1, Y: 2}},
			wantModified: true,
		},
		{
			name:         "Add duplicated element",
			s:            []point{{X: 1, Y: 2}},
			e:            point{X: 1, Y: 2},
			wantS:        []point{{X: 1, Y: 2}},
			wantModified: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotS, gotModified := UniqueAddFunc(tc.s, tc.e, func(a point) bool {
				return a.Y == tc.e.Y
			})

			if gotModified != tc.wantModified {
				t.Errorf("want %T got %T", tc.wantModified, gotModified)
			}

			if diff := cmp.Diff(gotS, tc.wantS); diff != "" {
				t.Errorf("unexpected slice:\n%s", diff)
			}
		})
	}
}

func TestDelete(t *testing.T) {
	for _, tc := range []struct {
		name         string
		s            []int
		e            int
		wantS        []int
		wantModified bool
	}{
		{
			name:         "Do not remove",
			s:            []int{2},
			e:            1,
			wantS:        []int{2},
			wantModified: false,
		},
		{
			name:         "Remove",
			s:            []int{1},
			e:            1,
			wantS:        []int{},
			wantModified: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotS, gotModified := Delete(tc.s, tc.e)

			if gotModified != tc.wantModified {
				t.Errorf("want %T got %T", tc.wantModified, gotModified)
			}

			if diff := cmp.Diff(gotS, tc.wantS); diff != "" {
				t.Errorf("unexpected slice:\n%s", diff)
			}
		})
	}
}
