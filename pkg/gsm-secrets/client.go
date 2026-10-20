//go:build !secret_generator_e2e

package gsmsecrets

import (
	"context"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"google.golang.org/api/option"
)

// NewClient creates an authenticated GSM client using credentialsFile.
func NewClient(ctx context.Context, credentialsFile string) (*secretmanager.Client, error) {
	return secretmanager.NewClient(ctx, option.WithAuthCredentialsFile(option.ServiceAccount, credentialsFile))
}
