package main

import (
	"testing"

	"github.com/openshift/ci-tools/pkg/testhelper"
)

func TestUpdateDockerfile(t *testing.T) {
	testCases := []struct {
		name           string
		dockerfile     []byte
		config         ocpImageConfig
		expectedErrMsg string
		expectUpdate   bool
	}{
		{
			name: "No changes",
			dockerfile: []byte(`# This dockerfile is used for building for OpenShift
FROM rhel-8-golang as rhel8
ADD . /go/src/github.com/dougbtv/whereabouts
WORKDIR /go/src/github.com/dougbtv/whereabouts
ENV CGO_ENABLED=1
ENV GO111MODULE=on
ENV VERSION=rhel8 COMMIT=unset
RUN go build -mod vendor -o bin/whereabouts cmd/whereabouts.go
WORKDIR /

FROM golang as rhel7
ADD . /go/src/github.com/dougbtv/whereabouts
WORKDIR /go/src/github.com/dougbtv/whereabouts
ENV CGO_ENABLED=1
ENV GO111MODULE=on
RUN go build -mod vendor -o bin/whereabouts cmd/whereabouts.go
WORKDIR /

FROM rhel
RUN mkdir -p /usr/src/whereabouts/images && \
       mkdir -p /usr/src/whereabouts/bin && \
       mkdir -p /usr/src/whereabouts/rhel7/bin && \
       mkdir -p /usr/src/whereabouts/rhel8/bin
COPY --from=rhel7 /go/src/github.com/dougbtv/whereabouts/bin/whereabouts /usr/src/whereabouts/rhel7/bin
COPY --from=rhel7 /go/src/github.com/dougbtv/whereabouts/bin/whereabouts /usr/src/whereabouts/bin
COPY --from=rhel8 /go/src/github.com/dougbtv/whereabouts/bin/whereabouts /usr/src/whereabouts/rhel8/bin

LABEL io.k8s.display-name="Whereabouts CNI" \
      io.k8s.description="This is a component of OpenShift Container Platform and provides a cluster-wide IPAM CNI plugin." \
      io.openshift.tags="openshift" \
      maintainer="CTO Networking <nfvpe-container@redhat.com>"`),
			config: ocpImageConfig{From: ocpImageConfigFrom{
				Builder: []ocpImageConfigFromStream{{"rhel-8-golang"}, {"golang"}},
				Stream:  ocpImageConfigFromStream{"rhel"},
			}},
		},
		{
			name: "Dockerfile gets updated",
			dockerfile: []byte(`# This dockerfile is used for building for OpenShift
FROM openshift/origin-release:rhel-8-golang-1.12 as rhel8
ADD . /go/src/github.com/dougbtv/whereabouts
WORKDIR /go/src/github.com/dougbtv/whereabouts
ENV CGO_ENABLED=1
ENV GO111MODULE=on
ENV VERSION=rhel8 COMMIT=unset
RUN go build -mod vendor -o bin/whereabouts cmd/whereabouts.go
WORKDIR /

FROM openshift/origin-release:rhel-7-golang-1.12 as rhel7
ADD . /go/src/github.com/dougbtv/whereabouts
WORKDIR /go/src/github.com/dougbtv/whereabouts
ENV CGO_ENABLED=1
ENV GO111MODULE=on
RUN go build -mod vendor -o bin/whereabouts cmd/whereabouts.go
WORKDIR /

FROM openshift/origin-base
RUN mkdir -p /usr/src/whereabouts/images && \
       mkdir -p /usr/src/whereabouts/bin && \
       mkdir -p /usr/src/whereabouts/rhel7/bin && \
       mkdir -p /usr/src/whereabouts/rhel8/bin
COPY --from=rhel7 /go/src/github.com/dougbtv/whereabouts/bin/whereabouts /usr/src/whereabouts/rhel7/bin
COPY --from=rhel7 /go/src/github.com/dougbtv/whereabouts/bin/whereabouts /usr/src/whereabouts/bin
COPY --from=rhel8 /go/src/github.com/dougbtv/whereabouts/bin/whereabouts /usr/src/whereabouts/rhel8/bin

LABEL io.k8s.display-name="Whereabouts CNI" \
      io.k8s.description="This is a component of OpenShift Container Platform and provides a cluster-wide IPAM CNI plugin." \
      io.openshift.tags="openshift" \
      maintainer="CTO Networking <nfvpe-container@redhat.com>"`),
			config: ocpImageConfig{From: ocpImageConfigFrom{
				Builder: []ocpImageConfigFromStream{{"rhel-8-golang"}, {"golang"}},
				Stream:  ocpImageConfigFromStream{"rhel"},
			}},
			expectUpdate: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var actualErrMsg string
			result, changed, err := updateDockerfile(tc.dockerfile, tc.config)
			if err != nil {
				actualErrMsg = err.Error()
			}
			if actualErrMsg != tc.expectedErrMsg {
				t.Fatalf("expected error to be %q, was %q", tc.expectedErrMsg, actualErrMsg)
			}
			if actualErrMsg != "" {
				return
			}

			if tc.expectUpdate != changed {
				t.Errorf("expected change: %t, got change: %t", tc.expectUpdate, changed)
			}
			if !tc.expectUpdate {
				return
			}

			testhelper.CompareWithFixture(t, result)
		})
	}
}
