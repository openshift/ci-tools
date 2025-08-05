package metrics

import (
	"testing"
)

func TestParseLeaseEventName(t *testing.T) {
	tests := []struct {
		raw           string
		wantRegion    string
		wantCanonical string
		wantSlice     string
	}{
		{
			raw:           "us-east-1--alibabacloud-cn-qe-quota-slice-0",
			wantRegion:    "us-east-1",
			wantCanonical: "alibabacloud-cn-qe-quota-slice",
			wantSlice:     "0",
		},
		{
			raw:           "us-east-2--aws-2-quota-slice-12",
			wantRegion:    "us-east-2",
			wantCanonical: "aws-2-quota-slice",
			wantSlice:     "12",
		},
		{
			raw:           "nosplitname",
			wantRegion:    "",
			wantCanonical: "nosplitname",
			wantSlice:     "",
		},
	}

	for _, tt := range tests {
		gotRegion, gotCanonical, gotSlice := parseLeaseEventName(tt.raw)
		if gotRegion != tt.wantRegion {
			t.Errorf("parseLeaseEventName(%q) region = %q; want %q", tt.raw, gotRegion, tt.wantRegion)
		}
		if gotCanonical != tt.wantCanonical {
			t.Errorf("parseLeaseEventName(%q) canonical = %q; want %q", tt.raw, gotCanonical, tt.wantCanonical)
		}
		if gotSlice != tt.wantSlice {
			t.Errorf("parseLeaseEventName(%q) slice = %q; want %q", tt.raw, gotSlice, tt.wantSlice)
		}
	}
}
