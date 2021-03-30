package secrets

import (
	"os"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/openshift/ci-tools/pkg/bitwarden"
)

type Client interface {
	GetFieldOnItem(itemName, fieldName string) ([]byte, error)
	GetAttachmentOnItem(itemName, attachmentName string) ([]byte, error)
	GetPassword(itemName string) ([]byte, error)
	GetInUseInformationForAllItems() (map[string]SecretUsageComparer, error)
	Logout() ([]byte, error)
}

type dryRunClient struct {
	bitwarden.Client
}

type SecretUsageComparer interface {
	LastChanged() time.Time
	UnusedFields(inUse sets.String) (Difference sets.String)
	UnusedAttachments(inUse sets.String) (Difference sets.String)
	HasPassword() bool
	SuperfluousFields() sets.String
}

// NewDryRunClient creates a fake client that writes debug output to a file.
func NewDryRunClient(output *os.File) (Client, error) {
	c, err := bitwarden.NewDryRunClient(output)
	if err != nil {
		return nil, err
	}
	return &dryRunClient{Client: c}, nil
}

func (*dryRunClient) GetInUseInformationForAllItems() (map[string]SecretUsageComparer, error) {
	return nil, nil
}
