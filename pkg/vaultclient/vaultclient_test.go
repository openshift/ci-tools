package vaultclient

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/ci-tools/pkg/testhelper"
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

func TestListKVRecursively(tt *testing.T) {
	tt.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	t := testhelper.NewT(ctx, tt)
	vaultAddr := testhelper.Vault(ctx, t)

	client, err := New("http://"+vaultAddr, testhelper.VaultTestingRootToken)
	if err != nil {
		t.Fatalf("failed to construct vault client: %v", err)
	}

	if err := client.UpsertKV("/secret/item", map[string]string{"some": "data"}); err != nil {
		t.Fatalf("failed to upsecret secret/item: %v", err)
	}
	if err := client.UpsertKV("/secret/nested/item", map[string]string{"some": "data"}); err != nil {
		t.Fatalf("failed to upsecret secret/nested/item: %v", err)
	}

	result, err := client.ListKVRecursively("secret")
	if err != nil {
		t.Fatalf("failed to list: %v", err)
	}
	expected := []string{"secret/item", "secret/nested/item"}

	if diff := cmp.Diff(result, expected); diff != "" {
		t.Errorf("actual resutl differs from expected: %v", err)
	}
}
