package gcpworkloadidentity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTokenReplacement(t *testing.T) {
	for _, tc := range []struct {
		name     string
		previous []byte
	}{{"initial", nil}, {"refresh", []byte("old")}} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "access-token")
			if tc.previous != nil {
				if err := os.WriteFile(path, tc.previous, 0444); err != nil {
					t.Fatal(err)
				}
			}
			if err := writeToken(path, "fresh"); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != "fresh" {
				t.Fatalf("replacement failed: %v", err)
			}
		})
	}
}
