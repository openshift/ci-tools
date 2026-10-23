package gsmsecrets

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/google/go-cmp/cmp"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// discoveryPage describes a scripted ListSecrets response with synthetic data.
type discoveryPage struct {
	code codes.Code
	ids  []string
	next string
}

// discoveryServer serves scripted pages over an in-memory gRPC connection.
type discoveryServer struct {
	secretmanagerpb.UnimplementedSecretManagerServiceServer
	mu       sync.Mutex
	pages    []discoveryPage
	requests []*secretmanagerpb.ListSecretsRequest
	cancel   context.CancelFunc
}

// ListSecrets records the request and returns the next scripted response.
func (server *discoveryServer) ListSecrets(_ context.Context, req *secretmanagerpb.ListSecretsRequest) (*secretmanagerpb.ListSecretsResponse, error) {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.requests = append(server.requests, req)
	if server.cancel != nil {
		server.cancel()
	}
	if len(server.requests) > len(server.pages) {
		return nil, status.Error(codes.Internal, "unexpected extra attempt")
	}
	page := server.pages[len(server.requests)-1]
	if page.code != codes.OK {
		return nil, status.Error(page.code, "synthetic-sensitive-response")
	}
	response := &secretmanagerpb.ListSecretsResponse{NextPageToken: page.next}
	for _, id := range page.ids {
		response.Secrets = append(response.Secrets, &secretmanagerpb.Secret{Name: "projects/123/secrets/" + id})
	}
	return response, nil
}

// discoveryDialer connects only to an in-memory listener, never the network.
type discoveryDialer struct {
	listener *bufconn.Listener
}

// dial opens an in-memory connection for the real SDK client.
func (dialer discoveryDialer) dial(ctx context.Context, _ string) (net.Conn, error) {
	return dialer.listener.DialContext(ctx)
}

// TestListSecretsRetryerRetry checks bounded attempts, classification and backoff ranges.
func TestListSecretsRetryerRetry(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name string
		code codes.Code
		want []bool
	}{
		{name: "unavailable retries twice", code: codes.Unavailable, want: []bool{true, true, false, false}},
		{name: "permission denied", code: codes.PermissionDenied, want: []bool{false}},
		{name: "resource exhausted is not broadened", code: codes.ResourceExhausted, want: []bool{false}},
		{name: "deadline exceeded", code: codes.DeadlineExceeded, want: []bool{false}},
		{name: "canceled", code: codes.Canceled, want: []bool{false}},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			retryer := newListSecretsRetryer()
			for attempt, want := range tc.want {
				pause, got := retryer.Retry(status.Error(tc.code, "synthetic-sensitive-response"))
				if got != want {
					t.Errorf("attempt %d: want retry=%t, got %t", attempt+1, want, got)
				}
				maxPause := (100 * time.Millisecond) << attempt
				if want && (pause <= 0 || pause > maxPause) {
					t.Errorf("attempt %d: backoff %s outside (0, %s]", attempt+1, pause, maxPause)
				}
				if !want && pause != 0 {
					t.Errorf("non-retryable error has backoff %s", pause)
				}
			}
		})
	}
}

// TestListSecretsDiscoveryErrorError verifies safe diagnostics and cause preservation.
func TestListSecretsDiscoveryErrorError(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name  string
		cause error
		code  codes.Code
	}{
		{name: "RPC message omitted", cause: status.Error(codes.Unavailable, "synthetic-sensitive-response"), code: codes.Unavailable},
		{name: "unknown error omitted", cause: errors.New("synthetic-sensitive-response"), code: codes.Unknown},
		{name: "canceled", cause: context.Canceled, code: codes.Canceled},
		{name: "deadline exceeded", cause: fmt.Errorf("synthetic-sensitive-response: %w", context.DeadlineExceeded), code: codes.DeadlineExceeded},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			failure := &listSecretsDiscoveryError{err: tc.cause}
			want := fmt.Sprintf("secretmanager ListSecrets discovery failed (status=%s, max_attempts_per_page=3, discovery_budget=1m0s)", tc.code)
			if got := fmt.Errorf("credential discovery: %w", failure).Error(); got != "credential discovery: "+want {
				t.Errorf("unexpected safe diagnostic: %q", got)
			}
			if !errors.Is(failure, tc.cause) {
				t.Error("discovery error does not preserve cause")
			}
		})
	}
}

