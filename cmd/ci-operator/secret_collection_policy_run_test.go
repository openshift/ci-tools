package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"k8s.io/client-go/rest"

	"github.com/openshift/ci-tools/pkg/api"
	"github.com/openshift/ci-tools/pkg/defaults"
	"github.com/openshift/ci-tools/pkg/group"
	"github.com/openshift/ci-tools/pkg/metrics"
	"github.com/openshift/ci-tools/pkg/registry/server"
	"github.com/openshift/ci-tools/pkg/secrets"
)

// policyRunTransport rejects every Kubernetes mutation and records attempted writes.
type policyRunTransport struct {
	writes []string
}

// RoundTrip supplies node metadata without a live cluster and rejects other requests.
func (transport *policyRunTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method != http.MethodGet {
		transport.writes = append(transport.writes, request.Method+" "+request.URL.Path)
		return nil, fmt.Errorf("unexpected Kubernetes mutation: %s %s", request.Method, request.URL.Path)
	}
	if request.URL.Path == "/api/v1/nodes" {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"apiVersion":"v1","kind":"NodeList","items":[]}`))}, nil
	}
	return &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"apiVersion":"v1","kind":"Status","status":"Failure","reason":"NotFound","code":404}`))}, nil
}

// policyRunResolver panics if Run unexpectedly tries to resolve configuration remotely.
type policyRunResolver struct {
	server.ResolverClient
}

// TestRun verifies actual graph generation and rejection before Kubernetes writes.
func TestRun(t *testing.T) {
	for _, tc := range []struct {
		name   string
		secret api.Secret
	}{
		{name: "synced bundle", secret: api.Secret{Bundle: "synced", Namespace: "ci"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ARTIFACTS", t.TempDir())
			t.Setenv(api.CIOperatorHTTPServerIPEnvVarName, "")
			transport := &policyRunTransport{}
			clusterConfig := &rest.Config{Host: "https://cluster.invalid", Transport: transport}
			censor := secrets.NewDynamicCensor()
			agent, err := metrics.NewMetricsAgent(context.Background(), clusterConfig, &censor)
			if err != nil {
				t.Fatal(err)
			}
			config := &api.ReleaseBuildConfiguration{
				InputConfiguration: api.InputConfiguration{BaseImages: map[string]api.ImageStreamTagReference{"src": {Namespace: "ocp", Name: "base", Tag: "latest"}}},
				Tests:              []api.TestStepConfiguration{{As: "selected", Secret: &tc.secret, ContainerTestConfiguration: &api.ContainerTestConfiguration{From: "src"}}},
			}
			o := options{
				configSpec:             config,
				graphConfig:            defaults.FromConfigStatic(config),
				jobSpec:                &api.JobSpec{},
				targets:                stringSlice{values: []string{"selected"}},
				clusterConfig:          clusterConfig,
				resolverClient:         policyRunResolver{},
				metricsAgent:           agent,
				censor:                 &censor,
				secretCollectionPolicy: group.Config{BlockedCollections: map[string]group.BlockedCollection{"blocked": {}}},
				gsmConfig: api.GSMConfig{Bundles: []api.GSMBundle{
					{Name: "synced", SyncToCluster: true, GSMSecrets: []api.GSMSecretRef{{Collection: "blocked"}}},
				}},
			}
			errors := o.Run()
			if len(errors) != 1 || !strings.Contains(errors[0].Error(), "test selected: The secret collection blocked is blocked.") {
				t.Fatalf("expected blocked collection error, got %v", errors)
			}
			if o.inputHash == "" {
				t.Error("Run did not reach input resolution")
			}
			if len(transport.writes) != 0 {
				t.Errorf("preflight attempted Kubernetes writes: %v", transport.writes)
			}
		})
	}
}
