package v1

import (
	"reflect"
	"testing"

	"github.com/openshift/ci-tools/pkg/api/utils"
)

func TestPullRequestPayloadQualificationRunLabel(t *testing.T) {
	if !reflect.DeepEqual(utils.Trim63(PullRequestPayloadQualificationRunLabel), PullRequestPayloadQualificationRunLabel) {
		t.Fatalf("value of PullRequestPayloadQualificationRunLabel is too big")
	}
}
