package vaultclient

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"k8s.io/apimachinery/pkg/util/sets"

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

func TestListKVRecursively(t *testing.T) {
	t.Parallel()
	vaultAddr := testhelper.Vault(t)

	client, err := New("http://"+vaultAddr, testhelper.VaultTestingRootToken)
	if err != nil {
		t.Fatalf("failed to construct vault client: %v", err)
	}

	if err := client.UpsertKV("secret/item", map[string]string{"some": "data"}); err != nil {
		t.Fatalf("failed to upsecret secret/item: %v", err)
	}
	if err := client.UpsertKV("secret/nested/item", map[string]string{"some": "data"}); err != nil {
		t.Fatalf("failed to upsecret secret/nested/item: %v", err)
	}
	if err := client.UpsertKV("secret/self-managed/mine-alone/my-secret", map[string]string{"some": "data"}); err != nil {
		t.Fatalf("failed to upsecret self-managed/mine-alone/my-secret: %v", err)
	}

	result, err := client.ListKVRecursively("secret")
	if err != nil {
		t.Fatalf("failed to list: %v", err)
	}
	expected := []string{"secret/item", "secret/nested/item", "secret/self-managed/mine-alone/my-secret"}

	if diff := cmp.Diff(sets.List(sets.New[string](result...)), expected); diff != "" {
		t.Errorf("actual resutl differs from expected: %v", diff)
	}
}

func TestUpsertDoesntCreateANewRevisionWhenDataDoesntChange(t *testing.T) {
	t.Parallel()

	vaultAddr := testhelper.Vault(t)

	client, err := New("http://"+vaultAddr, testhelper.VaultTestingRootToken)
	if err != nil {
		t.Fatalf("failed to construct vault client: %v", err)
	}

	if err := client.UpsertKV("secret/item", map[string]string{"some": "data"}); err != nil {
		t.Fatalf("failed to upsecret secret/item: %v", err)
	}
	if err := client.UpsertKV("secret/item", map[string]string{"some": "data"}); err != nil {
		t.Fatalf("failed to upsecret secret/item: %v", err)
	}

	data, err := client.GetKV("secret/item")
	if err != nil {
		t.Fatalf("failed to get data: %v", err)
	}

	if data.Metadata.Version != 1 {
		t.Errorf("Expcted version to be 1, was %d", data.Metadata.Version)
	}

	newData := map[string]string{"new": "data"}
	if err := client.UpsertKV("secret/item", newData); err != nil {
		t.Fatalf("failed to upsecret secret/item: %v", err)
	}

	data, err = client.GetKV("secret/item")
	if err != nil {
		t.Fatalf("failed to get data: %v", err)
	}
	if diff := cmp.Diff(newData, data.Data); diff != "" {
		t.Errorf("data in secret store differs from updated data: %s", diff)
	}
	if data.Metadata.Version != 2 {
		t.Errorf("expected versio to be 2, was %d", data.Metadata.Version)
	}

}