// serveDiscovery runs the in-memory server until cleanup stops it.
func serveDiscovery(t *testing.T, server *grpc.Server, listener *bufconn.Listener) {
	if err := server.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		t.Errorf("serving discovery: %v", err)
	}
}

// newDiscoveryClient constructs the pinned SDK client without credentials or network access.
func newDiscoveryClient(t *testing.T, fake *discoveryServer) *secretmanager.Client {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	secretmanagerpb.RegisterSecretManagerServiceServer(server, fake)
	go serveDiscovery(t, server, listener)
	t.Cleanup(server.Stop)
	connection, err := grpc.NewClient("passthrough:///discovery-test",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(discoveryDialer{listener: listener}.dial))
	if err != nil {
		t.Fatalf("creating in-memory connection: %v", err)
	}
	client, err := secretmanager.NewClient(context.Background(), option.WithGRPCConn(connection), option.WithoutAuthentication())
	if err != nil {
		if closeErr := connection.Close(); closeErr != nil {
			t.Errorf("closing connection after client creation failure: %v", closeErr)
		}
		t.Fatalf("creating SDK client: %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("closing SDK client: %v", err)
		}
	})
	return client
}

// discoveryContextClient captures the discovery context before delegating to the SDK.
type discoveryContextClient struct {
	SecretManagerClient
	ctx context.Context
}

// ListSecrets captures the context used for all pages of the SDK iterator.
func (client *discoveryContextClient) ListSecrets(ctx context.Context, req *secretmanagerpb.ListSecretsRequest, opts ...gax.CallOption) *secretmanager.SecretIterator {
	client.ctx = ctx
	return client.SecretManagerClient.ListSecrets(ctx, req, opts...)
}

