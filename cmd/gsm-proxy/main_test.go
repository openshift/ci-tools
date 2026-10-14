package main

import (
	"testing"
	"time"
)

func TestOptionsValidate(t *testing.T) {
	testCases := []struct {
		name   string
		change func(*options)
		want   string
	}{
		{
			name: "valid",
		},
		{
			name: "missing ownership",
			change: func(o *options) {
				o.groupsOwnershipFile = ""
			},
			want: "--groups-ownership-file must not be empty",
		},
		{
			name: "missing project",
			change: func(o *options) {
				o.gsmProjectID = ""
			},
			want: "--gsm-project-id must not be empty",
		},
		{
			name: "missing credentials",
			change: func(o *options) {
				o.gsmCredentialsFile = ""
			},
			want: "--gsm-credentials-file must not be empty",
		},
		{
			name: "missing listen address",
			change: func(o *options) {
				o.listenAddr = ""
			},
			want: "--listen-addr must not be empty",
		},
		{
			name: "negative grace period",
			change: func(o *options) {
				o.gracePeriod = -time.Second
			},
			want: "--grace-period must not be negative",
		},
		{
			name: "immediate shutdown",
			change: func(o *options) {
				o.gracePeriod = 0
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			o := options{
				listenAddr:          "127.0.0.1:8080",
				groupsOwnershipFile: "ownership.yaml",
				gsmProjectID:        "test-project",
				gsmCredentialsFile:  "proxy-key.json",
				gracePeriod:         time.Second,
			}
			if tc.change != nil {
				tc.change(&o)
			}
			err := o.validate()
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || err.Error() != tc.want {
				t.Fatalf("validation error = %v, want %q", err, tc.want)
			}
		})
	}
}
