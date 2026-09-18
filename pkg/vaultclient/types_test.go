package vaultclient

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/hashicorp/vault/api"
)

func TestIsWriteForbidden(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "vault 403", err: &api.ResponseError{StatusCode: 403}, want: true},
		{name: "read-only message", err: errors.New("Vault is in read-only mode for migration"), want: true},
		{name: "other", err: errors.New("connection refused"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, IsWriteForbidden(tt.err)); diff != "" {
				t.Errorf("IsWriteForbidden(): %s", diff)
			}
		})
	}
}