// TestListSecretFieldsByCollectionAndGroup exercises real SDK retry and pagination behavior.
func TestListSecretFieldsByCollectionAndGroup(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name       string
		group      string
		pages      []discoveryPage
		want       []string
		tokens     []string
		code       codes.Code
		cancel     bool
		cancelCall bool
		expired    bool
		longParent bool
	}{
		{
			name: "unavailable then success with unchanged filtering", group: "group",
			pages: []discoveryPage{{code: codes.Unavailable}, {ids: []string{
				"test__group__username", "other__group__field", "test__other__field",
				"test__group__nested__field", "test____index", "test__group__aws--u--creds",
			}}},
			want: []string{"username", "aws--u--creds"}, tokens: []string{"", ""},
		},
		{
			name: "persistent unavailable exhausts attempts", group: "group",
			pages:  []discoveryPage{{code: codes.Unavailable}, {code: codes.Unavailable}, {code: codes.Unavailable}},
			tokens: []string{"", "", ""}, code: codes.Unavailable,
		},
		{
			name: "permission denied fails immediately", group: "group",
			pages: []discoveryPage{{code: codes.PermissionDenied}}, tokens: []string{""}, code: codes.PermissionDenied,
		},
		{
			name: "invalid argument fails immediately", group: "group",
			pages: []discoveryPage{{code: codes.InvalidArgument}}, tokens: []string{""}, code: codes.InvalidArgument,
		},
		{
			name: "pagination retries only failed page with fresh budget", group: "group/subgroup",
			pages: []discoveryPage{
				{code: codes.Unavailable}, {code: codes.Unavailable},
				{ids: []string{"test__group__subgroup__username"}, next: "page2"},
				{code: codes.Unavailable}, {code: codes.Unavailable},
				{ids: []string{"test__group__subgroup__password"}},
			},
			want: []string{"username", "password"}, tokens: []string{"", "", "", "page2", "page2", "page2"},
		},
		{
			name: "page failure discards partial fields", group: "group",
			pages: []discoveryPage{
				{ids: []string{"test__group__username"}, next: "page2"},
				{code: codes.Unavailable}, {code: codes.Unavailable}, {code: codes.Unavailable},
			},
			tokens: []string{"", "page2", "page2", "page2"}, code: codes.Unavailable,
		},
		{
			name:  "empty page and empty group exclude index and nested fields",
			pages: []discoveryPage{{next: "page2"}, {ids: []string{"test____index", "test__username", "test__nested__field"}}},
			want:  []string{"username"}, tokens: []string{"", "page2"},
		},
		{name: "canceled before discovery", cancel: true, code: codes.Canceled},
		{name: "deadline exceeded before discovery", expired: true, code: codes.DeadlineExceeded},
		{name: "long parent deadline is capped", longParent: true, pages: []discoveryPage{{}}, tokens: []string{""}},
		{
			name: "cancellation stops retries", group: "group", cancelCall: true,
			pages: []discoveryPage{{code: codes.Unavailable}}, tokens: []string{""}, code: codes.Canceled,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			if tc.expired {
				var deadlineCancel context.CancelFunc
				ctx, deadlineCancel = context.WithDeadline(ctx, time.Unix(1, 0))
				defer deadlineCancel()
			}
			if tc.longParent {
				var deadlineCancel context.CancelFunc
				ctx, deadlineCancel = context.WithDeadline(ctx, time.Date(9999, time.January, 1, 0, 0, 0, 0, time.UTC))
				defer deadlineCancel()
			}
			fake := &discoveryServer{pages: tc.pages}
			if tc.cancelCall {
				fake.cancel = cancel
			}
			client := &discoveryContextClient{SecretManagerClient: newDiscoveryClient(t, fake)}
			fields, err := ListSecretFieldsByCollectionAndGroup(ctx, client, Config{ProjectIdNumber: "123"}, "test", tc.group)
			deadline, hasDeadline := client.ctx.Deadline()
			if !hasDeadline {
				t.Fatal("discovery context has no overall budget")
			}
			if parentDeadline, hasParentDeadline := ctx.Deadline(); hasParentDeadline {
				if tc.expired && !deadline.Equal(parentDeadline) {
					t.Error("discovery did not preserve earlier parent deadline")
				}
				if tc.longParent && !deadline.Before(parentDeadline) {
					t.Error("discovery did not cap longer parent deadline")
				}
			}
			if diff := cmp.Diff(tc.want, fields); diff != "" {
				t.Errorf("unexpected fields (-want +got):\n%s", diff)
			}
			if tc.code == codes.OK && err != nil {
				t.Fatalf("unexpected discovery error: %v", err)
			}
			if tc.code != codes.OK {
				wantError := fmt.Sprintf("secretmanager ListSecrets discovery failed (status=%s, max_attempts_per_page=3, discovery_budget=1m0s)", tc.code)
				if err == nil || err.Error() != wantError {
					t.Fatalf("expected safe error %q, got %v", wantError, err)
				}
				if status.Code(err) != tc.code && !errors.Is(err, ctx.Err()) {
					t.Errorf("original error status not preserved: %v", err)
				}
			}
			fake.mu.Lock()
			defer fake.mu.Unlock()
			var tokens []string
			filter := "name:test__"
			if tc.group != "" {
				filter += strings.ReplaceAll(tc.group, "/", "__") + "__"
			}
			for _, req := range fake.requests {
				tokens = append(tokens, req.PageToken)
				if req.Parent != "projects/123" || req.Filter != filter {
					t.Errorf("unexpected discovery request: %v", req)
				}
			}
			if diff := cmp.Diff(tc.tokens, tokens); diff != "" {
				t.Errorf("unexpected page attempts (-want +got):\n%s", diff)
			}
		})
	}
}
