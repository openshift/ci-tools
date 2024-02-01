package main

import (
	"bytes"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/openshift/imagebuilder"
	"github.com/openshift/imagebuilder/dockerfile/parser"
)

func TestReplaceLastFrom(t *testing.T) {
	testCases := []struct {
		testName           string
		inputDockerFile    string
		expectedDockerFile string
		newBaseImage       string
		alias              string
	}{
		{
			testName: "Replace golang base image with feodra",
			inputDockerFile: `FROM golang:1.21
				COPY test.go /app/main.go
				RUN go build -o /bin/hello ./main.go`,
			expectedDockerFile: `From fedora:latest
				COPY test.go /app/main.go
				RUN go build -o /bin/hello ./main.go`,
		},
		{
			testName: "Replace only the last FROM in a multi-stage Dockerfile",
			inputDockerFile: `FROM golang:1.21 as builder
				COPY . /app
				RUN go build -o /bin/app

				FROM alpine:3.10
				COPY --from=builder /bin/app /bin/app
				CMD ["/bin/app"]`,
			expectedDockerFile: `FROM golang:1.21 as builder
				COPY . /app
				RUN go build -o /bin/app

				FROM fedora:latest
				COPY --from=builder /bin/app /bin/app
				CMD ["/bin/app"]`,
		},
		{
			testName:           "Handle nil node input",
			inputDockerFile:    "",
			expectedDockerFile: "",
		},
		{
			testName: "Dockerfile with no FROM instruction",
			inputDockerFile: `COPY test.go /app/main.go
				RUN go build -o /bin/hello ./main.go`,
			expectedDockerFile: `COPY test.go /app/main.go
				RUN go build -o /bin/hello ./main.go`,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.testName, func(t *testing.T) {
			node, err := imagebuilder.ParseDockerfile(bytes.NewBufferString(testCase.inputDockerFile))
			if err != nil {
				t.Fatalf("error parsing dockerfile: %v", err)
			}

			replaceLastFrom(node, "fedora:latest", "")

			wantNode, err := imagebuilder.ParseDockerfile(bytes.NewBufferString(testCase.expectedDockerFile))
			if err != nil {
				t.Fatalf("error parsing dockerfile: %v", err)
			}

			nodeDump := node.Dump()
			wantNodeDump := wantNode.Dump()

			if diff := cmp.Diff(wantNodeDump, nodeDump); diff != "" {
				t.Fatalf("unexpected node difference: %q\n", diff)
			}
		})
	}
}

func TestNodeHasFromRef(t *testing.T) {
	testCases := []struct {
		testName      string
		nodeFlags     []string
		expectedFrom  string
		expectedFound bool
	}{
		{
			testName:      "Node with --from flag",
			nodeFlags:     []string{"--from=builder"},
			expectedFrom:  "builder",
			expectedFound: true,
		},
		{
			testName:      "Node without --from flag",
			nodeFlags:     []string{"--other-flag=value"},
			expectedFrom:  "",
			expectedFound: false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.testName, func(t *testing.T) {
			node := &parser.Node{Flags: testCase.nodeFlags}
			from, found := nodeHasFromRef(node)

			if from != testCase.expectedFrom || found != testCase.expectedFound {
				t.Fatalf("TestNodeHasFromRef %s failed: expected (%q, %v), got (%q, %v)",
					testCase.testName, testCase.expectedFrom, testCase.expectedFound, from, found)
			}
		})
	}
}
