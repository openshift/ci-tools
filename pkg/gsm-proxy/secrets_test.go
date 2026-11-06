package gsmproxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"

	gsmsecrets "github.com/openshift/ci-tools/pkg/gsm-secrets"
)

type fakeGSMServer struct {
	secretmanagerpb.UnimplementedSecretManagerServiceServer
	list       func(context.Context, *secretmanagerpb.ListSecretsRequest) (*secretmanagerpb.ListSecretsResponse, error)
	secret     *secretmanagerpb.Secret
	getError   error
	getRequest *secretmanagerpb.GetSecretRequest
}

func (s *fakeGSMServer) ListSecrets(ctx context.Context, request *secretmanagerpb.ListSecretsRequest) (*secretmanagerpb.ListSecretsResponse, error) {
	return s.list(ctx, request)
}

func (s *fakeGSMServer) GetSecret(_ context.Context, request *secretmanagerpb.GetSecretRequest) (*secretmanagerpb.Secret, error) {
	s.getRequest = request
	return s.secret, s.getError
}

func newTestGSMClient(t *testing.T, fake *fakeGSMServer) *GSMClient {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	secretmanagerpb.RegisterSecretManagerServiceServer(server, fake)
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			t.Errorf("fake GSM server: %v", err)
		}
	}()
	t.Cleanup(func() { server.Stop(); listener.Close() })
	connection, err := grpc.NewClient("passthrough:///fake-gsm",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	client, err := NewGSMClient(context.Background(), gsmsecrets.Config{ProjectIdString: "test-project"},
		option.WithGRPCConn(connection), option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close GSM client: %v", err)
		}
	})
	return client
}

func TestGSMClientList(t *testing.T) {
	type page struct {
		secretIDs     []string
		nextPageToken string
		err           error
	}
	testCases := []struct {
		name          string
		collection    string
		pages         map[string]page
		cancelContext bool
		want          []string
		wantErr       bool
		wantCode      codes.Code
		wantRequests  int32
	}{
		{
			name:       "sorted paths preserve underscores and encoded names",
			collection: "foo_bar",
			pages: map[string]page{
				"": {secretIDs: []string{"foo_bar__z", "foo_bar__g_1__nested__f_1", "foo_bar__a", "foo_bar__g__--dot--field--uu--name"}},
			},
			want:         []string{"a", "g/--dot--field--uu--name", "g_1/nested/f_1", "z"},
			wantRequests: 1,
		},
		{
			name:       "all pages are consumed",
			collection: "foo",
			pages: map[string]page{
				"":         {secretIDs: []string{"foo__z"}, nextPageToken: "page-two"},
				"page-two": {secretIDs: []string{"foo__a"}},
			},
			want:         []string{"a", "z"},
			wantRequests: 2,
		},
		{
			name:       "only secrets from the exact collection are returned",
			collection: "foo",
			pages: map[string]page{
				"": {secretIDs: []string{
					"foo__a", "foo__g__f",
					"foo-bar__a", "foobar__a", "foo_bar__a", "barfoo__a",
					"bar__foo__f", "Foo__g__f", "other__foo__leak",
				}},
			},
			want:         []string{"a", "g/f"},
			wantRequests: 1,
		},
		{
			name:       "index, updater, and malformed secrets are excluded",
			collection: "foo",
			pages: map[string]page{
				"": {secretIDs: []string{
					"foo____index", "foo__updater-service-account",
					"foo__g__service-account",
					"foo", "no-delimiter", "foo__", "",
				}},
			},
			wantRequests: 1,
			want:         []string{"g/service-account"},
		},
		{
			name:         "first page failure",
			collection:   "foo",
			pages:        map[string]page{"": {err: status.Error(codes.PermissionDenied, "upstream failure")}},
			wantErr:      true,
			wantCode:     codes.PermissionDenied,
			wantRequests: 1,
		},
		{
			name:       "later page failure discards partial results",
			collection: "foo",
			pages: map[string]page{
				"":         {secretIDs: []string{"foo__g__f"}, nextPageToken: "page-two"},
				"page-two": {err: status.Error(codes.PermissionDenied, "upstream failure")},
			},
			wantErr:      true,
			wantCode:     codes.PermissionDenied,
			wantRequests: 2,
		},
		{
			name:         "empty result",
			collection:   "foo",
			pages:        map[string]page{"": {}},
			wantRequests: 1,
		},
		{
			name:          "cancelled context",
			collection:    "foo",
			pages:         map[string]page{"": {}},
			cancelContext: true,
			wantErr:       true,
			wantCode:      codes.Canceled,
		},
		{
			name:    "empty collection is rejected",
			wantErr: true,
		},
		{
			name:       "uppercase collection is rejected",
			collection: "Foo",
			wantErr:    true,
		},
		{
			name:       "filter injection is rejected",
			collection: `foo" OR name:*`,
			wantErr:    true,
		},
		{
			name:       "collection containing the secret delimiter is rejected",
			collection: "foo__bar",
			wantErr:    true,
		},
		{
			name:       "collection containing a slash is rejected",
			collection: "foo/bar",
			wantErr:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			client := newTestGSMClient(t, &fakeGSMServer{list: func(_ context.Context, req *secretmanagerpb.ListSecretsRequest) (*secretmanagerpb.ListSecretsResponse, error) {
				requests.Add(1)
				if req.Parent != "projects/test-project" || req.Filter != fmt.Sprintf("name:%q", tc.collection+"__") {
					t.Errorf("request parent/filter = %q / %q", req.Parent, req.Filter)
				}
				page, ok := tc.pages[req.PageToken]
				if !ok {
					return nil, status.Error(codes.InvalidArgument, "unexpected page token")
				}
				if page.err != nil {
					return nil, page.err
				}
				// Deliberately ignore Google's filter to exercise the local access boundary.
				response := &secretmanagerpb.ListSecretsResponse{NextPageToken: page.nextPageToken}
				for _, secretID := range page.secretIDs {
					response.Secrets = append(response.Secrets, &secretmanagerpb.Secret{
						Name: "projects/test-project/secrets/" + secretID,
					})
				}
				return response, nil
			}})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancelContext {
				cancel()
			}
			got, err := client.List(ctx, tc.collection)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, want error = %t", err, tc.wantErr)
			}
			if tc.wantErr && got != nil {
				t.Fatalf("names = %v, want nil names on error", got)
			}
			if tc.wantCode != codes.OK && status.Code(err) != tc.wantCode {
				t.Errorf("error status = %v, want %v", status.Code(err), tc.wantCode)
			}
			if diff := cmp.Diff(tc.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("names mismatch (-want +got): %s", diff)
			}
			if got := requests.Load(); got != tc.wantRequests {
				t.Errorf("page fetch count = %d, want %d", got, tc.wantRequests)
			}
		})
	}
}

