package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

func TestUpdateDockerfile(t *testing.T) {
	testCases := []struct {
		name                string
		dockerfile          []byte
		config              ocpImageConfig
		expectededErrMsg    string
		expectedUpdate      bool
		expecteddDockerfile []byte
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
				Builder:                  []ocpImageConfigFromStream{{Stream: "rhel-8-golang"}, {Stream: "golang"}},
				ocpImageConfigFromStream: ocpImageConfigFromStream{Stream: "rhel"},
			}},
			expecteddDockerfile: []byte(`# This dockerfile is used for building for OpenShift
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
		},
		{
			name: "Dockerfile gets updated, comment preceeding directive",
			dockerfile: []byte(`# This dockerfile is used for building for OpenShift
FROM openshift/origin-release:rhel-8-golang-1.12 as rhel8
FROM something
`),
			config: ocpImageConfig{From: ocpImageConfigFrom{
				Builder:                  []ocpImageConfigFromStream{{Stream: "replaced"}},
				ocpImageConfigFromStream: ocpImageConfigFromStream{Stream: "replacement-2"},
			}},
			expectedUpdate: true,
			expecteddDockerfile: []byte(`# This dockerfile is used for building for OpenShift
FROM replaced as rhel8
FROM replacement-2
`),
		},
		{
			name: "Dockerfile gets updated, no comment preceeding directive",
			dockerfile: []byte(`FROM openshift/origin-release:rhel-8-golang-1.12 as rhel8
FROM something
`),
			config: ocpImageConfig{From: ocpImageConfigFrom{
				Builder:                  []ocpImageConfigFromStream{{Stream: "replaced"}},
				ocpImageConfigFromStream: ocpImageConfigFromStream{Stream: "replacement-2"},
			}},
			expectedUpdate: true,
			expecteddDockerfile: []byte(`FROM replaced as rhel8
FROM replacement-2
`),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var actualErrMsg string
			result, changed, err := updateDockerfile(tc.dockerfile, tc.config)
			if err != nil {
				actualErrMsg = err.Error()
			}
			if actualErrMsg != tc.expectededErrMsg {
				t.Fatalf("expecteded error to be %q, was %q", tc.expectededErrMsg, actualErrMsg)
			}
			if actualErrMsg != "" {
				return
			}

			if tc.expectedUpdate != changed {
				t.Errorf("expecteded change: %t, got change: %t", tc.expectedUpdate, changed)
			}
			if !tc.expectedUpdate {
				return
			}
			if diff := cmp.Diff(string(result), string(tc.expecteddDockerfile)); diff != "" {
				t.Errorf("result difers from expecteded: %s", diff)
			}
		})
	}
}

func TestDereferenceConfig(t *testing.T) {
	testCases := []struct {
		name           string
		config         ocpImageConfig
		majorMinor     majorMinor
		allConfigs     map[string]ocpImageConfig
		streamMap      streamMap
		groupYAML      groupYAML
		expectedConfig ocpImageConfig
		expectedError  error
	}{
		{
			name: "config.from.stream gets replaced",
			config: ocpImageConfig{
				From: ocpImageConfigFrom{
					ocpImageConfigFromStream: ocpImageConfigFromStream{Stream: "golang"},
				},
			},
			streamMap: streamMap{"golang": {UpstreamImage: "openshift/golang-builder:rhel_8_golang_1.14"}},
			expectedConfig: ocpImageConfig{
				From: ocpImageConfigFrom{
					ocpImageConfigFromStream: ocpImageConfigFromStream{Stream: "openshift/golang-builder:rhel_8_golang_1.14"},
				},
			},
		},
		{
			name: "config.from.member gets replaced",
			config: ocpImageConfig{
				From: ocpImageConfigFrom{
					ocpImageConfigFromStream: ocpImageConfigFromStream{Member: "openshift-enterprise-base"},
				},
			},
			majorMinor: majorMinor{major: "4", minor: "6"},
			allConfigs: map[string]ocpImageConfig{
				"images/openshift-enterprise-base.yml": {Name: "openshift/ose-base"},
			},
			expectedConfig: ocpImageConfig{
				From: ocpImageConfigFrom{
					ocpImageConfigFromStream: ocpImageConfigFromStream{
						Stream: "registry.svc.ci.openshift.org/ocp/4.6:base"},
				},
			},
		},
		{
			name:          "both config from.stream and config.from.member are empty, error",
			expectedError: errors.New("failed to find replacement for .from.stream"),
		},
		{
			name: "config.from.builder.stream gets replaced",
			config: ocpImageConfig{
				From: ocpImageConfigFrom{
					Builder:                  []ocpImageConfigFromStream{{Stream: "golang"}},
					ocpImageConfigFromStream: ocpImageConfigFromStream{Stream: "golang"},
				},
			},
			streamMap: streamMap{"golang": {UpstreamImage: "openshift/golang-builder:rhel_8_golang_1.14"}},
			expectedConfig: ocpImageConfig{
				From: ocpImageConfigFrom{
					Builder:                  []ocpImageConfigFromStream{{Stream: "openshift/golang-builder:rhel_8_golang_1.14"}},
					ocpImageConfigFromStream: ocpImageConfigFromStream{Stream: "openshift/golang-builder:rhel_8_golang_1.14"},
				},
			},
		},
		{
			name: "config.from.builder.member gets replaced",
			config: ocpImageConfig{
				From: ocpImageConfigFrom{
					Builder:                  []ocpImageConfigFromStream{{Member: "openshift-enterprise-base"}},
					ocpImageConfigFromStream: ocpImageConfigFromStream{Member: "openshift-enterprise-base"},
				},
			},
			majorMinor: majorMinor{major: "4", minor: "6"},
			allConfigs: map[string]ocpImageConfig{
				"images/openshift-enterprise-base.yml": {Name: "openshift/ose-base"},
			},
			expectedConfig: ocpImageConfig{
				From: ocpImageConfigFrom{
					Builder: []ocpImageConfigFromStream{{Stream: "registry.svc.ci.openshift.org/ocp/4.6:base"}},
					ocpImageConfigFromStream: ocpImageConfigFromStream{
						Stream: "registry.svc.ci.openshift.org/ocp/4.6:base"},
				},
			},
		},
		{
			name: "both config.from.builder.stream and config.from.builder.member are empty, error",
			config: ocpImageConfig{
				From: ocpImageConfigFrom{
					Builder: []ocpImageConfigFromStream{{}},
				},
			},
			expectedError: utilerrors.NewAggregate([]error{
				errors.New("failed to find replacement for .from.stream"),
				fmt.Errorf("failed to dereference from.builder.%d", 0),
			}),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.config.Content == nil {
				tc.config.Content = &ocpImageConfigContent{}
			}
			if tc.expectedConfig.Content == nil {
				tc.expectedConfig.Content = &ocpImageConfigContent{}
			}
			var actualErrMsg string
			err := dereferenceConfig(&tc.config, tc.majorMinor, tc.allConfigs, tc.streamMap, tc.groupYAML)
			if err != nil {
				actualErrMsg = err.Error()
			}
			var expectedErrMsg string
			if tc.expectedError != nil {
				expectedErrMsg = tc.expectedError.Error()
			}
			if actualErrMsg != expectedErrMsg {
				t.Fatalf("expected error %v, got error %v", tc.expectedError, err)
			}
			if err != nil {
				return
			}
			if diff := cmp.Diff(tc.config, tc.expectedConfig, cmp.AllowUnexported(ocpImageConfigFrom{})); diff != "" {
				t.Errorf("config differs from expectedConfig: %s", diff)
			}
		})
	}
}
