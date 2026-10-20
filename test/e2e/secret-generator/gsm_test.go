//go:build e2e
// +build e2e

package secret_generator

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"

	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeGSM stores secrets with a single version for the generator/bootstrap flow.
type fakeGSM struct {
	secretmanagerpb.UnimplementedSecretManagerServiceServer
	mu      sync.Mutex
	secrets map[string]*secretmanagerpb.SecretPayload
}

func startGSMTestServer(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	secretmanagerpb.RegisterSecretManagerServiceServer(server, &fakeGSM{secrets: map[string]*secretmanagerpb.SecretPayload{
		"projects/123456789/secrets/test-platform-infra____index": {},
	}})
	t.Cleanup(server.Stop)
	go serveGSM(t, server, listener)
	return listener.Addr().String()
}

func serveGSM(t *testing.T, server *grpc.Server, listener net.Listener) {
	if err := server.Serve(listener); err != nil {
		t.Errorf("serving GSM: %v", err)
	}
}

func (f *fakeGSM) GetSecret(_ context.Context, req *secretmanagerpb.GetSecretRequest) (*secretmanagerpb.Secret, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.secrets[req.Name]; !exists {
		return nil, status.Error(codes.NotFound, "secret not found")
	}
	return &secretmanagerpb.Secret{Name: req.Name}, nil
}

func (f *fakeGSM) CreateSecret(_ context.Context, req *secretmanagerpb.CreateSecretRequest) (*secretmanagerpb.Secret, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name := req.Parent + "/secrets/" + req.SecretId
	if _, exists := f.secrets[name]; exists {
		return nil, status.Error(codes.AlreadyExists, "secret already exists")
	}
	f.secrets[name] = nil
	return &secretmanagerpb.Secret{Name: name}, nil
}

func (f *fakeGSM) AddSecretVersion(_ context.Context, req *secretmanagerpb.AddSecretVersionRequest) (*secretmanagerpb.SecretVersion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	payload, exists := f.secrets[req.Parent]
	if !exists {
		return nil, status.Error(codes.NotFound, "secret not found")
	}
	if payload != nil && len(payload.Data) != 0 {
		return nil, status.Error(codes.FailedPrecondition, "fake only supports one populated version")
	}
	f.secrets[req.Parent] = req.Payload
	return &secretmanagerpb.SecretVersion{Name: req.Parent + "/versions/1", State: secretmanagerpb.SecretVersion_ENABLED}, nil
}

func (f *fakeGSM) ListSecretVersions(_ context.Context, req *secretmanagerpb.ListSecretVersionsRequest) (*secretmanagerpb.ListSecretVersionsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	payload, exists := f.secrets[req.Parent]
	if !exists {
		return nil, status.Error(codes.NotFound, "secret not found")
	}
	response := &secretmanagerpb.ListSecretVersionsResponse{}
	if payload != nil {
		response.Versions = []*secretmanagerpb.SecretVersion{{Name: req.Parent + "/versions/1", State: secretmanagerpb.SecretVersion_ENABLED}}
	}
	return response, nil
}

func (f *fakeGSM) AccessSecretVersion(_ context.Context, req *secretmanagerpb.AccessSecretVersionRequest) (*secretmanagerpb.AccessSecretVersionResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	payload := f.secrets[strings.TrimSuffix(req.Name, "/versions/latest")]
	if payload == nil {
		return nil, status.Error(codes.NotFound, "secret version not found")
	}
	return &secretmanagerpb.AccessSecretVersionResponse{Name: req.Name, Payload: payload}, nil
}
