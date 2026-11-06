package gsmproxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	gsmsecrets "github.com/openshift/ci-tools/pkg/gsm-secrets"
	gsmvalidation "github.com/openshift/ci-tools/pkg/gsm-validation"
)

// GSMClient performs secret operations using one Google client and project.
type GSMClient struct {
	client      gsmsecrets.SecretManagerClient
	parent      string
	closeClient func() error
}

// NewGSMClient opens a Secret Manager client for the configured project.
func NewGSMClient(ctx context.Context, config gsmsecrets.Config, opts ...option.ClientOption) (*GSMClient, error) {
	if config.ProjectIdString == "" {
		return nil, errors.New("GSM project ID must not be empty")
	}
	// The SDK can log full responses at debug level, including user annotations.
	// Override even an explicitly supplied logger so proxy verbosity cannot leak metadata.
	secretManagerClient, err := secretmanager.NewClient(ctx, append(opts, option.WithLogger(slog.New(slog.DiscardHandler)))...)
	if err != nil {
		return nil, fmt.Errorf("failed to create GSM client: %w", err)
	}
	return &GSMClient{
		client:      secretManagerClient,
		parent:      gsmsecrets.GetProjectResourceString(config.ProjectIdString),
		closeClient: secretManagerClient.Close,
	}, nil
}

// Close releases the Google client resources.
func (c *GSMClient) Close() error {
	return c.closeClient()
}

// List returns sorted data-secret paths belonging to the collection.
func (c *GSMClient) List(ctx context.Context, collection string) ([]string, error) {
	if !gsmvalidation.ValidateCollectionName(collection) {
		return nil, errors.New("invalid collection name")
	}
	it := c.client.ListSecrets(ctx, &secretmanagerpb.ListSecretsRequest{
		Parent: c.parent,
		// This substring filter only narrows candidates; it is not an access boundary.
		Filter: fmt.Sprintf("name:%q", collection+gsmvalidation.CollectionSecretDelimiter),
	})

	var names []string
	for {
		secret, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to list secrets: %w", err)
		}
		secretID := gsmsecrets.GetSecretID(secret.GetName())
		if gsmsecrets.ExtractCollectionFromSecretName(secretID) == collection && gsmsecrets.ClassifySecret(secretID) == gsmsecrets.SecretTypeGeneric {
			_, path, found := strings.Cut(secretID, gsmvalidation.CollectionSecretDelimiter)
			if found {
				names = append(names, strings.ReplaceAll(path, gsmvalidation.CollectionSecretDelimiter, "/"))
			}
		}
	}
	sort.Strings(names)
	return names, nil
}