func TestGSMClientDescribe(t *testing.T) {
	const passwordSecret = "projects/test-project/secrets/team__aws__prod__password"
	created := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	testCases := []struct {
		name        string
		path        string
		secret      *secretmanagerpb.Secret
		getErr      error
		want        SecretDescription
		wantErr     error
		wantCode    codes.Code
		wantRequest string
	}{
		{
			name: "only the supported metadata is returned",
			path: "aws/prod/password",
			secret: &secretmanagerpb.Secret{
				Name:        "projects/test-project/secrets/team__aws__prod__password",
				CreateTime:  timestamppb.New(created),
				Etag:        "etag-1",
				Labels:      map[string]string{"jira-project": "dptp", "extra": "ignored"},
				Annotations: map[string]string{"rotation-instructions": "rotate yearly", "request-information": "ticket-123", "extra": "ignored"},
			},
			want: SecretDescription{
				CreateTime: &created,
				Metadata:   SecretMetadata{JiraProject: "dptp", RotationInstructions: "rotate yearly", RequestInformation: "ticket-123"},
			},
			wantRequest: passwordSecret,
		},
		{
			name:        "secret without metadata returns an empty description",
			path:        "aws/prod/password",
			secret:      &secretmanagerpb.Secret{},
			wantRequest: passwordSecret,
		},
		{
			name:    "malformed path makes no Google call",
			path:    "aws//password",
			wantErr: ErrInvalidSecretPath,
		},
		{
			name:    "path without a group makes no Google call",
			path:    "updater-service-account",
			wantErr: ErrInvalidSecretPath,
		},
		{
			name:    "field containing the delimiter makes no Google call",
			path:    "aws/__index",
			wantErr: ErrInvalidSecretPath,
		},
		{
			name:    "reserved updater service account secret makes no Google call",
			path:    "aws/updater-service-account",
			wantErr: ErrInvalidSecretPath,
		},
		{
			name:        "Google NotFound status is preserved",
			path:        "aws/prod/password",
			getErr:      status.Error(codes.NotFound, "not found"),
			wantCode:    codes.NotFound,
			wantRequest: passwordSecret,
		},
		{
			name:        "Google PermissionDenied status is preserved",
			path:        "aws/prod/password",
			getErr:      status.Error(codes.PermissionDenied, "denied"),
			wantCode:    codes.PermissionDenied,
			wantRequest: passwordSecret,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &fakeGSMServer{secret: tc.secret, getError: tc.getErr}
			client := newTestGSMClient(t, upstream)

			got, err := client.Describe(context.Background(), "team", tc.path)

			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("error = %v, want %v", err, tc.wantErr)
				}
			case tc.wantCode != codes.OK:
				if status.Code(err) != tc.wantCode {
					t.Errorf("error code = %v, want %v (error: %v)", status.Code(err), tc.wantCode, err)
				}
			case err != nil:
				t.Errorf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("description mismatch (-want +got): %s", diff)
			}
			var gotRequest string
			if upstream.getRequest != nil {
				gotRequest = upstream.getRequest.Name
			}
			if gotRequest != tc.wantRequest {
				t.Errorf("Google request = %q, want %q", gotRequest, tc.wantRequest)
			}
		})
	}
}
