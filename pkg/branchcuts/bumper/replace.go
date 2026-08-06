package bumper

import (
	"fmt"
	"regexp"
	"strconv"
)

func ReplaceWithNextVersionInPlace(line *string, major int) error {
	newLine, err := ReplaceWithNextVersion(*line, major)
	if err != nil {
		return err
	}
	*line = newLine
	return nil
}

// Find every {major}.{minor} reference into 'line' and replace it
// with {major}.{minor+1}.
// Uses word-boundary-like context (non-digit or string edge) to avoid
// false positives from substring matches (e.g., "5.0" inside "15.0").
func ReplaceWithNextVersion(line string, major int) (string, error) {
	p := fmt.Sprintf(`%d\.(\d+)`, major)
	r := regexp.MustCompile(p)
	indices := r.FindAllStringSubmatchIndex(line, -1)
	if indices == nil {
		return line, nil
	}

	// Filter matches: keep only those bounded by non-digit (or string edge)
	var validIndices [][]int
	for _, idx := range indices {
		matchStart := idx[0]
		matchEnd := idx[1]
		if matchStart > 0 && line[matchStart-1] >= '0' && line[matchStart-1] <= '9' {
			continue
		}
		if matchEnd < len(line) && line[matchEnd] >= '0' && line[matchEnd] <= '9' {
			continue
		}
		validIndices = append(validIndices, idx)
	}

	if len(validIndices) == 0 {
		return line, nil
	}

	// Replace minor versions at exact positions, right-to-left to preserve indices
	for i := len(validIndices) - 1; i >= 0; i-- {
		idx := validIndices[i]
		minorStart, minorEnd := idx[2], idx[3]
		minorStr := line[minorStart:minorEnd]
		// Skip minors with leading zeroes (not standard version numbers)
		if len(minorStr) > 1 && minorStr[0] == '0' {
			continue
		}
		minor, err := strconv.Atoi(minorStr)
		if err != nil {
			return line, fmt.Errorf("collect dot-separated versions: %w", err)
		}
		nextMinor := strconv.Itoa(minor + 1)
		line = line[:minorStart] + nextMinor + line[minorEnd:]
	}

	return line, nil
}

// ReplaceVersionVariants replaces version strings in all common formats:
// X.Y (dot), X-Y (hyphen), and X_Y (underscore).
// This is useful for bumping versions in filenames, Slack channels, template names, etc.
func ReplaceVersionVariants(line string, major int) (string, error) {
	// First handle the standard dot format (X.Y)
	result, err := ReplaceWithNextVersion(line, major)
	if err != nil {
		return line, fmt.Errorf("replace dot-separated versions: %w", err)
	}

	// Handle hyphen format (X-Y) — e.g., cnv-release-5-0-z, prow-ocp-5-0-component-readiness
	result, err = replaceVersionWithSeparator(result, major, "-")
	if err != nil {
		return result, fmt.Errorf("replace hyphen-separated versions: %w", err)
	}

	// Handle underscore format (X_Y) — e.g., template_name_5_0
	result, err = replaceVersionWithSeparator(result, major, "_")
	if err != nil {
		return result, fmt.Errorf("replace underscore-separated versions: %w", err)
	}

	return result, nil
}

// replaceVersionWithSeparator finds {major}{sep}{minor} patterns and bumps them.
// It uses word-boundary-like context to avoid false positives (e.g., not matching
// "15-0" when major is 5).
func replaceVersionWithSeparator(line string, major int, sep string) (string, error) {
	// Match {major}{sep}{minor} without boundary characters in the pattern.
	// Boundaries are checked manually after matching to avoid:
	// 1. False positives from strings.ReplaceAll matching substrings (e.g., "5-0" inside "15-0")
	// 2. Adjacent token misses from consumed delimiters (e.g., only finding one "5-0" in "5-0-5-1")
	p := fmt.Sprintf(`%d%s(\d+)`, major, regexp.QuoteMeta(sep))
	r := regexp.MustCompile(p)
	indices := r.FindAllStringSubmatchIndex(line, -1)
	if indices == nil {
		return line, nil
	}

	// Filter matches: keep only those bounded by non-digit (or string edge)
	var validIndices [][]int
	for _, idx := range indices {
		matchStart := idx[0]
		matchEnd := idx[1]
		if matchStart > 0 && line[matchStart-1] >= '0' && line[matchStart-1] <= '9' {
			continue
		}
		if matchEnd < len(line) && line[matchEnd] >= '0' && line[matchEnd] <= '9' {
			continue
		}
		validIndices = append(validIndices, idx)
	}

	if len(validIndices) == 0 {
		return line, nil
	}

	// Replace minor versions at exact positions, right-to-left to preserve indices
	for i := len(validIndices) - 1; i >= 0; i-- {
		idx := validIndices[i]
		minorStart, minorEnd := idx[2], idx[3]
		minorStr := line[minorStart:minorEnd]
		minor, err := strconv.Atoi(minorStr)
		if err != nil {
			return line, fmt.Errorf("collect %q-separated versions: %w", sep, err)
		}
		nextMinor := strconv.Itoa(minor + 1)
		line = line[:minorStart] + nextMinor + line[minorEnd:]
	}

	return line, nil
}

