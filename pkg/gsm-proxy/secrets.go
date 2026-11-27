package gsmproxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	gsmsecrets "github.com/openshift/ci-tools/pkg/gsm-secrets"
	gsmvalidation "github.com/openshift/ci-tools/pkg/gsm-validation"
)

const (
	jiraProjectLabel               = "jira-project"
	rotationInstructionsAnnotation = "rotation-instructions"
	requestInformationAnnotation   = "request-information"
)

// ErrInvalidSecretPath is returned for a secret path that is malformed or identifies a reserved secret.
var ErrInvalidSecretPath = errors.New("invalid secret path")

// SecretMetadata contains the supported ownership and rotation metadata of a secret.
type SecretMetadata struct {
	JiraProject          string `json:"jira_project"`
	RotationInstructions string `json:"rotation_instructions"`
	RequestInformation   string `json:"request_information"`
}

// SecretDescription is the metadata returned for one secret.
type SecretDescription struct {
	CreateTime *time.Time     `json:"create_time,omitempty"`
	Metadata   SecretMetadata `json:"metadata"`
}

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

// Describe returns the metadata for the secret defined by collection and path.
// It returns ErrInvalidSecretPath if path is malformed or identifies a reserved secret.
func (c *GSMClient) Describe(ctx context.Context, collection, path string) (SecretDescription, error) {
	group, field, ok := gsmvalidation.ParseSecretPath(collection, path)
	if !ok {
		return SecretDescription{}, ErrInvalidSecretPath
	}
	name := gsmsecrets.GetGSMSecretName(collection, group, field)
	if gsmsecrets.ClassifySecret(name) != gsmsecrets.SecretTypeGeneric {
		return SecretDescription{}, ErrInvalidSecretPath
	}
	secret, err := c.client.GetSecret(ctx, &secretmanagerpb.GetSecretRequest{Name: c.parent + "/secrets/" + name})
	if err != nil {
		return SecretDescription{}, fmt.Errorf("failed to describe secret: %w", err)
	}
	result := SecretDescription{
		Metadata: SecretMetadata{
			JiraProject:          secret.Labels[jiraProjectLabel],
			RotationInstructions: secret.Annotations[rotationInstructionsAnnotation],
			RequestInformation:   secret.Annotations[requestInformationAnnotation],
		},
	}
	if secret.CreateTime != nil {
		created := secret.CreateTime.AsTime()
		result.CreateTime = &created
	}
	return result, nil
}
