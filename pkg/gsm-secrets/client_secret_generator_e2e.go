//go:build secret_generator_e2e

package gsmsecrets

import (
	"context"
	"os"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// NewClient connects to the fake GSM service in secret-generator e2e test builds.
func NewClient(ctx context.Context, _ string) (*secretmanager.Client, error) {
	return secretmanager.NewClient(ctx,
		option.WithEndpoint(os.Getenv("CI_TOOLS_TEST_GSM_ENDPOINT")),
		option.WithoutAuthentication(),
		option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
	)
}
