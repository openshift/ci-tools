package main

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"sigs.k8s.io/prow/pkg/bugzilla"
)

func TestMassCloneBugs(t *testing.T) {

	testCases := []struct {
		name        string
		fromRelease string
		toRelease   string
		bugs        []*bugzilla.Bug
		expected    map[int]bugzilla.Bug
	}{
		{
			name:        "nothing to clone",
			fromRelease: "4.9",
			toRelease:   "4.10",
			expected:    map[int]bugzilla.Bug{},
		},
		{
			name:        "one bug to clone",
			fromRelease: "4.9",
			toRelease:   "4.10",
			bugs: []*bugzilla.Bug{
				{ID: 1234567, TargetRelease: []string{"4.9"}},
			},
			expected: map[int]bugzilla.Bug{
				1234567: {ID: 1234567, Blocks: []int{1234568}, TargetRelease: []string{"4.9"}},
				1234568: {ID: 1234568, DependsOn: []int{1234567}, TargetRelease: []string{"4.10"}},
			},
		},
		{
			name:        "multiple bugs to clone",
			fromRelease: "4.9",
			toRelease:   "4.10",
			bugs: []*bugzilla.Bug{
				{ID: 1234567, TargetRelease: []string{"4.9"}},
				{ID: 1234568, TargetRelease: []string{"4.9"}},
				{ID: 1234569, TargetRelease: []string{"4.9"}},
				{ID: 1234570, TargetRelease: []string{"4.9"}},
			},
			expected: map[int]bugzilla.Bug{
				1234567: {ID: 1234567, Blocks: []int{1234571}, TargetRelease: []string{"4.9"}},
				1234568: {ID: 1234568, Blocks: []int{1234572}, TargetRelease: []string{"4.9"}},
				1234569: {ID: 1234569, Blocks: []int{1234573}, TargetRelease: []string{"4.9"}},
				1234570: {ID: 1234570, Blocks: []int{1234574}, TargetRelease: []string{"4.9"}},

				1234571: {ID: 1234571, DependsOn: []int{1234567}, TargetRelease: []string{"4.10"}},
				1234572: {ID: 1234572, DependsOn: []int{1234568}, TargetRelease: []string{"4.10"}},
				1234573: {ID: 1234573, DependsOn: []int{1234569}, TargetRelease: []string{"4.10"}},
				1234574: {ID: 1234574, DependsOn: []int{1234570}, TargetRelease: []string{"4.10"}},
			},
		},

		{
			name:        "multiple bugs to clone with existing clones",
			fromRelease: "4.9",
			toRelease:   "4.10",
			bugs: []*bugzilla.Bug{
				{ID: 1234567, TargetRelease: []string{"4.9"}},
				{ID: 1234568, TargetRelease: []string{"4.9"}},
				{ID: 1234569, TargetRelease: []string{"4.9"}, Blocks: []int{1234570}},
				{ID: 1234570, TargetRelease: []string{"4.10"}, DependsOn: []int{1234569}},
			},
			expected: map[int]bugzilla.Bug{
				1234567: {ID: 1234567, Blocks: []int{1234571}, TargetRelease: []string{"4.9"}},
				1234568: {ID: 1234568, Blocks: []int{1234572}, TargetRelease: []string{"4.9"}},
				1234569: {ID: 1234569, Blocks: []int{1234570}, TargetRelease: []string{"4.9"}},

				1234570: {ID: 1234570, DependsOn: []int{1234569}, TargetRelease: []string{"4.10"}},
				1234571: {ID: 1234571, DependsOn: []int{1234567}, TargetRelease: []string{"4.10"}},
				1234572: {ID: 1234572, DependsOn: []int{1234568}, TargetRelease: []string{"4.10"}},
			},
		},
	}

	for _, tc := range testCases {
		fakeClient := bugzilla.Fake{
			Bugs:         bugsByID(tc.bugs),
			SearchedBugs: tc.bugs,
			BugComments: map[int][]bugzilla.Comment{
				1234567: {bugzilla.Comment{BugID: 1234567, Text: "Whatever"}},
				1234568: {bugzilla.Comment{BugID: 1234567, Text: "Whatever"}},
				1234569: {bugzilla.Comment{BugID: 1234567, Text: "Whatever"}},
				1234570: {bugzilla.Comment{BugID: 1234567, Text: "Whatever"}},
			},
		}

		o := options{
			fromRelease: tc.fromRelease,
			toRelease:   tc.toRelease,
		}

		var initialBugs []*bugzilla.Bug
		for _, bug := range tc.bugs {
			if bug.Blocks != nil || bug.DependsOn != nil {
				continue
			}
			initialBugs = append(initialBugs, bug)
		}

		err := o.massCloneBugs(&fakeClient, initialBugs)
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff(fakeClient.Bugs, tc.expected); diff != "" {
			t.Fatal(diff)
		}

	}
}

func bugsByID(bugs []*bugzilla.Bug) map[int]bugzilla.Bug {
	ret := make(map[int]bugzilla.Bug)
	for _, bug := range bugs {
		ret[bug.ID] = *bug
	}
	return ret
}
