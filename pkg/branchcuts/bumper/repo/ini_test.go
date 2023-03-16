package repo_test

import (
	"io"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/branchcuts/bumper/repo"
)

func TestIniRead(t *testing.T) {
	bumpText := func(t string) string { return strings.ReplaceAll(t, "4.12", "4.13") }
	for _, testCase := range []struct {
		name        string
		ini         string
		wantIni     string
		bufferSizes []int
	}{
		{
			name:        "read lines into small buffers",
			ini:         "[section-4.12]\na=1\nb=2\n\n",
			wantIni:     "[section-4.13]\na=1\nb=2\n\n",
			bufferSizes: []int{9, 6, 4, 4, 1},
		},
		{
			name:        "section name only",
			ini:         "[section-4.12]",
			wantIni:     "[section-4.13]",
			bufferSizes: []int{14},
		},
		{
			name:        "no trailing newline",
			ini:         "[section-4.12]\na=1",
			wantIni:     "[section-4.13]\na=1",
			bufferSizes: []int{15, 3},
		},
		{
			name:        "no bumping",
			ini:         "[section-4.11]\na=1",
			wantIni:     "[section-4.11]\na=1",
			bufferSizes: []int{15, 3},
		},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			stringReader := io.NopCloser(strings.NewReader(testCase.ini))
			iniReader := repo.NewIniReadCloser(stringReader, bumpText)
			result := ""
			for _, size := range testCase.bufferSizes {
				b := make([]byte, size)
				n, err := iniReader.Read(b)
				if err != nil {
					break
				}
				if n != len(b) {
					t.Fatalf("read %d bytes but %d expected", n, len(b))
				}
				result += string(b)
			}

			_, err := iniReader.Read([]byte{})
			if io.EOF != err {
				t.Fatalf("want EOF but got: %q", err.Error())
			}

			if diff := cmp.Diff(result, testCase.wantIni); diff != "" {
				t.Fatalf("unexpected ini result: %s", diff)
			}
		})
	}
}
