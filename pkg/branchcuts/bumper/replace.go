package bumper

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"
)

func ReplaceWithNextVersionInPlace(line *string, major int) error {
	newLine, err := ReplaceWithNextVersion(*line, major)
	if err != nil {
		return err
	}
	*line = newLine
	return nil
}

// Find every {major}.{minor} reference into 'line' and the replace it
// with {major}.{minor+1}
func ReplaceWithNextVersion(line string, major int) (string, error) {
	p := fmt.Sprintf(`%d\.(?P<minor>\d+)`, major)
	r := regexp.MustCompile(p)
	m := r.FindAllStringSubmatch(line, -1)
	if m == nil {
		return line, nil
	}

	minors, err := uniqueSortedMinors(m)
	if err != nil {
		return line, err
	}

	for i := len(minors) - 1; i >= 0; i-- {
		minor := minors[i]
		curVersion := fmt.Sprintf("%d.%d", major, minor)
		nextVersion := fmt.Sprintf("%d.%d", major, minor+1)
		line = strings.ReplaceAll(line, curVersion, nextVersion)
	}

	return line, nil
}

// The function extracts all the minors from matches, removes duplicate and finally it returs them in
// increasing order.
//
// Matches is an array of regex matches that could be obtained by the following example:
// string: ocp_4.5-4.6-4.5
// pattern: 4\.\d+
// matches: [3]string{}
// matches[0]: []string{ "4.5", "5" }
// matches[1]: []string{ "4.6", "6" }
// matches[2]: []string{ "4.5", "5" }
//
// Given the previous input, the function returns []int{5, 6}
func uniqueSortedMinors(matches [][]string) ([]int, error) {
	minors := sets.NewInt()
	for _, m := range matches {
		for i := 1; i < len(m); i++ {
			minor, err := strconv.Atoi(m[i])
			if err != nil {
				return []int{}, err
			}
			minors.Insert(minor)
		}
	}
	return minors.List(), nil
}
