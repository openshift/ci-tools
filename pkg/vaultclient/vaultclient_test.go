package vaultclient

import (
	"testing"
)

func TestMetadataDataInsertion(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name string
		in   string
		want string
		fn   func(string) string
	}{
		{
			name: "Metadata, single element",
			in:   "secret",
			want: "secret/metadata",
			fn:   InsertMetadataIntoPath,
		},
		{
			name: "Metadata, multi element",
			in:   "secret/and/some/nesting",
			want: "secret/metadata/and/some/nesting",
			fn:   InsertMetadataIntoPath,
		},
		{
			name: "Data, single element",
			in:   "secret",
			want: "secret/data",
			fn:   InsertDataIntoPath,
		},
		{
			name: "Data, multi element",
			in:   "secret/and/some/nesting",
			want: "secret/data/and/some/nesting",
			fn:   InsertDataIntoPath,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if want, actual := tc.want, tc.fn(tc.in); want != actual {
				t.Errorf("want %s, got %s", want, actual)
			}
		})
	}
}
