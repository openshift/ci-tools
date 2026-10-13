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

// SecretLister returns the group/field paths of one collection.
// The result is names only. A GSM Secret must not cross this interface:
// its annotations hold free text typed by users.
type SecretLister interface {
	List(ctx context.Context, collection string) ([]string, error)
}

type GSMSecretLister struct {
	client      gsmsecrets.SecretManagerClient
	parent      string
	closeClient func() error
}

var _ SecretLister = (*GSMSecretLister)(nil)

func NewGSMSecretLister(ctx context.Context, config gsmsecrets.Config, opts ...option.ClientOption) (*GSMSecretLister, error) {
	if config.ProjectIdString == "" {
		return nil, errors.New("GSM project ID must not be empty")
	}
	// The SDK can log full responses at debug level, including user annotations.
	// Override even an explicitly supplied logger so proxy verbosity cannot leak metadata.
	secretManagerClient, err := secretmanager.NewClient(ctx, append(opts, option.WithLogger(slog.New(slog.DiscardHandler)))...)
	if err != nil {
		return nil, fmt.Errorf("failed to create GSM client: %w", err)
	}
	return &GSMSecretLister{
		client:      secretManagerClient,
		parent:      gsmsecrets.GetProjectResourceString(config.ProjectIdString),
		closeClient: secretManagerClient.Close,
	}, nil
}

func (lister *GSMSecretLister) Close() error {
	return lister.closeClient()
}

func (lister *GSMSecretLister) List(ctx context.Context, collection string) ([]string, error) {
	if !gsmvalidation.ValidateCollectionName(collection) {
		return nil, errors.New("invalid collection name")
	}
	it := lister.client.ListSecrets(ctx, &secretmanagerpb.ListSecretsRequest{
		Parent: lister.parent,
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
