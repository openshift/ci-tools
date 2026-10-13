package gsmproxy

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestBearerToken(t *testing.T) {
	testCases := []struct {
		name    string
		header  string
		want    string
		wantErr bool
	}{
		{
			name:   "bearer",
			header: "Bearer abc",
			want:   "abc",
		},
		{
			name:   "case insensitive scheme",
			header: "bearer abc",
			want:   "abc",
		},
		{
			name:   "whitespace around token is trimmed",
			header: "Bearer   abc \t",
			want:   "abc",
		},
		{
			name:    "missing header",
			wantErr: true,
		},
		{
			name:    "missing separator",
			header:  "Bearer",
			wantErr: true,
		},
		{
			name:    "empty token",
			header:  "Bearer   ",
			wantErr: true,
		},
		{
			name:    "not bearer",
			header:  "Basic abc",
			wantErr: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := BearerToken(tc.header)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, want error = %t", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("token = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIdentityFromContext(t *testing.T) {
	identity := Identity{Username: "test-user", Groups: []string{"team-a"}}
	testCases := []struct {
		name string
		ctx  context.Context
		want Identity
		ok   bool
	}{
		{
			name: "no identity",
			ctx:  context.Background(),
		},
		{
			name: "verified identity",
			ctx:  context.WithValue(context.Background(), identityContextKey{}, identity),
			want: identity,
			ok:   true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := IdentityFromContext(tc.ctx)
			if ok != tc.ok {
				t.Errorf("identity present = %t, want %t", ok, tc.ok)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("identity mismatch (-want +got): %s", diff)
			}
		})
	}
}
